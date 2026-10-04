package dockerdeploy

import (
	"context"
	"fmt"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"path/filepath"
)

// Deterministic installed retry retains control files at either existing
// removal location. The installed target still binds the original owner path;
// only exact removal is permitted through this scoped continuation.
func removePendingTerminalCompanionV1(ctx context.Context, operation *deploy.OperationLock, pair OwnedImageReferenceV1, owner deploy.EnvironmentGenerationState, environment, dir string) error {
	if ctx == nil {
		return fmt.Errorf("pending companion retirement requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := operation.RequireWritable(); err != nil {
		return err
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !found || state.Deployment == nil || state.Deployment.Installation.TargetDir != absolute {
		return fmt.Errorf("pending companion retirement installed authority differs")
	}
	tombstone, err := providerUninstallTombstoneV1(absolute)
	if err != nil {
		return err
	}
	control, err := providerUninstallControlV1(absolute)
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(operation.Path()))
	if root != tombstone && root != control {
		return fmt.Errorf("pending companion retirement lock is outside retained control authority")
	}
	if err := validatePortableOwnedReferenceV1(pair, owner, environment, absolute); err != nil {
		return err
	}
	return removePortableOwnedImageV1(ctx, pair, owner, runDockerOutput)
}

func loadTerminalCurrentBuildV1(_ context.Context, operation *deploy.OperationLock, _ providerstore.Store, environment, dir string) (CurrentBuild, bool, error) {
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Current == nil {
		return CurrentBuild{}, false, err
	}
	lock, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		return CurrentBuild{}, false, err
	}
	if !found {
		return CurrentBuild{}, false, fmt.Errorf("terminal current lock is missing")
	}
	if _, err := projectEnvironmentOwnedReferencesV1(*state.Current, lock, environment, dir, acceptProviderProfileOwnerForCutoverV1); err != nil {
		return CurrentBuild{}, false, err
	}
	return CurrentBuild{State: state, Generation: *state.Current, Lock: lock}, true, nil
}

func recoverTerminalPublicationV1(ctx context.Context, operation *deploy.OperationLock, dir string) error {
	if err := operation.RequireWritable(); err != nil {
		return err
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("terminal removal state is missing")
	}
	doc, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return err
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		return err
	}
	_, err = recoverTerminalPublicationWithStoreV1(ctx, operation, store, state.Current, doc.Environment.ID, dir)
	return err
}

func recoverTerminalPublicationWithStoreV1(ctx context.Context, operation *deploy.OperationLock, store providerstore.Store, current *deploy.EnvironmentGenerationState, environment, dir string) (bool, error) {
	if err := operation.RequireOwnerWritable(); err != nil {
		return false, err
	}
	_, validated, err := operation.ReadPendingValidatedBuildV1()
	if err != nil {
		return false, err
	}
	_, pending, err := operation.ReadPendingBuild()
	if err != nil {
		return false, err
	}
	if validated && pending {
		return false, fmt.Errorf("terminal removal has conflicting publication intents")
	}
	if validated {
		return RecoverPendingValidatedPublicationV1(ctx, operation, store, environment, dir)
	}
	if pending {
		return RecoverPendingPublication(ctx, operation, store, current, environment, dir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
	}
	// Discard progress belongs to retirement, not publication recovery. Without
	// an intent there is no publication to replay or abandoned alias to discover.
	return false, nil
}

// Retirement projects ownership without accepting portable content for reuse.
// Every retained owner must be canonical before any retirement effect begins.
func requireTerminalRetirementAuthorityV1(operation *deploy.OperationLock, environment, dir string) error {
	if _, pending, err := operation.ReadPendingBuild(); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("terminal retirement requires current publication recovery")
	}
	if _, pending, err := operation.ReadPendingValidatedBuildV1(); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("terminal retirement requires validated publication recovery")
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("terminal retirement state is missing")
	}
	if state.Current != nil {
		lock, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("current lock missing before terminal retirement")
		}
		if _, err := projectEnvironmentOwnedReferencesV1(*state.Current, lock, environment, dir, acceptProviderProfileOwnerForCutoverV1); err != nil {
			return err
		}
	}
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found {
		return err
	}
	_, err = validateValidatedRetirementV1(operation, record, environment, dir)
	return err
}
