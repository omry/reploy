package dockerdeploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestServerStateV1UsesCurrentRuntimeTestAndRejectsUnknownSchema(t *testing.T) {
	dir := runtimeStateEnvelope(t, "state-v1")
	original := runCurrentRuntimeTest
	t.Cleanup(func() { runCurrentRuntimeTest = original })
	var calls []CurrentRuntimeTestInputV1
	runCurrentRuntimeTest = func(ctx context.Context, input CurrentRuntimeTestInputV1) error {
		if ctx == nil {
			t.Fatal("runtime test context is nil")
		}
		calls = append(calls, input)
		return nil
	}
	if err := TestServer(TestOptions{
		Dir: dir, Timeout: 17 * time.Second, Stdout: io.Discard,
		RestartingDiagnostics: "details", DockerPreflightTimeout: 3 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].DeploymentDir != dir || calls[0].Timeout != 17*time.Second || calls[0].Stdout != io.Discard || calls[0].RestartingDiagnostics != "details" || calls[0].DockerPreflightTimeout != 3*time.Second {
		t.Fatalf("current runtime test calls = %#v", calls)
	}
	statePath := filepath.Join(dir, StateFileName)
	if err := os.WriteFile(statePath, []byte(`{"schema":"state-v2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := TestServer(TestOptions{Dir: dir}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown state schema error = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("unknown schema reached current test runner: %d calls", len(calls))
	}
}

func TestInstallSuccessLinesUsesRecordedStateV1Plan(t *testing.T) {
	dir := t.TempDir()
	document := blueprint.Document{
		Environment: blueprint.Environment{
			ID: "demo",
			Workload: &blueprint.Workload{Endpoints: map[string]blueprint.Endpoint{
				"http": {Scheme: "https", Port: 8080},
			}},
			Install: blueprint.Install{Success: blueprint.InstallSuccess{Lines: []string{
				"server url: {{ reploy.workload.endpoints.http.publish.address }}:{{ reploy.workload.endpoints.http.publish.port }}",
			}}},
		},
	}
	state := deploy.StateV1{Blueprint: testResolvedBlueprintV1(t, document), Deployment: &deploy.DeploymentStateV1{}}
	current := CurrentBuild{State: state}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, planned := false, false
	lines, err := installSuccessLinesV1(t.Context(), dir, StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, installSuccessLinesBackendV1{
		acquire: deploy.AcquireOperationLock,
		newStore: func(got string) (providerstore.Store, error) {
			if got != dir {
				t.Fatalf("store dir = %q, want %q", got, dir)
			}
			return store, nil
		},
		readState: func(*deploy.OperationLock) (deploy.StateV1, bool, error) { return state, true, nil },
		loadCurrent: func(_ context.Context, _ *deploy.OperationLock, gotStore providerstore.Store, environment, gotDir string) (CurrentBuild, bool, error) {
			loaded = true
			if gotStore.Root() != store.Root() || environment != "demo" || gotDir != dir {
				t.Fatalf("load current = %q, %q, %q", gotStore.Root(), environment, gotDir)
			}
			return current, true, nil
		},
		plan: func(input CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			planned = true
			return CurrentRuntimePlanV1{Document: document, Docker: DockerExecutionPlan{
				Phase: blueprint.PhaseInstalled,
				Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
					"http": {PublishAddress: "127.0.0.1", PublishedPort: 18080},
				}},
			}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded || !planned || len(lines) != 1 || lines[0] != "server url: 127.0.0.1:18080" {
		t.Fatalf("loaded=%t planned=%t lines=%#v", loaded, planned, lines)
	}
}

func TestRequireComposeServiceRunning(t *testing.T) {
	dir := runtimeCommandDeployment(t, "demo")
	original := runTestCommandOutput
	t.Cleanup(func() { runTestCommandOutput = original })

	for _, test := range []struct {
		name        string
		output      string
		diagnostics string
		want        string
	}{
		{name: "running", output: `[{"State":"running"}]`},
		{name: "absent", output: `[]`, want: "service is not started"},
		{name: "exited", output: `[{"State":"exited","ExitCode":1}]`, want: "exited (exit code 1)"},
		{name: "restarting", output: `[{"State":"restarting"}]`, diagnostics: "latest startup failure", want: "latest startup failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runTestCommandOutput = func(spec CommandSpec, options RunOptions) ([]byte, error) {
				if !containsAdjacent(spec.Args, "--project-name", "demo") || !containsInOrder(spec.Args, []string{"ps", "--all", "--format", "json"}) {
					t.Fatalf("compose ps command = %#v", spec)
				}
				return []byte(test.output), nil
			}
			err := requireComposeServiceRunning(dir, test.diagnostics, 0)
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("service error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestComposeServiceStateParsing(t *testing.T) {
	states, err := parseComposeServiceStates([]byte("{\"State\":\"running\"}\n{\"State\":\"exited\",\"ExitCode\":2}\n{\"State\":\"exited\",\"ExitCode\":0}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(states, ",") != "running,exited (exit code 2),exited" {
		t.Fatalf("states = %#v", states)
	}
	if _, err := parseComposeServiceStates([]byte("not json")); err == nil {
		t.Fatal("invalid compose output was accepted")
	}
}

func TestComposeServiceStatesIncludesCommandFailureOutput(t *testing.T) {
	dir := runtimeCommandDeployment(t, "demo")
	original := runTestCommandOutput
	t.Cleanup(func() { runTestCommandOutput = original })
	runTestCommandOutput = func(CommandSpec, RunOptions) ([]byte, error) {
		return []byte("permission denied while connecting to Docker\n"), errors.New("exit status 1")
	}
	_, err := composeServiceStates(dir, 0)
	if err == nil || !strings.Contains(err.Error(), "exit status 1") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("compose service error = %v", err)
	}
}

func TestCommandOutputSeparatesSuccessfulStderrAndIncludesFailureStderr(t *testing.T) {
	output, err := commandOutput(CommandSpec{Name: "sh", Args: []string{"-c", "printf stdout; printf stderr >&2"}}, RunOptions{})
	if err != nil || string(output) != "stdout" {
		t.Fatalf("successful output = %q, %v", output, err)
	}
	output, err = commandOutput(CommandSpec{Name: "sh", Args: []string{"-c", "printf stdout; printf stderr >&2; exit 7"}}, RunOptions{})
	if err == nil || !strings.Contains(string(output), "stdout") || !strings.Contains(string(output), "stderr") {
		t.Fatalf("failure output = %q, %v", output, err)
	}
}
