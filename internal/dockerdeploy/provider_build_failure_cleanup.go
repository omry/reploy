package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

// cleanupFailedProviderBuildV1 preserves the current generation and any
// validated candidate after publication recovery. Objects produced by a
// failed, unpublished candidate are unreachable and are removed.
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
	if err := recoverPendingValidatedBuildV1(
		ctx, preparation.Operation, preparation.Store, preparation.Environment,
		preparation.DeploymentDir, removeEnvironmentValidatedBuildReference,
	); err != nil {
		// A pending validated build can predate lock publication. If its recovery
		// did not finish, retain the store as-is so cleanup cannot remove a lock
		// or object needed by a later retry.
		return fmt.Errorf("recover failed validated provider build: %w", err)
	}
	state, found, err = preparation.Operation.ReadStateV1()
	if err != nil {
		return fmt.Errorf("reread failed provider build state: %w", err)
	}
	digests := make([]canonical.Digest, 0, 2)
	builds := make([]deploy.BuildLockV1, 0, 2)
	retained := make(map[canonical.Digest]struct{}, 2)
	addRoot := func(digest canonical.Digest, lock deploy.BuildLockV1) {
		if _, exists := retained[digest]; exists {
			return
		}
		retained[digest] = struct{}{}
		digests = append(digests, digest)
		builds = append(builds, lock)
	}
	if found && state.Current != nil {
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
		addRoot(state.Current.BuildLockDigest, lock)
	}
	validated, validatedFound, err := preparation.Operation.ReadValidatedBuildV1()
	if err != nil {
		return err
	}
	if validatedFound && !validated.Discarded {
		lock, lockFound, err := preparation.Operation.ReadBuildLock(validated.BuildLockDigest, validateProfile)
		if err != nil {
			return err
		}
		if !lockFound {
			return fmt.Errorf("validated build lock %s is missing during failed build cleanup", validated.BuildLockDigest)
		}
		if lock.FinalImage != validated.Image || lock.Platform != validated.Platform {
			return fmt.Errorf("validated build does not match its build lock during failed build cleanup")
		}
		addRoot(validated.BuildLockDigest, lock)
	}
	if !found || state.Current == nil {
		if len(digests) == 0 {
			if err := preparation.Operation.RemoveAllBuildLocks(validateProfile); err != nil {
				return err
			}
			if err := preparation.Operation.RemoveAllBuildObjects(preparation.Store); err != nil {
				return err
			}
			return preparation.Store.RemoveTemporaryEntries()
		}
	}
	if err := preparation.Operation.RemoveBuildLocksExcept(digests, validateProfile); err != nil {
		return err
	}
	// A no-cache rebuild is the provider-schema cutover path. If that rebuild
	// fails, the selected lock may still reference bundle payloads that the
	// current binary cannot decode. Preserve immutable objects conservatively;
	// successful replacement will prune them through the new lock.
	if preparation.NoCache {
		return preparation.Store.RemoveTemporaryEntries()
	}
	if err := preparation.Operation.RemoveUnreachableBuildObjectsForBuilds(
		preparation.Store, builds,
		validateProfile, validateBundle,
	); err != nil {
		return err
	}
	return preparation.Store.RemoveTemporaryEntries()
}
