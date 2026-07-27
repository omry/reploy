package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunCurrentRuntimeObservationV1ChecksPublishedInputsBeforeCommand(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{EnvironmentID: "demo"}}
	order := []string{}
	backend := currentRuntimeObservationTestBackend(t, dir, current, planned, &order)
	originalLogsCommand := backend.logsCommand
	backend.logsCommand = func(containerID string, options RuntimeCommandOptions) (CommandSpec, error) {
		if !options.Follow || options.Tail != "25" {
			t.Fatalf("log options = %#v", options)
		}
		return originalLogsCommand(containerID, options)
	}
	stdout, stderr := io.Discard, io.Discard
	commandOptions := RuntimeCommandOptions{Follow: true, Tail: "25"}

	err := runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{
		DeploymentDir: dir,
		Action:        "logs",
		Runtime:       StagedProviderBuildRuntimeV1{Host: "linux", UID: 1000, GID: 1000},
		Command:       commandOptions,
		RunOptions:    RunOptions{Stdout: stdout, Stderr: stderr},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acquire", "store", "state", "current", "plan", "match", "inputs", "container id", "command logs", "run"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestRuntimeContainerLogsCommandV1MakesTimestampsExplicit(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	plain, err := RuntimeContainerLogsCommandV1(containerID, RuntimeCommandOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if containsRuntimeObservationStep(plain.Args, "--timestamps") {
		t.Fatalf("default logs requested timestamps: %#v", plain.Args)
	}
	timestamped, err := RuntimeContainerLogsCommandV1(containerID, RuntimeCommandOptions{Timestamps: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsRuntimeObservationStep(timestamped.Args, "--timestamps") {
		t.Fatalf("timestamped logs omitted timestamps: %#v", timestamped.Args)
	}
}

func TestRunCurrentRuntimeObservationV1StopsAtStaleBuildOrInputs(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{EnvironmentID: "demo"}}
	order := []string{}
	backend := currentRuntimeObservationTestBackend(t, dir, current, planned, &order)
	backend.matches = func(CurrentBuild, DockerExecutionPlan) (bool, error) {
		order = append(order, "match")
		return false, nil
	}
	err := runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: "status"}, backend)
	if err == nil || !strings.Contains(err.Error(), "reploy build") {
		t.Fatalf("stale build error = %v", err)
	}
	if containsRuntimeObservationStep(order, "inputs", "command status", "run") {
		t.Fatalf("stale build reached a later step: %v", order)
	}

	order = nil
	want := errors.New("runtime inputs are stale")
	backend = currentRuntimeObservationTestBackend(t, dir, current, planned, &order)
	backend.requireInputs = func(*deploy.OperationLock, string, CurrentRuntimePlanV1) error {
		order = append(order, "inputs")
		return want
	}
	err = runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: "ps"}, backend)
	if !errors.Is(err, want) {
		t.Fatalf("stale inputs error = %v", err)
	}
	if containsRuntimeObservationStep(order, "command ps", "run") {
		t.Fatalf("stale inputs reached command execution: %v", order)
	}
}

func TestRunCurrentRuntimeObservationV1RejectsUnsupportedActionAndMissingState(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentRuntimeObservationTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order)
	if err := runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: "up"}, backend); err == nil || !strings.Contains(err.Error(), "ps, status, or logs") {
		t.Fatalf("unsupported action error = %v", err)
	}
	backend.readState = func(*deploy.OperationLock) (deploy.StateV1, bool, error) {
		order = append(order, "state")
		return deploy.StateV1{}, false, nil
	}
	if err := runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: "ps"}, backend); err == nil || !strings.Contains(err.Error(), "reploy stage") {
		t.Fatalf("missing state error = %v", err)
	}
}

func TestRunCurrentRuntimeObservationV1NormalizesStagedAndDeployedStatusButNotLogs(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{EnvironmentID: "demo"}}

	for _, test := range []struct {
		name     string
		action   string
		deployed bool
		want     string
	}{
		{name: "staged status", action: "status", want: strings.Join([]string{
			"[STAGING : demo] status: running",
			"[STAGING : demo] started: 5 seconds ago",
			"[STAGING : demo] endpoint http: http://127.0.0.1:18076",
			"",
		}, "\n")},
		{name: "deployed status", action: "status", deployed: true, want: strings.Join([]string{
			"[DEPLOYED : demo] status: running",
			"[DEPLOYED : demo] started: 5 seconds ago",
			"[DEPLOYED : demo] endpoint http: http://127.0.0.1:18076",
			"",
		}, "\n")},
		{name: "logs", action: "logs", want: "compose output\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			order := []string{}
			testCurrent := current
			if test.deployed {
				testCurrent.State.Deployment = &deploy.DeploymentStateV1{}
			}
			planned.Docker.Workload = &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
				"http": {Scheme: "http", PublishAddress: "127.0.0.1", PublishedPort: 18076},
			}}
			backend := currentRuntimeObservationTestBackend(t, dir, testCurrent, planned, &order)
			originalRun := backend.run
			backend.run = func(spec CommandSpec, options RunOptions) error {
				if err := originalRun(spec, options); err != nil {
					return err
				}
				_, err := io.WriteString(options.Stdout, "compose output\n")
				return err
			}
			var stdout bytes.Buffer
			err := runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{
				DeploymentDir: dir, Action: test.action, RunOptions: RunOptions{Stdout: &stdout},
			}, backend)
			if err != nil {
				t.Fatal(err)
			}
			if stdout.String() != test.want {
				t.Fatalf("output = %q, want %q", stdout.String(), test.want)
			}
		})
	}
}

func currentRuntimeObservationTestBackend(
	t *testing.T,
	dir string,
	current CurrentBuild,
	planned CurrentRuntimePlanV1,
	order *[]string,
) currentRuntimeObservationBackendV1 {
	t.Helper()
	var operation *deploy.OperationLock
	return currentRuntimeObservationBackendV1{
		acquire: func(ctx context.Context, got string) (*deploy.OperationLock, error) {
			*order = append(*order, "acquire")
			if got != filepath.Clean(dir) {
				t.Fatalf("deployment dir = %q", got)
			}
			var err error
			operation, err = deploy.AcquireOperationLock(ctx, got)
			return operation, err
		},
		newStore: func(string) (providerstore.Store, error) {
			*order = append(*order, "store")
			return providerstore.Store{}, nil
		},
		readState: func(*deploy.OperationLock) (deploy.StateV1, bool, error) {
			*order = append(*order, "state")
			return current.State, true, nil
		},
		loadCurrent: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
			*order = append(*order, "current")
			return current, true, nil
		},
		plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			*order = append(*order, "plan")
			return planned, nil
		},
		matches: func(CurrentBuild, DockerExecutionPlan) (bool, error) {
			*order = append(*order, "match")
			return true, nil
		},
		requireInputs: func(operation *deploy.OperationLock, got string, _ CurrentRuntimePlanV1) error {
			*order = append(*order, "inputs")
			if err := operation.RequireHeld(); err != nil {
				t.Fatalf("operation lock was not held: %v", err)
			}
			if got != filepath.Clean(dir) {
				t.Fatalf("runtime input dir = %q", got)
			}
			return nil
		},
		command: func(gotDir string, action string, options RuntimeCommandOptions) (CommandSpec, error) {
			*order = append(*order, "command "+action)
			if gotDir != filepath.Clean(dir) {
				t.Fatalf("command dir = %q", gotDir)
			}
			return CommandSpec{Name: "docker", Args: []string{action}}, nil
		},
		containerID: func(ctx context.Context, plan DockerExecutionPlan, _ time.Duration) (string, error) {
			*order = append(*order, "container id")
			if ctx == nil || operation.RequireHeld() != nil {
				t.Fatal("container ID was not resolved under the operation lock")
			}
			return strings.Repeat("a", 64), nil
		},
		logsCommand: func(containerID string, options RuntimeCommandOptions) (CommandSpec, error) {
			*order = append(*order, "command logs")
			if containerID != strings.Repeat("a", 64) {
				t.Fatalf("logs container ID = %q", containerID)
			}
			return RuntimeContainerLogsCommandV1(containerID, options)
		},
		status: func(context.Context, DockerExecutionPlan, time.Duration) (RuntimeStatusV1, error) {
			*order = append(*order, "status")
			return RuntimeStatusV1{Status: "running", Started: "5 seconds ago"}, nil
		},
		run: func(spec CommandSpec, options RunOptions) error {
			*order = append(*order, "run")
			if spec.Name != "docker" || options.Context == nil {
				t.Fatalf("run = %#v, %#v", spec, options)
			}
			logs := len(spec.Args) > 0 && spec.Args[0] == "logs"
			follow := containsRuntimeObservationStep(spec.Args, "--follow")
			if logs && follow && operation.RequireHeld() == nil {
				t.Fatal("operation lock remained held while following pinned container logs")
			}
			if (!logs || !follow) && operation.RequireHeld() != nil {
				t.Fatal("operation lock was released during finite observation")
			}
			return nil
		},
	}
}

func containsRuntimeObservationStep(steps []string, forbidden ...string) bool {
	for _, step := range steps {
		for _, candidate := range forbidden {
			if step == candidate {
				return true
			}
		}
	}
	return false
}
