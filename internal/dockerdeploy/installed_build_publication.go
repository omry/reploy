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
	if err := validatePublicationDeployment(sourceOperation, sourceStore, input.SourceDeploymentDir); err != nil {
		return deploy.StateV1{}, fmt.Errorf("installed build source: %w", err)
	}
	if err := validatePublicationDeployment(destinationOperation, destinationStore, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, fmt.Errorf("installed build destination: %w", err)
	}
	if err := validateInstalledBuildSource(input); err != nil {
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
	lockDigest, err := deploy.BuildLockDigestV1(input.Source.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	lockedSourceDigest, err := deploy.BuildLockDigestV1(lockedSourceBuild, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if lockedSourceDigest != lockDigest {
		return deploy.StateV1{}, fmt.Errorf("installed build source lock changed after build selection")
	}
	if lockDigest != input.Source.Generation.BuildLockDigest {
		return deploy.StateV1{}, fmt.Errorf("installed build source lock digest does not match its generation")
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(input.Source.Lock.RuntimePolicy)
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
	if _, pending, err := destinationOperation.ReadPendingBuild(); err != nil {
		return deploy.StateV1{}, err
	} else if pending {
		return deploy.StateV1{}, fmt.Errorf("installed build destination has a pending publication; recovery is required")
	}
	var old *deploy.EnvironmentGenerationState
	var oldImage *providers.RealizedImageV1
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
	}

	closure, err := backend.transferClosure(
		ctx, sourceOperation, destinationOperation, sourceStore, destinationStore, input.Source.Lock,
	)
	if err != nil {
		return deploy.StateV1{}, fmt.Errorf("publish installed build closure: %w", err)
	}
	candidate := input.Source.Generation
	candidate.Reference = references.Generation
	pending := deploy.PendingBuildV1{
		Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseValidated, Old: old,
		Candidate: deploy.PendingCandidateV1{
			TemporaryReference: references.Temporary, GenerationReference: references.Generation,
			Image: input.Source.Lock.FinalImage, BuildLockDigest: lockDigest, StoreObjects: closure,
		},
		Cleanup: publicationCleanupItems(references, old),
	}
	if err := destinationOperation.WritePendingBuild(pending); err != nil {
		return deploy.StateV1{}, err
	}
	if err := backend.createReference(ctx, input.Source.Lock.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := backend.createReference(ctx, input.Source.Lock.FinalImage, references, EnvironmentReferenceGeneration, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := destinationOperation.AdvancePendingBuildPhase(deploy.PendingBuildPhaseGenerationCreated); err != nil {
		return deploy.StateV1{}, err
	}
	publishedDigest, err := destinationOperation.PublishBuildLock(input.Source.Lock, registry.ValidateRequirementProfileV1)
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
	}
	if err := backend.removeReference(ctx, input.Source.Lock.FinalImage, references, EnvironmentReferenceTemporary, input.Environment, input.DestinationDeploymentDir); err != nil {
		return deploy.StateV1{}, err
	}
	if err := destinationOperation.RemoveOtherBuildLocks(lockDigest, registry.ValidateRequirementProfileV1); err != nil {
		return deploy.StateV1{}, err
	}
	if err := destinationOperation.RemoveUnreachableBuildObjects(destinationStore, input.Source.Lock, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		return deploy.StateV1{}, err
	}
	if err := destinationOperation.RemovePendingBuild(); err != nil {
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
