package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type BuildPublicationInput struct {
	Environment   string
	DeploymentDir string
	Document      blueprint.Document
	Lock          deploy.BuildLockV1
	NoCache       bool
}

type buildPublicationBackend struct {
	newReferences   func(string, string) (EnvironmentImageReferences, error)
	createReference func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error
	removeReference func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error
	createCompanion func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error
	removeCompanion func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error
	// Persistence seams retain the existing operation-lock commit boundaries.
	commitState  func(*deploy.EnvironmentGenerationState, deploy.StateV1) error
	writeIntent  func(deploy.PendingBuildV1) error
	advancePhase func(string) error
	publishLock  func(deploy.BuildLockV1) (canonical.Digest, error)
	pruneLocks   func([]canonical.Digest) error
	pruneStore   func([]deploy.BuildLockV1) error
	removeIntent func() error
}

func PublishBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	input BuildPublicationInput,
) (deploy.StateV1, error) {
	return publishBuild(ctx, operation, store, input, buildPublicationBackend{
		newReferences:   NewEnvironmentImageReferences,
		createReference: CreateEnvironmentImageReference,
		removeReference: RemoveEnvironmentImageReference,
		createCompanion: CreatePortableEnvironmentReferenceV1,
		removeCompanion: RemovePortableEnvironmentReferenceV1,
	})
}

func publishBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	input BuildPublicationInput,
	backend buildPublicationBackend,
) (deploy.StateV1, error) {
	if ctx == nil {
		return deploy.StateV1{}, fmt.Errorf("publish build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.StateV1{}, err
	}
	if operation == nil {
		return deploy.StateV1{}, fmt.Errorf("publish build requires an operation lock")
	}
	if err := operation.RequireOwnerWritable(); err != nil {
		return deploy.StateV1{}, err
	}
	if backend.newReferences == nil || backend.createReference == nil || backend.removeReference == nil {
		return deploy.StateV1{}, fmt.Errorf("publish build requires a complete image-reference backend")
	}
	if err := validatePublicationDeployment(operation, store, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := requireValidatedPruningBoundaryV1(operation); err != nil {
		return deploy.StateV1{}, err
	}
	validateRetainedProfile, validateRetainedBundle := providerBuildRecoveryValidatorsV1(input.NoCache)
	writeIntent := backend.writeIntent
	if writeIntent == nil {
		writeIntent = operation.WritePendingBuild
	}
	advancePhase := backend.advancePhase
	if advancePhase == nil {
		advancePhase = operation.AdvancePendingBuildPhase
	}
	publishLock := backend.publishLock
	if publishLock == nil {
		publishLock = func(lock deploy.BuildLockV1) (canonical.Digest, error) {
			return operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
		}
	}
	pruneLocks := backend.pruneLocks
	if pruneLocks == nil {
		pruneLocks = func(roots []canonical.Digest) error {
			return operation.RemoveBuildLocksExcept(roots, validateRetainedProfile)
		}
	}
	pruneStore := backend.pruneStore
	if pruneStore == nil {
		pruneStore = func(roots []deploy.BuildLockV1) error {
			return operation.RemoveUnreachableBuildObjectsForBuilds(store, roots, validateRetainedProfile, validateRetainedBundle)
		}
	}
	removeIntent := backend.removeIntent
	if removeIntent == nil {
		removeIntent = operation.RemovePendingBuild
	}
	blueprintPayload, err := blueprint.EncodeResolvedDocumentV1(input.Document)
	if err != nil {
		return deploy.StateV1{}, err
	}
	blueprintDigest, err := blueprint.ResolvedDocumentDigestV1(blueprintPayload)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if blueprintDigest != input.Lock.BlueprintDigest {
		return deploy.StateV1{}, fmt.Errorf("publish build blueprint does not match its build lock")
	}
	if input.Document.Environment.ID != input.Environment {
		return deploy.StateV1{}, fmt.Errorf("publish build document environment does not match its ownership context")
	}
	if err := blueprint.ValidateSelectedPlatform(input.Document, input.Lock.Platform); err != nil {
		return deploy.StateV1{}, fmt.Errorf("publish build platform: %w", err)
	}

	lockDigest, err := deploy.BuildLockDigestV1(input.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("publish build lock: %w", err)
	}
	closure, err := deploy.BuildLockStoreClosure(input.Lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("publish build closure: %w", err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(input.Lock.RuntimePolicy)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("publish build runtime policy: %w", err)
	}
	references, err := backend.newReferences(input.Environment, input.DeploymentDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if err := ValidateEnvironmentImageReferences(references, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}

	state, found, err := operation.ReadStateV1()
	if err != nil {
		return deploy.StateV1{}, err
	}
	if found {
		recorded, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
		if err != nil {
			return deploy.StateV1{}, err
		}
		if recorded.Environment.ID != input.Environment {
			return deploy.StateV1{}, fmt.Errorf("publication cannot replace another environment's owner context")
		}
	}
	var old *deploy.EnvironmentGenerationState
	var oldImage *providers.RealizedImageV1
	var oldPairs []OwnedImageReferenceV1
	if found {
		old = state.Current
	}
	if old != nil {
		oldLock, lockFound, err := operation.ReadBuildLock(old.BuildLockDigest, validateRetainedProfile)
		if err != nil {
			return deploy.StateV1{}, err
		}
		if !lockFound {
			return deploy.StateV1{}, fmt.Errorf("current generation build lock %s is missing", old.BuildLockDigest)
		}
		if err := validateGenerationBuildLock(*old, oldLock, validateRetainedProfile); err != nil {
			return deploy.StateV1{}, fmt.Errorf("current generation: %w", err)
		}
		oldReferences := references
		oldReferences.Generation = old.Reference
		if err := ValidateEnvironmentImageReferences(oldReferences, input.Environment, input.DeploymentDir); err != nil {
			return deploy.StateV1{}, fmt.Errorf("current generation reference: %w", err)
		}
		image := oldLock.FinalImage
		oldImage = &image
		oldPairs = []OwnedImageReferenceV1{{Reference: old.Reference, Image: image}}
		if oldLock.PortableRuntimeLayer != nil {
			oldPairs, err = projectEnvironmentOwnedReferencesV1(*old, oldLock, input.Environment, input.DeploymentDir, validateRetainedProfile)
			if err != nil {
				return deploy.StateV1{}, err
			}
		}
	}

	candidate := deploy.EnvironmentGenerationState{
		Reference: references.Generation, ImageDigest: input.Lock.FinalImage.Digest,
		RootFSSubject: input.Lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: input.Lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	pairs, err := ProjectEnvironmentOwnedReferencesV1(candidate, input.Lock, input.Environment, input.DeploymentDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if (len(pairs) == 2 && backend.createCompanion == nil) || (len(oldPairs) == 2 && backend.removeCompanion == nil) {
		return deploy.StateV1{}, fmt.Errorf("portable publication requires complete companion operations")
	}
	if _, _, _, err := pendingPublicationRootsV1(operation, store, &input.Lock, lockDigest, input.Environment, input.DeploymentDir, validateRetainedProfile, validateRetainedBundle); err != nil {
		return deploy.StateV1{}, err
	}
	pending := deploy.PendingBuildV1{
		Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseValidated, Old: old,
		Candidate: deploy.PendingCandidateV1{
			TemporaryReference: references.Temporary, GenerationReference: references.Generation,
			Image: input.Lock.FinalImage, BuildLockDigest: lockDigest, StoreObjects: closure,
		},
		Cleanup:       publicationCleanupItems(references, old),
		OldReferences: oldPairs,
	}
	if len(pairs) == 2 {
		pending.Candidate.Owner = &candidate
		pending.Candidate.Companion = &pairs[1]
	}
	if err := validatePendingOwnedReferencesV1(pending, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := requireCurrentPublicationValidatedSeparationV1(operation, pending, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := writeIntent(pending); err != nil {
		return deploy.StateV1{}, err
	}

	if err := backend.createReference(ctx, input.Lock.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if len(pairs) == 2 {
		if err := backend.createCompanion(ctx, operation, pairs[1], candidate, input.Environment, input.DeploymentDir); err != nil {
			return deploy.StateV1{}, err
		}
	}
	if err := backend.createReference(ctx, input.Lock.FinalImage, references, EnvironmentReferenceGeneration, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := advancePhase(deploy.PendingBuildPhaseGenerationCreated); err != nil {
		return deploy.StateV1{}, err
	}

	publishedDigest, err := publishLock(input.Lock)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if publishedDigest != lockDigest {
		return deploy.StateV1{}, fmt.Errorf("published build lock digest %s does not match candidate %s", publishedDigest, lockDigest)
	}
	if err := advancePhase(deploy.PendingBuildPhaseLockPublished); err != nil {
		return deploy.StateV1{}, err
	}

	result := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: blueprintPayload, BlueprintSource: state.BlueprintSource,
		Platform: input.Lock.Platform, Overlay: input.Lock.Overlay, Current: &candidate,
		Staging: state.Staging, Deployment: state.Deployment,
	}
	commitState := backend.commitState
	if commitState == nil {
		commitState = operation.CommitStateV1
	}
	if err := commitState(old, result); err != nil {
		return deploy.StateV1{}, err
	}
	if err := advancePhase(deploy.PendingBuildPhaseStateCommitted); err != nil {
		return deploy.StateV1{}, err
	}
	if err := advancePhase(deploy.PendingBuildPhaseCleanup); err != nil {
		return deploy.StateV1{}, err
	}

	if old != nil {
		oldReferences := references
		oldReferences.Generation = old.Reference
		if err := backend.removeReference(ctx, *oldImage, oldReferences, EnvironmentReferenceGeneration, input.Environment, input.DeploymentDir); err != nil {
			return deploy.StateV1{}, err
		}
		if len(oldPairs) == 2 {
			if err := backend.removeCompanion(ctx, operation, oldPairs[1], *old, input.Environment, input.DeploymentDir); err != nil {
				return deploy.StateV1{}, err
			}
		}
	}
	if err := backend.removeReference(ctx, input.Lock.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	roots, digests, storePruneSafe, err := pendingPublicationRootsV1(operation, store, &input.Lock, lockDigest, input.Environment, input.DeploymentDir, validateRetainedProfile, validateRetainedBundle)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if err := pruneLocks(digests); err != nil {
		return deploy.StateV1{}, err
	}
	if storePruneSafe {
		if err := pruneStore(roots); err != nil {
			return deploy.StateV1{}, err
		}
	}
	if err := removeIntent(); err != nil {
		return deploy.StateV1{}, err
	}
	return result, nil
}

func validatePublicationDeployment(operation *deploy.OperationLock, store providerstore.Store, deploymentDir string) error {
	if err := operation.ValidateProviderStore(store); err != nil {
		return err
	}
	absolute, err := filepath.Abs(deploymentDir)
	if err != nil {
		return fmt.Errorf("resolve build publication directory: %w", err)
	}
	wantStore := filepath.Join(absolute, ".reploy", providerstore.StoreDirName)
	if filepath.Clean(store.Root()) != wantStore {
		return fmt.Errorf("build publication directory does not own the provider store")
	}
	return nil
}

func publicationCleanupItems(references EnvironmentImageReferences, old *deploy.EnvironmentGenerationState) []deploy.CleanupItemV1 {
	items := []deploy.CleanupItemV1{
		{Kind: deploy.CleanupKindTemporaryImageReference, Identity: references.Temporary},
	}
	if old != nil {
		items = append(items, deploy.CleanupItemV1{Kind: deploy.CleanupKindGenerationReference, Identity: old.Reference})
	}
	sort.Slice(items, func(left int, right int) bool {
		if items[left].Kind != items[right].Kind {
			return items[left].Kind < items[right].Kind
		}
		return items[left].Identity < items[right].Identity
	})
	return items
}
