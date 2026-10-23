package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestInstalledServiceWaitsBeforeCurrentBuildEngineVerification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installed system service fixture requires Unix paths")
	}
	dir, operation, _, current := installedBuildPublicationSourceFixture(t)
	installed := current.State
	installed.Deployment = &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1, Installation: installedBuildPublicationInstallation(dir),
	}
	if err := operation.CommitStateV1(current.State.Current, installed); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	readinessCalls, engineChecks := 0, 0
	oldBind, oldOutput := bindInstalledServiceCommandRunner, runDockerOutput
	bindInstalledServiceCommandRunner = func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error) {
		readinessCalls++
		cancel()
		return CommandSpec{}, nil, os.ErrNotExist
	}
	runDockerOutput = func(context.Context, ...string) (string, error) {
		engineChecks++
		return "", errors.New("current-build verification contacted the engine before readiness")
	}
	t.Cleanup(func() { bindInstalledServiceCommandRunner, runDockerOutput = oldBind, oldOutput })
	err := RunInstalledServiceContainerV1(ctx, dir, "run", "/usr/bin/docker", RunOptions{})
	if readinessCalls != 1 || engineChecks != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("readiness calls=%d engine checks=%d error=%v", readinessCalls, engineChecks, err)
	}
	for _, name := range []string{PrivateWorkloadEnvironmentFileName, ComposeFileName, DockerEnvFileName} {
		if _, err := os.Stat(dir + string(os.PathSeparator) + name); !os.IsNotExist(err) {
			t.Fatalf("cancelled engine readiness created %s: %v", name, err)
		}
	}
}

func TestInstalledServicePreparationBoundsLockWaitBeforeEngineWork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installed system service fixture requires Unix paths")
	}
	dir, operation, _, _ := installedBuildPublicationSourceFixture(t)
	defer operation.Unlock()
	parent, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	calls := 0
	oldBind := bindInstalledServiceCommandRunner
	bindInstalledServiceCommandRunner = func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error) {
		calls++
		return CommandSpec{}, nil, errors.New("engine must not run while the deployment lock is held")
	}
	t.Cleanup(func() { bindInstalledServiceCommandRunner = oldBind })
	started := time.Now()
	err := runInstalledServiceContainerV1(parent, dir, "run", "/usr/bin/docker", RunOptions{}, 30*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || calls != 0 || parent.Err() != nil || time.Since(started) > time.Second {
		t.Fatalf("error=%v calls=%d parent=%v elapsed=%v", err, calls, parent.Err(), time.Since(started))
	}
	for _, part := range []string{"lock/setup=", "engine wait=0s", "limit 30ms", "application startup=not started"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("missing %q: %v", part, err)
		}
	}
	for _, name := range []string{PrivateWorkloadEnvironmentFileName, ComposeFileName, DockerEnvFileName} {
		if _, err := os.Stat(dir + string(os.PathSeparator) + name); !os.IsNotExist(err) {
			t.Fatalf("expired preparation created %s: %v", name, err)
		}
	}
}

func TestInstalledServiceEngineWaitUsesRemainingPreparationDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installed system service fixture requires Unix paths")
	}
	dir, operation, _, current := installedBuildPublicationSourceFixture(t)
	installed := current.State
	installed.Deployment = &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1, Installation: installedBuildPublicationInstallation(dir),
	}
	if err := operation.CommitStateV1(current.State.Current, installed); err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var engineDeadline time.Time
	calls := 0
	oldBind := bindInstalledServiceCommandRunner
	bindInstalledServiceCommandRunner = func(ctx context.Context, _ CommandSpec, _ time.Duration) (CommandSpec, commandRunner, error) {
		calls++
		engineDeadline, _ = ctx.Deadline()
		<-ctx.Done()
		return CommandSpec{}, nil, ctx.Err()
	}
	t.Cleanup(func() { bindInstalledServiceCommandRunner = oldBind })
	released := make(chan error, 1)
	go func() {
		time.Sleep(75 * time.Millisecond)
		released <- operation.Unlock()
	}()
	const preparationLimit = 250 * time.Millisecond
	started := time.Now()
	err := runInstalledServiceContainerV1(parent, dir, "run", "/usr/bin/docker", RunOptions{}, preparationLimit)
	if releaseErr := <-released; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 || parent.Err() != nil || engineDeadline.IsZero() {
		t.Fatalf("error=%v calls=%d parent=%v deadline=%v", err, calls, parent.Err(), engineDeadline)
	}
	if engineDeadline.After(started.Add(preparationLimit + 25*time.Millisecond)) {
		t.Fatalf("engine wait renewed the preparation deadline: got=%v original=%v", engineDeadline, started.Add(preparationLimit))
	}
	for _, part := range []string{"lock/setup=", "engine wait=", "limit 250ms", "application startup=not started"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("missing %q: %v", part, err)
		}
	}
}

func TestInstalledServiceEngineWaitDefersWorkUntilSelectedEngineIsReady(t *testing.T) {
	want := CommandSpec{Name: "/usr/bin/docker", Dir: t.TempDir(), Env: []string{"DOCKER_HOST=unix:///run/podman/selected.sock", "DOCKER_CONTEXT="}}
	attempts, executions := 0, 0
	runner := func(CommandSpec, RunOptions) error { executions++; return nil }
	var output bytes.Buffer
	pinned, run, err := waitInstalledServiceEngineV1(t.Context(), want, RunOptions{Stderr: &output, DockerPreflightTimeout: 7 * time.Second}, installedServiceEngineWaitV1{
		timeout: time.Second, interval: time.Millisecond,
		bind: func(ctx context.Context, spec CommandSpec, timeout time.Duration) (CommandSpec, commandRunner, error) {
			attempts++
			if !reflect.DeepEqual(spec, want) || timeout != 7*time.Second || executions != 0 {
				t.Fatal("readiness changed the selected command or ran workload work")
			}
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Second {
				t.Fatal("readiness attempt has no bounded deadline")
			}
			if attempts == 1 {
				return CommandSpec{}, nil, os.ErrNotExist // CLI not yet mounted.
			}
			if attempts == 2 {
				return CommandSpec{}, nil, errors.New("selected engine socket not yet available")
			}
			return spec, runner, nil
		},
	})
	if err != nil || !reflect.DeepEqual(pinned, want) || attempts != 3 || executions != 0 {
		t.Fatalf("attempts=%d executions=%d command=%#v error=%v", attempts, executions, pinned, err)
	}
	if strings.Count(output.String(), "waiting for") != 1 {
		t.Fatalf("readiness output = %q", output.String())
	}
	if err := run(pinned, RunOptions{Context: t.Context()}); err != nil || executions != 1 {
		t.Fatalf("ready runner: executions=%d error=%v", executions, err)
	}
}

func TestInstalledServiceEngineWaitRetainsTimeoutAndUnderlyingFailure(t *testing.T) {
	for _, cause := range []error{os.ErrNotExist, os.ErrPermission, errors.New("selected daemon refused connection")} {
		t.Run(cause.Error(), func(t *testing.T) {
			attempts := 0
			_, run, err := waitInstalledServiceEngineV1(t.Context(), CommandSpec{Name: "/usr/bin/docker"}, RunOptions{}, installedServiceEngineWaitV1{
				timeout: 20 * time.Millisecond, interval: time.Millisecond,
				bind: func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error) {
					attempts++
					return CommandSpec{}, nil, cause
				},
			})
			if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, cause) || run != nil || attempts < 1 {
				t.Fatalf("attempts=%d runner=%v error=%v", attempts, run != nil, err)
			}
		})
	}
}

func TestInstalledServiceEngineWaitHonorsCancellationBeforeAndDuringBinding(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "during", true: "before"}[before], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if before {
				cancel()
			}
			attempts := 0
			_, run, err := waitInstalledServiceEngineV1(ctx, CommandSpec{Name: "/usr/bin/docker"}, RunOptions{}, installedServiceEngineWaitV1{
				timeout: time.Second, interval: time.Second,
				bind: func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error) {
					attempts++
					cancel()
					return CommandSpec{}, func(CommandSpec, RunOptions) error { t.Fatal("cancelled readiness ran a command"); return nil }, nil
				},
			})
			if !errors.Is(err, context.Canceled) || run != nil || before && attempts != 0 || !before && attempts != 1 {
				t.Fatalf("attempts=%d runner=%v error=%v", attempts, run != nil, err)
			}
		})
	}
}
