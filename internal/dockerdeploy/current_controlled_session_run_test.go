package dockerdeploy

import (
	"context"
	"errors"
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
		privateEnv: func(string) (privateWorkloadEnvironmentV1, error) {
			return privateWorkloadEnvironmentV1{}, nil
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
			if (dir != controllerDir && dir != workloadDir) || wait {
				t.Fatalf("admission = dir %q wait %t", dir, wait)
			}
			if err := operations[controllerDir].RequireHeld(); err != nil {
				t.Fatalf("controller generation lock was not held through admission: %v", err)
			}
			wantGeneration := fixture.WorkloadCurrent.Generation.Reference
			if dir == controllerDir {
				wantGeneration = fixture.ControllerCurrent.Generation.Reference
			}
			if candidate.GenerationReference != wantGeneration || !candidate.Exclusive || candidate.WritableMount != "workspace" || !reflect.DeepEqual(candidate.WritablePaths, []string{"/workspace"}) {
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
		run: func(_ context.Context, operation *deploy.OperationLock, controllerOperation *deploy.OperationLock, plan ControlledSessionExecutionPlanV1, gotOptions ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error) {
			if err := operation.RequireHeld(); err != nil {
				t.Fatalf("workload operation was not transferred held: %v", err)
			}
			if controllerOperation != operations[controllerDir] {
				t.Fatal("controller operation was not transferred to supervisor")
			}
			if err := controllerOperation.RequireHeld(); err != nil {
				t.Fatalf("controller lifetime reservation was not held: %v", err)
			}
			probeContext, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			probe, probeErr := deploy.AcquireOperationLock(probeContext, controllerDir)
			if probe != nil {
				_ = probe.Unlock()
			}
			if !errors.Is(probeErr, context.DeadlineExceeded) {
				t.Fatalf("competing controller operation acquired during session: %v", probeErr)
			}
			if plan.Authorization.Handle != "session-"+strings.Repeat("a", 64) || plan.LiveRunID != "run-0000000000000043" || !reflect.DeepEqual(gotOptions, options) {
				t.Fatalf("supervisor input = plan %#v options %#v", plan, gotOptions)
			}
			if _, removed, err := operation.RemoveLiveRunV1(plan.LiveRunID); err != nil || !removed {
				t.Fatalf("remove admitted run = %t, %v", removed, err)
			}
			if _, removed, err := controllerOperation.RemoveLiveRunV1(plan.LiveRunID); err != nil || !removed {
				t.Fatalf("remove controller reservation = %t, %v", removed, err)
			}
			if err := operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			if err := controllerOperation.Unlock(); err != nil {
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
	controllerOperation, err := deploy.AcquireOperationLock(t.Context(), controllerDir)
	if err != nil {
		t.Fatalf("controller reservation was not released after session: %v", err)
	}
	if err := controllerOperation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRunCurrentControlledSessionV1RejectsActiveControllerBeforeWorkloadAdmission(t *testing.T) {
	root := t.TempDir()
	controllerDir := filepath.Join(root, "controller")
	workloadDir := filepath.Join(root, "workload")
	for _, dir := range []string{controllerDir, workloadDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := controlledSessionPlanFixtureV1(t)
	fixture.ControllerRuntime.Docker.DeploymentDir = controllerDir
	fixture.WorkloadRuntime.Docker.DeploymentDir = workloadDir

	controllerOperation, err := deploy.AcquireOperationLock(t.Context(), controllerDir)
	if err != nil {
		t.Fatal(err)
	}
	const existingID = "run-0000000000000042"
	existingLease, err := controllerOperation.AcquireLiveRunLeaseV1(existingID)
	if err != nil {
		t.Fatal(err)
	}
	defer existingLease.Release()
	if status, err := controllerOperation.AdmitLiveRunV1(deploy.LiveRunV1{
		ID: existingID, Kind: deploy.LiveRunKindAppV1, Name: "existing-controller",
		GenerationReference: fixture.ControllerCurrent.Generation.Reference,
	}, false); err != nil || status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("existing controller admission = %q, %v", status, err)
	}
	if err := controllerOperation.Unlock(); err != nil {
		t.Fatal(err)
	}

	workloadAdmissionAttempted := false
	_, err = runCurrentControlledSessionV1(t.Context(), CurrentControlledSessionRunInputV1{
		ControllerDeploymentDir: controllerDir,
		WorkloadDeploymentDir:   workloadDir,
	}, currentControlledSessionRunBackendV1{
		acquire: deploy.AcquireOperationLock,
		loadRuntime: func(_ context.Context, _ *deploy.OperationLock, dir string, _ StagedProviderBuildRuntimeV1) (currentControlledSessionRuntimeV1, error) {
			if dir == controllerDir {
				return currentControlledSessionRuntimeV1{current: fixture.ControllerCurrent, plan: fixture.ControllerRuntime}, nil
			}
			return currentControlledSessionRuntimeV1{current: fixture.WorkloadCurrent, plan: fixture.WorkloadRuntime}, nil
		},
		privateEnv: func(string) (privateWorkloadEnvironmentV1, error) { return privateWorkloadEnvironmentV1{}, nil },
		concurrency: func(blueprint.Document, DockerExecutionPlan, *transientOutputMount) (LiveRunConcurrencyDecisionV1, error) {
			return LiveRunConcurrencyDecisionV1{AllowsOverlap: true}, nil
		},
		newRunID:  func() (string, error) { return "run-0000000000000043", nil },
		newHandle: func() (string, error) { return "session-" + strings.Repeat("a", 64), nil },
		acquireLease: func(operation *deploy.OperationLock, id string) (*deploy.QueueEntryLeaseV1, error) {
			return operation.AcquireLiveRunLeaseV1(id)
		},
		await: func(ctx context.Context, dir string, operation *deploy.OperationLock, candidate deploy.LiveRunV1, wait bool, notice io.Writer) (*deploy.OperationLock, error) {
			if dir == workloadDir {
				workloadAdmissionAttempted = true
			}
			return AwaitLiveRunAdmissionWithNoticeV1(ctx, dir, operation, candidate, wait, notice)
		},
		plan: func(ControlledSessionPlanInputV1) (ControlledSessionExecutionPlanV1, error) {
			return ControlledSessionExecutionPlanV1{}, nil
		},
		run: func(context.Context, *deploy.OperationLock, *deploy.OperationLock, ControlledSessionExecutionPlanV1, ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error) {
			return ControlledSessionRunResultV1{}, errors.New("must not run")
		},
	})
	if !errors.Is(err, deploy.ErrLiveRunConflict) || workloadAdmissionAttempted {
		t.Fatalf("controller conflict = workload attempted %t, error %v", workloadAdmissionAttempted, err)
	}

	inspection, err := deploy.AcquireOperationLock(t.Context(), controllerDir)
	if err != nil {
		t.Fatal(err)
	}
	queue, _, err := inspection.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != existingID {
		t.Fatalf("controller queue = %#v, %v", queue, err)
	}
	if _, removed, err := inspection.RemoveLiveRunV1(existingID); err != nil || !removed {
		t.Fatalf("remove existing controller = %t, %v", removed, err)
	}
	if err := inspection.Unlock(); err != nil {
		t.Fatal(err)
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
		privateEnv: func(string) (privateWorkloadEnvironmentV1, error) {
			return privateWorkloadEnvironmentV1{}, nil
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
		run: func(context.Context, *deploy.OperationLock, *deploy.OperationLock, ControlledSessionExecutionPlanV1, ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error) {
			return ControlledSessionRunResultV1{}, nil
		},
	})
	if err == nil || called {
		t.Fatalf("same-directory result = called %t, error %v", called, err)
	}
}

func TestRunCurrentControlledSessionV1RejectsConfiguredPrivateEnvironmentBeforePlanning(t *testing.T) {
	for _, configuredRole := range []string{"controller", "workload"} {
		t.Run(configuredRole, func(t *testing.T) {
			root := t.TempDir()
			controllerDir := filepath.Join(root, "controller")
			workloadDir := filepath.Join(root, "workload")
			for _, dir := range []string{controllerDir, workloadDir} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			configuredDir := controllerDir
			if configuredRole == "workload" {
				configuredDir = workloadDir
			}
			if created, err := publishPrivateWorkloadEnvironmentFileV1(
				filepath.Join(configuredDir, PrivateWorkloadEnvironmentFileName),
				[]byte("TOKEN=private\n"),
				false,
			); err != nil || !created {
				t.Fatalf("create private environment = %t, %v", created, err)
			}
			planned := false
			_, err := runCurrentControlledSessionV1(t.Context(), CurrentControlledSessionRunInputV1{
				ControllerDeploymentDir: controllerDir,
				WorkloadDeploymentDir:   workloadDir,
			}, currentControlledSessionRunBackendV1{
				acquire: deploy.AcquireOperationLock,
				loadRuntime: func(context.Context, *deploy.OperationLock, string, StagedProviderBuildRuntimeV1) (currentControlledSessionRuntimeV1, error) {
					return currentControlledSessionRuntimeV1{}, nil
				},
				privateEnv: preparePrivateWorkloadEnvironmentV1,
				concurrency: func(blueprint.Document, DockerExecutionPlan, *transientOutputMount) (LiveRunConcurrencyDecisionV1, error) {
					return LiveRunConcurrencyDecisionV1{}, nil
				},
				newRunID:  func() (string, error) { return "run-0000000000000043", nil },
				newHandle: func() (string, error) { return "session-" + strings.Repeat("a", 64), nil },
				acquireLease: func(*deploy.OperationLock, string) (*deploy.QueueEntryLeaseV1, error) {
					return nil, errors.New("must not acquire admission lease")
				},
				await: func(context.Context, string, *deploy.OperationLock, deploy.LiveRunV1, bool, io.Writer) (*deploy.OperationLock, error) {
					return nil, errors.New("must not admit")
				},
				plan: func(ControlledSessionPlanInputV1) (ControlledSessionExecutionPlanV1, error) {
					planned = true
					return ControlledSessionExecutionPlanV1{}, nil
				},
				run: func(context.Context, *deploy.OperationLock, *deploy.OperationLock, ControlledSessionExecutionPlanV1, ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error) {
					return ControlledSessionRunResultV1{}, errors.New("must not run")
				},
			})
			if err == nil || !strings.Contains(err.Error(), "private environment injection for the "+configuredRole) || planned {
				t.Fatalf("private-environment result = planned %t, error %v", planned, err)
			}
		})
	}
}

func TestRequireDistinctControlledSessionDeploymentFilesV1RejectsFilesystemAlias(t *testing.T) {
	directory := t.TempDir()
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	err = requireDistinctControlledSessionDeploymentFilesV1(
		"/controller", "/CONTROLLER",
		func(string) (os.FileInfo, error) { return info, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "requires distinct") {
		t.Fatalf("filesystem-alias error = %v", err)
	}
}
