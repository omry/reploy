package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// MaterializationMountInputs maps a previously validated resolved bundle to
// the immutable files referenced by a closed materialization transaction. The
// returned records contain logical paths and descriptors only; store paths are
// resolved later by PrepareMaterializationContext.
func MaterializationMountInputs(bundle providers.ResolvedBundle, transaction providers.MaterializationTransaction) ([]MaterializationMountInput, error) {
	if err := providers.ValidateMaterializationTransaction(transaction); err != nil {
		return nil, err
	}
	if err := bundle.Identity.Validate(); err != nil {
		return nil, fmt.Errorf("materialization bundle identity: %w", err)
	}
	if bundle.Payload.NodeID != transaction.NodeID {
		return nil, fmt.Errorf("materialization bundle node %q does not match transaction node %q", bundle.Payload.NodeID, transaction.NodeID)
	}
	if bundle.Payload.Artifacts == nil {
		return nil, fmt.Errorf("materialization bundle artifacts must use an array")
	}
	artifacts := make(map[string]providerstore.ArtifactDescriptor, len(bundle.Payload.Artifacts))
	for index, artifact := range bundle.Payload.Artifacts {
		if err := artifact.Validate(); err != nil {
			return nil, fmt.Errorf("materialization bundle artifact %d: %w", index, err)
		}
		if index > 0 && bundle.Payload.Artifacts[index-1].LogicalPath >= artifact.LogicalPath {
			return nil, fmt.Errorf("materialization bundle artifacts must be unique and sorted")
		}
		artifacts[artifact.LogicalPath] = artifact
	}
	retainedSourceDigests := make(map[canonical.Digest]bool, len(bundle.Payload.SelectedSources))
	for index, source := range bundle.Payload.SelectedSources {
		if err := providers.ValidateResolvedSourceInput(source); err != nil {
			return nil, fmt.Errorf("materialization bundle selected source %d: %w", index, err)
		}
		retainedSourceDigests[source.SourceArtifactDigest] = true
	}

	references := make(map[string][]string, len(transaction.Mounts))
	seenReferences := map[string]bool{}
	for _, argument := range transaction.Argv {
		if argument.Kind != providers.TypedArgumentMountedArtifact {
			continue
		}
		key := argument.MountID + "\x00" + argument.RelativePath
		if seenReferences[key] {
			return nil, fmt.Errorf("materialization artifact %q is referenced more than once by mount %q", argument.RelativePath, argument.MountID)
		}
		seenReferences[key] = true
		references[argument.MountID] = append(references[argument.MountID], argument.RelativePath)
	}

	usedArtifacts := map[string]bool{}
	inputs := make([]MaterializationMountInput, 0, len(transaction.Mounts))
	for _, mount := range transaction.Mounts {
		paths := append([]string{}, references[mount.ID]...)
		sort.Strings(paths)
		if len(paths) == 0 {
			return nil, fmt.Errorf("materialization mount %q has no typed artifact references", mount.ID)
		}
		input := MaterializationMountInput{ID: mount.ID, SourceDigest: mount.SourceDigest, Files: make([]MaterializationMountFile, 0, len(paths))}
		for _, relativePath := range paths {
			artifact, exists := artifacts[relativePath]
			if !exists {
				return nil, fmt.Errorf("materialization mount %q references artifact %q absent from the resolved bundle", mount.ID, relativePath)
			}
			if usedArtifacts[relativePath] {
				return nil, fmt.Errorf("materialization artifact %q is assigned to more than one mount", relativePath)
			}
			switch mount.SourceKind {
			case providers.BuildMountSourceScript:
				if artifact != transaction.Script {
					return nil, fmt.Errorf("materialization script mount %q references a non-script bundle artifact %q", mount.ID, relativePath)
				}
			case providers.BuildMountSourceArtifact:
				if mount.SourceDigest != bundle.Identity {
					return nil, fmt.Errorf("materialization artifact mount %q does not bind the resolved bundle identity", mount.ID)
				}
				if artifact == transaction.Script {
					return nil, fmt.Errorf("materialization artifact mount %q references the trusted script", mount.ID)
				}
			default:
				return nil, fmt.Errorf("materialization mount %q cannot stage source kind %q", mount.ID, mount.SourceKind)
			}
			usedArtifacts[relativePath] = true
			input.Files = append(input.Files, MaterializationMountFile{RelativePath: relativePath, Artifact: artifact})
		}
		inputs = append(inputs, input)
	}
	retainedSources := map[canonical.Digest]bool{}
	unused := []string{}
	for logicalPath, artifact := range artifacts {
		if usedArtifacts[logicalPath] {
			continue
		}
		if retainedSourceDigests[artifact.SHA256] {
			retainedSources[artifact.SHA256] = true
			continue
		}
		unused = append(unused, logicalPath)
	}
	if len(unused) != 0 {
		sort.Strings(unused)
		return nil, fmt.Errorf("materialization transaction does not account for bundle artifacts: %v", unused)
	}
	for digest := range retainedSourceDigests {
		if !retainedSources[digest] {
			return nil, fmt.Errorf("materialization bundle retained source artifact %s is missing", digest)
		}
	}
	return inputs, nil
}

func bindVerifiedMaterializationArtifacts(inputs []MaterializationMountInput, verified map[canonical.Digest]string) error {
	if len(verified) == 0 {
		return nil
	}
	used := make(map[canonical.Digest]bool, len(verified))
	for inputIndex := range inputs {
		for fileIndex := range inputs[inputIndex].Files {
			file := &inputs[inputIndex].Files[fileIndex]
			if path, found := verified[file.Artifact.SHA256]; found {
				file.verifiedPath = path
				used[file.Artifact.SHA256] = true
			}
		}
	}
	for digest := range verified {
		if !used[digest] {
			return fmt.Errorf("verified artifact %s is absent from the materialization bundle", digest)
		}
	}
	return nil
}
