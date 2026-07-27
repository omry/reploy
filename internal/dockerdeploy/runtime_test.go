package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeCommandActions(t *testing.T) {
	dir := runtimeCommandDeployment(t, "demo-project")
	cases := []struct {
		action string
		suffix []string
	}{
		{action: "up", suffix: []string{"up", "--pull", "never", "-d"}},
		{action: "restart", suffix: []string{"up", "--pull", "never", "-d", "--force-recreate"}},
		{action: "down", suffix: []string{"down", "--remove-orphans"}},
		{action: "ps", suffix: []string{"ps"}},
		{action: "status", suffix: []string{"ps", "--all"}},
		{action: "logs", suffix: []string{"logs", "--timestamps"}},
	}
	for _, test := range cases {
		t.Run(test.action, func(t *testing.T) {
			spec, err := RuntimeCommand(dir, test.action)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Name != "docker" || !containsAdjacent(spec.Args, "--project-name", "demo-project") {
				t.Fatalf("runtime command = %#v", spec)
			}
			if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(test.suffix):], test.suffix) {
				t.Fatalf("suffix = %#v, want %#v", spec.Args[len(spec.Args)-len(test.suffix):], test.suffix)
			}
			if (test.action == "up" || test.action == "restart") && !containsAdjacent(spec.Args, "--pull", "never") {
				t.Fatalf("%s command permits image pulls: %#v", test.action, spec.Args)
			}
		})
	}
}

func TestRuntimeCommandLogOptions(t *testing.T) {
	dir := runtimeCommandDeployment(t, "demo-project")
	spec, err := RuntimeCommandWithOptions(dir, "logs", RuntimeCommandOptions{
		Follow: true, Tail: "100", Since: "2026-07-09T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"logs", "--timestamps", "--since", "2026-07-09T00:00:00Z", "--tail", "100", "-f"}
	if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(want):], want) {
		t.Fatalf("log suffix = %#v, want %#v", spec.Args[len(spec.Args)-len(want):], want)
	}
}

func TestRuntimeDispatchesStateV1Workloads(t *testing.T) {
	dir := runtimeStateEnvelope(t, "state-v1")
	original := runCurrentWorkload
	originalBuild := runRuntimeProviderBuild
	t.Cleanup(func() {
		runCurrentWorkload = original
		runRuntimeProviderBuild = originalBuild
	})
	var buildCalls []ProviderBuildRunInputV1
	var order []string
	runRuntimeProviderBuild = func(ctx context.Context, input ProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
		if ctx == nil || input.DeploymentDir != dir || !input.Automatic {
			t.Fatalf("provider build input = %#v", input)
		}
		buildCalls = append(buildCalls, input)
		order = append(order, "build")
		return LockedProviderBuildExecutionResultV1{}, nil
	}
	var calls []CurrentWorkloadRunInputV1
	runCurrentWorkload = func(ctx context.Context, input CurrentWorkloadRunInputV1) error {
		if ctx == nil {
			t.Fatal("workload context is nil")
		}
		if input.Notice == nil {
			t.Fatal("quiet current workload notice output is nil")
		}
		calls = append(calls, input)
		order = append(order, input.Action)
		if input.Action == "up" {
			if input.Progress == nil {
				t.Fatal("current workload progress is nil")
			}
			fmt.Fprintln(input.Progress, "workload recovery progress")
		}
		return nil
	}
	var progress bytes.Buffer
	for _, action := range []string{"up", "down", "restart"} {
		options := RuntimeOptions{Dir: dir, Action: action, ControlMode: ControlAdmissionDrainV1, Stdout: io.Discard, Stderr: io.Discard}
		if action == "up" {
			options.Progress = &progress
		}
		if err := Runtime(options); err != nil {
			t.Fatal(err)
		}
	}
	if progress.String() != "prepare current build\nvalidate current build\nworkload recovery progress\n" {
		t.Fatalf("up progress = %q", progress.String())
	}
	if len(calls) != 3 {
		t.Fatalf("workload calls = %#v", calls)
	}
	if len(buildCalls) != 2 {
		t.Fatalf("provider build calls = %#v, want up and restart only", buildCalls)
	}
	if got, want := order, []string{"build", "up", "down", "build", "restart"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatch order = %#v, want %#v", got, want)
	}
	for _, build := range buildCalls {
		if build.DeploymentDir != dir || build.NoCache || build.ValidateLayers {
			t.Fatalf("implicit provider build = %#v", build)
		}
	}
	for index, action := range []string{"up", "down", "restart"} {
		if calls[index].Action != action || calls[index].DeploymentDir != dir || calls[index].ControlMode != ControlAdmissionDrainV1 {
			t.Fatalf("workload call %d = %#v", index, calls[index])
		}
		if calls[index].RunOptions.Stdout != nil || calls[index].RunOptions.Stderr != nil {
			t.Fatalf("quiet workload output = %#v", calls[index].RunOptions)
		}
	}

	if err := Runtime(RuntimeOptions{Dir: dir, Action: "restart", Verbose: true, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if calls[3].RunOptions.Stdout == nil || calls[3].RunOptions.Stderr == nil {
		t.Fatalf("verbose workload output = %#v", calls[3].RunOptions)
	}
}

func TestRuntimeDoesNotBuildInstalledWorkloadControls(t *testing.T) {
	dir := runtimeStateEnvelope(t, "state-v1")
	path := filepath.Join(dir, StateFileName)
	if err := os.WriteFile(path, []byte(`{"schema":"state-v1","deployment":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	originalBuild := runRuntimeProviderBuild
	originalWorkload := runCurrentWorkload
	t.Cleanup(func() {
		runRuntimeProviderBuild = originalBuild
		runCurrentWorkload = originalWorkload
	})
	runRuntimeProviderBuild = func(context.Context, ProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
		t.Fatal("installed runtime control attempted to build")
		return LockedProviderBuildExecutionResultV1{}, nil
	}
	runCurrentWorkload = func(context.Context, CurrentWorkloadRunInputV1) error { return nil }
	for _, action := range []string{"up", "restart"} {
		if err := Runtime(RuntimeOptions{Dir: dir, Action: action}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeDispatchesStateV1Observations(t *testing.T) {
	dir := runtimeStateEnvelope(t, "state-v1")
	original := runCurrentRuntimeObservation
	t.Cleanup(func() { runCurrentRuntimeObservation = original })
	var calls []CurrentRuntimeObservationInputV1
	runCurrentRuntimeObservation = func(ctx context.Context, input CurrentRuntimeObservationInputV1) error {
		if ctx == nil {
			t.Fatal("observation context is nil")
		}
		calls = append(calls, input)
		return nil
	}
	var stdout, stderr bytes.Buffer
	for _, action := range []string{"status", "logs", "ps"} {
		if err := Runtime(RuntimeOptions{Dir: dir, Action: action, Follow: true, Tail: "25", Stdout: &stdout, Stderr: &stderr}); err != nil {
			t.Fatal(err)
		}
	}
	if len(calls) != 3 || calls[0].Action != "status" || calls[1].Action != "logs" || calls[2].Action != "ps" {
		t.Fatalf("observation calls = %#v", calls)
	}
	if !calls[1].Command.Follow || calls[1].Command.Tail != "25" {
		t.Fatalf("log observation = %#v", calls[1])
	}
}

func TestRuntimeRejectsUnknownSchemaAndAction(t *testing.T) {
	dir := runtimeStateEnvelope(t, "state-v2")
	if err := Runtime(RuntimeOptions{Dir: dir, Action: "status"}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown schema error = %v", err)
	}
	dir = runtimeStateEnvelope(t, "state-v1")
	if err := Runtime(RuntimeOptions{Dir: dir, Action: "explode"}); err == nil || !strings.Contains(err.Error(), "unsupported runtime action") {
		t.Fatalf("unknown action error = %v", err)
	}
	if _, err := RuntimeCommand(runtimeCommandDeployment(t, "demo"), "explode"); err == nil {
		t.Fatal("RuntimeCommand accepted an unknown action")
	}
}

func TestExtractRuntimeStartupLogSnippetUsesOnlyMarkerWindows(t *testing.T) {
	logs := strings.Join([]string{
		"app | before marker",
		"app | reploy:event phase=config-check event=start",
		"app | current config failure",
		"app | reploy:event phase=config-check event=end status=failed exit=2",
		"app | between phases",
		"app | reploy:event phase=service event=start",
		"app | current service failure",
		"",
	}, "\n")
	diagnostics := extractRuntimeStartupLogDiagnostics(logs)
	if diagnostics.Failure != "config check failed (exit code 2)" {
		t.Fatalf("failure = %q", diagnostics.Failure)
	}
	for _, want := range []string{"current config failure", "current service failure"} {
		if !strings.Contains(diagnostics.Snippet, want) {
			t.Fatalf("snippet missing %q:\n%s", want, diagnostics.Snippet)
		}
	}
	for _, unwanted := range []string{"before marker", "between phases", "reploy:event"} {
		if strings.Contains(diagnostics.Snippet, unwanted) {
			t.Fatalf("snippet contains %q:\n%s", unwanted, diagnostics.Snippet)
		}
	}
}

func runtimeStateEnvelope(t *testing.T, schema string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, StateFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":"`+schema+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runtimeCommandDeployment(t *testing.T, project string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, DockerEnvFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("REPLOY_CONTAINER_NAME="+project+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
