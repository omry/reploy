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
	Pending         deploy.PendingBuildV1
	Decision        deploy.PendingRecoveryDecision
	SelectedLock    *deploy.BuildLockV1
	SelectedDigest  canonical.Digest
	OldImage        *providers.RealizedImageV1
	ObservedCurrent *deploy.EnvironmentGenerationState
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
	if ctx == nil {
		return false, fmt.Errorf("pending publication recovery requires a context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
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
		if err := removeAbandonedBuildReferences(ctx, store); err != nil {
			return false, fmt.Errorf("clean abandoned build references: %w", err)
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
	recoverReferences := func(ctx context.Context, pending deploy.PendingBuildV1, decision deploy.PendingRecoveryDecision, oldImage *providers.RealizedImageV1, environment, dir string) error {
		return recoverPendingImageReferences(ctx, pending, decision, oldImage, environment, dir, RemoveEnvironmentImageReference, func(owner deploy.EnvironmentGenerationState, pair OwnedImageReferenceV1) error {
			return RemovePortableEnvironmentReferenceV1(ctx, operation, pair, owner, environment, dir)
		})
	}
	if err := executePendingPublicationRecovery(ctx, operation, store, plan, environment, deploymentDir, validateProfileOwner, validateBundleOwner, recoverReferences); err != nil {
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
	if err := validatePendingOwnedReferencesV1(pending, environment, deploymentDir); err != nil {
		return PendingPublicationRecovery{}, err
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
	plan := PendingPublicationRecovery{Pending: pending, Decision: decision, ObservedCurrent: current}
	if len(pending.OldReferences) != 0 {
		image := pending.OldReferences[0].Image
		plan.OldImage = &image
	} else if pending.Old != nil {
		oldLock, err := loadOnce(pending.Old.BuildLockDigest)
		if err != nil {
			return PendingPublicationRecovery{}, fmt.Errorf("load old recovery build lock: %w", err)
		}
		if err := validateGenerationBuildLock(*pending.Old, oldLock, validateProfileOwner); err != nil {
			return PendingPublicationRecovery{}, err
		}
		if oldLock.PortableRuntimeLayer != nil {
			return PendingPublicationRecovery{}, fmt.Errorf("portable old owner requires retained exact cleanup inventory")
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
	if decision == deploy.PendingRecoveryDiscardCandidate && selected.PortableRuntimeLayer != nil && len(pending.OldReferences) != 2 {
		return PendingPublicationRecovery{}, fmt.Errorf("portable surviving old owner is missing pending inventory")
	}
	if decision == deploy.PendingRecoveryKeepCandidate {
		if err := validatePendingCandidateOwnershipV1(pending, *selectedGeneration, selected, environment, deploymentDir, validateProfileOwner); err != nil {
			return PendingPublicationRecovery{}, err
		}
	} else if len(pending.OldReferences) != 0 {
		pairs := []OwnedImageReferenceV1{{Reference: selectedGeneration.Reference, Image: selected.FinalImage}}
		if selected.PortableRuntimeLayer != nil {
			pairs, err = projectEnvironmentOwnedReferencesV1(*selectedGeneration, selected, environment, deploymentDir, validateProfileOwner)
			if err != nil {
				return PendingPublicationRecovery{}, err
			}
		}
		if !reflect.DeepEqual(pairs, pending.OldReferences) {
			return PendingPublicationRecovery{}, fmt.Errorf("pending old inventory differs from surviving old owner")
		}
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
	if ctx == nil {
		return fmt.Errorf("pending publication recovery requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	state, _, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(state.Current, plan.ObservedCurrent) {
		return fmt.Errorf("current generation changed after recovery preflight")
	}
	roots, digests, err := pendingPublicationRootsV1(operation, store, plan.SelectedLock, plan.SelectedDigest, environment, deploymentDir, validateProfileOwner, validateBundleOwner)
	if err != nil {
		return err
	}
	if err := recoverReferences(ctx, plan.Pending, plan.Decision, plan.OldImage, environment, deploymentDir); err != nil {
		return err
	}
	if err := operation.RemoveBuildLocksExcept(digests, validateProfileOwner); err != nil {
		return err
	}
	if err := operation.RemoveUnreachableBuildObjectsForBuilds(store, roots, validateProfileOwner, validateBundleOwner); err != nil {
		return err
	}
	if err := removeAbandonedProviderContainers(ctx, store); err != nil {
		return fmt.Errorf("clean abandoned provider helper containers: %w", err)
	}
	if err := removeAbandonedBuildReferences(ctx, store); err != nil {
		return fmt.Errorf("clean abandoned build references: %w", err)
	}
	if err := store.RemoveTemporaryEntries(); err != nil {
		return fmt.Errorf("clean abandoned provider store temporary entries: %w", err)
	}
	return operation.RemovePendingBuild()
}

func validatePendingCandidateOwnershipV1(pending deploy.PendingBuildV1, owner deploy.EnvironmentGenerationState, lock deploy.BuildLockV1, environment, dir string, validateProfile providers.RequirementProfileOwnerValidator) error {
	pairs := []OwnedImageReferenceV1{{Reference: owner.Reference, Image: lock.FinalImage}}
	if lock.PortableRuntimeLayer != nil {
		var err error
		pairs, err = projectEnvironmentOwnedReferencesV1(owner, lock, environment, dir, validateProfile)
		if err != nil {
			return err
		}
	}
	if len(pairs) == 1 {
		if pending.Candidate.Companion != nil {
			return fmt.Errorf("pending companion has no locked portable layer")
		}
	} else if pending.Candidate.Owner == nil || *pending.Candidate.Owner != owner || pending.Candidate.Companion == nil || *pending.Candidate.Companion != pairs[1] {
		return fmt.Errorf("pending companion differs from committed candidate ownership")
	}
	return nil
}

// Preserve current and independently committed validated ownership, even when
// there is no current generation or both owners share the same lock digest.
func pendingPublicationRootsV1(operation *deploy.OperationLock, store providerstore.Store, selected *deploy.BuildLockV1, selectedDigest canonical.Digest, environment, dir string, validateProfile providers.RequirementProfileOwnerValidator, validateBundle providers.ResolvedBundleOwnerValidator) ([]deploy.BuildLockV1, []canonical.Digest, error) {
	roots := []deploy.BuildLockV1{}
	digests := []canonical.Digest{}
	if selected != nil {
		if _, err := deploy.BuildLockStoreClosure(*selected, store, validateProfile, validateBundle); err != nil {
			return nil, nil, err
		}
		roots = append(roots, *selected)
		digests = append(digests, selectedDigest)
	}
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil {
		return nil, nil, err
	}
	if !found || record.Discarded {
		return roots, digests, nil
	}
	if err := ValidateEnvironmentGenerationReference(record.ImageReference, environment, dir); err != nil {
		return nil, nil, err
	}
	validated, found, err := operation.ReadBuildLock(record.BuildLockDigest, validateProfile)
	if err != nil {
		return nil, nil, err
	}
	if !found || validated.FinalImage != record.Image || validated.Platform != record.Platform || validated.PackageOverrides.EnvironmentID != environment {
		return nil, nil, fmt.Errorf("committed validated owner is missing its exact build lock")
	}
	if _, err := deploy.BuildLockStoreClosure(validated, store, validateProfile, validateBundle); err != nil {
		return nil, nil, err
	}
	for _, digest := range digests {
		if digest == record.BuildLockDigest {
			return roots, digests, nil
		}
	}
	return append(roots, validated), append(digests, record.BuildLockDigest), nil
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
