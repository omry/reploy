package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type StagedDeploymentRemoveInputV1 struct {
	DeploymentDir string
	ControlMode   ControlAdmissionModeV1
	RunOptions    RunOptions
}

type StagedDeploymentRemoveResultV1 struct {
	DeploymentDir string
	Environment   string
}

type stagedDeploymentRemoveBackendV1 struct {
	acquire          func(context.Context, string) (*deploy.OperationLock, error)
	newStore         func(string) (providerstore.Store, error)
	recoverPending   func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string) (bool, error)
	admit            func(context.Context, string, *deploy.OperationLock, ControlAdmissionInputV1) (AdmittedControlV1, error)
	stopOwned        func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error
	beginRemoval     func(*deploy.OperationLock, deploy.StateV1) error
	discardValidated func(context.Context, *deploy.OperationLock, string, string) error
	removeMarker     func(*deploy.OperationLock, string) error
	releaseLease     func(*deploy.ControlLeaseV1) error
	reserve          func(string) (string, error)
	rename           func(string, string) error
	unlock           func(*deploy.OperationLock) error
	removeReference  func(context.Context, providers.RealizedImageV1, string, string, string) error
	removeCompanion  func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error
	removeAll        func(string) error
	complete         func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error
}

// RemoveStagedDeploymentV1 removes one complete staging directory and only
// the Docker resources whose ownership is proven by its retained state.
func RemoveStagedDeploymentV1(
	ctx context.Context,
	input StagedDeploymentRemoveInputV1,
) (StagedDeploymentRemoveResultV1, error) {
	return removeStagedDeploymentV1(ctx, input, stagedDeploymentRemoveBackendV1{
		acquire:  deploy.AcquireStagedRemovalOperationLock,
		newStore: providerstore.NewStore,
		recoverPending: func(
			ctx context.Context,
			operation *deploy.OperationLock,
			store providerstore.Store,
			current *deploy.EnvironmentGenerationState,
			environment string,
			dir string,
		) (bool, error) {
			return recoverTerminalPublicationWithStoreV1(ctx, operation, store, current, environment, dir)
		},
		admit:     AdmitControlOperationV1,
		stopOwned: stopStagedWorkloadForRemovalV1,
		discardValidated: func(ctx context.Context, operation *deploy.OperationLock, environment, deploymentDir string) error {
			_, _, err := discardValidatedBuildWithBackendV1(ctx, operation, environment, deploymentDir, validatedRetirementBackend(RemoveEnvironmentGenerationReference))
			return err
		},
		removeMarker: func(operation *deploy.OperationLock, markerID string) error {
			_, removed, err := operation.RemoveControlMarkerV1(markerID)
			if err == nil && !removed {
				return fmt.Errorf("control marker %q is not outstanding", markerID)
			}
			return err
		},
		releaseLease:    func(lease *deploy.ControlLeaseV1) error { return lease.Release() },
		reserve:         reserveStagedDeploymentTombstoneV1,
		rename:          os.Rename,
		unlock:          func(operation *deploy.OperationLock) error { return operation.Unlock() },
		removeReference: RemoveEnvironmentGenerationReference,
		removeCompanion: RemovePortableEnvironmentReferenceV1,
		removeAll:       os.RemoveAll,
		complete:        CompleteControlAdmissionV1,
	})
}

func removeStagedDeploymentV1(
	ctx context.Context,
	input StagedDeploymentRemoveInputV1,
	backend stagedDeploymentRemoveBackendV1,
) (result StagedDeploymentRemoveResultV1, err error) {
	if ctx == nil {
		return result, fmt.Errorf("remove staged deployment requires a context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if input.DeploymentDir == "" {
		return result, fmt.Errorf("remove staged deployment requires a deployment directory")
	}
	if input.ControlMode == "" {
		input.ControlMode = ControlAdmissionImmediateV1
	}
	if !validControlAdmissionModeV1(input.ControlMode) {
		return result, fmt.Errorf("staging removal control mode must be immediate, wait, drain, or force")
	}
	if backend.acquire == nil || backend.newStore == nil ||
		backend.recoverPending == nil || backend.admit == nil ||
		backend.stopOwned == nil || backend.discardValidated == nil ||
		backend.removeMarker == nil || backend.releaseLease == nil ||
		backend.reserve == nil || backend.rename == nil ||
		backend.unlock == nil || backend.removeReference == nil ||
		backend.removeAll == nil || backend.complete == nil {
		return result, fmt.Errorf("remove staged deployment requires a complete backend")
	}
	dir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return result, fmt.Errorf("resolve staged deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, dir)
	if err != nil {
		return result, err
	}
	markerID := ""
	var lease *deploy.ControlLeaseV1
	defer func() {
		if operation == nil {
			return
		}
		var releaseErr error
		if markerID != "" {
			releaseErr = backend.complete(operation, markerID, lease)
		} else {
			if lease != nil {
				releaseErr = backend.releaseLease(lease)
			}
			releaseErr = errors.Join(releaseErr, backend.unlock(operation))
		}
		err = errors.Join(err, releaseErr)
	}()

	state, environment, err := readStagedDeploymentForRemovalV1(operation)
	if err != nil {
		return result, err
	}
	result = StagedDeploymentRemoveResultV1{
		DeploymentDir: dir,
		Environment:   environment,
	}
	store, err := backend.newStore(dir)
	if err != nil {
		return result, err
	}
	if !state.Staging.TerminalRemoval {
		if _, err := backend.recoverPending(
			ctx, operation, store, state.Current, environment, dir,
		); err != nil {
			return result, fmt.Errorf("recover staged build before removal: %w", err)
		}
	}
	state, environment, err = readStagedDeploymentForRemovalV1(operation)
	if err != nil {
		return result, err
	}
	if !state.Staging.TerminalRemoval {
		generationReference := "staged/" + environment
		if state.Current != nil {
			generationReference = state.Current.Reference
		}
		admissionOperation := operation
		operation = nil
		admitted, err := backend.admit(ctx, dir, admissionOperation, ControlAdmissionInputV1{
			Operation:              deploy.ControlOperationStageV1,
			GenerationReference:    generationReference,
			Mode:                   input.ControlMode,
			DockerPreflightTimeout: input.RunOptions.DockerPreflightTimeout,
			Notice:                 controlWaitNoticeWriterV1(input.RunOptions),
		})
		if err != nil {
			return result, err
		}
		operation = admitted.Operation
		markerID = admitted.Marker.ID
		lease = admitted.Lease

		state, environment, err = readStagedDeploymentForRemovalV1(operation)
		if err != nil {
			return result, err
		}
		if _, err := backend.recoverPending(
			ctx, operation, store, state.Current, environment, dir,
		); err != nil {
			return result, fmt.Errorf("recover staged build after removal admission: %w", err)
		}
		state, environment, err = readStagedDeploymentForRemovalV1(operation)
		if err != nil {
			return result, err
		}
		result.Environment = environment

	}
	if err := requireTerminalRetirementAuthorityV1(operation, environment, dir); err != nil {
		return result, err
	}
	var pairs []OwnedImageReferenceV1
	if state.Current != nil {
		lock, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
		if err != nil {
			return result, err
		}
		if !found {
			return result, fmt.Errorf("staged generation build lock is missing")
		}
		pairs, err = projectEnvironmentOwnedReferencesV1(*state.Current, lock, environment, dir, acceptProviderProfileOwnerForCutoverV1)
		if err != nil {
			return result, err
		}
		if len(pairs) == 2 && backend.removeCompanion == nil {
			return result, fmt.Errorf("staged retirement requires companion removal")
		}
	}
	begin := backend.beginRemoval
	if begin == nil {
		begin = func(op *deploy.OperationLock, st deploy.StateV1) error { return op.BeginStagedRemovalV1(st) }
	}
	if err := begin(operation, state); err != nil {
		return result, fmt.Errorf("begin staged terminal removal: %w", err)
	}
	state, environment, err = readStagedDeploymentForRemovalV1(operation)
	if err != nil {
		return result, err
	}
	if !state.Staging.TerminalRemoval {
		return result, fmt.Errorf("staged terminal removal guard is missing")
	}
	if err := operation.RequireWritable(); err != nil {
		return result, err
	}
	if err := backend.stopOwned(
		ctx, operation, state, dir, input.RunOptions,
	); err != nil {
		return result, fmt.Errorf("stop staged workload before removal: %w", err)
	}
	if err := backend.discardValidated(
		context.WithoutCancel(ctx), operation, environment, dir,
	); err != nil {
		return result, fmt.Errorf("discard validated staging build before removal: %w", err)
	}
	for index, pair := range pairs {
		var removeErr error
		if index == 0 {
			removeErr = backend.removeReference(context.WithoutCancel(ctx), pair.Image, pair.Reference, environment, dir)
		} else {
			removeErr = backend.removeCompanion(context.WithoutCancel(ctx), operation, pair, *state.Current, environment, dir)
		}
		if removeErr != nil {
			return result, fmt.Errorf("remove staged image reference %q: %w", pair.Reference, removeErr)
		}
	}
	tombstone, err := backend.reserve(dir)
	if err != nil {
		return result, fmt.Errorf("reserve staged removal path: %w", err)
	}
	if markerID != "" {
		if err := backend.removeMarker(operation, markerID); err != nil {
			return result, fmt.Errorf("complete staging removal admission: %w", err)
		}
		markerID = ""
	}
	if lease != nil {
		if err := backend.releaseLease(lease); err != nil {
			return result, fmt.Errorf("release staging removal queue ownership: %w", err)
		}
		lease = nil
	}
	if stagedRemovalMustUnlockBeforeRename() {
		if err := backend.unlock(operation); err != nil {
			operation = nil
			return result, fmt.Errorf("release operation lock before moving staging: %w", err)
		}
		operation = nil
	}
	if err := backend.rename(dir, tombstone); err != nil {
		return result, fmt.Errorf("move staging directory for removal: %w", err)
	}
	if operation != nil {
		if err := backend.unlock(operation); err != nil {
			operation = nil
			return result, fmt.Errorf("release operation lock after moving staging: %w", err)
		}
		operation = nil
	}

	if err := backend.removeAll(tombstone); err != nil {
		return result, fmt.Errorf(
			"remove staging directory: %w; partial removal retained at %s",
			err,
			tombstone,
		)
	}
	return result, nil
}

func readStagedDeploymentForRemovalV1(
	operation *deploy.OperationLock,
) (deploy.StateV1, string, error) {
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return deploy.StateV1{}, "", fmt.Errorf("read staged deployment for removal: %w", err)
	}
	if !found {
		return deploy.StateV1{}, "", fmt.Errorf("staging state is missing")
	}
	if state.Staging == nil || state.Deployment != nil {
		return deploy.StateV1{}, "", fmt.Errorf("stage --remove requires a staging deployment")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return deploy.StateV1{}, "", fmt.Errorf("decode staged blueprint for removal: %w", err)
	}
	return state, document.Environment.ID, nil
}

func stopStagedWorkloadForRemovalV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	state deploy.StateV1,
	deploymentDir string,
	options RunOptions,
) error {
	if _, err := os.Lstat(filepath.Join(deploymentDir, DockerEnvFileName)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect staged runtime environment: %w", err)
	}
	return stopOwnedCurrentWorkloadV1(ctx, operation, state, deploymentDir, options)
}

func reserveStagedDeploymentTombstoneV1(deploymentDir string) (string, error) {
	parent := filepath.Dir(deploymentDir)
	pattern := "." + filepath.Base(deploymentDir) + ".reploy-stage-remove-*"
	reserved, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return "", err
	}
	if err := os.Remove(reserved); err != nil {
		_ = os.RemoveAll(reserved)
		return "", err
	}
	return reserved, nil
}
