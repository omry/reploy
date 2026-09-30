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
	newReferences                             func(string, string) (EnvironmentImageReferences, error)
	createReference                           func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error
	removeReference                           func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error
	createPortableRuntimeLayerDigestReference func(context.Context, providers.RealizedImageV1, string, string) error
	createPortableRuntimeLayerReference       func(context.Context, providers.RealizedImageV1, string, string, string) error
	removePortableRuntimeLayerReference       func(context.Context, providers.RealizedImageV1, string, string, string) error
	removePortableRuntimeLayerDigestReference func(context.Context, providers.RealizedImageV1, string, string) error
	commitState                               func(*deploy.OperationLock, *deploy.EnvironmentGenerationState, deploy.StateV1) error
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
		createPortableRuntimeLayerDigestReference: CreateEnvironmentPortableRuntimeLayerDigestReference,
		createPortableRuntimeLayerReference:       CreateEnvironmentPortableRuntimeLayerReference,
		removePortableRuntimeLayerReference:       RemoveEnvironmentPortableRuntimeLayerReference,
		removePortableRuntimeLayerDigestReference: RemoveEnvironmentPortableRuntimeLayerDigestReference,
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
	if input.Lock.PortableRuntimeLayer != nil &&
		(backend.createPortableRuntimeLayerDigestReference == nil ||
			backend.createPortableRuntimeLayerReference == nil ||
			backend.removePortableRuntimeLayerReference == nil ||
			backend.removePortableRuntimeLayerDigestReference == nil) {
		return deploy.StateV1{}, fmt.Errorf("publish build requires complete portable runtime layer reference support")
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
	var oldLock *deploy.BuildLockV1
	priorProfileValidator := providers.RequirementProfileOwnerValidator(registry.ValidateRequirementProfileV1)
	if input.NoCache {
		priorProfileValidator = acceptProviderProfileOwnerForCutoverV1
	}
	if found {
		old = state.Current
	}
	if old != nil {
		loadedOldLock, lockFound, err := operation.ReadBuildLock(old.BuildLockDigest, priorProfileValidator)
		if err != nil {
			return deploy.StateV1{}, err
		}
		if !lockFound {
			return deploy.StateV1{}, fmt.Errorf("current generation build lock %s is missing", old.BuildLockDigest)
		}
		if err := validateGenerationBuildLock(*old, loadedOldLock, priorProfileValidator); err != nil {
			return deploy.StateV1{}, fmt.Errorf("current generation: %w", err)
		}
		oldReferences := references
		oldReferences.Generation = old.Reference
		if err := ValidateEnvironmentImageReferences(oldReferences, input.Environment, input.DeploymentDir); err != nil {
			return deploy.StateV1{}, fmt.Errorf("current generation reference: %w", err)
		}
		image := loadedOldLock.FinalImage
		oldImage = &image
		oldLockCopy := loadedOldLock
		oldLock = &oldLockCopy
	}
	if oldLock != nil && oldLock.PortableRuntimeLayer != nil &&
		(backend.removePortableRuntimeLayerReference == nil || backend.removePortableRuntimeLayerDigestReference == nil) {
		return deploy.StateV1{}, fmt.Errorf("publish build requires portable runtime layer cleanup support")
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
	if input.Lock.PortableRuntimeLayer != nil {
		portableReference, err := NewEnvironmentPortableRuntimeLayerReference(
			input.Lock.PortableRuntimeLayer.Result.ConfigDigest, input.Environment, input.DeploymentDir,
		)
		if err != nil {
			return deploy.StateV1{}, fmt.Errorf("candidate portable runtime layer reference: %w", err)
		}
		pending.Cleanup = append(pending.Cleanup, deploy.CleanupItemV1{
			Kind: deploy.CleanupKindTemporaryImageReference, Identity: portableReference,
		})
	}
	if old != nil && oldLock != nil && oldLock.PortableRuntimeLayer != nil {
		portableReference, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(
			oldLock.PortableRuntimeLayer.Result.ConfigDigest, old.Reference,
			input.Environment, input.DeploymentDir,
		)
		if err != nil {
			return deploy.StateV1{}, fmt.Errorf("old portable runtime layer reference: %w", err)
		}
		pending.Cleanup = append(pending.Cleanup, deploy.CleanupItemV1{
			Kind: deploy.CleanupKindGenerationReference, Identity: portableReference,
		})
	}
	sort.Slice(pending.Cleanup, func(left int, right int) bool {
		if pending.Cleanup[left].Kind != pending.Cleanup[right].Kind {
			return pending.Cleanup[left].Kind < pending.Cleanup[right].Kind
		}
		return pending.Cleanup[left].Identity < pending.Cleanup[right].Identity
	})
	// Pending-publication recovery owns candidate alias cleanup. CommitStateV1
	// may persist the new state and still return an error (for example, if the
	// parent-directory sync fails); removing the alias here would then orphan
	// the selected generation.
	if err := operation.WritePendingBuild(pending); err != nil {
		return deploy.StateV1{}, err
	}
	if input.Lock.PortableRuntimeLayer != nil {
		if err := backend.createPortableRuntimeLayerDigestReference(
			ctx, input.Lock.PortableRuntimeLayer.Result, input.Environment, input.DeploymentDir,
		); err != nil {
			return deploy.StateV1{}, err
		}
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
	if input.Lock.PortableRuntimeLayer != nil {
		if err := backend.createPortableRuntimeLayerReference(
			ctx, input.Lock.PortableRuntimeLayer.Result, references.Generation,
			input.Environment, input.DeploymentDir,
		); err != nil {
			return deploy.StateV1{}, err
		}
	}

	result := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: blueprintPayload, BlueprintSource: state.BlueprintSource,
		Platform: input.Lock.Platform, Overlay: input.Lock.Overlay, Current: &candidate,
		Staging: state.Staging, Deployment: state.Deployment,
	}
	commitState := backend.commitState
	if commitState == nil {
		commitState = func(operation *deploy.OperationLock, expected *deploy.EnvironmentGenerationState, state deploy.StateV1) error {
			return operation.CommitStateV1(expected, state)
		}
	}
	if err := commitState(operation, old, result); err != nil {
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
		if oldLock != nil && oldLock.PortableRuntimeLayer != nil {
			if err := backend.removePortableRuntimeLayerReference(
				ctx, oldLock.PortableRuntimeLayer.Result, old.Reference,
				input.Environment, input.DeploymentDir,
			); err != nil {
				return deploy.StateV1{}, err
			}
			if err := backend.removePortableRuntimeLayerDigestReference(
				ctx, oldLock.PortableRuntimeLayer.Result, input.Environment, input.DeploymentDir,
			); err != nil {
				return deploy.StateV1{}, err
			}
		}
	}
	if err := backend.removeReference(ctx, input.Lock.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if input.Lock.PortableRuntimeLayer != nil {
		if err := backend.removePortableRuntimeLayerDigestReference(
			ctx, input.Lock.PortableRuntimeLayer.Result, input.Environment, input.DeploymentDir,
		); err != nil {
			return deploy.StateV1{}, err
		}
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
