package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

const controlledSessionControllerPackageDefaultCleanupTimeoutV1 = 30 * time.Second

// CurrentControlledSessionRunInputV1 selects two exact current generations,
// one declared controller command, and an optional controller-only output for
// a controlled session. Admission is intentionally immediate: a future queued
// form must preserve the same atomic two-deployment generation check while it
// waits.
type CurrentControlledSessionRunInputV1 struct {
	ControllerDeploymentDir string
	WorkloadDeploymentDir   string
	ControllerCommand       string
	ControllerArguments     []string
	EndpointIDs             []string
	OutputDir               string
	OutputFile              string
	InitialColumns          uint32
	InitialRows             uint32
	Runtime                 StagedProviderBuildRuntimeV1
	SupervisorOptions       ControlledSessionRunOptionsV1
	Notice                  io.Writer
}

type ControlledSessionControllerOutputKindV1 string

const (
	ControlledSessionControllerOutputNotRequestedV1      ControlledSessionControllerOutputKindV1 = "not-requested"
	ControlledSessionControllerOutputDirectoryRetainedV1 ControlledSessionControllerOutputKindV1 = "directory-retained"
	ControlledSessionControllerOutputFilePublishedV1     ControlledSessionControllerOutputKindV1 = "file-published"
	ControlledSessionControllerOutputFileDiscardedV1     ControlledSessionControllerOutputKindV1 = "file-discarded"
	ControlledSessionControllerOutputFailedV1            ControlledSessionControllerOutputKindV1 = "failed"
)

type ControlledSessionControllerOutputStatusV1 struct {
	Kind   ControlledSessionControllerOutputKindV1 `json:"kind"`
	Reason string                                  `json:"reason,omitempty"`
}

type CurrentControlledSessionRunResultV1 struct {
	ControlledSessionRunResultV1
	ControllerOutput ControlledSessionControllerOutputStatusV1
}

type currentControlledSessionRuntimeV1 struct {
	current CurrentBuild
	plan    CurrentRuntimePlanV1
}

type currentControlledSessionRunBackendV1 struct {
	acquire           func(context.Context, string) (*deploy.OperationLock, error)
	loadRuntime       func(context.Context, *deploy.OperationLock, string, StagedProviderBuildRuntimeV1) (currentControlledSessionRuntimeV1, error)
	privateEnv        func(string) (privateWorkloadEnvironmentV1, error)
	prepareController func(context.Context, string, CurrentBuild) (*preparedControlledSessionControllerV1, error)
	cleanupController func(context.Context, *preparedControlledSessionControllerV1) error
	prepareOutput     func(string, string, RuntimeUserPlan) (*oneShotOutputSession, error)
	abortOutput       func(*oneShotOutputSession) error
	publishOutput     func(*oneShotOutputSession) error
	concurrency       func(blueprint.Document, DockerExecutionPlan, *transientOutputMount) (LiveRunConcurrencyDecisionV1, error)
	newRunID          func() (string, error)
	newHandle         func() (string, error)
	acquireLease      func(*deploy.OperationLock, string) (*deploy.QueueEntryLeaseV1, error)
	await             func(context.Context, string, *deploy.OperationLock, deploy.LiveRunV1, bool, io.Writer) (*deploy.OperationLock, error)
	plan              func(ControlledSessionPlanInputV1) (ControlledSessionExecutionPlanV1, error)
	run               func(context.Context, *deploy.OperationLock, *deploy.OperationLock, ControlledSessionExecutionPlanV1, ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error)
}

// RunCurrentControlledSessionV1 validates and admits one exact controller and
// workload pair before delegating all resource and lifecycle ownership to the
// controlled-session supervisor. It retains the controller deployment lock for
// the supervisor's complete lifetime, so another app, shell, control operation,
// or controlled session cannot use that controller generation concurrently.
func RunCurrentControlledSessionV1(
	ctx context.Context,
	input CurrentControlledSessionRunInputV1,
) (CurrentControlledSessionRunResultV1, error) {
	return runCurrentControlledSessionV1(ctx, input, currentControlledSessionRunBackendV1{
		acquire:           deploy.AcquireOperationLock,
		loadRuntime:       loadCurrentControlledSessionRuntimeV1,
		privateEnv:        preparePrivateWorkloadEnvironmentV1,
		prepareController: prepareControlledSessionControllerPackageV1,
		cleanupController: cleanupControlledSessionControllerPackageV1,
		prepareOutput:     prepareOneShotOutput,
		abortOutput:       func(output *oneShotOutputSession) error { return output.abort() },
		publishOutput:     func(output *oneShotOutputSession) error { return output.publish() },
		concurrency:       PlanLiveRunConcurrencyV1,
		newRunID:          deploy.NewLiveRunIDV1,
		newHandle:         controlledsession.NewHandleV1,
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
) (result CurrentControlledSessionRunResultV1, err error) {
	if ctx == nil {
		return result, fmt.Errorf("run current controlled session requires a context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if backend.acquire == nil || backend.loadRuntime == nil || backend.privateEnv == nil || backend.prepareController == nil || backend.cleanupController == nil || backend.prepareOutput == nil || backend.abortOutput == nil || backend.publishOutput == nil || backend.concurrency == nil || backend.newRunID == nil || backend.newHandle == nil || backend.acquireLease == nil || backend.await == nil || backend.plan == nil || backend.run == nil {
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
	preparedController, err := backend.prepareController(ctx, controllerDir, controller.current)
	if err != nil {
		return result, fmt.Errorf("prepare controlled-session controller package: %w", err)
	}
	if preparedController == nil {
		return result, fmt.Errorf("prepare controlled-session controller package returned no package")
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			controlledSessionControllerPackageCleanupTimeoutV1(input.SupervisorOptions),
		)
		defer cleanupCancel()
		if cleanupErr := backend.cleanupController(cleanupCtx, preparedController); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup controlled-session controller package: %w", cleanupErr))
		}
	}()
	output, err := backend.prepareOutput(
		input.OutputDir,
		input.OutputFile,
		controller.plan.Docker.Sandbox.RuntimeUser,
	)
	if err != nil {
		return result, fmt.Errorf("prepare controlled-session controller output: %w", err)
	}
	if output == nil {
		return result, fmt.Errorf("prepare controlled-session controller output returned no session")
	}
	if input.OutputDir != "" {
		result.ControllerOutput.Kind = ControlledSessionControllerOutputDirectoryRetainedV1
	} else if input.OutputFile == "" {
		result.ControllerOutput.Kind = ControlledSessionControllerOutputNotRequestedV1
	}
	defer func() {
		status, outputErr := finalizeControlledSessionControllerOutputV1(
			input.OutputDir, input.OutputFile, result.SessionResult.ControllerFinalizationStatus.Kind,
			func() error { return backend.publishOutput(output) },
			func() error { return backend.abortOutput(output) },
		)
		result.ControllerOutput = status
		err = errors.Join(err, outputErr)
	}()
	controllerConcurrency, err := backend.concurrency(controller.plan.Document, controller.plan.Docker, output.mount)
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
		ControllerPackage:            preparedController.Package,
		ControllerRuntime:            controller.plan,
		ControllerCommand:            input.ControllerCommand,
		ControllerForwardedArguments: append([]string(nil), input.ControllerArguments...),
		controllerOutput:             output.mount,
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
	result.ControlledSessionRunResultV1, err = backend.run(ctx, workloadOperation, controllerOperation, plan, input.SupervisorOptions)
	return result, err
}

func controlledSessionControllerPackageCleanupTimeoutV1(options ControlledSessionRunOptionsV1) time.Duration {
	if options.CleanupTimeout > 0 {
		return options.CleanupTimeout
	}
	return controlledSessionControllerPackageDefaultCleanupTimeoutV1
}

func finalizeControlledSessionControllerOutputV1(
	outputDir string,
	outputFile string,
	finalization controlledsession.ControllerFinalizationStatusKindV1,
	publish func() error,
	abort func() error,
) (ControlledSessionControllerOutputStatusV1, error) {
	if finalization == controlledsession.ControllerFinalizationCompletedV1 {
		if err := publish(); err != nil {
			return ControlledSessionControllerOutputStatusV1{
				Kind: ControlledSessionControllerOutputFailedV1, Reason: "controller output publication failed",
			}, fmt.Errorf("publish controlled-session controller output: %w", err)
		}
		switch {
		case outputFile != "":
			return ControlledSessionControllerOutputStatusV1{Kind: ControlledSessionControllerOutputFilePublishedV1}, nil
		case outputDir != "":
			return ControlledSessionControllerOutputStatusV1{Kind: ControlledSessionControllerOutputDirectoryRetainedV1}, nil
		default:
			return ControlledSessionControllerOutputStatusV1{Kind: ControlledSessionControllerOutputNotRequestedV1}, nil
		}
	}
	if err := abort(); err != nil {
		return ControlledSessionControllerOutputStatusV1{
			Kind: ControlledSessionControllerOutputFailedV1, Reason: "controller output staging cleanup failed",
		}, fmt.Errorf("abort controlled-session controller output: %w", err)
	}
	if outputFile != "" {
		return ControlledSessionControllerOutputStatusV1{
			Kind:   ControlledSessionControllerOutputFileDiscardedV1,
			Reason: "controller output file was discarded because controller finalization did not complete",
		}, nil
	}
	if outputDir != "" {
		return ControlledSessionControllerOutputStatusV1{Kind: ControlledSessionControllerOutputDirectoryRetainedV1}, nil
	}
	return ControlledSessionControllerOutputStatusV1{Kind: ControlledSessionControllerOutputNotRequestedV1}, nil
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
