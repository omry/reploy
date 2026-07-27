package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/omry/reploy/internal/deploy"
)

type ProviderUninstallInputV1 struct {
	DeploymentDir string
	Runtime       StagedProviderBuildRuntimeV1
	ControlMode   ControlAdmissionModeV1
	Service       string
	RemoveDir     bool
	RunOptions    RunOptions
}

type providerUninstallRunBackendV1 struct {
	acquire          func(context.Context, string) (*deploy.OperationLock, error)
	release          func(*deploy.OperationLock) error
	plan             func(providerUninstallPlanningInputV1) (providerUninstallPlanV1, error)
	admit            func(context.Context, string, *deploy.OperationLock, ControlAdmissionInputV1) (AdmittedControlV1, error)
	complete         func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error
	execute          func(context.Context, *deploy.OperationLock, providerUninstallPlanV1, RunOptions) error
	removeDeployment func(context.Context, *deploy.OperationLock, string, *deploy.ControlLeaseV1, providerUninstallPlanV1, RunOptions) error
}

func runProviderUninstallV1(
	ctx context.Context,
	input ProviderUninstallInputV1,
	backend providerUninstallRunBackendV1,
) (err error) {
	if ctx == nil {
		return fmt.Errorf("run provider uninstall requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.DeploymentDir == "" {
		return fmt.Errorf("run provider uninstall requires a deployment directory")
	}
	if input.ControlMode == "" {
		input.ControlMode = ControlAdmissionImmediateV1
	}
	if !validControlAdmissionModeV1(input.ControlMode) {
		return fmt.Errorf("uninstall control admission mode must be immediate, wait, drain, or force")
	}
	if backend.acquire == nil || backend.release == nil || backend.plan == nil || backend.admit == nil || backend.complete == nil || backend.execute == nil {
		return fmt.Errorf("run provider uninstall requires a complete backend")
	}
	if input.RemoveDir && backend.removeDeployment == nil {
		return fmt.Errorf("run provider uninstall --remove-dir requires deployment removal")
	}
	deploymentDir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return fmt.Errorf("resolve provider uninstall deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, deploymentDir)
	if err != nil {
		return err
	}
	markerID := ""
	var controlLease *deploy.ControlLeaseV1
	defer func() {
		if operation == nil {
			return
		}
		var releaseErr error
		if markerID == "" {
			releaseErr = backend.release(operation)
		} else {
			releaseErr = backend.complete(operation, markerID, controlLease)
		}
		err = errors.Join(err, releaseErr)
	}()

	planningInput := providerUninstallPlanningInputV1{
		Operation: operation, DeploymentDir: deploymentDir, Runtime: input.Runtime,
		Service: input.Service, RemoveDir: input.RemoveDir,
	}
	plan, err := backend.plan(planningInput)
	if err != nil {
		return err
	}
	admitted, err := backend.admit(ctx, deploymentDir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationUninstallV1, GenerationReference: plan.GenerationReference,
		Mode: input.ControlMode, DockerPreflightTimeout: input.RunOptions.DockerPreflightTimeout,
	})
	if err != nil {
		operation = nil // admission owns lock release on every error path
		if errors.Is(err, deploy.ErrLiveRunConflict) {
			return fmt.Errorf("%w; rerun with --wait to queue this uninstall", err)
		}
		return err
	}
	previousOperation := operation
	operation = admitted.Operation
	markerID = admitted.Marker.ID
	controlLease = admitted.Lease
	if operation == nil {
		return fmt.Errorf("uninstall admission returned no operation lock")
	}
	if operation != previousOperation {
		planningInput.Operation = operation
		waitedPlan, planErr := backend.plan(planningInput)
		if planErr != nil {
			return fmt.Errorf("revalidate provider uninstall after waiting: %w", planErr)
		}
		if !reflect.DeepEqual(waitedPlan.State, plan.State) {
			return fmt.Errorf("installed state changed while uninstall was waiting; retry the command")
		}
		plan = waitedPlan
	}
	if err := backend.execute(ctx, operation, plan, input.RunOptions); err != nil {
		return err
	}
	if !plan.RemoveDir {
		return nil
	}
	ownedOperation := operation
	ownedMarkerID := markerID
	ownedControlLease := controlLease
	operation = nil
	markerID = ""
	controlLease = nil
	return backend.removeDeployment(ctx, ownedOperation, ownedMarkerID, ownedControlLease, plan, input.RunOptions)
}

// RunProviderUninstallV1 removes the persistent host integration for one
// installed state-v1 deployment under serialized runtime admission.
func RunProviderUninstallV1(ctx context.Context, input ProviderUninstallInputV1) error {
	return runProviderUninstallV1(ctx, input, newProviderUninstallRunBackendV1())
}
