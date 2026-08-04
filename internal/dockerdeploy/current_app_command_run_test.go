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

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunCurrentAppCommandV1OrdersStaleCheckOutputGateAndContainer(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}}
	order := []string{}
	backend := currentAppCommandRunTestBackend(t, dir, current, planned, &order)
	originalExecution := backend.execution
	backend.execution = func(plan DockerExecutionPlan, command ResolvedEnvironmentCommand, output *transientOutputMount, runID string, interactive bool, tty bool) (TransientContainerExecutionV1, error) {
		if output == nil || !interactive || !tty {
			t.Fatalf("execution input = %#v, interactive=%t, tty=%t", output, interactive, tty)
		}
		return originalExecution(plan, command, output, runID, interactive, tty)
	}
	var stdout bytes.Buffer

	err := runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{
		DeploymentDir: dir, Arguments: []string{"export", "--format", "json"},
		Runtime: StagedProviderBuildRuntimeV1{Host: "linux", UID: 1000, GID: 1000},
		TTY:     true, RunOptions: RunOptions{Stdin: strings.NewReader("input"), Stdout: &stdout},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acquire", "store", "state", "current", "plan runtime", "match", "plan command", "prepare output", "invocation", "concurrency", "run id", "admit", "final gate", "execution", "run admitted", "publish output"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if stdout.String() != "[STAGING : demo] command output\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunCurrentAppCommandV1DoesNotReserveOutputForStaleBuild(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentAppCommandRunTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order)
	backend.matches = func(CurrentBuild, DockerExecutionPlan) (bool, error) {
		order = append(order, "match")
		return false, nil
	}
	err := runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"export"}}, backend)
	if err == nil || !strings.Contains(err.Error(), "reploy build") {
		t.Fatalf("stale build error = %v", err)
	}
	if containsRuntimeObservationStep(order, "plan command", "prepare output", "final gate", "temporary") {
		t.Fatalf("stale build reached output or runtime work: %v", order)
	}
}

func TestRunCurrentAppCommandV1RequiresExplicitBuildWhenCurrentGenerationIsMissing(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	current.State.Current = nil
	order := []string{}
	backend := currentAppCommandRunTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order)
	backend.loadCurrent = func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
		order = append(order, "current")
		return CurrentBuild{}, false, nil
	}

	err := runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{
		DeploymentDir: dir, Arguments: []string{"export"},
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "runtime build is missing; run `reploy build`") {
		t.Fatalf("missing build error = %v", err)
	}
	if containsRuntimeObservationStep(order, "plan runtime", "prepare output", "admit", "execution", "run admitted") {
		t.Fatalf("missing build reached runtime work: %v", order)
	}
}

func TestRunCurrentAppCommandV1RejectsDeployedOnlyAgainstStagingState(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentAppCommandRunTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order)
	err := runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{
		DeploymentDir: dir, Arguments: []string{"export"}, DeployedOnly: true,
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "installed deployment") {
		t.Fatalf("deployed-only staging error = %v", err)
	}
	if containsRuntimeObservationStep(order, "current", "plan runtime", "prepare output", "final gate") {
		t.Fatalf("deployed-only staging reached runtime work: %v", order)
	}
}

func TestRunCurrentAppCommandV1AbortsReservedOutputAfterContainerFailure(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentAppCommandRunTestBackend(t, dir, current, CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}}, &order)
	want := errors.New("container failed")
	backend.runAdmitted = func(context.Context, string, *deploy.OperationLock, string, TransientContainerExecutionV1, RunOptions) error {
		order = append(order, "run admitted")
		return want
	}
	err := runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"export"}}, backend)
	if err == nil || !strings.Contains(err.Error(), "app command failed") {
		t.Fatalf("container error = %v", err)
	}
	if !containsRuntimeObservationStep(order, "abort output") || containsRuntimeObservationStep(order, "publish output") {
		t.Fatalf("failed output lifecycle = %v", order)
	}
}

func TestRunCurrentAppCommandV1DoesNotPublishOutputAfterIntentionalStop(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentAppCommandRunTestBackend(t, dir, current, CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}}, &order)
	backend.runAdmitted = func(context.Context, string, *deploy.OperationLock, string, TransientContainerExecutionV1, RunOptions) error {
		order = append(order, "run admitted")
		return ErrLiveRunStoppedV1
	}
	err := runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"export"}}, backend)
	if !errors.Is(err, ErrLiveRunStoppedV1) {
		t.Fatalf("intentional stop error = %v", err)
	}
	if !containsRuntimeObservationStep(order, "abort output") || containsRuntimeObservationStep(order, "publish output") {
		t.Fatalf("stopped output lifecycle = %v", order)
	}
}

func TestRunCurrentAppCommandV1PreservesFinalGateCorrection(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentAppCommandRunTestBackend(t, dir, current, CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}}, &order)
	want := errors.New("runtime host-source check: mount changed; run `reploy build`")
	backend.runPublished = func(context.Context, PublishedRuntimeContainerInput, PublishedRuntimeContainerRunnerV1) error {
		order = append(order, "final gate")
		return want
	}
	err := runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"export"}}, backend)
	if !errors.Is(err, want) || strings.Contains(err.Error(), "app command failed") {
		t.Fatalf("final gate error = %v", err)
	}
	if !containsRuntimeObservationStep(order, "abort output") || containsRuntimeObservationStep(order, "temporary", "publish output") {
		t.Fatalf("final gate output lifecycle = %v", order)
	}
}

func TestRunCurrentAppCommandV1RejectsGenerationChangeAfterAdmission(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentAppCommandRunTestBackend(t, dir, current, CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}}, &order)
	originalAwait := backend.await
	backend.await = func(ctx context.Context, gotDir string, operation *deploy.OperationLock, candidate deploy.LiveRunV1, wait bool, notice io.Writer) (*deploy.OperationLock, error) {
		if !wait {
			t.Fatal("wait option was not forwarded to admission")
		}
		return originalAwait(ctx, gotDir, operation, candidate, wait, notice)
	}
	backend.runPublished = func(ctx context.Context, input PublishedRuntimeContainerInput, run PublishedRuntimeContainerRunnerV1) error {
		order = append(order, "final gate")
		changed := current
		changed.Generation.Reference = "reploy/env/demo:g-reinstalled"
		return run(ctx, changed)
	}
	err := runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"export"}, Wait: true}, backend)
	if err == nil || !strings.Contains(err.Error(), "generation changed") || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("generation change error = %v", err)
	}
	if !containsRuntimeObservationStep(order, "abort output") || containsRuntimeObservationStep(order, "execution", "run admitted", "publish output") {
		t.Fatalf("generation change lifecycle = %v", order)
	}
}

func currentAppCommandRunTestBackend(
	t *testing.T,
	dir string,
	current CurrentBuild,
	planned CurrentRuntimePlanV1,
	order *[]string,
) currentAppCommandRunBackendV1 {
	t.Helper()
	return currentAppCommandRunBackendV1{
		acquire: func(ctx context.Context, got string) (*deploy.OperationLock, error) {
			*order = append(*order, "acquire")
			if got != filepath.Clean(dir) {
				t.Fatalf("deployment dir = %q", got)
			}
			return deploy.AcquireOperationLock(ctx, got)
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
		planRuntime: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			*order = append(*order, "plan runtime")
			return planned, nil
		},
		matches: func(CurrentBuild, DockerExecutionPlan) (bool, error) {
			*order = append(*order, "match")
			return true, nil
		},
		planCommand: func(CurrentRuntimePlanV1, []providers.RealizedOutput, []string, bool) (ResolvedEnvironmentCommand, error) {
			*order = append(*order, "plan command")
			return ResolvedEnvironmentCommand{Name: "export", Argv: []string{"/opt/demo", "export"}}, nil
		},
		prepareOutput: func(_ string, _ string, runtimeUser RuntimeUserPlan) (*oneShotOutputSession, error) {
			*order = append(*order, "prepare output")
			if !reflect.DeepEqual(runtimeUser, planned.Docker.Sandbox.RuntimeUser) {
				t.Fatalf("output runtime user = %#v, want %#v", runtimeUser, planned.Docker.Sandbox.RuntimeUser)
			}
			return &oneShotOutputSession{mount: &transientOutputMount{
				HostDirectory: dir, Variable: runtimeOutputDirectoryVariable, ContainerPath: runtimeOutputRoot,
			}}, nil
		},
		abortOutput: func(*oneShotOutputSession) error {
			*order = append(*order, "abort output")
			return nil
		},
		publishOutput: func(*oneShotOutputSession) error {
			*order = append(*order, "publish output")
			return nil
		},
		invocation: func(DockerExecutionPlan, string, *transientOutputMount) (RuntimeInvocationV1, error) {
			*order = append(*order, "invocation")
			return RuntimeInvocationV1{PlanID: "command:export:output"}, nil
		},
		concurrency: func(_ blueprint.Document, _ DockerExecutionPlan, output *transientOutputMount) (LiveRunConcurrencyDecisionV1, error) {
			*order = append(*order, "concurrency")
			if output == nil {
				t.Fatal("concurrency plan did not receive output mount")
			}
			return LiveRunConcurrencyDecisionV1{WritableMount: "--output-dir"}, nil
		},
		newRunID: func() (string, error) {
			*order = append(*order, "run id")
			return "run-0000000000000001", nil
		},
		acquireLease: func(operation *deploy.OperationLock, id string) (*deploy.QueueEntryLeaseV1, error) {
			return operation.AcquireLiveRunLeaseV1(id)
		},
		await: func(_ context.Context, gotDir string, operation *deploy.OperationLock, candidate deploy.LiveRunV1, wait bool, _ io.Writer) (*deploy.OperationLock, error) {
			*order = append(*order, "admit")
			if gotDir != filepath.Clean(dir) || candidate.ID != "run-0000000000000001" || candidate.Kind != deploy.LiveRunKindAppV1 || candidate.Name != "export" || candidate.GenerationReference != current.Generation.Reference || !candidate.Exclusive || candidate.WritableMount != "--output-dir" {
				t.Fatalf("admission input = %q, %#v, wait=%t", gotDir, candidate, wait)
			}
			return operation, nil
		},
		runPublished: func(ctx context.Context, input PublishedRuntimeContainerInput, run PublishedRuntimeContainerRunnerV1) error {
			*order = append(*order, "final gate")
			if err := input.Operation.RequireHeld(); err != nil {
				t.Fatalf("operation lock was not held: %v", err)
			}
			return run(ctx, current)
		},
		execution: func(_ DockerExecutionPlan, _ ResolvedEnvironmentCommand, _ *transientOutputMount, runID string, _ bool, _ bool) (TransientContainerExecutionV1, error) {
			*order = append(*order, "execution")
			return TransientContainerExecutionV1{Container: "demo-" + runID}, nil
		},
		runAdmitted: func(_ context.Context, gotDir string, operation *deploy.OperationLock, runID string, execution TransientContainerExecutionV1, options RunOptions) error {
			*order = append(*order, "run admitted")
			if gotDir != filepath.Clean(dir) || runID != "run-0000000000000001" || execution.Container != "demo-"+runID {
				t.Fatalf("admitted execution = %q, %q, %#v", gotDir, runID, execution)
			}
			if err := operation.RequireHeld(); err != nil {
				t.Fatalf("admitted operation lock = %v", err)
			}
			if options.Context == nil || options.Stdin == nil {
				t.Fatalf("run options = %#v", options)
			}
			_, err := io.WriteString(options.Stdout, "command output\n")
			return err
		},
	}
}
