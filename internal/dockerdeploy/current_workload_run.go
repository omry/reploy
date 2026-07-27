package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type CurrentWorkloadRunInputV1 struct {
	DeploymentDir string
	Action        string
	ControlMode   ControlAdmissionModeV1
	Runtime       StagedProviderBuildRuntimeV1
	RunOptions    RunOptions
	Progress      io.Writer
	Notice        io.Writer
}

type currentWorkloadRunBackendV1 struct {
	acquire          func(context.Context, string) (*deploy.OperationLock, error)
	newStore         func(string) (providerstore.Store, error)
	readState        func(*deploy.OperationLock) (deploy.StateV1, bool, error)
	loadCurrent      currentBuildLoader
	admit            func(context.Context, string, *deploy.OperationLock, ControlAdmissionInputV1) (AdmittedControlV1, error)
	complete         func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error
	plan             func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
	precheck         func(RuntimeReadinessInput) error
	workloadCommands func(deploy.StateV1) (*CommandSpec, *CommandSpec, error)
	stopOwned        func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error
	publishInputs    func(*deploy.OperationLock, string, CurrentRuntimePlanV1) (bool, error)
	invocation       func(DockerExecutionPlan) (RuntimeInvocationV1, error)
	runLifecycle     func(context.Context, CurrentWorkloadLifecycleInputV1) error
	startupFailure   func(string, error, RuntimeOptions, time.Time) error
}

const unavailableRuntimeGenerationV1 = "runtime-generation-unavailable"

// RunCurrentWorkloadV1 owns one state-v1 workload-container launch. It holds
// the deployment operation lock from the initial current-build read through
// container creation and never builds, resolves providers, or repairs pending
// publication state.
func RunCurrentWorkloadV1(ctx context.Context, input CurrentWorkloadRunInputV1) error {
	return runCurrentWorkloadV1(ctx, input, currentWorkloadRunBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		readState: func(operation *deploy.OperationLock) (deploy.StateV1, bool, error) {
			return operation.ReadStateV1()
		},
		loadCurrent:      ValidateCurrentBuild,
		admit:            AdmitControlOperationV1,
		complete:         CompleteControlAdmissionV1,
		plan:             PlanCurrentRuntimeV1,
		precheck:         RequireRuntimeReady,
		workloadCommands: currentWorkloadCommandsV1,
		stopOwned:        stopOwnedCurrentWorkloadV1,
		publishInputs:    PublishCurrentRuntimeInputsV1,
		invocation:       WorkloadRuntimeInvocationV1,
		runLifecycle:     RunCurrentWorkloadLifecycleV1,
		startupFailure:   runtimePostStartError,
	})
}

func runCurrentWorkloadV1(ctx context.Context, input CurrentWorkloadRunInputV1, backend currentWorkloadRunBackendV1) (err error) {
	if ctx == nil {
		return fmt.Errorf("run current workload requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.Action != "up" && input.Action != "down" && input.Action != "restart" {
		return fmt.Errorf("current workload action must be up, down, or restart")
	}
	if input.DeploymentDir == "" {
		return fmt.Errorf("run current workload requires a deployment directory")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.readState == nil || backend.loadCurrent == nil || backend.admit == nil || backend.complete == nil || backend.plan == nil || backend.precheck == nil || backend.workloadCommands == nil || backend.stopOwned == nil || backend.publishInputs == nil || backend.invocation == nil || backend.runLifecycle == nil || backend.startupFailure == nil {
		return fmt.Errorf("run current workload requires a complete backend")
	}
	controlOperation, err := currentWorkloadControlOperationV1(input.Action)
	if err != nil {
		return err
	}
	controlMode := currentWorkloadControlModeV1(input.Action, input.ControlMode)
	dir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return fmt.Errorf("resolve current workload deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return err
	}
	markerID := ""
	var controlLease *deploy.ControlLeaseV1
	defer func() {
		if operation == nil {
			return
		}
		var cleanupErr error
		if markerID == "" {
			cleanupErr = operation.Unlock()
		} else {
			cleanupErr = backend.complete(operation, markerID, controlLease)
		}
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	store, err := backend.newStore(dir)
	if err != nil {
		return err
	}
	state, found, err := backend.readState(operation)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("runtime state is missing; run `reploy stage` or `reploy install`")
	}
	recoverStop := func(cause error) error {
		if input.Action != "down" {
			return cause
		}
		if input.RunOptions.Stderr != nil {
			fmt.Fprintf(input.RunOptions.Stderr, "warning: runtime validation failed (%v); stopping the recorded deployment without lifecycle hooks\n", cause)
		}
		if stopErr := backend.stopOwned(ctx, operation, state, dir, input.RunOptions); stopErr != nil {
			return fmt.Errorf("runtime validation failed: %v; stop recorded deployment: %w", cause, stopErr)
		}
		return nil
	}
	generationReference := unavailableRuntimeGenerationV1
	if state.Current != nil {
		generationReference = state.Current.Reference
	}
	initialGenerationAvailable := state.Current != nil
	admissionOperation := operation
	operation = nil
	admitted, err := backend.admit(ctx, dir, admissionOperation, ControlAdmissionInputV1{
		Operation:              controlOperation,
		GenerationReference:    generationReference,
		Mode:                   controlMode,
		DockerPreflightTimeout: input.RunOptions.DockerPreflightTimeout,
		Notice:                 input.Notice,
	})
	if err != nil {
		if errors.Is(err, deploy.ErrLiveRunConflict) {
			return fmt.Errorf("%w; rerun with --wait to queue this %s operation", err, controlOperation)
		}
		return err
	}
	operation = admitted.Operation
	markerID = admitted.Marker.ID
	controlLease = admitted.Lease
	if operation != admissionOperation {
		state, found, err = backend.readState(operation)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("deployment state changed while %s was waiting; retry the command", controlOperation)
		}
		generationChanged := state.Current == nil && initialGenerationAvailable
		generationChanged = generationChanged || state.Current != nil && (!initialGenerationAvailable || state.Current.Reference != generationReference)
		if generationChanged {
			return fmt.Errorf("deployment generation changed while %s was waiting; retry the command", controlOperation)
		}
	}
	if state.Current == nil {
		return recoverStop(fmt.Errorf("%s", currentBuildRecoveryMessageV1(state, "runtime build is missing")))
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return recoverStop(fmt.Errorf("runtime blueprint: %w", err))
	}
	current, found, err := backend.loadCurrent(ctx, operation, store, document.Environment.ID, dir)
	if err != nil {
		return recoverStop(fmt.Errorf("runtime current build: %w", err))
	}
	if !found {
		return recoverStop(fmt.Errorf("%s", currentBuildRecoveryMessageV1(state, "runtime build is missing")))
	}
	if current.Generation.Reference != generationReference {
		return fmt.Errorf("deployment generation changed while %s was waiting; retry the command", controlOperation)
	}
	planned, err := backend.plan(CurrentRuntimePlanInputV1{DeploymentDir: dir, Current: current, Runtime: input.Runtime})
	if err != nil {
		return recoverStop(err)
	}
	if planned.Docker.Workload == nil {
		return recoverStop(fmt.Errorf("environment has no workload to %s", currentWorkloadActionVerbV1(input.Action)))
	}
	invocation, err := backend.invocation(planned.Docker)
	if err != nil {
		return recoverStop(err)
	}
	readiness := RuntimeReadinessInput{
		Current: current, DockerPlan: planned.Docker,
		PlanID: invocation.PlanID, Sources: invocation.Sources,
	}
	if err := backend.precheck(readiness); err != nil {
		return recoverStop(err)
	}
	if _, err := backend.publishInputs(operation, dir, planned); err != nil {
		return recoverStop(err)
	}
	startCommand, stopCommand, err := backend.workloadCommands(state)
	if err != nil {
		return recoverStop(err)
	}
	logSince := time.Time{}
	if input.Action == "up" || input.Action == "restart" {
		logSince = runtimeLogSinceTime()
	}
	if err := operation.Unlock(); err != nil {
		return err
	}
	operation = nil
	err = backend.runLifecycle(ctx, CurrentWorkloadLifecycleInputV1{
		Store: store, Current: current, Plan: planned,
		Environment: document.Environment.ID, DeploymentDir: dir,
		Action: input.Action, RunOptions: input.RunOptions, Progress: input.Progress,
		StartCommand: startCommand, StopCommand: stopCommand,
	})
	cleanupContext := context.WithoutCancel(ctx)
	operation, reacquireErr := backend.acquire(cleanupContext, dir)
	if reacquireErr != nil {
		leaseErr := controlLease.Release()
		controlLease = nil
		markerID = ""
		return errors.Join(err, fmt.Errorf("reacquire operation lock after lifecycle operation: %w", reacquireErr), leaseErr)
	}
	if err == nil || (input.Action != "up" && input.Action != "restart") {
		return err
	}
	if !strings.Contains(err.Error(), "lifecycle start ") && !strings.Contains(err.Error(), "lifecycle after_start ") {
		return err
	}
	message := "service failed after start"
	if input.Action == "restart" {
		message = "environment restart failed"
	} else if strings.Contains(err.Error(), "lifecycle after_start ") {
		message = "environment after_start failed"
	}
	return backend.startupFailure(message, err, RuntimeOptions{
		Dir:                    dir,
		Action:                 input.Action,
		DockerPreflightTimeout: input.RunOptions.DockerPreflightTimeout,
	}, logSince)
}

func currentWorkloadControlModeV1(action string, requested ControlAdmissionModeV1) ControlAdmissionModeV1 {
	if action == "down" || action == "restart" {
		if requested == "" {
			return ControlAdmissionForceV1
		}
		if requested == ControlAdmissionWaitV1 {
			return ControlAdmissionDrainV1
		}
	} else if requested == "" {
		return ControlAdmissionImmediateV1
	}
	return requested
}

func currentWorkloadActionVerbV1(action string) string {
	switch action {
	case "down":
		return "stop"
	case "restart":
		return "restart"
	default:
		return "start"
	}
}

func currentWorkloadCommandsV1(state deploy.StateV1) (*CommandSpec, *CommandSpec, error) {
	if state.Deployment == nil || state.Deployment.Installation.Scope != "system" || state.Deployment.Installation.UnitPath == "" {
		return nil, nil, nil
	}
	systemctl, err := providerInstallAbsoluteToolPathV1(exec.LookPath, "systemctl")
	if err != nil {
		return nil, nil, fmt.Errorf("systemctl command not found: %w", err)
	}
	unit := state.Deployment.Installation.Service + ".service"
	start := CommandSpec{Name: systemctl, Args: []string{"start", unit}}
	stop := CommandSpec{Name: systemctl, Args: []string{"stop", unit}}
	return &start, &stop, nil
}

func stopOwnedCurrentWorkloadV1(ctx context.Context, operation *deploy.OperationLock, state deploy.StateV1, dir string, options RunOptions) error {
	if err := operation.RequireHeld(); err != nil {
		return err
	}
	var spec CommandSpec
	if state.Deployment == nil {
		var err error
		spec, err = RuntimeCommand(dir, "down")
		if err != nil {
			return err
		}
	} else {
		installation := state.Deployment.Installation
		if installation.Scope == "system" && installation.UnitPath != "" {
			systemctl, err := providerInstallAbsoluteToolPathV1(exec.LookPath, "systemctl")
			if err != nil {
				return fmt.Errorf("systemctl command not found: %w", err)
			}
			spec = CommandSpec{Name: systemctl, Args: []string{"stop", installation.Service + ".service"}}
		} else {
			spec = composeCommandWithProject(installation.TargetDir, installation.ComposeProject, "down", "--remove-orphans")
		}
	}
	options.Context = ctx
	return runRuntimeCommand(spec, options)
}

func currentBuildRecoveryMessageV1(state deploy.StateV1, problem string) string {
	if state.Deployment != nil {
		return problem + "; rerun the original `reploy install` command"
	}
	return problem + "; run `reploy build`"
}

func currentWorkloadControlOperationV1(action string) (deploy.ControlOperationV1, error) {
	switch action {
	case "up":
		return deploy.ControlOperationUpV1, nil
	case "down":
		return deploy.ControlOperationStopV1, nil
	case "restart":
		return deploy.ControlOperationRestartV1, nil
	default:
		return "", fmt.Errorf("current workload action must be up, down, or restart")
	}
}
