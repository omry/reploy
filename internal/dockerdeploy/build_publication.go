package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
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
	if backend.newReferences == nil || backend.createReference == nil || backend.removeReference == nil {
		return deploy.StateV1{}, fmt.Errorf("publish build requires a complete image-reference backend")
	}
	if err := validatePublicationDeployment(operation, store, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
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
	var old *deploy.EnvironmentGenerationState
	var oldImage *providers.RealizedImageV1
	priorProfileValidator := providers.RequirementProfileOwnerValidator(registry.ValidateRequirementProfileV1)
	if input.NoCache {
		priorProfileValidator = acceptProviderProfileOwnerForCutoverV1
	}
	if found {
		old = state.Current
	}
	if old != nil {
		oldLock, lockFound, err := operation.ReadBuildLock(old.BuildLockDigest, priorProfileValidator)
		if err != nil {
			return deploy.StateV1{}, err
		}
		if !lockFound {
			return deploy.StateV1{}, fmt.Errorf("current generation build lock %s is missing", old.BuildLockDigest)
		}
		if err := validateGenerationBuildLock(*old, oldLock, priorProfileValidator); err != nil {
			return deploy.StateV1{}, fmt.Errorf("current generation: %w", err)
		}
		oldReferences := references
		oldReferences.Generation = old.Reference
		if err := ValidateEnvironmentImageReferences(oldReferences, input.Environment, input.DeploymentDir); err != nil {
			return deploy.StateV1{}, fmt.Errorf("current generation reference: %w", err)
		}
		image := oldLock.FinalImage
		oldImage = &image
	}

	candidate := deploy.EnvironmentGenerationState{
		Reference: references.Generation, ImageDigest: input.Lock.FinalImage.Digest,
		RootFSSubject: input.Lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: input.Lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	pending := deploy.PendingBuildV1{
		Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseValidated, Old: old,
		Candidate: deploy.PendingCandidateV1{
			TemporaryReference: references.Temporary, GenerationReference: references.Generation,
			Image: input.Lock.FinalImage, BuildLockDigest: lockDigest, StoreObjects: closure,
		},
		Cleanup: publicationCleanupItems(references, old),
	}
	if err := operation.WritePendingBuild(pending); err != nil {
		return deploy.StateV1{}, err
	}

	if err := backend.createReference(ctx, input.Lock.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := backend.createReference(ctx, input.Lock.FinalImage, references, EnvironmentReferenceGeneration, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := operation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseGenerationCreated); err != nil {
		return deploy.StateV1{}, err
	}

	publishedDigest, err := operation.PublishBuildLock(input.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if publishedDigest != lockDigest {
		return deploy.StateV1{}, fmt.Errorf("published build lock digest %s does not match candidate %s", publishedDigest, lockDigest)
	}
	if err := operation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseLockPublished); err != nil {
		return deploy.StateV1{}, err
	}

	result := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: blueprintPayload, BlueprintSource: state.BlueprintSource,
		Platform: input.Lock.Platform, Overlay: input.Lock.Overlay, Current: &candidate,
		Staging: state.Staging, Deployment: state.Deployment,
	}
	if err := operation.CommitStateV1(old, result); err != nil {
		return deploy.StateV1{}, err
	}
	if err := operation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseStateCommitted); err != nil {
		return deploy.StateV1{}, err
	}
	if err := operation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseCleanup); err != nil {
		return deploy.StateV1{}, err
	}

	if old != nil {
		oldReferences := references
		oldReferences.Generation = old.Reference
		if err := backend.removeReference(ctx, *oldImage, oldReferences, EnvironmentReferenceGeneration, input.Environment, input.DeploymentDir); err != nil {
			return deploy.StateV1{}, err
		}
	}
	if err := backend.removeReference(ctx, input.Lock.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := operation.RemoveOtherBuildLocks(lockDigest, priorProfileValidator); err != nil {
		return deploy.StateV1{}, err
	}
	if err := operation.RemoveUnreachableBuildObjects(store, input.Lock, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		return deploy.StateV1{}, err
	}
	if err := operation.RemovePendingBuild(); err != nil {
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
