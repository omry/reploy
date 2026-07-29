package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type PendingPublicationRecovery struct {
	Pending        deploy.PendingBuildV1
	Decision       deploy.PendingRecoveryDecision
	SelectedLock   *deploy.BuildLockV1
	SelectedDigest canonical.Digest
	OldImage       *providers.RealizedImageV1
}

func RecoverPendingPublication(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	current *deploy.EnvironmentGenerationState,
	environment string,
	deploymentDir string,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) (bool, error) {
	if operation == nil {
		return false, fmt.Errorf("pending publication recovery requires an operation lock")
	}
	if err := operation.ValidateProviderStore(store); err != nil {
		return false, err
	}
	pending, found, err := operation.ReadPendingBuild()
	if err != nil {
		return false, err
	}
	if !found {
		if err := removeAbandonedProviderContainers(ctx, store); err != nil {
			return false, fmt.Errorf("clean abandoned provider helper containers: %w", err)
		}
		if err := store.RemoveTemporaryEntries(); err != nil {
			return false, fmt.Errorf("clean abandoned provider store temporary entries: %w", err)
		}
		return false, nil
	}
	load := func(digest canonical.Digest) (deploy.BuildLockV1, error) {
		lock, found, err := operation.ReadBuildLock(digest, validateProfileOwner)
		if err != nil {
			return deploy.BuildLockV1{}, err
		}
		if !found {
			return deploy.BuildLockV1{}, fmt.Errorf("build lock %s is missing", digest)
		}
		return lock, nil
	}
	plan, err := PreparePendingPublicationRecovery(current, pending, store, environment, deploymentDir, validateProfileOwner, validateBundleOwner, load)
	if err != nil {
		return false, err
	}
	if err := executePendingPublicationRecovery(ctx, operation, store, plan, environment, deploymentDir, validateProfileOwner, validateBundleOwner, RecoverPendingImageReferences); err != nil {
		return false, err
	}
	return true, nil
}

func PreparePendingPublicationRecovery(
	current *deploy.EnvironmentGenerationState,
	pending deploy.PendingBuildV1,
	store providerstore.Store,
	environment string,
	deploymentDir string,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
	load deploy.PendingBuildLockLoader,
) (PendingPublicationRecovery, error) {
	if load == nil {
		return PendingPublicationRecovery{}, fmt.Errorf("pending publication recovery requires a build lock loader")
	}
	references := EnvironmentImageReferences{Temporary: pending.Candidate.TemporaryReference, Generation: pending.Candidate.GenerationReference}
	if err := ValidateEnvironmentImageReferences(references, environment, deploymentDir); err != nil {
		return PendingPublicationRecovery{}, err
	}
	loaded := map[canonical.Digest]deploy.BuildLockV1{}
	loadOnce := func(digest canonical.Digest) (deploy.BuildLockV1, error) {
		if lock, found := loaded[digest]; found {
			return lock, nil
		}
		lock, err := load(digest)
		if err != nil {
			return deploy.BuildLockV1{}, err
		}
		loaded[digest] = lock
		return lock, nil
	}
	decision, candidate, err := deploy.DecidePendingRecoveryWithBuildLock(current, pending, loadOnce, validateProfileOwner)
	if err != nil {
		return PendingPublicationRecovery{}, err
	}
	if decision == deploy.PendingRecoveryStateConflict {
		return PendingPublicationRecovery{}, fmt.Errorf("pending publication state conflict; recovery changed nothing")
	}
	plan := PendingPublicationRecovery{Pending: pending, Decision: decision}
	if pending.Old != nil {
		oldLock, err := loadOnce(pending.Old.BuildLockDigest)
		if err != nil {
			return PendingPublicationRecovery{}, fmt.Errorf("load old recovery build lock: %w", err)
		}
		if err := validateGenerationBuildLock(*pending.Old, oldLock, validateProfileOwner); err != nil {
			return PendingPublicationRecovery{}, err
		}
		oldImage := oldLock.FinalImage
		plan.OldImage = &oldImage
	}
	var selectedGeneration *deploy.EnvironmentGenerationState
	switch decision {
	case deploy.PendingRecoveryKeepCandidate:
		if candidate == nil {
			return PendingPublicationRecovery{}, fmt.Errorf("committed candidate recovery is missing candidate state")
		}
		selectedGeneration = candidate
	case deploy.PendingRecoveryDiscardCandidate:
		selectedGeneration = pending.Old
	default:
		return PendingPublicationRecovery{}, fmt.Errorf("unsupported pending recovery decision %q", decision)
	}
	if selectedGeneration == nil {
		return plan, nil
	}
	selected, err := loadOnce(selectedGeneration.BuildLockDigest)
	if err != nil {
		return PendingPublicationRecovery{}, fmt.Errorf("load selected recovery build lock: %w", err)
	}
	if err := validateGenerationBuildLock(*selectedGeneration, selected, validateProfileOwner); err != nil {
		return PendingPublicationRecovery{}, err
	}
	closure, err := deploy.BuildLockStoreClosure(selected, store, validateProfileOwner, validateBundleOwner)
	if err != nil {
		return PendingPublicationRecovery{}, err
	}
	if decision == deploy.PendingRecoveryKeepCandidate && !reflect.DeepEqual(closure, pending.Candidate.StoreObjects) {
		return PendingPublicationRecovery{}, fmt.Errorf("pending candidate store inventory does not match its selected build lock closure")
	}
	plan.SelectedLock = &selected
	plan.SelectedDigest = selectedGeneration.BuildLockDigest
	return plan, nil
}

type pendingReferenceRecovery func(context.Context, deploy.PendingBuildV1, deploy.PendingRecoveryDecision, *providers.RealizedImageV1, string, string) error

func executePendingPublicationRecovery(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	plan PendingPublicationRecovery,
	environment string,
	deploymentDir string,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
	recoverReferences pendingReferenceRecovery,
) error {
	if recoverReferences == nil {
		return fmt.Errorf("pending publication recovery requires a reference backend")
	}
	currentPending, found, err := operation.ReadPendingBuild()
	if err != nil {
		return err
	}
	if !found || !reflect.DeepEqual(currentPending, plan.Pending) {
		return fmt.Errorf("pending publication changed after recovery preflight")
	}
	if err := recoverReferences(ctx, plan.Pending, plan.Decision, plan.OldImage, environment, deploymentDir); err != nil {
		return err
	}
	if plan.SelectedLock != nil {
		if err := operation.RemoveOtherBuildLocks(plan.SelectedDigest, validateProfileOwner); err != nil {
			return err
		}
		if err := operation.RemoveUnreachableBuildObjects(store, *plan.SelectedLock, validateProfileOwner, validateBundleOwner); err != nil {
			return err
		}
	} else {
		if err := operation.RemoveAllBuildLocks(validateProfileOwner); err != nil {
			return err
		}
		if err := operation.RemoveAllBuildObjects(store); err != nil {
			return err
		}
	}
	if err := removeAbandonedProviderContainers(ctx, store); err != nil {
		return fmt.Errorf("clean abandoned provider helper containers: %w", err)
	}
	if err := store.RemoveTemporaryEntries(); err != nil {
		return fmt.Errorf("clean abandoned provider store temporary entries: %w", err)
	}
	return operation.RemovePendingBuild()
}

func validateGenerationBuildLock(generation deploy.EnvironmentGenerationState, lock deploy.BuildLockV1, validateProfileOwner providers.RequirementProfileOwnerValidator) error {
	digest, err := deploy.BuildLockDigestV1(lock, validateProfileOwner)
	if err != nil {
		return err
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		return err
	}
	if generation.BuildLockDigest != digest || generation.ImageDigest != lock.FinalImage.Digest || generation.RootFSSubject != lock.FinalImage.RootFSSubject || generation.Platform != lock.Platform || generation.RuntimePolicyDigest != policyDigest {
		return fmt.Errorf("selected generation does not match its build lock")
	}
	return nil
}
