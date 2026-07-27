package dockerdeploy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type MaterializationMountFile struct {
	RelativePath string
	Artifact     providerstore.ArtifactDescriptor
	verifiedPath string
}

type MaterializationMountInput struct {
	ID           string
	SourceDigest canonical.Digest
	Files        []MaterializationMountFile
}

type PreparedMaterializationContext struct {
	Dir     string
	Sources []MaterializationMountSource
}

func PrepareMaterializationContext(store providerstore.Store, transaction providers.MaterializationTransaction, inputs []MaterializationMountInput) (PreparedMaterializationContext, func(), error) {
	if err := providers.ValidateMaterializationTransaction(transaction); err != nil {
		return PreparedMaterializationContext{}, func() {}, err
	}
	if len(inputs) != len(transaction.Mounts) {
		return PreparedMaterializationContext{}, func() {}, fmt.Errorf("materialization mount inputs do not match transaction mounts")
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		return PreparedMaterializationContext{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(workspace) }
	contextDir := filepath.Join(workspace, "context")
	if err := os.Mkdir(contextDir, 0o700); err != nil {
		cleanup()
		return PreparedMaterializationContext{}, func() {}, fmt.Errorf("create materialization context: %w", err)
	}
	prepared := PreparedMaterializationContext{Dir: contextDir, Sources: make([]MaterializationMountSource, 0, len(inputs))}
	for index, mount := range transaction.Mounts {
		input := inputs[index]
		if input.ID != mount.ID || input.SourceDigest != mount.SourceDigest {
			cleanup()
			return PreparedMaterializationContext{}, func() {}, fmt.Errorf("materialization mount input %d does not match mount %q", index, mount.ID)
		}
		if mount.SourceKind == providers.BuildMountSourcePrivateOutput {
			cleanup()
			return PreparedMaterializationContext{}, func() {}, fmt.Errorf("private-output mount %q belongs to the disposable resolver runner", mount.ID)
		}
		if mount.ExpectedKind != "directory" {
			cleanup()
			return PreparedMaterializationContext{}, func() {}, fmt.Errorf("materialization mount %q must stage a directory for relative typed operands", mount.ID)
		}
		if input.Files == nil || len(input.Files) == 0 {
			cleanup()
			return PreparedMaterializationContext{}, func() {}, fmt.Errorf("materialization mount input %q must contain files", input.ID)
		}
		if mount.SourceKind == providers.BuildMountSourceScript && (len(input.Files) != 1 || input.Files[0].Artifact.SHA256 != mount.SourceDigest) {
			cleanup()
			return PreparedMaterializationContext{}, func() {}, fmt.Errorf("script mount %q must contain exactly its declared script digest", mount.ID)
		}
		mountRelative := path.Join("mounts", mount.ID)
		mountDir := filepath.Join(contextDir, filepath.FromSlash(mountRelative))
		if err := os.MkdirAll(mountDir, 0o700); err != nil {
			cleanup()
			return PreparedMaterializationContext{}, func() {}, fmt.Errorf("create materialization mount directory: %w", err)
		}
		for fileIndex, file := range input.Files {
			if err := validateMaterializationMountFile(file, input.Files, fileIndex); err != nil {
				cleanup()
				return PreparedMaterializationContext{}, func() {}, fmt.Errorf("materialization mount %q: %w", mount.ID, err)
			}
			source, err := materializationArtifactSource(store, file)
			if err != nil {
				cleanup()
				return PreparedMaterializationContext{}, func() {}, fmt.Errorf("verify materialization mount %q file %q: %w", mount.ID, file.RelativePath, err)
			}
			target := filepath.Join(mountDir, filepath.FromSlash(file.RelativePath))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				cleanup()
				return PreparedMaterializationContext{}, func() {}, fmt.Errorf("create materialization mount parent: %w", err)
			}
			if err := os.Link(source, target); err != nil {
				cleanup()
				return PreparedMaterializationContext{}, func() {}, fmt.Errorf("hardlink materialization mount %q file %q: %w", mount.ID, file.RelativePath, err)
			}
		}
		prepared.Sources = append(prepared.Sources, MaterializationMountSource{ID: mount.ID, ContextPath: mountRelative})
	}
	return prepared, cleanup, nil
}

func materializationArtifactSource(store providerstore.Store, file MaterializationMountFile) (string, error) {
	if file.verifiedPath == "" {
		if err := store.VerifyArtifact(file.Artifact); err != nil {
			return "", err
		}
		return store.BlobPath(file.Artifact.SHA256)
	}
	root := filepath.Join(store.Root(), "tmp")
	relative, err := filepath.Rel(root, file.verifiedPath)
	inTemporaryRoot := err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
	storePath, storePathErr := store.BlobPath(file.Artifact.SHA256)
	if !inTemporaryRoot && (storePathErr != nil || file.verifiedPath != storePath) {
		return "", fmt.Errorf("verified artifact path must be the exact store blob or beneath the provider-store temporary root")
	}
	if err := providerstore.InspectArtifactFile(file.verifiedPath, file.Artifact); err != nil {
		return "", err
	}
	return file.verifiedPath, nil
}

func validateMaterializationMountFile(file MaterializationMountFile, files []MaterializationMountFile, index int) error {
	if file.RelativePath == "" || path.IsAbs(file.RelativePath) || path.Clean(file.RelativePath) != file.RelativePath || file.RelativePath == "." || strings.Contains(file.RelativePath, `\`) {
		return fmt.Errorf("file path %q must be a normalized relative slash path", file.RelativePath)
	}
	for _, component := range strings.Split(file.RelativePath, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("file path %q contains an invalid component", file.RelativePath)
		}
	}
	if index > 0 && files[index-1].RelativePath >= file.RelativePath {
		return fmt.Errorf("files must be unique and sorted by relative path")
	}
	if err := file.Artifact.Validate(); err != nil {
		return fmt.Errorf("file %q artifact: %w", file.RelativePath, err)
	}
	return nil
}
