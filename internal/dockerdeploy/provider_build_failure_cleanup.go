package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

// Failed execution cannot invalidate either independently committed owner.
// Resolve publication first, then prune against their complete union of roots.
func cleanupFailedProviderBuildV1(ctx context.Context, preparation LockedProviderBuildPreparationV1) error {
	if err := validatePublicationDeployment(preparation.Operation, preparation.Store, preparation.DeploymentDir); err != nil {
		return err
	}
	if _, err := RecoverPendingValidatedPublicationV1(ctx, preparation.Operation, preparation.Store, preparation.Environment, preparation.DeploymentDir); err != nil {
		return fmt.Errorf("recover failed provider validated publication: %w", err)
	}
	validated, validatedFound, err := preparation.Operation.ReadValidatedBuildV1()
	if err != nil {
		return err
	}
	if validatedFound {
		if _, err := validateValidatedRetirementV1(preparation.Operation, validated, preparation.Environment, preparation.DeploymentDir); err != nil {
			return err
		}
	}
	if validatedFound && len(validated.PendingCleanup) != 0 {
		var cleanupErrors []error
		validated, cleanupErrors = cleanupPendingValidatedBuildReferences(ctx, preparation.Operation, validated, preparation.Environment, preparation.DeploymentDir, RemoveEnvironmentGenerationReference)
		if len(cleanupErrors) != 0 {
			return fmt.Errorf("retire superseded validated references before failed provider cleanup: %w", errors.Join(cleanupErrors...))
		}
	}
	state, found, err := preparation.Operation.ReadStateV1()
	if err != nil {
		return fmt.Errorf("read failed provider build state: %w", err)
	}
	var current *deploy.EnvironmentGenerationState
	if found {
		current = state.Current
	}
	validateProfile, validateBundle := providerBuildRecoveryValidatorsV1(preparation.NoCache)
	if _, err := RecoverPendingPublication(ctx, preparation.Operation, preparation.Store, current, preparation.Environment, preparation.DeploymentDir, validateProfile, validateBundle); err != nil {
		return fmt.Errorf("recover failed provider build publication: %w", err)
	}
	state, found, err = preparation.Operation.ReadStateV1()
	if err != nil {
		return fmt.Errorf("reread failed provider build state: %w", err)
	}
	roots := []deploy.BuildLockV1{}
	digests := []canonical.Digest{}
	if found && state.Current != nil {
		lock, lockFound, err := preparation.Operation.ReadBuildLock(state.Current.BuildLockDigest, validateProfile)
		if err != nil {
			return err
		}
		if !lockFound {
			return fmt.Errorf("current build lock %s is missing during failed build cleanup", state.Current.BuildLockDigest)
		}
		if _, err := projectEnvironmentOwnedReferencesV1(*state.Current, lock, preparation.Environment, preparation.DeploymentDir, validateProfile); err != nil {
			return err
		}
		roots = append(roots, lock)
		digests = append(digests, state.Current.BuildLockDigest)
	}
	validated, validatedFound, err = preparation.Operation.ReadValidatedBuildV1()
	if err != nil {
		return err
	}
	if validatedFound && !validated.Discarded {
		if _, err := validateValidatedRetirementV1(preparation.Operation, validated, preparation.Environment, preparation.DeploymentDir); err != nil {
			return err
		}
		lock, lockFound, err := preparation.Operation.ReadBuildLock(validated.BuildLockDigest, validateProfile)
		if err != nil {
			return err
		}
		if !lockFound {
			return fmt.Errorf("validated build lock %s is missing during failed build cleanup", validated.BuildLockDigest)
		}
		if len(digests) == 0 || digests[0] != validated.BuildLockDigest {
			roots = append(roots, lock)
			digests = append(digests, validated.BuildLockDigest)
		}
	}
	// No-cache is the provider-schema cutover path. Its retained canonical
	// locks can refer to old payload schemas, so keep all immutable objects.
	if !preparation.NoCache {
		if err := preparation.Operation.RemoveUnreachableBuildObjectsForBuilds(preparation.Store, roots, validateProfile, validateBundle); err != nil {
			return err
		}
	}
	if err := preparation.Operation.RemoveBuildLocksExcept(digests, validateProfile); err != nil {
		return err
	}
	if !preparation.NoCache && validatedFound && validated.PendingStorageCleanup {
		if validated.Discarded {
			if err := preparation.Operation.RemoveValidatedBuildV1(); err != nil {
				return err
			}
		} else {
			validated.PendingStorageCleanup = false
			if err := preparation.Operation.CommitValidatedBuildV1(validated); err != nil {
				return err
			}
		}
	}
	return preparation.Store.RemoveTemporaryEntries()
}
