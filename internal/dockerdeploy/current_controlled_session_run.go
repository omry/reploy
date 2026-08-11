package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

// CurrentControlledSessionRunInputV1 selects two exact current generations and
// one declared controller command for a controlled session. Admission is
// intentionally immediate: a future queued form must preserve the same atomic
// two-deployment generation check while it waits.
type CurrentControlledSessionRunInputV1 struct {
	ControllerDeploymentDir string
	WorkloadDeploymentDir   string
	ControllerCommand       string
	ControllerArguments     []string
	EndpointIDs             []string
	InitialColumns          uint32
	InitialRows             uint32
	Runtime                 StagedProviderBuildRuntimeV1
	SupervisorOptions       ControlledSessionRunOptionsV1
	Notice                  io.Writer
}

type currentControlledSessionRuntimeV1 struct {
	current CurrentBuild
	plan    CurrentRuntimePlanV1
}

type currentControlledSessionRunBackendV1 struct {
	acquire      func(context.Context, string) (*deploy.OperationLock, error)
	loadRuntime  func(context.Context, *deploy.OperationLock, string, StagedProviderBuildRuntimeV1) (currentControlledSessionRuntimeV1, error)
	privateEnv   func(string) (privateWorkloadEnvironmentV1, error)
	concurrency  func(blueprint.Document, DockerExecutionPlan, *transientOutputMount) (LiveRunConcurrencyDecisionV1, error)
	newRunID     func() (string, error)
	newHandle    func() (string, error)
	acquireLease func(*deploy.OperationLock, string) (*deploy.QueueEntryLeaseV1, error)
	await        func(context.Context, string, *deploy.OperationLock, deploy.LiveRunV1, bool, io.Writer) (*deploy.OperationLock, error)
	plan         func(ControlledSessionPlanInputV1) (ControlledSessionExecutionPlanV1, error)
	run          func(context.Context, *deploy.OperationLock, *deploy.OperationLock, ControlledSessionExecutionPlanV1, ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error)
}

// RunCurrentControlledSessionV1 validates and admits one exact controller and
// workload pair before delegating all resource and lifecycle ownership to the
// controlled-session supervisor. It retains the controller deployment lock for
// the supervisor's complete lifetime, so another app, shell, control operation,
// or controlled session cannot use that controller generation concurrently.
func RunCurrentControlledSessionV1(
	ctx context.Context,
	input CurrentControlledSessionRunInputV1,
) (ControlledSessionRunResultV1, error) {
	return runCurrentControlledSessionV1(ctx, input, currentControlledSessionRunBackendV1{
		acquire:     deploy.AcquireOperationLock,
		loadRuntime: loadCurrentControlledSessionRuntimeV1,
		privateEnv:  preparePrivateWorkloadEnvironmentV1,
		concurrency: PlanLiveRunConcurrencyV1,
		newRunID:    deploy.NewLiveRunIDV1,
		newHandle:   controlledsession.NewHandleV1,
		acquireLease: func(operation *deploy.OperationLock, id string) (*deploy.QueueEntryLeaseV1, error) {
			return operation.AcquireLiveRunLeaseV1(id)
		},
		await: AwaitLiveRunAdmissionWithNoticeV1,
		plan:  PlanControlledSessionV1,
		run:   runControlledSessionWithControllerReservationV1,
	})
}

func runCurrentControlledSessionV1(
	ctx context.Context,
	input CurrentControlledSessionRunInputV1,
	backend currentControlledSessionRunBackendV1,
) (result ControlledSessionRunResultV1, err error) {
	if ctx == nil {
		return result, fmt.Errorf("run current controlled session requires a context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if backend.acquire == nil || backend.loadRuntime == nil || backend.privateEnv == nil || backend.concurrency == nil || backend.newRunID == nil || backend.newHandle == nil || backend.acquireLease == nil || backend.await == nil || backend.plan == nil || backend.run == nil {
		return result, fmt.Errorf("run current controlled session requires a complete backend")
	}
	controllerDir, workloadDir, err := controlledSessionDeploymentDirectoriesV1(input.ControllerDeploymentDir, input.WorkloadDeploymentDir)
	if err != nil {
		return result, err
	}

	lockDirectories := []string{controllerDir, workloadDir}
	sort.Strings(lockDirectories)
	operations := map[string]*deploy.OperationLock{}
	runID := ""
	for _, dir := range lockDirectories {
		operation, acquireErr := backend.acquire(ctx, dir)
		if acquireErr != nil {
			return result, errors.Join(acquireErr, unlockControlledSessionOperationsV1(operations))
		}
		operations[dir] = operation
	}
	defer func() {
		if err != nil {
			for _, operation := range operations {
				if operation != nil && deploy.ValidateLiveRunIDV1(runID) == nil {
					_, _, removeErr := operation.RemoveLiveRunV1(runID)
					err = errors.Join(err, removeErr)
				}
			}
		}
		err = errors.Join(err, unlockControlledSessionOperationsV1(operations))
	}()

	controller, err := backend.loadRuntime(ctx, operations[controllerDir], controllerDir, input.Runtime)
	if err != nil {
		return result, fmt.Errorf("controlled-session controller runtime: %w", err)
	}
	workload, err := backend.loadRuntime(ctx, operations[workloadDir], workloadDir, input.Runtime)
	if err != nil {
		return result, fmt.Errorf("controlled-session workload runtime: %w", err)
	}
	for _, runtime := range []struct {
		role string
		dir  string
	}{
		{role: "controller", dir: controllerDir},
		{role: "workload", dir: workloadDir},
	} {
		privateEnvironment, privateErr := backend.privateEnv(runtime.dir)
		if privateErr != nil {
			return result, fmt.Errorf("prepare controlled-session %s private environment: %w", runtime.role, privateErr)
		}
		if privateEnvironment.Present {
			return result, fmt.Errorf("plan controlled session does not yet support private environment injection for the %s", runtime.role)
		}
	}
	controllerConcurrency, err := backend.concurrency(controller.plan.Document, controller.plan.Docker, nil)
	if err != nil {
		return result, fmt.Errorf("plan controlled-session controller concurrency: %w", err)
	}
	workloadConcurrency, err := backend.concurrency(workload.plan.Document, workload.plan.Docker, nil)
	if err != nil {
		return result, fmt.Errorf("plan controlled-session workload concurrency: %w", err)
	}
	runID, err = backend.newRunID()
	if err != nil {
		return result, fmt.Errorf("create controlled-session live-run identity: %w", err)
	}
	leases := make([]*deploy.QueueEntryLeaseV1, 0, 2)
	defer func() {
		for _, lease := range leases {
			if leaseErr := lease.Release(); leaseErr != nil {
				err = errors.Join(err, fmt.Errorf("release controlled-session queue ownership: %w", leaseErr))
			}
		}
	}()
	for _, reservation := range []struct {
		role string
		dir  string
	}{
		{role: "controller", dir: controllerDir},
		{role: "workload", dir: workloadDir},
	} {
		lease, acquireErr := backend.acquireLease(operations[reservation.dir], runID)
		if acquireErr != nil {
			return result, fmt.Errorf("acquire controlled-session %s queue ownership: %w", reservation.role, acquireErr)
		}
		leases = append(leases, lease)
	}
	handle, err := backend.newHandle()
	if err != nil {
		return result, fmt.Errorf("create controlled-session handle: %w", err)
	}

	plan, err := backend.plan(ControlledSessionPlanInputV1{
		Handle:                       handle,
		LiveRunID:                    runID,
		ControllerCurrent:            controller.current,
		ControllerRuntime:            controller.plan,
		ControllerCommand:            input.ControllerCommand,
		ControllerForwardedArguments: append([]string(nil), input.ControllerArguments...),
		WorkloadCurrent:              workload.current,
		WorkloadRuntime:              workload.plan,
		EndpointIDs:                  append([]string(nil), input.EndpointIDs...),
		InitialColumns:               input.InitialColumns,
		InitialRows:                  input.InitialRows,
	})
	if err != nil {
		return result, err
	}
	for _, admission := range []struct {
		role        string
		dir         string
		current     CurrentBuild
		plan        CurrentRuntimePlanV1
		concurrency LiveRunConcurrencyDecisionV1
	}{
		{role: "controller", dir: controllerDir, current: controller.current, plan: controller.plan, concurrency: controllerConcurrency},
		{role: "workload", dir: workloadDir, current: workload.current, plan: workload.plan, concurrency: workloadConcurrency},
	} {
		candidate := deploy.LiveRunV1{
			ID:                  runID,
			Kind:                deploy.LiveRunKindShellV1,
			Name:                admission.plan.Document.Environment.ID,
			GenerationReference: admission.current.Generation.Reference,
			Exclusive:           admission.role == "controller" || !admission.concurrency.AllowsOverlap,
			WritableMount:       admission.concurrency.WritableMount,
			WritablePaths:       admission.concurrency.WritablePaths,
		}
		operations[admission.dir], err = backend.await(
			ctx, admission.dir, operations[admission.dir], candidate, false, input.Notice,
		)
		if err != nil {
			delete(operations, admission.dir)
			return result, err
		}
	}
	workloadOperation := operations[workloadDir]
	controllerOperation := operations[controllerDir]
	delete(operations, workloadDir)
	delete(operations, controllerDir)
	return backend.run(ctx, workloadOperation, controllerOperation, plan, input.SupervisorOptions)
}

func controlledSessionDeploymentDirectoriesV1(controller string, workload string) (string, string, error) {
	if controller == "" {
		return "", "", fmt.Errorf("run current controlled session requires a controller deployment directory")
	}
	if workload == "" {
		return "", "", fmt.Errorf("run current controlled session requires a workload deployment directory")
	}
	controllerDir, err := canonicalPathAllowMissingV1(controller)
	if err != nil {
		return "", "", fmt.Errorf("resolve controlled-session controller deployment directory: %w", err)
	}
	workloadDir, err := canonicalPathAllowMissingV1(workload)
	if err != nil {
		return "", "", fmt.Errorf("resolve controlled-session workload deployment directory: %w", err)
	}
	if controllerDir == workloadDir {
		return "", "", fmt.Errorf("run current controlled session requires distinct controller and workload deployment directories")
	}
	if err := requireDistinctControlledSessionDeploymentFilesV1(controllerDir, workloadDir, os.Stat); err != nil {
		return "", "", err
	}
	return controllerDir, workloadDir, nil
}

func requireDistinctControlledSessionDeploymentFilesV1(
	controllerDir string,
	workloadDir string,
	stat func(string) (os.FileInfo, error),
) error {
	controllerInfo, err := stat(controllerDir)
	if err != nil {
		return fmt.Errorf("inspect controlled-session controller deployment directory: %w", err)
	}
	workloadInfo, err := stat(workloadDir)
	if err != nil {
		return fmt.Errorf("inspect controlled-session workload deployment directory: %w", err)
	}
	if os.SameFile(controllerInfo, workloadInfo) {
		return fmt.Errorf("run current controlled session requires distinct controller and workload deployment directories")
	}
	return nil
}

func loadCurrentControlledSessionRuntimeV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	dir string,
	runtime StagedProviderBuildRuntimeV1,
) (currentControlledSessionRuntimeV1, error) {
	store, err := providerstore.NewStore(dir)
	if err != nil {
		return currentControlledSessionRuntimeV1{}, err
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return currentControlledSessionRuntimeV1{}, err
	}
	if !found {
		return currentControlledSessionRuntimeV1{}, fmt.Errorf("runtime state is missing; run `reploy stage` or `reploy install`")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return currentControlledSessionRuntimeV1{}, fmt.Errorf("runtime blueprint: %w", err)
	}
	current, found, err := ValidateCurrentBuild(ctx, operation, store, document.Environment.ID, dir)
	if err != nil {
		return currentControlledSessionRuntimeV1{}, fmt.Errorf("runtime current build: %w", err)
	}
	if !found {
		return currentControlledSessionRuntimeV1{}, fmt.Errorf("%s", currentBuildRecoveryMessageV1(state, "runtime build is missing"))
	}
	planned, err := PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{
		DeploymentDir: dir,
		Current:       current,
		Runtime:       runtime,
	})
	if err != nil {
		return currentControlledSessionRuntimeV1{}, err
	}
	matched, err := CurrentBuildMatchesRuntimeV1(current, planned.Docker)
	if err != nil {
		return currentControlledSessionRuntimeV1{}, fmt.Errorf("runtime current-build check: %w", err)
	}
	if !matched {
		return currentControlledSessionRuntimeV1{}, fmt.Errorf("%s", currentBuildRecoveryMessageV1(state, "runtime build is missing or stale"))
	}
	return currentControlledSessionRuntimeV1{current: current, plan: planned}, nil
}

func unlockControlledSessionOperationsV1(operations map[string]*deploy.OperationLock) error {
	directories := make([]string, 0, len(operations))
	for dir := range operations {
		directories = append(directories, dir)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	var err error
	for _, dir := range directories {
		if operation := operations[dir]; operation != nil {
			err = errors.Join(err, operation.Unlock())
		}
	}
	return err
}
