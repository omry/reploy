package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

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
	removeCompanion func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error
	cleanupStorage  func(*deploy.OperationLock, providerstore.Store, *deploy.BuildLockV1) error
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
		acquire:  deploy.AcquireExistingOperationLock,
		newStore: providerstore.NewStore,
		recoverPending: func(ctx context.Context, operation *deploy.OperationLock, store providerstore.Store, current *deploy.EnvironmentGenerationState, environment string, dir string) (bool, error) {
			_, currentPending, err := operation.ReadPendingBuild()
			if err != nil {
				return false, err
			}
			_, validatedPending, err := operation.ReadPendingValidatedBuildV1()
			if err != nil {
				return false, err
			}
			if currentPending && validatedPending {
				return false, fmt.Errorf("current and validated publication intents conflict; ownership was preserved")
			}
			if validatedPending {
				return RecoverPendingValidatedPublicationV1(ctx, operation, store, environment, dir)
			}
			if !currentPending {
				return false, nil
			}
			return RecoverPendingPublication(
				ctx, operation, store, current, environment, dir,
				registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
			)
		},
		admit:           AdmitControlOperationV1,
		complete:        CompleteControlAdmissionV1,
		stopOwned:       stopOwnedCurrentWorkloadV1,
		removeReference: RemoveEnvironmentGenerationReference,
		removeCompanion: RemovePortableEnvironmentReferenceV1,
		cleanupStorage:  cleanupValidatedBuildStorage,
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
	payload, err := blueprint.EncodeResolvedDocumentV1(desired.Document)
	if err != nil {
		return result, err
	}
	candidate := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: payload, BlueprintSource: desired.BlueprintSource,
		Platform: selected, Overlay: deploy.EmptyRequestOverlayV1(), Current: nil,
		Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1},
	}
	if err := deploy.ValidateStateV1(candidate); err != nil {
		return result, fmt.Errorf("validate force-replacement staged state: %w", err)
	}
	cleanupStorage := backend.cleanupStorage
	if cleanupStorage == nil {
		cleanupStorage = cleanupValidatedBuildStorage
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
		// A prior replacement may have committed before storage cleanup failed.
		// Its committed state has no current or validated owner. Leave ordinary
		// owner-bearing staging to stageSame, including deferred reference cleanup.
		if state.Current == nil {
			_, validated, err := operation.ReadValidatedBuildV1()
			if err != nil {
				return result, err
			}
			if !validated {
				store, err := backend.newStore(dir)
				if err != nil {
					return result, err
				}
				if err := cleanupStorage(operation, store, nil); err != nil {
					return result, fmt.Errorf("clean retained replacement storage: %w", err)
				}
			}
		}
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
	{
		_, currentPending, readErr := operation.ReadPendingBuild()
		if readErr != nil {
			return result, fmt.Errorf("inspect current publication before force replacement recovery: %w", readErr)
		}
		validatedIntent, validatedPending, readErr := operation.ReadPendingValidatedBuildV1()
		if readErr != nil {
			return result, fmt.Errorf("inspect validated publication before force replacement recovery: %w", readErr)
		}
		if currentPending && validatedPending {
			return result, fmt.Errorf("current and validated publication intents conflict; ownership was preserved")
		}
		if validatedPending {
			candidateReferences, err := validatedRecordReferencesV1(validatedIntent.Candidate, oldEnvironment, dir)
			if err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
			cleanup, err := pendingValidatedCleanupV1(validatedIntent.Previous, validatedIntent.Candidate, oldEnvironment, dir)
			if err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
			if !reflect.DeepEqual(cleanup, validatedIntent.Candidate.PendingCleanup) {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: pending validated cleanup differs from previous ownership")
			}
			if _, err := validateValidatedRecordLockV1(operation, store, validatedIntent.Candidate, oldEnvironment, dir); err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
			selected, selectedFound, err := operation.ReadValidatedBuildV1()
			if err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
			committed := selectedFound && reflect.DeepEqual(selected, validatedIntent.Candidate)
			if !committed && !sameValidatedRecordV1(selected, selectedFound, validatedIntent.Previous) {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: pending validated state conflict; ownership was preserved")
			}
			retiring := append(append([]deploy.ValidatedBuildReferenceV1{}, candidateReferences...), cleanup...)
			if previous := validatedIntent.Previous; previous != nil && !previous.Discarded {
				previousReferences, err := validatedRecordReferencesV1(*previous, oldEnvironment, dir)
				if err != nil {
					return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
				}
				if _, err := validateValidatedRecordLockV1(operation, store, *previous, oldEnvironment, dir); err != nil {
					return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
				}
				retiring = append(retiring, previousReferences...)
			}
			if err := requireValidatedRetirementSeparationV1(operation, retiring, oldEnvironment, dir); err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
		}
		if currentPending {
			if _, err := stagedRemovalOwnedReferencesV1(operation, state, oldEnvironment, dir); err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
		}
	}
	if _, err := backend.recoverPending(ctx, operation, store, state.Current, oldEnvironment, dir); err != nil {
		return result, fmt.Errorf("recover staged build before force replacement: %w", err)
	}
	state, _, oldEnvironment, err = readForceReplacementStateV1(operation)
	if err != nil {
		return result, err
	}
	if err := requireNoPendingValidatedBuildV1(operation); err != nil {
		return result, err
	}
	if _, pending, err := operation.ReadPendingBuild(); err != nil {
		return result, fmt.Errorf("inspect current publication before force replacement: %w", err)
	} else if pending {
		return result, fmt.Errorf("force replacement requires completed current publication")
	}
	pairs, err := stagedRemovalOwnedReferencesV1(operation, state, oldEnvironment, dir)
	if err != nil {
		return result, fmt.Errorf("validate former owners before force replacement: %w", err)
	}
	validated, validatedFound, err := operation.ReadValidatedBuildV1()
	if err != nil {
		return result, err
	}
	if len(pairs) > 1 && backend.removeCompanion == nil {
		return result, fmt.Errorf("force replacement requires portable companion retirement")
	}
	if validatedFound && backend.removeCompanion == nil {
		// Validate the complete validated/superseded inventory before stopping work.
		validatedPairs, err := validateValidatedRetirementV1(operation, validated, oldEnvironment, dir)
		if err != nil {
			return result, err
		}
		for _, pair := range append(validatedPairs, validated.PendingCleanup...) {
			if pair.CompanionOwner != nil {
				return result, fmt.Errorf("force replacement requires validated companion retirement")
			}
		}
	}
	retainedState, retainedEnvironment := state, oldEnvironment
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
	{
		_, currentPending, readErr := operation.ReadPendingBuild()
		if readErr != nil {
			return result, fmt.Errorf("inspect current publication after force replacement admission: %w", readErr)
		}
		validatedIntent, validatedPending, readErr := operation.ReadPendingValidatedBuildV1()
		if readErr != nil {
			return result, fmt.Errorf("inspect validated publication after force replacement admission: %w", readErr)
		}
		if currentPending && validatedPending {
			return result, fmt.Errorf("current and validated publication intents conflict; ownership was preserved")
		}
		if validatedPending {
			candidateReferences, err := validatedRecordReferencesV1(validatedIntent.Candidate, oldEnvironment, dir)
			if err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
			cleanup, err := pendingValidatedCleanupV1(validatedIntent.Previous, validatedIntent.Candidate, oldEnvironment, dir)
			if err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
			if !reflect.DeepEqual(cleanup, validatedIntent.Candidate.PendingCleanup) {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: pending validated cleanup differs from previous ownership")
			}
			if _, err := validateValidatedRecordLockV1(operation, store, validatedIntent.Candidate, oldEnvironment, dir); err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
			selected, selectedFound, err := operation.ReadValidatedBuildV1()
			if err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
			committed := selectedFound && reflect.DeepEqual(selected, validatedIntent.Candidate)
			if !committed && !sameValidatedRecordV1(selected, selectedFound, validatedIntent.Previous) {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: pending validated state conflict; ownership was preserved")
			}
			retiring := append(append([]deploy.ValidatedBuildReferenceV1{}, candidateReferences...), cleanup...)
			if previous := validatedIntent.Previous; previous != nil && !previous.Discarded {
				previousReferences, err := validatedRecordReferencesV1(*previous, oldEnvironment, dir)
				if err != nil {
					return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
				}
				if _, err := validateValidatedRecordLockV1(operation, store, *previous, oldEnvironment, dir); err != nil {
					return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
				}
				retiring = append(retiring, previousReferences...)
			}
			if err := requireValidatedRetirementSeparationV1(operation, retiring, oldEnvironment, dir); err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
		}
		if currentPending {
			if _, err := stagedRemovalOwnedReferencesV1(operation, state, oldEnvironment, dir); err != nil {
				return result, fmt.Errorf("validate retained owners before force replacement recovery: %w", err)
			}
		}
	}
	if _, err := backend.recoverPending(ctx, operation, store, state.Current, oldEnvironment, dir); err != nil {
		return result, fmt.Errorf("recover staged build after force replacement admission: %w", err)
	}
	state, _, oldEnvironment, err = readForceReplacementStateV1(operation)
	if err != nil {
		return result, err
	}
	if err := requireNoPendingValidatedBuildV1(operation); err != nil {
		return result, err
	}
	if _, pending, err := operation.ReadPendingBuild(); err != nil {
		return result, fmt.Errorf("inspect current publication before force replacement: %w", err)
	} else if pending {
		return result, fmt.Errorf("force replacement requires completed current publication")
	}
	currentPairs, err := stagedRemovalOwnedReferencesV1(operation, state, oldEnvironment, dir)
	if err != nil {
		return result, err
	}
	currentValidated, currentValidatedFound, err := operation.ReadValidatedBuildV1()
	if err != nil {
		return result, err
	}
	if oldEnvironment != retainedEnvironment || !reflect.DeepEqual(state, retainedState) ||
		!reflect.DeepEqual(pairs, currentPairs) || validatedFound != currentValidatedFound || !reflect.DeepEqual(validated, currentValidated) {
		return result, fmt.Errorf("former ownership changed while force replacement was waiting; retry the command")
	}
	if state.Current != nil {
		if err := backend.stopOwned(ctx, operation, state, dir, input.RunOptions); err != nil {
			return result, fmt.Errorf("stop staged workload before force replacement: %w", err)
		}
	}

	retired, found, err := discardValidatedBuildWithBackendV1(ctx, operation, oldEnvironment, dir, validatedRetirementBackendV1{
		removeReference: backend.removeReference, removeCompanion: backend.removeCompanion,
	})
	if err != nil {
		return result, fmt.Errorf("retire validated owners before force replacement: %w", err)
	}
	if found {
		if !retired.Discarded || len(retired.PendingCleanup) != 0 {
			return result, fmt.Errorf("validated retirement remains incomplete; old environment was preserved")
		}
		if err := operation.RemoveValidatedBuildV1(); err != nil {
			return result, fmt.Errorf("remove retired validated record before force replacement: %w", err)
		}
	}
	for index, pair := range pairs {
		if index == 0 {
			err = backend.removeReference(ctx, pair.Image, pair.Reference, oldEnvironment, dir)
		} else {
			err = backend.removeCompanion(ctx, operation, pair, *state.Current, oldEnvironment, dir)
		}
		if err != nil {
			return result, fmt.Errorf("retire current owner before force replacement: %w", err)
		}
	}
	if err := backend.commit(operation, state.Current, candidate); err != nil {
		actual, found, _, readErr := readForceReplacementStateV1(operation)
		if readErr == nil && found && reflect.DeepEqual(actual, candidate) {
			result = deploy.DesiredStateUpdateResult{State: actual, Changed: true}
		}
		return result, errors.Join(fmt.Errorf("write force-replacement staged state after former owners retired: %w", err), readErr)
	}
	result = deploy.DesiredStateUpdateResult{State: candidate, Changed: true}
	if err := cleanupStorage(operation, store, nil); err != nil {
		return result, fmt.Errorf("staging was replaced but storage cleanup remains pending; retry the command: %w", err)
	}
	return result, nil
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
