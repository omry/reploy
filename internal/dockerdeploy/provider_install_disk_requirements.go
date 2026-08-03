package dockerdeploy

import (
	"fmt"
	"math"
	"os"
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
	pathUpdates []PathUpdateAction,
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
		publication.Build, sourceStore,
		registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		return nil, fmt.Errorf("install disk requirements closure: %w", err)
	}
	lockContent, err := deploy.EncodeBuildLockV1(publication.Build, registry.ValidateRequirementProfileV1)
	if err != nil {
		return nil, err
	}
	destinationGeneration := publication.Source.Generation
	destinationGeneration.Reference = publication.References.Generation
	lockDigest, err := deploy.BuildLockDigestV1(publication.Build, registry.ValidateRequirementProfileV1)
	if err != nil {
		return nil, err
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(publication.Build.RuntimePolicy)
	if err != nil {
		return nil, err
	}
	destinationGeneration.ImageDigest = publication.Build.FinalImage.Digest
	destinationGeneration.RootFSSubject = publication.Build.FinalImage.RootFSSubject
	destinationGeneration.BuildLockDigest = lockDigest
	destinationGeneration.Platform = publication.Build.Platform
	destinationGeneration.RuntimePolicyDigest = policyDigest
	destinationState := publication.Source.State
	destinationState.Current = &destinationGeneration
	destinationState.Staging = nil
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
			Image:               publication.Build.FinalImage,
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
	for index, action := range pathUpdates {
		requirement, include, err := providerInstallPathUpdateDiskRequirementV1(action)
		if err != nil {
			return nil, fmt.Errorf("install disk requirement path update %d: %w", index, err)
		}
		if include {
			requirements = append(requirements, requirement)
		}
	}
	return requirements, nil
}

func providerInstallPathUpdateDiskRequirementV1(action PathUpdateAction) (providerInstallDiskRequirementV1, bool, error) {
	if action.Kind == PathPreservePrivateEnv || action.Kind == PathReplacePrivateEnv {
		if action.Target == "" || !filepath.IsAbs(action.Target) || filepath.Clean(action.Target) != action.Target {
			return providerInstallDiskRequirementV1{}, false, fmt.Errorf("private environment target must be an absolute clean path")
		}
		if action.Kind == PathPreservePrivateEnv {
			if _, err := os.Lstat(action.Target); err == nil {
				if _, loadErr := loadPrivateWorkloadEnvironmentV1(filepath.Dir(action.Target)); loadErr != nil {
					return providerInstallDiskRequirementV1{}, false, loadErr
				}
				return providerInstallDiskRequirementV1{}, false, nil
			} else if !os.IsNotExist(err) {
				return providerInstallDiskRequirementV1{}, false, fmt.Errorf("inspect preserved private environment: %w", err)
			}
		}
		environment, err := loadPrivateWorkloadEnvironmentV1(filepath.Dir(action.Source))
		if err != nil {
			return providerInstallDiskRequirementV1{}, false, err
		}
		if !environment.Exists {
			return providerInstallDiskRequirementV1{}, false, fmt.Errorf("staging private environment source is missing")
		}
		if len(environment.Raw) == 0 {
			return providerInstallDiskRequirementV1{}, false, nil
		}
		return providerInstallDiskRequirementV1{Path: action.Target, Bytes: uint64(len(environment.Raw))}, true, nil
	}
	return providerInstallManagedBindDiskRequirementV1(action)
}

func providerInstallManagedBindDiskRequirementV1(action PathUpdateAction) (providerInstallDiskRequirementV1, bool, error) {
	if action.Kind != PathPreserveManagedBind && action.Kind != PathReplaceManagedBind {
		return providerInstallDiskRequirementV1{}, false, nil
	}
	if action.Target == "" || !filepath.IsAbs(action.Target) || filepath.Clean(action.Target) != action.Target {
		return providerInstallDiskRequirementV1{}, false, fmt.Errorf("managed mount %q target must be an absolute clean path", action.Name)
	}
	if action.Kind == PathPreserveManagedBind {
		info, err := os.Lstat(action.Target)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return providerInstallDiskRequirementV1{}, false, fmt.Errorf("preserved managed mount %q must be a real directory: %s", action.Name, action.Target)
			}
			return providerInstallDiskRequirementV1{}, false, nil
		}
		if !os.IsNotExist(err) {
			return providerInstallDiskRequirementV1{}, false, fmt.Errorf("inspect preserved managed mount %q: %w", action.Name, err)
		}
	}
	sourceInfo, err := os.Lstat(action.Source)
	if err != nil {
		return providerInstallDiskRequirementV1{}, false, fmt.Errorf("inspect staging managed mount %q: %w", action.Name, err)
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return providerInstallDiskRequirementV1{}, false, fmt.Errorf("staging managed mount %q must be a real directory: %s", action.Name, action.Source)
	}
	bytes := uint64(0)
	err = filepath.WalkDir(action.Source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink: %s", path)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to copy special file: %s", path)
		}
		size := uint64(info.Size())
		if math.MaxUint64-bytes < size {
			return fmt.Errorf("managed mount %q size overflows uint64", action.Name)
		}
		bytes += size
		return nil
	})
	if err != nil {
		return providerInstallDiskRequirementV1{}, false, err
	}
	return providerInstallDiskRequirementV1{Path: action.Target, Bytes: bytes}, true, nil
}
