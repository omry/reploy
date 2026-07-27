package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
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
	validateProfile, validateBundle := providerBuildRecoveryValidatorsV1(preparation.NoCache)
	if _, err := RecoverPendingPublication(
		ctx, preparation.Operation, preparation.Store, current,
		preparation.Environment, preparation.DeploymentDir,
		validateProfile, validateBundle,
	); err != nil {
		return fmt.Errorf("recover failed provider build publication: %w", err)
	}
	state, found, err = preparation.Operation.ReadStateV1()
	if err != nil {
		return fmt.Errorf("reread failed provider build state: %w", err)
	}
	if !found || state.Current == nil {
		if err := preparation.Operation.RemoveAllBuildLocks(validateProfile); err != nil {
			return err
		}
		if err := preparation.Operation.RemoveAllBuildObjects(preparation.Store); err != nil {
			return err
		}
		return preparation.Store.RemoveTemporaryEntries()
	}
	lock, lockFound, err := preparation.Operation.ReadBuildLock(state.Current.BuildLockDigest, validateProfile)
	if err != nil {
		return err
	}
	if !lockFound {
		return fmt.Errorf("current build lock %s is missing during failed build cleanup", state.Current.BuildLockDigest)
	}
	if err := validateGenerationBuildLock(*state.Current, lock, validateProfile); err != nil {
		return err
	}
	if err := preparation.Operation.RemoveOtherBuildLocks(state.Current.BuildLockDigest, validateProfile); err != nil {
		return err
	}
	// A no-cache rebuild is the provider-schema cutover path. If that rebuild
	// fails, the selected lock may still reference bundle payloads that the
	// current binary cannot decode. Preserve immutable objects conservatively;
	// successful replacement will prune them through the new lock.
	if preparation.NoCache {
		return preparation.Store.RemoveTemporaryEntries()
	}
	if err := preparation.Operation.RemoveUnreachableBuildObjects(
		preparation.Store, lock,
		validateProfile, validateBundle,
	); err != nil {
		return err
	}
	return preparation.Store.RemoveTemporaryEntries()
}
