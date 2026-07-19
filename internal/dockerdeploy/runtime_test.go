package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestRuntimeCommandActions(t *testing.T) {
	dir, projectName := makeRuntimeDeployment(t)
	cases := []struct {
		action string
		suffix []string
	}{
		{action: "up", suffix: []string{"up", "-d"}},
		{action: "restart", suffix: []string{"up", "-d", "--force-recreate"}},
		{action: "down", suffix: []string{"down", "--remove-orphans"}},
		{action: "ps", suffix: []string{"ps"}},
		{action: "status", suffix: []string{"ps", "--all"}},
		{action: "logs", suffix: []string{"logs", "--timestamps"}},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			spec, err := RuntimeCommand(dir, tc.action)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Name != "docker" {
				t.Fatalf("name = %q", spec.Name)
			}
			if !containsAdjacent(spec.Args, "--project-name", projectName) {
				t.Fatalf("args did not include staging compose project: %#v", spec.Args)
			}
			if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(tc.suffix):], tc.suffix) {
				t.Fatalf("suffix = %#v, want %#v", spec.Args[len(spec.Args)-len(tc.suffix):], tc.suffix)
			}
		})
	}
}

func TestRuntimeCommandCanFollowLogs(t *testing.T) {
	dir, projectName := makeRuntimeDeployment(t)
	spec, err := RuntimeCommandWithOptions(dir, "logs", RuntimeCommandOptions{
		Follow: true,
		Tail:   "100",
		Since:  "2026-07-09T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAdjacent(spec.Args, "--project-name", projectName) {
		t.Fatalf("args did not include staging compose project: %#v", spec.Args)
	}
	suffix := []string{"logs", "--timestamps", "--since", "2026-07-09T00:00:00Z", "--tail", "100", "-f"}
	if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(suffix):], suffix) {
		t.Fatalf("suffix = %#v, want %#v", spec.Args[len(spec.Args)-len(suffix):], suffix)
	}
}

func TestExecuteEnvironmentRestartRunsCompleteStopStartLifecycle(t *testing.T) {
	dir, _ := makeRuntimeDeployment(t)
	commands := []string{}
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		switch {
		case containsInOrder(spec.Args, []string{"down", "--remove-orphans"}):
			commands = append(commands, "stop")
		case containsInOrder(spec.Args, []string{"up", "-d"}):
			commands = append(commands, "start")
		default:
			commands = append(commands, spec.Args[len(spec.Args)-1])
		}
		return nil
	})
	defer restoreRuntime()
	restoreRunning := stubRuntimePostStartServiceRunningCheck(func(string, string, time.Duration) error { return nil })
	defer restoreRunning()
	restoreHealth := stubRuntimePostStartHealthCheck(func(TestOptions) error {
		t.Fatal("health check should not run without a configured legacy health hook")
		return nil
	})
	defer restoreHealth()

	command := func(name string) *ResolvedEnvironmentCommand {
		return &ResolvedEnvironmentCommand{Name: name, Argv: []string{"/bin/demo", name}}
	}
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleCommand, Event: "before_stop", Command: command("before-stop")},
		{Kind: LifecycleStop, Event: "stop"},
		{Kind: LifecycleCommand, Event: "after_stop", Command: command("after-stop")},
		{Kind: LifecycleCommand, Event: "before_start", Command: command("before-start")},
		{Kind: LifecycleStart, Event: "start"},
		{Kind: LifecycleCommand, Event: "after_start", Command: command("after-start")},
	}}
	plan := DockerExecutionPlan{Image: "demo:image", ContainerName: "demo", RuntimeUser: RuntimeUserPlan{DockerUser: "1000:1000"}}
	if err := executeEnvironmentRestart(RuntimeOptions{Dir: dir, Action: "restart"}, deploy.AppPack{}, plan, lifecycle, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"before-stop", "stop", "after-stop", "before-start", "start", "after-start"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestRequireRecordedEnvironmentImageDoesNotResolveMissingBundle(t *testing.T) {
	dir, _ := makeRuntimeDeployment(t)
	state, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = requireRecordedEnvironmentImage(context.Background(), dir, blueprint.Document{}, state)
	if err == nil || !strings.Contains(err.Error(), "restart requires a prepared installation bundle") {
		t.Fatalf("error = %v", err)
	}
}

func TestRuntimeUpAutomaticallyPreparesBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restoreBundle := stubSuccessfulBundlePrepare(t, &commands)
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		return nil
	})
	defer restoreRuntime()
	restoreRunning := stubRuntimePostStartServiceRunningCheck(func(gotDir string, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
		if gotDir != dir {
			t.Fatalf("service state dir = %q, want %q", gotDir, dir)
		}
		return nil
	})
	defer restoreRunning()
	restoreHealth := stubRuntimePostStartHealthCheck(func(options TestOptions) error {
		t.Fatal("health check should not run without runtime after_start health_check")
		return nil
	})
	defer restoreHealth()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "up"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "check", "warm runtime", "up -d"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestRuntimeUpReportsPreparationProgress(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restoreBundle := stubSuccessfulBundlePrepare(t, &commands)
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		return nil
	})
	defer restoreRuntime()
	restoreRunning := stubRuntimePostStartServiceRunningCheck(func(gotDir string, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
		if gotDir != dir {
			t.Fatalf("service state dir = %q, want %q", gotDir, dir)
		}
		return nil
	})
	defer restoreRunning()
	restoreHealth := stubRuntimePostStartHealthCheck(func(options TestOptions) error {
		t.Fatal("health check should not run without runtime after_start health_check")
		return nil
	})
	defer restoreHealth()

	var progress bytes.Buffer
	if err := Runtime(RuntimeOptions{Dir: dir, Action: "up", Progress: &progress}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"prepare installation bundle",
		"prepare workspace",
		"build wheelhouse",
		"validate bundle",
		"warm Python runtime",
		"prepare runtime compose",
		"prepare runtime cache",
		"start app",
		"check app state",
	} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("progress missing %q:\n%s", want, progress.String())
		}
	}
}

func TestRuntimeUpRecoversStaleDockerNetwork(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	upCount := 0
	restoreBundle := stubSuccessfulBundlePrepare(t, &commands)
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		action := spec.Args[len(spec.Args)-1]
		if action == "-d" && len(spec.Args) >= 2 && spec.Args[len(spec.Args)-2] == "up" {
			action = "up"
		}
		if action == "--remove-orphans" && len(spec.Args) >= 2 && spec.Args[len(spec.Args)-2] == "down" {
			action = "down"
		}
		commands = append(commands, action)
		if action == "up" {
			upCount++
			if upCount == 1 {
				return fmt.Errorf("docker failed: exit status 1\ncommand output:\nnetwork b2f601ad24f6dbb403c8f25b418d314854c35d7fc33ac351355b45d12937cbb3 not found")
			}
		}
		return nil
	})
	defer restoreRuntime()
	restoreRunning := stubRuntimePostStartServiceRunningCheck(func(gotDir string, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
		if gotDir != dir {
			t.Fatalf("service state dir = %q, want %q", gotDir, dir)
		}
		return nil
	})
	defer restoreRunning()
	restoreHealth := stubRuntimePostStartHealthCheck(func(options TestOptions) error {
		t.Fatal("health check should not run without runtime after_start health_check")
		return nil
	})
	defer restoreHealth()

	var stderr bytes.Buffer
	if err := Runtime(RuntimeOptions{Dir: dir, Action: "up", Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "check", "warm runtime", "up", "down", "up"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
	if !strings.Contains(stderr.String(), "network b2f601ad24f6dbb403c8f25b418d314854c35d7fc33ac351355b45d12937cbb3 not found") {
		t.Fatalf("stderr missing original stale network error:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "[STAGING : demo] detected stale Docker network state; running down --remove-orphans and retrying up\n") {
		t.Fatalf("stderr missing stale network recovery message:\n%s", stderr.String())
	}
}

func TestRuntimeUpFailsWhenServiceExitsAfterStart(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restoreBundle := stubSuccessfulBundlePrepare(t, &commands)
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		return nil
	})
	defer restoreRuntime()
	logSince := time.Date(2026, 7, 9, 12, 36, 0, 0, time.UTC)
	restoreLogSince := stubRuntimeLogSinceTime(logSince)
	defer restoreLogSince()
	restoreRunning := stubRuntimePostStartServiceRunningCheck(func(gotDir string, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
		if gotDir != dir {
			t.Fatalf("service state dir = %q, want %q", gotDir, dir)
		}
		return fmt.Errorf("service is not running; current state: exited")
	})
	defer restoreRunning()
	restoreHealth := stubRuntimePostStartHealthCheck(func(options TestOptions) error {
		t.Fatal("health check should not run when the service is not running")
		return nil
	})
	defer restoreHealth()
	restoreLogs := stubRuntimeLogOutput(t, logSince, []byte(strings.Join([]string{
		"demo | 2026-07-09T12:36:01Z reploy:event phase=config-check event=start",
		"demo | 2026-07-09T12:36:02Z omegaconf-inspector error: missing config: /conf/inspector.yaml",
		"demo | 2026-07-09T12:36:03Z configuration check failed; app will not start until the config passes",
		"demo | 2026-07-09T12:36:04Z reploy:event phase=config-check event=end status=failed exit=1",
		"",
	}, "\n")))
	defer restoreLogs()

	err = Runtime(RuntimeOptions{Dir: dir, Action: "up"})
	if err == nil {
		t.Fatal("expected service state error")
	}
	for _, want := range []string{
		"service failed after start",
		"config check failed (exit code 1)",
		"current state: exited",
		"startup log snippet:",
		"omegaconf-inspector error: missing config: /conf/inspector.yaml",
		"configuration check failed; app will not start until the config passes",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
	for _, unwanted := range []string{
		"recent logs:",
		"current state: exited (exit code 0)",
	} {
		if strings.Contains(err.Error(), unwanted) {
			t.Fatalf("error unexpectedly contained %q:\n%v", unwanted, err)
		}
	}
	wantCommands := []string{"build", "check", "warm runtime", "up -d"}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestRuntimeUpRunsConfiguredAfterStartHealthCheck(t *testing.T) {
	packDir := makeTestPackWithManifest(t, testPackManifestWithRuntimeAfterStartHealthCheck())
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restoreBundle := stubSuccessfulBundlePrepare(t, &commands)
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		return nil
	})
	defer restoreRuntime()
	restoreRunning := stubRuntimePostStartServiceRunningCheck(func(gotDir string, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
		if gotDir != dir {
			t.Fatalf("service state dir = %q, want %q", gotDir, dir)
		}
		return nil
	})
	defer restoreRunning()
	healthCalled := false
	restoreHealth := stubRuntimePostStartHealthCheck(func(options TestOptions) error {
		healthCalled = true
		if options.Dir != dir {
			t.Fatalf("health dir = %q, want %q", options.Dir, dir)
		}
		return nil
	})
	defer restoreHealth()

	var progress bytes.Buffer
	if err := Runtime(RuntimeOptions{Dir: dir, Action: "up", Progress: &progress}); err != nil {
		t.Fatal(err)
	}
	if !healthCalled {
		t.Fatal("expected configured after-start health check to run")
	}
	if !strings.Contains(progress.String(), "check app health") {
		t.Fatalf("progress missing health check:\n%s", progress.String())
	}
	wantCommands := []string{"build", "check", "warm runtime", "up -d"}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestRuntimeUpFailsWhenConfiguredHealthCheckFails(t *testing.T) {
	packDir := makeTestPackWithManifest(t, testPackManifestWithRuntimeAfterStartHealthCheck())
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restoreBundle := stubSuccessfulBundlePrepare(t, &commands)
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		return nil
	})
	defer restoreRuntime()
	logSince := time.Date(2026, 7, 9, 12, 40, 0, 0, time.UTC)
	restoreLogSince := stubRuntimeLogSinceTime(logSince)
	defer restoreLogSince()
	restoreRunning := stubRuntimePostStartServiceRunningCheck(func(gotDir string, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
		if gotDir != dir {
			t.Fatalf("service state dir = %q, want %q", gotDir, dir)
		}
		return nil
	})
	defer restoreRunning()
	restoreHealth := stubRuntimePostStartHealthCheck(func(options TestOptions) error {
		if options.Dir != dir {
			t.Fatalf("health dir = %q, want %q", options.Dir, dir)
		}
		return fmt.Errorf("server health check failed: connection refused")
	})
	defer restoreHealth()
	restoreLogs := stubRuntimeLogOutput(t, logSince, []byte(strings.Join([]string{
		"demo | 2026-07-09T12:40:01Z reploy:event phase=service event=start",
		"demo | 2026-07-09T12:40:02Z application still warming up",
		"",
	}, "\n")))
	defer restoreLogs()

	err = Runtime(RuntimeOptions{Dir: dir, Action: "up"})
	if err == nil {
		t.Fatal("expected configured health error")
	}
	for _, want := range []string{
		"service health check failed after start",
		"server health check failed: connection refused",
		"startup log snippet:",
		"application still warming up",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
	wantCommands := []string{"build", "check", "warm runtime", "up -d"}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestExtractRuntimeStartupLogSnippetUsesOnlyMarkerWindows(t *testing.T) {
	logs := strings.Join([]string{
		"app | 2026-07-09T12:00:00Z before marker",
		"app | 2026-07-09T12:00:04Z reploy:event phase=config-check event=start",
		"app | 2026-07-09T12:00:05Z current config failure",
		"app | 2026-07-09T12:00:06Z reploy:event phase=config-check event=end status=failed exit=2",
		"app | 2026-07-09T12:00:07Z between phases",
		"app | 2026-07-09T12:00:08Z reploy:event phase=service event=start",
		"app | 2026-07-09T12:00:09Z current service failure",
		"",
	}, "\n")

	diagnostics := extractRuntimeStartupLogDiagnostics(logs)
	snippet := diagnostics.Snippet
	if diagnostics.Failure != "config check failed (exit code 2)" {
		t.Fatalf("failure = %q", diagnostics.Failure)
	}
	for _, want := range []string{
		"current config failure",
		"current service failure",
	} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("snippet missing %q:\n%s", want, snippet)
		}
	}
	for _, unwanted := range []string{
		"before marker",
		"between phases",
		"reploy:event",
	} {
		if strings.Contains(snippet, unwanted) {
			t.Fatalf("snippet unexpectedly contained %q:\n%s", unwanted, snippet)
		}
	}
}

func TestRuntimeLifecycleOutputRequiresVerbose(t *testing.T) {
	dir, _ := makeRuntimeDeployment(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		if options.Stdout != nil || options.Stderr != nil {
			t.Fatalf("non-verbose lifecycle command should capture output internally: %#v", options)
		}
		return nil
	})
	defer restoreRuntime()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "down", Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRuntimeUpVerboseStreamsBundlePrepareOutput(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	commands := []string{}
	restoreBundle := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		if options.Stdout == nil || options.Stderr == nil {
			t.Fatalf("bundle prepare should stream verbose output: %#v", options)
		}
		switch {
		case containsInOrder(spec.Args, []string{"python", "-m", "pip", "--disable-pip-version-check", "wheel", "--no-cache-dir"}):
			command := strings.Join(spec.Args, " ")
			if strings.Contains(command, "--progress-bar raw") || strings.Contains(command, "reploy-bundle-pip") {
				return fmt.Errorf("verbose bundle prepare command should not enable raw progress:\n%s", command)
			}
			commands = append(commands, "build")
			if _, err := options.Stdout.Write([]byte("build output\n")); err != nil {
				return err
			}
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			commands = append(commands, "check")
			if _, err := options.Stderr.Write([]byte("check output\n")); err != nil {
				return err
			}
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			commands = append(commands, "warm runtime")
			if _, err := options.Stdout.Write([]byte("warm output\n")); err != nil {
				return err
			}
			return nil
		default:
			return fmt.Errorf("unexpected bundle command: %#v", spec.Args)
		}
	})
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		if _, err := options.Stdout.Write([]byte("compose output\n")); err != nil {
			return err
		}
		return nil
	})
	defer restoreRuntime()
	restoreRunning := stubRuntimePostStartServiceRunningCheck(func(gotDir string, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
		if gotDir != dir {
			t.Fatalf("service state dir = %q, want %q", gotDir, dir)
		}
		return nil
	})
	defer restoreRunning()
	restoreHealth := stubRuntimePostStartHealthCheck(func(options TestOptions) error {
		t.Fatal("health check should not run without runtime after_start health_check")
		return nil
	})
	defer restoreHealth()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "up", Verbose: true, Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "check", "warm runtime", "up -d"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
	if !strings.Contains(stdout.String(), "[STAGING : demo] build output\n") {
		t.Fatalf("stdout missing staging prefix: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "[STAGING : demo] check output\n") {
		t.Fatalf("stderr missing staging prefix: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[STAGING : demo] warm output\n") {
		t.Fatalf("stdout missing staging warmup output: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[STAGING : demo] compose output\n") {
		t.Fatalf("stdout missing verbose compose output: %q", stdout.String())
	}
}

func TestRuntimeUpFailsClearlyWhenManagedFileIsMissing(t *testing.T) {
	packDir := makeTestPackWithManifest(t, testPackManifestWithManagedFile())
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	called := false
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		called = true
		return nil
	})
	defer restoreRuntime()

	err = Runtime(RuntimeOptions{Dir: dir, Action: "up"})
	if err == nil {
		t.Fatal("expected missing managed file error")
	}
	if called {
		t.Fatal("runtime invoked Docker despite missing managed file")
	}
	if !strings.Contains(err.Error(), "managed file is missing") || !strings.Contains(err.Error(), ".arbiter.env") {
		t.Fatalf("missing managed file error was not clear: %v", err)
	}
}

func TestRuntimeStatusDoesNotPrepareBundle(t *testing.T) {
	dir, _ := makeRuntimeDeployment(t)
	commands := []string{}
	restoreBundle := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, "bundle")
		return nil
	})
	defer restoreBundle()
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, strings.Join(spec.Args[len(spec.Args)-2:], " "))
		return nil
	})
	defer restoreRuntime()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "status"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"ps --all"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestEnvironmentRuntimeStatusIncludesReadOnlyInspection(t *testing.T) {
	ref, err := deploy.ParsePackRef("file:../../examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		_, err := io.WriteString(options.Stdout, "compose status\n")
		return err
	})
	defer restoreRuntime()

	var stdout bytes.Buffer
	if err := Runtime(RuntimeOptions{Dir: dir, Action: "status", Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"environment: omegaconf-inspector",
		"candidate bundle identity: unresolved",
		"commands:",
		"endpoints:",
		"backend files:",
		"compose status",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("status missing %q:\n%s", want, stdout.String())
		}
	}
	state, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Materialization != nil || state.Images != nil || state.Bundle.PreparedFingerprint != "" {
		t.Fatalf("status mutated deployment state: %#v", state)
	}
}

func TestRuntimePrefixesStagedOutput(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	dir, _ := makeRuntimeDeployment(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		if _, err := options.Stdout.Write([]byte("compose out\n")); err != nil {
			return err
		}
		if _, err := options.Stderr.Write([]byte("compose err\n")); err != nil {
			return err
		}
		return nil
	})
	defer restoreRuntime()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "status", Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "[STAGING : demo] compose out\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "[STAGING : demo] compose err\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRuntimeLogsUseRawComposeOutput(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	dir, _ := makeRuntimeDeployment(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	restoreRuntime := stubRuntimeRunner(func(spec CommandSpec, options RunOptions) error {
		if _, err := options.Stdout.Write([]byte("app | log out\n")); err != nil {
			return err
		}
		if _, err := options.Stderr.Write([]byte("app | log err\n")); err != nil {
			return err
		}
		return nil
	})
	defer restoreRuntime()

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "logs", Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "app | log out\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "app | log err\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func stubRuntimeRunner(run func(CommandSpec, RunOptions) error) func() {
	previousRuntime := runRuntimeCommand
	previousRuntimeVolumeInit := runRuntimeVolumeInitCommand
	runRuntimeCommand = run
	runRuntimeVolumeInitCommand = func(CommandSpec, RunOptions) error { return nil }
	return func() {
		runRuntimeCommand = previousRuntime
		runRuntimeVolumeInitCommand = previousRuntimeVolumeInit
	}
}

func stubRuntimePostStartServiceRunningCheck(check func(string, string, time.Duration) error) func() {
	previous := runRuntimePostStartServiceRunningCheck
	runRuntimePostStartServiceRunningCheck = check
	return func() {
		runRuntimePostStartServiceRunningCheck = previous
	}
}

func stubRuntimePostStartHealthCheck(check func(TestOptions) error) func() {
	previous := runRuntimePostStartHealthCheck
	runRuntimePostStartHealthCheck = check
	return func() {
		runRuntimePostStartHealthCheck = previous
	}
}

func stubRuntimeLogSinceTime(since time.Time) func() {
	previous := runtimeLogSinceTime
	runtimeLogSinceTime = func() time.Time {
		return since
	}
	return func() {
		runtimeLogSinceTime = previous
	}
}

func stubRuntimeLogOutput(t *testing.T, since time.Time, output []byte) func() {
	t.Helper()
	original := runTestCommandOutput
	runTestCommandOutput = func(spec CommandSpec, options RunOptions) ([]byte, error) {
		wantSince := since.UTC().Format(time.RFC3339Nano)
		if !containsInOrder(spec.Args, []string{"logs", "--timestamps", "--since", wantSince, "--tail", runtimeLogSnippetTail}) {
			t.Fatalf("logs command did not include startup snippet window: %#v", spec.Args)
		}
		return output, nil
	}
	return func() {
		runTestCommandOutput = original
	}
}

func makeRuntimeDeployment(t *testing.T) (string, string) {
	t.Helper()
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	values, err := readDockerEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	projectName := envValue(values, "REPLOY_CONTAINER_NAME", "")
	if projectName == "" {
		t.Fatal("missing REPLOY_CONTAINER_NAME")
	}
	if err := os.RemoveAll(packDir); err != nil {
		t.Fatal(err)
	}
	return dir, projectName
}

func testPackManifestWithRuntimeAfterStartHealthCheck() string {
	return strings.Replace(testPackManifest(), "  health:\n", `  runtime:
    hooks:
      after_start:
        - health_check:
            wait: true
  health:
`, 1)
}

func TestRuntimeCommandUsesInstalledComposeProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	state := deploy.DeploymentState{
		SchemaVersion: 1,
		Phase:         deploy.PhaseInstalled,
		Install: &deploy.InstallState{
			ComposeProject: "demo-12345678",
		},
	}
	content, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := RuntimeCommandWithOptions(dir, "ps", RuntimeCommandOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAdjacent(spec.Args, "--project-name", "demo-12345678") {
		t.Fatalf("args did not include installed compose project: %#v", spec.Args)
	}
}

func TestRuntimeCommandRejectsUnknownAction(t *testing.T) {
	dir, _ := makeRuntimeDeployment(t)
	_, err := RuntimeCommand(dir, "explode")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRuntimeInputsDoesNotRequireAppEnvFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DockerEnvFileName), []byte("REPLOY_CONFIG_DIR=./conf\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateRuntimeInputs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
