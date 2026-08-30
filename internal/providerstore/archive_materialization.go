package providerstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const archiveMaterializationWorkspacePattern = ".reploy-materialize-*"

// MaterializeArchive verifies and installs one closed archive without
// invoking any command or using the network.
func (store Store) MaterializeArchive(ctx context.Context, request ArchiveMaterializationRequest) (ArchiveMaterializationResult, error) {
	if ctx == nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("archive materialization context is required")
	}
	if err := ctx.Err(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	validated, err := validateArchiveMaterializationRequest(request)
	if err != nil {
		return ArchiveMaterializationResult{}, err
	}

	finalPath := filepath.Join(request.DestinationRoot, request.InstallDirectory)
	if err := requireAbsentArchiveDestination(finalPath); err != nil {
		return ArchiveMaterializationResult{}, err
	}

	archiveFile, err := store.OpenVerifiedArtifact(request.Artifact)
	if err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("open verified archive: %w", err)
	}
	defer archiveFile.Close()

	stage, err := os.MkdirTemp(request.DestinationRoot, archiveMaterializationWorkspacePattern)
	if err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("create archive materialization workspace: %w", err)
	}
	defer cleanupArchiveMaterializationWorkspace(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("protect archive materialization workspace: %w", err)
	}

	materializer := archiveMaterializer{
		ctx:              ctx,
		stage:            stage,
		request:          validated,
		archivePaths:     map[string]struct{}{},
		nodes:            map[string]archiveMaterializedNode{".": {kind: ArchiveEntryKindDirectory, explicit: false}},
		destinationPaths: map[string]string{portableArchiveDestinationKey("."): "."},
		executablePaths:  make(map[string]string, len(request.ExecutablePaths)),
	}
	for _, executable := range request.ExecutablePaths {
		materializer.executablePaths[executable] = ""
	}

	if err := materializer.extract(archiveFile); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := materializer.validateExecutablePaths(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := VerifyOpenArtifact(archiveFile, request.Artifact); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("reverify archive after materialization: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := normalizeMaterializedTree(stage); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := syncStoreDirectory(stage); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("sync materialized archive: %w", err)
	}
	if err := requireAbsentArchiveDestination(finalPath); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := publishArchiveMaterializedDirectory(stage, finalPath); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("publish materialized archive: %w", err)
	}

	return ArchiveMaterializationResult{
		FinalPath:            finalPath,
		ObservedEntryCount:   strconv.FormatUint(materializer.entryCount, 10),
		ObservedUnpackedSize: strconv.FormatUint(materializer.unpackedSize, 10),
		ObservedEntries:      append([]ArchiveMaterializationEntry(nil), materializer.entries...),
	}, nil
}

// MaterializeArchive is the package-level form for callers that keep the
// store separate from the request construction.
func MaterializeArchive(ctx context.Context, store Store, request ArchiveMaterializationRequest) (ArchiveMaterializationResult, error) {
	return store.MaterializeArchive(ctx, request)
}

func (materializer *archiveMaterializer) extract(file *os.File) error {
	switch materializer.request.Format {
	case ArchiveFormatTarGz:
		return materializer.extractTarGz(file)
	case ArchiveFormatZip:
		return materializer.extractZipFile(file)
	default:
		return fmt.Errorf("archive materialization format %q is unsupported", materializer.request.Format)
	}
}
