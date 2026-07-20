package dockerdeploy

import (
	"fmt"
	"math"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

// providerInstallDiskRequirementsV1 computes the logical additional bytes
// that may coexist at the install publication peak. It deliberately counts
// the complete locked store closure even if the destination may already hold
// some objects; transfer still stages and verifies every selected object.
func providerInstallDiskRequirementsV1(
	sourceStore providerstore.Store,
	destinationStore providerstore.Store,
	publication InstalledBuildPublicationInputV1,
	old *deploy.EnvironmentGenerationState,
	candidates []providerInstallFileCandidateV1,
) ([]providerInstallDiskRequirementV1, error) {
	if candidates == nil {
		return nil, fmt.Errorf("install disk requirements require file candidates")
	}
	if err := validateInstalledBuildSource(publication); err != nil {
		return nil, fmt.Errorf("install disk requirements: %w", err)
	}
	if publication.Installation.Status != deploy.InstallationStatusConfiguring {
		return nil, fmt.Errorf("install disk requirements require a configuring installation")
	}
	wantSourceRoot := filepath.Join(publication.SourceDeploymentDir, ".reploy", providerstore.StoreDirName)
	if sourceStore.Root() != wantSourceRoot {
		return nil, fmt.Errorf("install disk requirements source store does not match the source deployment")
	}
	wantDestinationRoot := filepath.Join(publication.DestinationDeploymentDir, ".reploy", providerstore.StoreDirName)
	if destinationStore.Root() != wantDestinationRoot {
		return nil, fmt.Errorf("install disk requirements destination store does not match the destination deployment")
	}
	if old != nil {
		if err := deploy.ValidateEnvironmentGenerationState(*old); err != nil {
			return nil, fmt.Errorf("install disk requirements old generation: %w", err)
		}
	}

	closure, closureBytes, err := deploy.InspectBuildLockStoreClosure(
		publication.Source.Lock, sourceStore,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		return nil, fmt.Errorf("install disk requirements closure: %w", err)
	}
	lockContent, err := deploy.EncodeBuildLockV1(publication.Source.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		return nil, err
	}
	destinationGeneration := publication.Source.Generation
	destinationGeneration.Reference = publication.References.Generation
	destinationState := publication.Source.State
	destinationState.Current = &destinationGeneration
	destinationState.Deployment = &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1, Installation: publication.Installation,
	}
	stateContent, err := deploy.EncodeStateV1(destinationState)
	if err != nil {
		return nil, err
	}
	pending := deploy.PendingBuildV1{
		Schema: deploy.PendingBuildSchemaV1, Phase: deploy.PendingBuildPhaseValidated, Old: old,
		Candidate: deploy.PendingCandidateV1{
			TemporaryReference:  publication.References.Temporary,
			GenerationReference: publication.References.Generation,
			Image:               publication.Source.Lock.FinalImage,
			BuildLockDigest:     destinationGeneration.BuildLockDigest,
			StoreObjects:        closure,
		},
		Cleanup: publicationCleanupItems(publication.References, old),
	}
	maximumPendingBytes := uint64(0)
	for _, phase := range []string{
		deploy.PendingBuildPhaseValidated,
		deploy.PendingBuildPhaseGenerationCreated,
		deploy.PendingBuildPhaseLockPublished,
		deploy.PendingBuildPhaseStateCommitted,
		deploy.PendingBuildPhaseCleanup,
	} {
		pending.Phase = phase
		content, err := deploy.EncodePendingBuild(pending)
		if err != nil {
			return nil, err
		}
		if uint64(len(content)) > maximumPendingBytes {
			maximumPendingBytes = uint64(len(content))
		}
	}

	destinationBytes := closureBytes
	for _, size := range []uint64{
		uint64(len(lockContent)),
		uint64(len(stateContent)),
		maximumPendingBytes,
		maximumPendingBytes,
	} {
		if math.MaxUint64-destinationBytes < size {
			return nil, fmt.Errorf("install disk requirements overflow uint64")
		}
		destinationBytes += size
	}
	requirements := []providerInstallDiskRequirementV1{{Path: destinationStore.Root(), Bytes: destinationBytes}}
	for index, candidate := range candidates {
		if err := validateProviderInstallFileCandidateV1(candidate); err != nil {
			return nil, fmt.Errorf("install disk requirement candidate %d: %w", index, err)
		}
		if index > 0 && candidates[index-1].Path >= candidate.Path {
			return nil, fmt.Errorf("install disk requirement candidates must have unique paths sorted by destination")
		}
		requirements = append(requirements, providerInstallDiskRequirementV1{
			Path: candidate.Path, Bytes: uint64(len(candidate.Content)),
		})
	}
	return requirements, nil
}
