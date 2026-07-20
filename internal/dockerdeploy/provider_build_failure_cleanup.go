package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

// cleanupFailedProviderBuildV1 preserves only the generation selected by the
// deployment state after publication recovery. Objects produced by a failed,
// unpublished candidate are unreachable and are removed.
func cleanupFailedProviderBuildV1(ctx context.Context, preparation LockedProviderBuildPreparationV1) error {
	state, found, err := preparation.Operation.ReadStateV1()
	if err != nil {
		return fmt.Errorf("read failed provider build state: %w", err)
	}
	var current *deploy.EnvironmentGenerationState
	if found {
		current = state.Current
	}
	if _, err := RecoverPendingPublication(
		ctx, preparation.Operation, preparation.Store, current,
		preparation.Environment, preparation.DeploymentDir,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	); err != nil {
		return fmt.Errorf("recover failed provider build publication: %w", err)
	}
	state, found, err = preparation.Operation.ReadStateV1()
	if err != nil {
		return fmt.Errorf("reread failed provider build state: %w", err)
	}
	if !found || state.Current == nil {
		if err := preparation.Operation.RemoveAllBuildLocks(registry.ValidateRequirementProfileV1); err != nil {
			return err
		}
		if err := preparation.Operation.RemoveAllBuildObjects(preparation.Store); err != nil {
			return err
		}
		return preparation.Store.RemoveTemporaryEntries()
	}
	lock, lockFound, err := preparation.Operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		return err
	}
	if !lockFound {
		return fmt.Errorf("current build lock %s is missing during failed build cleanup", state.Current.BuildLockDigest)
	}
	if err := validateGenerationBuildLock(*state.Current, lock, registry.ValidateRequirementProfileV1); err != nil {
		return err
	}
	if err := preparation.Operation.RemoveOtherBuildLocks(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil {
		return err
	}
	if err := preparation.Operation.RemoveUnreachableBuildObjects(
		preparation.Store, lock,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	); err != nil {
		return err
	}
	return preparation.Store.RemoveTemporaryEntries()
}
