package dockerdeploy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestRunCurrentControlledSessionV1AdmitsExactPairAndDelegatesSupervisor(t *testing.T) {
	root := t.TempDir()
	controllerDir := filepath.Join(root, "z-controller")
	workloadDir := filepath.Join(root, "a-workload")
	for _, dir := range []string{controllerDir, workloadDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, planBackend := controlledSessionPlanFixtureV1(t)
	fixture.ControllerRuntime.Docker.DeploymentDir = controllerDir
	fixture.WorkloadRuntime.Docker.DeploymentDir = workloadDir

	var acquired []string
	operations := map[string]*deploy.OperationLock{}
	wantResult := ControlledSessionRunResultV1{ResultDelivered: true}
	options := testControlledSessionRunOptionsV1()
	backend := currentControlledSessionRunBackendV1{
		acquire: func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
			acquired = append(acquired, dir)
			operation, err := deploy.AcquireOperationLock(ctx, dir)
			if err == nil {
				operations[dir] = operation
			}
			return operation, err
		},
		loadRuntime: func(_ context.Context, operation *deploy.OperationLock, dir string, _ StagedProviderBuildRuntimeV1) (currentControlledSessionRuntimeV1, error) {
			if err := operation.RequireHeld(); err != nil {
				t.Fatalf("load runtime operation for %q is not held: %v", dir, err)
			}
			switch dir {
			case controllerDir:
				return currentControlledSessionRuntimeV1{current: fixture.ControllerCurrent, plan: fixture.ControllerRuntime}, nil
			case workloadDir:
				return currentControlledSessionRuntimeV1{current: fixture.WorkloadCurrent, plan: fixture.WorkloadRuntime}, nil
			default:
				t.Fatalf("unexpected runtime directory %q", dir)
				return currentControlledSessionRuntimeV1{}, nil
			}
		},
		concurrency: func(_ blueprint.Document, _ DockerExecutionPlan, _ *transientOutputMount) (LiveRunConcurrencyDecisionV1, error) {
			return LiveRunConcurrencyDecisionV1{AllowsOverlap: false, WritableMount: "workspace", WritablePaths: []string{"/workspace"}}, nil
		},
		newRunID:  func() (string, error) { return "run-0000000000000043", nil },
		newHandle: func() (string, error) { return "session-" + strings.Repeat("a", 64), nil },
		acquireLease: func(operation *deploy.OperationLock, id string) (*deploy.QueueEntryLeaseV1, error) {
			return operation.AcquireLiveRunLeaseV1(id)
		},
		await: func(_ context.Context, dir string, operation *deploy.OperationLock, candidate deploy.LiveRunV1, wait bool, _ io.Writer) (*deploy.OperationLock, error) {
			if dir != workloadDir || wait {
				t.Fatalf("admission = dir %q wait %t", dir, wait)
			}
			if err := operations[controllerDir].RequireHeld(); err != nil {
				t.Fatalf("controller generation lock was not held through admission: %v", err)
			}
			if candidate.GenerationReference != fixture.WorkloadCurrent.Generation.Reference || !candidate.Exclusive || candidate.WritableMount != "workspace" || !reflect.DeepEqual(candidate.WritablePaths, []string{"/workspace"}) {
				t.Fatalf("admission candidate = %#v", candidate)
			}
			status, err := operation.AdmitLiveRunV1(candidate, false)
			if err != nil {
				return nil, err
			}
			if status != deploy.LiveRunStatusActiveV1 {
				t.Fatalf("admission status = %q", status)
			}
			return operation, nil
		},
		plan: func(input ControlledSessionPlanInputV1) (ControlledSessionExecutionPlanV1, error) {
			if input.Handle != "session-"+strings.Repeat("a", 64) || input.LiveRunID != "run-0000000000000043" || input.ControllerCommand != "inspect" || !reflect.DeepEqual(input.ControllerForwardedArguments, []string{"record"}) {
				t.Fatalf("controlled-session plan input = %#v", input)
			}
			return planControlledSessionV1(input, planBackend)
		},
		run: func(_ context.Context, operation *deploy.OperationLock, plan ControlledSessionExecutionPlanV1, gotOptions ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error) {
			if err := operation.RequireHeld(); err != nil {
				t.Fatalf("workload operation was not transferred held: %v", err)
			}
			if err := operations[controllerDir].RequireHeld(); err == nil {
				t.Fatal("controller operation remained held after admission")
			}
			if plan.Authorization.Handle != "session-"+strings.Repeat("a", 64) || plan.LiveRunID != "run-0000000000000043" || !reflect.DeepEqual(gotOptions, options) {
				t.Fatalf("supervisor input = plan %#v options %#v", plan, gotOptions)
			}
			if _, removed, err := operation.RemoveLiveRunV1(plan.LiveRunID); err != nil || !removed {
				t.Fatalf("remove admitted run = %t, %v", removed, err)
			}
			if err := operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			return wantResult, nil
		},
	}

	got, err := runCurrentControlledSessionV1(t.Context(), CurrentControlledSessionRunInputV1{
		ControllerDeploymentDir: controllerDir,
		WorkloadDeploymentDir:   workloadDir,
		ControllerCommand:       "inspect",
		ControllerArguments:     []string{"record"},
		InitialColumns:          100,
		InitialRows:             28,
		SupervisorOptions:       options,
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantResult) {
		t.Fatalf("result = %#v, want %#v", got, wantResult)
	}
	if !reflect.DeepEqual(acquired, []string{workloadDir, controllerDir}) {
		t.Fatalf("operation lock order = %#v", acquired)
	}
}

func TestRunCurrentControlledSessionV1RejectsOneDeploymentBeforeLocking(t *testing.T) {
	dir := t.TempDir()
	called := false
	_, err := runCurrentControlledSessionV1(t.Context(), CurrentControlledSessionRunInputV1{
		ControllerDeploymentDir: dir,
		WorkloadDeploymentDir:   dir,
	}, currentControlledSessionRunBackendV1{
		acquire: func(context.Context, string) (*deploy.OperationLock, error) {
			called = true
			return nil, nil
		},
		loadRuntime: func(context.Context, *deploy.OperationLock, string, StagedProviderBuildRuntimeV1) (currentControlledSessionRuntimeV1, error) {
			return currentControlledSessionRuntimeV1{}, nil
		},
		concurrency: func(blueprint.Document, DockerExecutionPlan, *transientOutputMount) (LiveRunConcurrencyDecisionV1, error) {
			return LiveRunConcurrencyDecisionV1{}, nil
		},
		newRunID:     func() (string, error) { return "", nil },
		newHandle:    func() (string, error) { return "", nil },
		acquireLease: func(*deploy.OperationLock, string) (*deploy.QueueEntryLeaseV1, error) { return nil, nil },
		await: func(context.Context, string, *deploy.OperationLock, deploy.LiveRunV1, bool, io.Writer) (*deploy.OperationLock, error) {
			return nil, nil
		},
		plan: func(ControlledSessionPlanInputV1) (ControlledSessionExecutionPlanV1, error) {
			return ControlledSessionExecutionPlanV1{}, nil
		},
		run: func(context.Context, *deploy.OperationLock, ControlledSessionExecutionPlanV1, ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error) {
			return ControlledSessionRunResultV1{}, nil
		},
	})
	if err == nil || called {
		t.Fatalf("same-directory result = called %t, error %v", called, err)
	}
}
