package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ForceReplaceStagedDesiredStateInputV1 struct {
	DesiredState DesiredStateStageInputV1
	RunOptions   RunOptions
}

type forceReplaceStagedDesiredStateBackendV1 struct {
	acquire         func(context.Context, string) (*deploy.OperationLock, error)
	newStore        func(string) (providerstore.Store, error)
	recoverPending  func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error)
	admit           func(context.Context, string, *deploy.OperationLock, ControlAdmissionInputV1) (AdmittedControlV1, error)
	complete        func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error
	stopOwned       func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error
	removeReference func(context.Context, providers.RealizedImageV1, string, string, string) error
	commit          func(*deploy.OperationLock, *deploy.EnvironmentGenerationState, deploy.StateV1) error
	stageSame       func(context.Context, DesiredStateStageInputV1) (deploy.DesiredStateUpdateResult, error)
	probeNative     func(context.Context) (blueprint.Platform, error)
}

// ForceReplaceStagedDesiredStateV1 replaces staging owned by another
// environment. It first force-admits a serialized control operation, stopping
// live work that still uses the old generation, and then publishes a fresh,
// unbuilt desired state for the replacement blueprint.
func ForceReplaceStagedDesiredStateV1(ctx context.Context, input ForceReplaceStagedDesiredStateInputV1) (deploy.DesiredStateUpdateResult, error) {
	return forceReplaceStagedDesiredStateV1(ctx, input, forceReplaceStagedDesiredStateBackendV1{
		acquire:  deploy.AcquireOperationLock,
		newStore: providerstore.NewStore,
		recoverPending: func(ctx context.Context, operation *deploy.OperationLock, store providerstore.Store, current *deploy.EnvironmentGenerationState, environment string, dir string) (bool, error) {
			return RecoverPendingPublication(
				ctx, operation, store, current, environment, dir,
				registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
			)
		},
		admit:           AdmitControlOperationV1,
		complete:        CompleteControlAdmissionV1,
		stopOwned:       stopOwnedCurrentWorkloadV1,
		removeReference: RemoveEnvironmentGenerationReference,
		commit: func(operation *deploy.OperationLock, expected *deploy.EnvironmentGenerationState, state deploy.StateV1) error {
			return operation.CommitStateV1(expected, state)
		},
		stageSame:   StageDesiredStateV1,
		probeNative: ProbeDockerNativePlatform,
	})
}

func forceReplaceStagedDesiredStateV1(
	ctx context.Context,
	input ForceReplaceStagedDesiredStateInputV1,
	backend forceReplaceStagedDesiredStateBackendV1,
) (result deploy.DesiredStateUpdateResult, err error) {
	desired := input.DesiredState
	if ctx == nil {
		return result, fmt.Errorf("force-replace staged desired state requires a context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if desired.DeploymentDir == "" {
		return result, fmt.Errorf("force-replace staged desired state requires a deployment directory")
	}
	if desired.Create {
		return result, fmt.Errorf("force replacement is only supported when updating staging")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.recoverPending == nil || backend.admit == nil || backend.complete == nil || backend.stopOwned == nil || backend.removeReference == nil || backend.commit == nil || backend.stageSame == nil {
		return result, fmt.Errorf("force-replace staged desired state requires a complete backend")
	}
	selected, err := selectDesiredStateTargetV1(ctx, desired, backend.probeNative)
	if err != nil {
		return result, err
	}
	dir, err := filepath.Abs(desired.DeploymentDir)
	if err != nil {
		return result, fmt.Errorf("resolve force-replacement staging directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return result, err
	}
	markerID := ""
	var controlLease *deploy.ControlLeaseV1
	defer func() {
		if operation == nil {
			return
		}
		var releaseErr error
		if markerID == "" {
			releaseErr = operation.Unlock()
		} else {
			releaseErr = backend.complete(operation, markerID, controlLease)
		}
		err = errors.Join(err, releaseErr)
	}()

	state, found, oldEnvironment, err := readForceReplacementStateV1(operation)
	if err != nil {
		return result, err
	}
	if !found {
		return result, fmt.Errorf("staging state is missing; run `reploy stage` first")
	}
	if state.Staging == nil || state.Deployment != nil {
		return result, fmt.Errorf("--force can only replace a staging deployment")
	}
	if oldEnvironment == desired.Document.Environment.ID {
		if unlockErr := operation.Unlock(); unlockErr != nil {
			return result, unlockErr
		}
		operation = nil
		return backend.stageSame(ctx, desired)
	}

	store, err := backend.newStore(dir)
	if err != nil {
		return result, err
	}
	if _, err := backend.recoverPending(ctx, operation, store, state.Current, oldEnvironment, dir); err != nil {
		return result, fmt.Errorf("recover staged build before force replacement: %w", err)
	}
	state, _, oldEnvironment, err = readForceReplacementStateV1(operation)
	if err != nil {
		return result, err
	}
	generationReference := "staged/" + oldEnvironment
	if state.Current != nil {
		generationReference = state.Current.Reference
	}
	admissionOperation := operation
	operation = nil
	admitted, err := backend.admit(ctx, dir, admissionOperation, ControlAdmissionInputV1{
		Operation:              deploy.ControlOperationStageV1,
		GenerationReference:    generationReference,
		Mode:                   ControlAdmissionForceV1,
		DockerPreflightTimeout: input.RunOptions.DockerPreflightTimeout,
	})
	if err != nil {
		return result, err
	}
	operation = admitted.Operation
	markerID = admitted.Marker.ID
	controlLease = admitted.Lease

	state, found, oldEnvironment, err = readForceReplacementStateV1(operation)
	if err != nil {
		return result, err
	}
	if !found || state.Staging == nil || state.Deployment != nil {
		return result, fmt.Errorf("staging deployment changed while force replacement was waiting; retry the command")
	}
	if oldEnvironment == desired.Document.Environment.ID {
		return result, fmt.Errorf("staging blueprint changed while force replacement was waiting; retry the command")
	}
	if _, err := backend.recoverPending(ctx, operation, store, state.Current, oldEnvironment, dir); err != nil {
		return result, fmt.Errorf("recover staged build after force replacement admission: %w", err)
	}
	state, _, oldEnvironment, err = readForceReplacementStateV1(operation)
	if err != nil {
		return result, err
	}

	var oldBuild *CurrentBuild
	if state.Current != nil {
		loaded, currentFound, loadErr := LoadRecordedCurrentBuildV1(ctx, operation, store, oldEnvironment, dir)
		if loadErr != nil {
			return result, fmt.Errorf("load staged build for force replacement: %w", loadErr)
		}
		if !currentFound {
			return result, fmt.Errorf("staged generation is missing its build record")
		}
		oldBuild = &loaded
		if err := backend.stopOwned(ctx, operation, state, dir, input.RunOptions); err != nil {
			return result, fmt.Errorf("stop staged workload before force replacement: %w", err)
		}
	}

	payload, err := blueprint.EncodeResolvedDocumentV1(desired.Document)
	if err != nil {
		return result, err
	}
	candidate := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: payload, BlueprintSource: desired.BlueprintSource,
		Platform: selected, Overlay: deploy.EmptyRequestOverlayV1(), Current: nil,
		Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1, WorkspaceRoot: desired.WorkspaceRoot},
	}
	if err := deploy.ValidateStateV1(candidate); err != nil {
		return result, fmt.Errorf("validate force-replacement staged state: %w", err)
	}
	if err := backend.commit(operation, state.Current, candidate); err != nil {
		if oldBuild != nil {
			return result, fmt.Errorf("write force-replacement staged state after stopping the old workload: %w", err)
		}
		return result, fmt.Errorf("write force-replacement staged state: %w", err)
	}
	if oldBuild != nil {
		if err := backend.removeReference(ctx, oldBuild.Lock.FinalImage, oldBuild.Generation.Reference, oldEnvironment, dir); err != nil {
			return deploy.DesiredStateUpdateResult{State: candidate, Changed: true}, fmt.Errorf("staging was replaced but the old image reference could not be removed: %w", err)
		}
	}
	return deploy.DesiredStateUpdateResult{State: candidate, Changed: true}, nil
}

func readForceReplacementStateV1(operation *deploy.OperationLock) (deploy.StateV1, bool, string, error) {
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return deploy.StateV1{}, false, "", fmt.Errorf("read force-replacement staged state: %w", err)
	}
	if !found {
		return state, false, "", nil
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return deploy.StateV1{}, false, "", fmt.Errorf("decode force-replacement staged blueprint: %w", err)
	}
	return state, true, document.Environment.ID, nil
}
