package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type InstalledBuildPublicationInputV1 struct {
	Environment              string
	SourceDeploymentDir      string
	DestinationDeploymentDir string
	Source                   CurrentBuild
	Build                    deploy.BuildLockV1
	Installation             deploy.InstallationStateV1
	References               EnvironmentImageReferences
}

type installedBuildPublicationBackend struct {
	transferClosure func(
		context.Context,
		*deploy.OperationLock,
		*deploy.OperationLock,
		providerstore.Store,
		providerstore.Store,
		deploy.BuildLockV1,
	) ([]providerstore.StoreObjectRef, error)
	createReference func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error
	removeReference func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error
	createCompanion func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error
	removeCompanion func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error
	removeIntent    func() error
}

// PublishInstalledBuildV1 transfers and publishes one already-current staged
// build into an installed destination. The caller acquires and retains the
// source operation lock before the destination operation lock.
func PublishInstalledBuildV1(
	ctx context.Context,
	sourceOperation *deploy.OperationLock,
	destinationOperation *deploy.OperationLock,
	sourceStore providerstore.Store,
	destinationStore providerstore.Store,
	input InstalledBuildPublicationInputV1,
) (deploy.StateV1, error) {
	return publishInstalledBuildV1(ctx, sourceOperation, destinationOperation, sourceStore, destinationStore, input, installedBuildPublicationBackend{
		transferClosure: transferInstalledBuildClosure,
		createReference: CreateEnvironmentImageReference,
		removeReference: RemoveEnvironmentImageReference,
		createCompanion: CreatePortableEnvironmentReferenceV1,
		removeCompanion: RemovePortableEnvironmentReferenceV1,
	})
}

func publishInstalledBuildV1(
	ctx context.Context,
	sourceOperation *deploy.OperationLock,
	destinationOperation *deploy.OperationLock,
	sourceStore providerstore.Store,
	destinationStore providerstore.Store,
	input InstalledBuildPublicationInputV1,
	backend installedBuildPublicationBackend,
) (deploy.StateV1, error) {
	if ctx == nil {
		return deploy.StateV1{}, fmt.Errorf("publish installed build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.StateV1{}, err
	}
	if sourceOperation == nil || destinationOperation == nil || sourceOperation == destinationOperation {
		return deploy.StateV1{}, fmt.Errorf("publish installed build requires distinct source and destination operation locks")
	}
	if backend.transferClosure == nil || backend.createReference == nil || backend.removeReference == nil {
		return deploy.StateV1{}, fmt.Errorf("publish installed build requires a complete backend")
	}
	if err := sourceOperation.RequireOwnerWritable(); err != nil {
		return deploy.StateV1{}, fmt.Errorf("installed build source: %w", err)
	}
	if err := validatePublicationDeployment(sourceOperation, sourceStore, input.SourceDeploymentDir); err != nil {
		return deploy.StateV1{}, fmt.Errorf("installed build source: %w", err)
	}
	if err := destinationOperation.RequireOwnerWritable(); err != nil {
		return deploy.StateV1{}, fmt.Errorf("installed build destination: %w", err)
	}
	if err := validatePublicationDeployment(destinationOperation, destinationStore, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, fmt.Errorf("installed build destination: %w", err)
	}
	if err := validateInstalledBuildSource(input); err != nil {
		return deploy.StateV1{}, err
	}
	if err := requireValidatedPruningBoundaryV1(sourceOperation); err != nil {
		return deploy.StateV1{}, err
	}
	if err := requireValidatedPruningBoundaryV1(destinationOperation); err != nil {
		return deploy.StateV1{}, err
	}
	destinationDir, err := filepath.Abs(input.DestinationDeploymentDir)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("resolve installed build destination: %w", err)
	}
	if input.Installation.TargetDir != destinationDir {
		return deploy.StateV1{}, fmt.Errorf("installed build installation target does not match the destination deployment")
	}
	lockedSourceState, found, err := sourceOperation.ReadStateV1()
	if err != nil {
		return deploy.StateV1{}, err
	}
	if !found || !reflect.DeepEqual(lockedSourceState, input.Source.State) {
		return deploy.StateV1{}, fmt.Errorf("installed build source state changed after build selection")
	}
	if _, pending, err := sourceOperation.ReadPendingBuild(); err != nil {
		return deploy.StateV1{}, err
	} else if pending {
		return deploy.StateV1{}, fmt.Errorf("installed build source has a pending publication; recovery is required")
	}
	lockedSourceBuild, found, err := sourceOperation.ReadBuildLock(input.Source.Generation.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if !found {
		return deploy.StateV1{}, fmt.Errorf("installed build source lock is missing after build selection")
	}
	sourceLockDigest, err := deploy.BuildLockDigestV1(input.Source.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	lockedSourceDigest, err := deploy.BuildLockDigestV1(lockedSourceBuild, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if lockedSourceDigest != sourceLockDigest {
		return deploy.StateV1{}, fmt.Errorf("installed build source lock changed after build selection")
	}
	if sourceLockDigest != input.Source.Generation.BuildLockDigest {
		return deploy.StateV1{}, fmt.Errorf("installed build source lock digest does not match its generation")
	}
	lockDigest, err := deploy.BuildLockDigestV1(input.Build, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(input.Build.RuntimePolicy)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if policyDigest != input.Source.Generation.RuntimePolicyDigest {
		return deploy.StateV1{}, fmt.Errorf("installed build source runtime policy does not match its generation")
	}

	references := input.References
	if err := ValidateEnvironmentImageReferences(references, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	destinationState, found, err := destinationOperation.ReadStateV1()
	if err != nil {
		return deploy.StateV1{}, err
	}
	if found {
		document, err := blueprint.DecodeResolvedDocumentV1(destinationState.Blueprint)
		if err != nil {
			return deploy.StateV1{}, err
		}
		if document.Environment.ID != input.Environment {
			return deploy.StateV1{}, fmt.Errorf("installed publication cannot replace another environment's destination state")
		}
	}
	if _, pending, err := destinationOperation.ReadPendingBuild(); err != nil {
		return deploy.StateV1{}, err
	} else if pending {
		return deploy.StateV1{}, fmt.Errorf("installed build destination has a pending publication; recovery is required")
	}
	var old *deploy.EnvironmentGenerationState
	var oldImage *providers.RealizedImageV1
	var oldPairs []OwnedImageReferenceV1
	if found {
		old = destinationState.Current
	}
	if old != nil {
		oldLock, lockFound, err := destinationOperation.ReadBuildLock(old.BuildLockDigest, registry.ValidateRequirementProfileV1)
		if err != nil {
			return deploy.StateV1{}, err
		}
		if !lockFound {
			return deploy.StateV1{}, fmt.Errorf("installed destination current build lock %s is missing", old.BuildLockDigest)
		}
		if err := validateGenerationBuildLock(*old, oldLock, registry.ValidateRequirementProfileV1); err != nil {
			return deploy.StateV1{}, fmt.Errorf("installed destination current generation: %w", err)
		}
		oldReferences := references
		oldReferences.Generation = old.Reference
		if err := ValidateEnvironmentImageReferences(oldReferences, input.Environment, input.DestinationDeploymentDir); err != nil {
			return deploy.StateV1{}, fmt.Errorf("installed destination current generation reference: %w", err)
		}
		image := oldLock.FinalImage
		oldImage = &image
		oldPairs, err = ProjectEnvironmentOwnedReferencesV1(*old, oldLock, input.Environment, input.DestinationDeploymentDir)
		if err != nil {
			return deploy.StateV1{}, err
		}
	}

	candidate := input.Source.Generation
	candidate.Reference = references.Generation
	candidate.ImageDigest = input.Build.FinalImage.Digest
	candidate.RootFSSubject = input.Build.FinalImage.RootFSSubject
	candidate.BuildLockDigest = lockDigest
	candidate.Platform = input.Build.Platform
	candidate.RuntimePolicyDigest = policyDigest
	if _, err := ProjectEnvironmentOwnedReferencesV1(input.Source.Generation, lockedSourceBuild, input.Environment, input.SourceDeploymentDir); err != nil {
		return deploy.StateV1{}, fmt.Errorf("installed build source ownership: %w", err)
	}
	pairs, err := ProjectEnvironmentOwnedReferencesV1(candidate, input.Build, input.Environment, input.DestinationDeploymentDir)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if (len(pairs) == 2 && backend.createCompanion == nil) || (len(oldPairs) == 2 && backend.removeCompanion == nil) {
		return deploy.StateV1{}, fmt.Errorf("portable installed publication requires complete companion operations")
	}
	if _, _, _, err := pendingPublicationRootsV1(destinationOperation, destinationStore, nil, "", input.Environment, input.DestinationDeploymentDir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		return deploy.StateV1{}, err
	}
	pending := deploy.PendingBuildV1{
		Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseValidated, Old: old,
		Candidate: deploy.PendingCandidateV1{
			TemporaryReference: references.Temporary, GenerationReference: references.Generation,
			Image: input.Build.FinalImage, BuildLockDigest: lockDigest, StoreObjects: []providerstore.StoreObjectRef{},
		},
		Cleanup: publicationCleanupItems(references, old), OldReferences: oldPairs,
	}
	if len(pairs) == 2 {
		pending.Candidate.Owner = &candidate
		pending.Candidate.Companion = &pairs[1]
	}
	if err := validatePendingOwnedReferencesV1(pending, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := requireCurrentPublicationValidatedSeparationV1(destinationOperation, pending, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	closure, err := backend.transferClosure(
		ctx, sourceOperation, destinationOperation, sourceStore, destinationStore, input.Build,
	)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("publish installed build closure: %w", err)
	}
	pending.Candidate.StoreObjects = closure
	if err := destinationOperation.WritePendingBuild(pending); err != nil {
		return deploy.StateV1{}, err
	}
	if err := backend.createReference(ctx, input.Build.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if len(pairs) == 2 {
		if err := backend.createCompanion(ctx, destinationOperation, pairs[1], candidate, input.Environment, input.DestinationDeploymentDir); err != nil {
			return deploy.StateV1{}, err
		}
	}
	if err := backend.createReference(ctx, input.Build.FinalImage, references, EnvironmentReferenceGeneration, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := destinationOperation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseGenerationCreated); err != nil {
		return deploy.StateV1{}, err
	}
	publishedDigest, err := destinationOperation.PublishBuildLock(input.Build, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if publishedDigest != lockDigest {
		return deploy.StateV1{}, fmt.Errorf("installed build published lock digest %s does not match candidate %s", publishedDigest, lockDigest)
	}
	if err := destinationOperation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseLockPublished); err != nil {
		return deploy.StateV1{}, err
	}
	result, _, err := destinationOperation.CommitInstalledStateV1(old, input.Source.State, candidate, input.Installation)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if err := destinationOperation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseStateCommitted); err != nil {
		return deploy.StateV1{}, err
	}
	if err := destinationOperation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseCleanup); err != nil {
		return deploy.StateV1{}, err
	}
	if old != nil {
		oldReferences := references
		oldReferences.Generation = old.Reference
		if err := backend.removeReference(ctx, *oldImage, oldReferences, EnvironmentReferenceGeneration, input.Environment, input.DestinationDeploymentDir); err != nil {
			return deploy.StateV1{}, err
		}
		if len(oldPairs) == 2 {
			if err := backend.removeCompanion(ctx, destinationOperation, oldPairs[1], *old, input.Environment, input.DestinationDeploymentDir); err != nil {
				return deploy.StateV1{}, err
			}
		}
	}
	if err := backend.removeReference(ctx, input.Build.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	roots, digests, storePruneSafe, err := pendingPublicationRootsV1(destinationOperation, destinationStore, &input.Build, lockDigest, input.Environment, input.DestinationDeploymentDir, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if err := destinationOperation.RemoveBuildLocksExcept(digests, registry.ValidateRequirementProfileV1); err != nil {
		return deploy.StateV1{}, err
	}
	if storePruneSafe {
		if err := destinationOperation.RemoveUnreachableBuildObjectsForBuilds(destinationStore, roots, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
			return deploy.StateV1{}, err
		}
	}
	removeIntent := backend.removeIntent
	if removeIntent == nil {
		removeIntent = destinationOperation.RemovePendingBuild
	}
	if err := removeIntent(); err != nil {
		return deploy.StateV1{}, err
	}
	return result, nil
}

func validateInstalledBuildSource(input InstalledBuildPublicationInputV1) error {
	if err := deploy.ValidateStateV1(input.Source.State); err != nil {
		return fmt.Errorf("installed build source state: %w", err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(input.Source.State.Blueprint)
	if err != nil {
		return err
	}
	if document.Environment.ID != input.Environment {
		return fmt.Errorf("installed build environment does not match the source blueprint")
	}
	if input.Source.State.Deployment != nil {
		return fmt.Errorf("installed build source must be staged")
	}
	if input.Source.State.Current == nil || !reflect.DeepEqual(*input.Source.State.Current, input.Source.Generation) {
		return fmt.Errorf("installed build source state does not name the selected generation")
	}
	if err := validateGenerationBuildLock(input.Source.Generation, input.Source.Lock, registry.ValidateRequirementProfileV1); err != nil {
		return fmt.Errorf("installed build source: %w", err)
	}
	blueprintDigest, err := blueprint.ResolvedDocumentDigestV1(input.Source.State.Blueprint)
	if err != nil {
		return err
	}
	if blueprintDigest != input.Source.Lock.BlueprintDigest || input.Source.State.Platform != input.Source.Lock.Platform || !reflect.DeepEqual(input.Source.State.Overlay, input.Source.Lock.Overlay) {
		return fmt.Errorf("installed build source state is stale relative to its selected build lock")
	}
	if err := deploy.ValidateBuildLockV1(input.Build, registry.ValidateRequirementProfileV1); err != nil {
		return fmt.Errorf("installed build candidate: %w", err)
	}
	sourceShape := input.Source.Lock
	buildShape := input.Build
	sourceShape.RuntimeLayer = deploy.ApplicationRuntimeLayerV1{}
	sourceShape.ValidationRecord = providerstore.StoreObjectRef{}
	sourceShape.FinalImage = providers.RealizedImageV1{}
	buildShape.RuntimeLayer = deploy.ApplicationRuntimeLayerV1{}
	buildShape.ValidationRecord = providerstore.StoreObjectRef{}
	buildShape.FinalImage = providers.RealizedImageV1{}
	if !reflect.DeepEqual(sourceShape, buildShape) ||
		input.Build.RuntimeLayer.Upstream != input.Source.Lock.RuntimeLayer.Upstream ||
		input.Build.RuntimeLayer.Verifier != input.Source.Lock.RuntimeLayer.Verifier {
		return fmt.Errorf("installed build candidate may differ from its staged source only in the application runtime identity and resulting validation")
	}
	if err := ValidateEnvironmentGenerationReference(input.Source.Generation.Reference, input.Environment, input.SourceDeploymentDir); err != nil {
		return fmt.Errorf("installed build source generation reference: %w", err)
	}
	if err := deploy.ValidateInstallationStateV1(input.Installation); err != nil {
		return err
	}
	return nil
}

func transferInstalledBuildClosure(
	ctx context.Context,
	sourceOperation *deploy.OperationLock,
	destinationOperation *deploy.OperationLock,
	sourceStore providerstore.Store,
	destinationStore providerstore.Store,
	build deploy.BuildLockV1,
) ([]providerstore.StoreObjectRef, error) {
	return sourceOperation.TransferBuildLockStoreClosure(
		ctx, destinationOperation, sourceStore, destinationStore, build,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	)
}
