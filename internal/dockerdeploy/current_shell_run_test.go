package dockerdeploy

import (
	"context"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunCurrentShellV1OrdersStaleCheckFinalGateAndInteractiveContainer(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}}
	order := []string{}
	backend := currentShellRunTestBackend(t, dir, current, planned, &order)
	err := runCurrentShellV1(t.Context(), CurrentShellRunInputV1{
		DeploymentDir: dir, Runtime: StagedProviderBuildRuntimeV1{Host: "linux", UID: 1000, GID: 1000},
		TTY: true, RunOptions: RunOptions{Stdin: strings.NewReader("input"), Stdout: io.Discard},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acquire", "store", "state", "current", "plan", "match", "invocation", "concurrency", "run id", "admit", "final gate", "prepare probe", "execution", "run admitted", "cleanup probe"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestRunCurrentShellV1NeverCreatesContainerForStaleBuild(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentShellRunTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order)
	backend.matches = func(CurrentBuild, DockerExecutionPlan) (bool, error) {
		order = append(order, "match")
		return false, nil
	}
	err := runCurrentShellV1(t.Context(), CurrentShellRunInputV1{DeploymentDir: dir}, backend)
	if err == nil || !strings.Contains(err.Error(), "reploy build") {
		t.Fatalf("stale shell error = %v", err)
	}
	if containsRuntimeObservationStep(order, "invocation", "final gate", "shell", "temporary") {
		t.Fatalf("stale shell reached container work: %v", order)
	}
}

func TestRunCurrentShellV1ReadOnlyChangesOnlyTransientMounts(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	original := DockerExecutionPlan{
		ContainerName: "demo",
		Mounts: []MountExecutionPlan{
			{Name: "config", Target: "/conf"},
			{Name: "data", Target: "/data"},
		},
	}
	order := []string{}
	backend := currentShellRunTestBackend(t, dir, current, CurrentRuntimePlanV1{Docker: original}, &order)
	backend.matches = func(_ CurrentBuild, plan DockerExecutionPlan) (bool, error) {
		if !reflect.DeepEqual(plan, original) {
			t.Fatalf("published-plan match received %#v, want original %#v", plan, original)
		}
		return true, nil
	}
	backend.concurrency = func(_ blueprint.Document, plan DockerExecutionPlan, _ *transientOutputMount) (LiveRunConcurrencyDecisionV1, error) {
		for _, mount := range plan.Mounts {
			if !mount.ReadOnly {
				t.Fatalf("read-only shell concurrency saw writable mount %#v", mount)
			}
		}
		return LiveRunConcurrencyDecisionV1{AllowsOverlap: true}, nil
	}
	backend.execution = func(plan DockerExecutionPlan, _ PreparedProbeWorkspace, runID string, interactive bool, tty bool) (TransientContainerExecutionV1, error) {
		for _, mount := range plan.Mounts {
			if !mount.ReadOnly {
				t.Fatalf("read-only shell execution saw writable mount %#v", mount)
			}
		}
		if plan.Sandbox.TemporaryHome != original.Sandbox.TemporaryHome {
			t.Fatalf("temporary home changed from %q to %q", original.Sandbox.TemporaryHome, plan.Sandbox.TemporaryHome)
		}
		return TransientContainerExecutionV1{Container: "demo-" + runID}, nil
	}
	if err := runCurrentShellV1(t.Context(), CurrentShellRunInputV1{
		DeploymentDir: dir, ReadOnly: true,
		RunOptions: RunOptions{Stdin: strings.NewReader("input"), Stdout: io.Discard},
	}, backend); err != nil {
		t.Fatal(err)
	}
}

func TestRunCurrentShellV1RejectsGenerationChangeAfterWait(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	order := []string{}
	backend := currentShellRunTestBackend(t, dir, current, CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}}, &order)
	originalAwait := backend.await
	backend.await = func(ctx context.Context, gotDir string, operation *deploy.OperationLock, candidate deploy.LiveRunV1, wait bool, notice io.Writer) (*deploy.OperationLock, error) {
		if !wait {
			t.Fatal("shell wait option was not forwarded")
		}
		return originalAwait(ctx, gotDir, operation, candidate, wait, notice)
	}
	backend.runPublished = func(ctx context.Context, input PublishedRuntimeContainerInput, run PublishedRuntimeContainerRunnerV1) error {
		order = append(order, "final gate")
		changed := current
		changed.Generation.Reference = "reploy/env/demo:g-reinstalled"
		return run(ctx, changed)
	}
	err := runCurrentShellV1(t.Context(), CurrentShellRunInputV1{DeploymentDir: dir, Wait: true}, backend)
	if err == nil || !strings.Contains(err.Error(), "generation changed") || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("shell generation change error = %v", err)
	}
	if containsRuntimeObservationStep(order, "execution", "run admitted") {
		t.Fatalf("shell generation change reached container: %v", order)
	}
}

func currentShellRunTestBackend(t *testing.T, dir string, current CurrentBuild, planned CurrentRuntimePlanV1, order *[]string) currentShellRunBackendV1 {
	t.Helper()
	return currentShellRunBackendV1{
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
		plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			*order = append(*order, "plan")
			return planned, nil
		},
		matches: func(CurrentBuild, DockerExecutionPlan) (bool, error) {
			*order = append(*order, "match")
			return true, nil
		},
		invocation: func(DockerExecutionPlan) (RuntimeInvocationV1, error) {
			*order = append(*order, "invocation")
			return RuntimeInvocationV1{PlanID: runtimeShellPlanID}, nil
		},
		concurrency: func(blueprint.Document, DockerExecutionPlan, *transientOutputMount) (LiveRunConcurrencyDecisionV1, error) {
			*order = append(*order, "concurrency")
			return LiveRunConcurrencyDecisionV1{AllowsOverlap: true}, nil
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
			if gotDir != filepath.Clean(dir) || candidate.ID != "run-0000000000000001" || candidate.Kind != deploy.LiveRunKindShellV1 || candidate.Name != "shell" || candidate.GenerationReference != current.Generation.Reference || candidate.Exclusive || candidate.WritableMount != "" {
				t.Fatalf("shell admission = %q, %#v, wait=%t", gotDir, candidate, wait)
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
		prepareProbe: func(_ context.Context, _ providerstore.Store, platform blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
			*order = append(*order, "prepare probe")
			if !reflect.DeepEqual(platform, current.Lock.Platform) {
				t.Fatalf("probe platform = %#v, want %#v", platform, current.Lock.Platform)
			}
			return PreparedProbeWorkspace{}, func() error {
				*order = append(*order, "cleanup probe")
				return nil
			}, nil
		},
		execution: func(_ DockerExecutionPlan, _ PreparedProbeWorkspace, runID string, interactive bool, tty bool) (TransientContainerExecutionV1, error) {
			*order = append(*order, "execution")
			if !interactive || !tty {
				t.Fatalf("shell interactive=%t, tty=%t", interactive, tty)
			}
			return TransientContainerExecutionV1{Container: "demo-" + runID}, nil
		},
		runAdmitted: func(_ context.Context, gotDir string, operation *deploy.OperationLock, runID string, execution TransientContainerExecutionV1, options RunOptions) error {
			*order = append(*order, "run admitted")
			if gotDir != filepath.Clean(dir) || runID != "run-0000000000000001" || execution.Container != "demo-"+runID {
				t.Fatalf("shell execution = %q, %q, %#v", gotDir, runID, execution)
			}
			if err := operation.RequireHeld(); err != nil {
				t.Fatalf("shell admitted operation = %v", err)
			}
			if options.Context == nil || options.Stdin == nil || options.Stdout != io.Discard {
				t.Fatalf("shell run options = %#v", options)
			}
			return nil
		},
	}
}
