package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

const dockerControllerTestContainerIDV1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestDockerControllerV1OrdersChannelCreateStartAndExactLifecycle(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	var actionsMu sync.Mutex
	actions := []string{}
	record := func(value string) {
		actionsMu.Lock()
		defer actionsMu.Unlock()
		actions = append(actions, value)
	}
	exit := make(chan int, 1)
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error {
			record("channel ready")
			return nil
		},
		run: func(spec CommandSpec, options RunOptions) error {
			record(strings.Join(spec.Args, " "))
			writeDockerControllerTestCreateIDV1(plan, spec, options)
			return nil
		},
		observe: func(_ context.Context, _ CommandSpec, container string) (int, error) {
			record("observe " + container)
			return <-exit, nil
		},
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Wait(t.Context()); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("pre-start wait error = %v", err)
	}
	if err := controller.RequestGracefulStop(t.Context()); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("pre-start stop error = %v", err)
	}
	if err := controller.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "already attempted") {
		t.Fatalf("second start error = %v", err)
	}
	if err := controller.RequestGracefulStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.ForceStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	exit <- 42
	status, err := controller.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Kind != controlledsession.ProcessStatusExitedV1 || status.Code == nil || *status.Code != 42 {
		t.Fatalf("wait status = %#v", status)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatalf("repeated cleanup = %v", err)
	}

	actionsMu.Lock()
	got := append([]string(nil), actions...)
	actionsMu.Unlock()
	wantPrefix := []string{
		"channel ready",
		strings.Join(plan.Create.Args, " "),
		"start " + dockerControllerTestContainerIDV1,
	}
	if len(got) < len(wantPrefix) || !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("initial actions = %#v, want prefix %#v", got, wantPrefix)
	}
	for _, want := range []string{
		"observe " + dockerControllerTestContainerIDV1,
		"kill --signal TERM " + dockerControllerTestContainerIDV1,
		"kill --signal KILL " + dockerControllerTestContainerIDV1,
		"container rm --force " + dockerControllerTestContainerIDV1,
	} {
		if !slicesContainStringV1(got, want) {
			t.Fatalf("actions = %#v, missing %q", got, want)
		}
	}
	if countStringsV1(got, "container rm --force "+dockerControllerTestContainerIDV1) != 1 {
		t.Fatalf("cleanup actions = %#v", got)
	}
}

func TestPrepareDockerControllerV1RequiresReadyChannelBeforeCreate(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	run := false
	verifiedNoContainer := false
	_, err := prepareDockerControllerV1(t.Context(), plan, dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error {
			return errors.New("channel socket missing")
		},
		run: func(CommandSpec, RunOptions) error {
			run = true
			return nil
		},
		observe:           func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
		recordNoContainer: func() { verifiedNoContainer = true },
	})
	if err == nil || !strings.Contains(err.Error(), "channel socket missing") || run || !verifiedNoContainer {
		t.Fatalf("prepare error = %v, Docker invoked = %t, no container verified = %t", err, run, verifiedNoContainer)
	}
}

func TestPrepareDockerControllerV1ReportsVerifiedPreCreateBindFailure(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	bindErr := errors.New("injected Docker endpoint bind failure")
	verifiedNoContainer := false
	_, err := prepareDockerControllerV1(t.Context(), plan, dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		bind: func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error) {
			return CommandSpec{}, nil, bindErr
		},
		observe:           func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
		recordNoContainer: func() { verifiedNoContainer = true },
	})
	if !errors.Is(err, bindErr) || !verifiedNoContainer {
		t.Fatalf("prepare error = %v, no container verified = %t", err, verifiedNoContainer)
	}
}

func TestPrepareDockerControllerV1RejectsWorkloadAndIncompleteBackend(t *testing.T) {
	workload := controlledSessionWorkloadPlanFixtureV1(t)
	complete := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run:                 func(CommandSpec, RunOptions) error { return nil },
		observe:             func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	if _, err := prepareDockerControllerV1(t.Context(), workload, complete); err == nil || !strings.Contains(err.Error(), "container role") {
		t.Fatalf("workload role error = %v", err)
	}
	if _, err := prepareDockerControllerV1(t.Context(), controlledSessionControllerPlanFixtureV1(t), dockerControllerBackendV1{}); err == nil || !strings.Contains(err.Error(), "backend is incomplete") {
		t.Fatalf("incomplete backend error = %v", err)
	}
}

func TestPrepareDockerControllerV1DoesNotRemoveAfterAmbiguousCreateFailure(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	runs := []CommandSpec{}
	verifiedNoContainer := false
	_, err := prepareDockerControllerV1(t.Context(), plan, dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, options RunOptions) error {
			runs = append(runs, spec)
			if reflect.DeepEqual(spec.Args, plan.Create.Args) {
				_, _ = options.Stderr.Write([]byte("daemon response was lost"))
				return errors.New("create response was lost")
			}
			return nil
		},
		observe:           func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
		recordNoContainer: func() { verifiedNoContainer = true },
	})
	if err == nil || !strings.Contains(err.Error(), "create response was lost") ||
		!strings.Contains(err.Error(), "daemon response was lost") ||
		!strings.Contains(err.Error(), "did not return an exact container ID") {
		t.Fatalf("ambiguous create error = %v", err)
	}
	if len(runs) != 1 || !reflect.DeepEqual(runs[0].Args, plan.Create.Args) {
		t.Fatalf("ambiguous create failure invoked reconciliation: %#v", runs)
	}
	if verifiedNoContainer {
		t.Fatal("ambiguous create failure reported that no container exists")
	}
}

func TestDockerControllerV1PinsOneDockerEndpointForItsLifetime(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	const endpoint = "unix:///first/docker.sock"
	binds := 0
	runs := []CommandSpec{}
	exit := make(chan int, 1)
	controller, err := prepareDockerControllerV1(t.Context(), plan, dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		bind: func(_ context.Context, spec CommandSpec, timeout time.Duration) (CommandSpec, commandRunner, error) {
			binds++
			if timeout != defaultDockerPreflightTimeout {
				t.Fatalf("bind timeout = %s", timeout)
			}
			pinned := pinDockerEndpointV1(spec, endpoint)
			return pinned, func(command CommandSpec, options RunOptions) error {
				runs = append(runs, pinDockerEndpointV1(command, endpoint))
				writeDockerControllerTestCreateIDV1(plan, command, options)
				return nil
			}, nil
		},
		observe: func(_ context.Context, docker CommandSpec, container string) (int, error) {
			if got := commandEnvironmentValueV1(docker, "DOCKER_HOST"); got != endpoint {
				t.Errorf("observed Docker endpoint = %q", got)
			}
			if got := commandEnvironmentValueV1(docker, "DOCKER_CONTEXT"); got != "" {
				t.Errorf("observed Docker context = %q", got)
			}
			if container != dockerControllerTestContainerIDV1 {
				t.Errorf("observed container = %q", container)
			}
			return <-exit, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.RequestGracefulStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	exit <- 0
	if _, err := controller.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if binds != 1 {
		t.Fatalf("Docker endpoint binds = %d, want 1", binds)
	}
	if len(runs) != 4 {
		t.Fatalf("Docker lifecycle commands = %#v", runs)
	}
	for _, command := range runs {
		if got := commandEnvironmentValueV1(command, "DOCKER_HOST"); got != endpoint {
			t.Fatalf("command %#v endpoint = %q", command.Args, got)
		}
	}
}

func TestParseDockerControllerContainerIDV1(t *testing.T) {
	if got, err := parseDockerContainerIDV1("\n" + dockerControllerTestContainerIDV1 + "\n"); err != nil || got != dockerControllerTestContainerIDV1 {
		t.Fatalf("valid container ID = %q, %v", got, err)
	}
	for _, invalid := range []string{"", "abc", strings.Repeat("g", 64)} {
		if _, err := parseDockerContainerIDV1(invalid); err == nil {
			t.Fatalf("invalid container ID %q was accepted", invalid)
		}
	}
}

func TestDockerControllerV1RollsBackAmbiguousStartFailure(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	runs := []CommandSpec{}
	observed := false
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, options RunOptions) error {
			runs = append(runs, spec)
			writeDockerControllerTestCreateIDV1(plan, spec, options)
			if reflect.DeepEqual(spec.Args, []string{"start", dockerControllerTestContainerIDV1}) {
				return errors.New("start response was lost")
			}
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) {
			observed = true
			return 0, nil
		},
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	err = controller.Start(t.Context())
	if err == nil || !strings.Contains(err.Error(), "start response was lost") {
		t.Fatalf("ambiguous start error = %v", err)
	}
	if len(runs) != 3 || !reflect.DeepEqual(runs[0].Args, plan.Create.Args) ||
		!reflect.DeepEqual(runs[1].Args, []string{"start", dockerControllerTestContainerIDV1}) ||
		!reflect.DeepEqual(runs[2].Args, []string{"container", "rm", "--force", dockerControllerTestContainerIDV1}) {
		t.Fatalf("start rollback commands = %#v", runs)
	}
	if observed {
		t.Fatal("ambiguous start failure launched an exit observer")
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatalf("cleanup after successful rollback = %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("successful rollback was not remembered: %#v", runs)
	}
}

func TestDockerControllerV1ReportsObservationLossAndCallerWaitCancellation(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	release := make(chan struct{})
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, options RunOptions) error {
			writeDockerControllerTestCreateIDV1(plan, spec, options)
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) {
			<-release
			return 0, errors.New("daemon observation ended")
		},
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, err := controller.Wait(waitCtx); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("canceled caller wait error = %v", err)
	}
	close(release)
	status, err := controller.Wait(t.Context())
	if err == nil || !strings.Contains(err.Error(), "daemon observation ended") {
		t.Fatalf("observation error = %v", err)
	}
	if status.Kind != controlledsession.ProcessStatusUnavailableV1 || status.Reason != "Docker controller observation was lost" {
		t.Fatalf("observation-loss status = %#v", status)
	}
}

func TestDockerControllerV1RetriesFailedCleanup(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	cleanupAttempts := 0
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, options RunOptions) error {
			writeDockerControllerTestCreateIDV1(plan, spec, options)
			if reflect.DeepEqual(spec.Args, []string{"container", "rm", "--force", dockerControllerTestContainerIDV1}) {
				cleanupAttempts++
				if cleanupAttempts == 1 {
					return errors.New("daemon unavailable")
				}
			}
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Cleanup(t.Context()); err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("first cleanup error = %v", err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatalf("retry cleanup = %v", err)
	}
	if cleanupAttempts != 2 {
		t.Fatalf("cleanup attempts = %d, want 2", cleanupAttempts)
	}
}

func TestDockerControllerV1TreatsMissingContainerAsCleaned(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	cleanupAttempts := 0
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, options RunOptions) error {
			writeDockerControllerTestCreateIDV1(plan, spec, options)
			if reflect.DeepEqual(spec.Args, []string{"container", "rm", "--force", dockerControllerTestContainerIDV1}) {
				cleanupAttempts++
				return errors.New("Error response from daemon: No such container: " + dockerControllerTestContainerIDV1)
			}
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatalf("missing-container cleanup = %v", err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatalf("repeated cleanup = %v", err)
	}
	if cleanupAttempts != 1 {
		t.Fatalf("cleanup attempts = %d, want 1", cleanupAttempts)
	}
}

func TestDockerControllerV1FreezesCallerOwnedPlanSlices(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	wantStart := []string{"start", dockerControllerTestContainerIDV1}
	wantCleanup := []string{"container", "rm", "--force", dockerControllerTestContainerIDV1}
	runs := []CommandSpec{}
	exit := make(chan int, 1)
	controller, err := prepareDockerControllerV1(t.Context(), plan, dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, options RunOptions) error {
			runs = append(runs, spec)
			writeDockerControllerTestCreateIDV1(plan, spec, options)
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) { return <-exit, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	plan.Start.Args[0] = "tampered-start"
	plan.Cleanup.Args[0] = "tampered-cleanup"
	if err := controller.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	exit <- 0
	if _, err := controller.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || !reflect.DeepEqual(runs[1].Args, wantStart) || !reflect.DeepEqual(runs[2].Args, wantCleanup) {
		t.Fatalf("frozen lifecycle commands = %#v, want start %#v cleanup %#v", runs, wantStart, wantCleanup)
	}
}

func controlledSessionControllerPlanFixtureV1(t *testing.T) ControlledSessionContainerPlanV1 {
	t.Helper()
	input, backend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Controller
}

func countStringsV1(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func writeDockerControllerTestCreateIDV1(
	plan ControlledSessionContainerPlanV1,
	spec CommandSpec,
	options RunOptions,
) {
	if reflect.DeepEqual(spec.Args, plan.Create.Args) && options.Stdout != nil {
		_, _ = options.Stdout.Write([]byte(dockerControllerTestContainerIDV1 + "\n"))
	}
}
