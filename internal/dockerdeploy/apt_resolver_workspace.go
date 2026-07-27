package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

// PreparedAPTResolverWorkspace is one initially empty, deployment-local
// resolver scratch directory. It is removed after the containing operation.
type PreparedAPTResolverWorkspace struct {
	HostDir        string
	ContainerDir   string
	SeededArchives []providerstore.ArtifactDescriptor
}

// APTArchiveInventoryEntry is one exact post-download cache file. Membership
// in this inventory is not bundle membership; later control inspection joins
// entries to the selected package plan. UnchangedSeed distinguishes reusable
// cache input from content produced or replaced by the current APT run.
type APTArchiveInventoryEntry struct {
	Filename      string
	HostPath      string
	Artifact      providerstore.ArtifactDescriptor
	UnchangedSeed bool
}

// InventoryAPTResolverArchives hashes every real .deb in the private archive
// cache after a successful download. It rejects partial and unexpected output
// and publishes nothing.
func InventoryAPTResolverArchives(ctx context.Context, prepared PreparedAPTResolverWorkspace) ([]APTArchiveInventoryEntry, error) {
	if ctx == nil {
		return nil, fmt.Errorf("APT archive inventory context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if prepared.HostDir == "" || !filepath.IsAbs(prepared.HostDir) || filepath.Clean(prepared.HostDir) != prepared.HostDir || prepared.ContainerDir != aptprovider.ResolverScratchDirectory {
		return nil, fmt.Errorf("APT archive inventory workspace is invalid")
	}
	archives := filepath.Join(prepared.HostDir, "archives")
	archiveInfo, err := os.Lstat(archives)
	if err != nil || !archiveInfo.IsDir() || archiveInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("APT archive inventory requires a real archive directory")
	}
	partial := filepath.Join(archives, "partial")
	partialInfo, err := os.Lstat(partial)
	if err != nil || !partialInfo.IsDir() || partialInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("APT archive inventory partial path must be a real directory")
	}
	// APT may chown this empty directory to its sandbox user, making a host-side
	// ReadDir fail even though no partial artifact exists. Removing a directory
	// is the exact O(1) emptiness check: it fails if any entry remains. Recreate
	// the provider-owned private directory only after that check succeeds.
	if err := os.Remove(partial); err != nil {
		return nil, fmt.Errorf("APT archive inventory contains partial output")
	}
	if err := os.Mkdir(partial, 0o700); err != nil {
		return nil, fmt.Errorf("restore APT archive inventory partial directory: %w", err)
	}
	seeded := make(map[string]providerstore.ArtifactDescriptor, len(prepared.SeededArchives))
	for index, artifact := range prepared.SeededArchives {
		if err := artifact.Validate(); err != nil || artifact.Kind != "deb" {
			return nil, fmt.Errorf("APT archive inventory seed %d is invalid", index)
		}
		if index > 0 && prepared.SeededArchives[index-1].LogicalPath >= artifact.LogicalPath {
			return nil, fmt.Errorf("APT archive inventory seeds must be unique and sorted")
		}
		filename := filepath.Base(filepath.FromSlash(artifact.LogicalPath))
		if filename == "." || !strings.HasSuffix(filename, ".deb") {
			return nil, fmt.Errorf("APT archive inventory seed %d has an invalid filename", index)
		}
		if _, duplicate := seeded[filename]; duplicate {
			return nil, fmt.Errorf("APT archive inventory seed filename %q is duplicated", filename)
		}
		seeded[filename] = artifact
	}
	entries, err := os.ReadDir(archives)
	if err != nil {
		return nil, fmt.Errorf("read APT archive inventory: %w", err)
	}
	result := make([]APTArchiveInventoryEntry, 0, len(entries)-1)
	for _, entry := range entries {
		if entry.Name() == "partial" {
			continue
		}
		if entry.Name() == "lock" {
			path := filepath.Join(archives, entry.Name())
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 0 {
				return nil, fmt.Errorf("APT archive inventory lock must be an empty real regular file")
			}
			continue
		}
		if entry.Name() == "" || filepath.Base(entry.Name()) != entry.Name() || !strings.HasSuffix(entry.Name(), ".deb") {
			return nil, fmt.Errorf("APT archive inventory contains unexpected entry %q", entry.Name())
		}
		path := filepath.Join(archives, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("APT archive inventory entry %q must be a real regular file", entry.Name())
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open APT archive inventory entry %q: %w", entry.Name(), err)
		}
		hash := sha256.New()
		hashErr := copyAPTSeed(ctx, hash, file)
		finalInfo, statErr := file.Stat()
		closeErr := file.Close()
		if hashErr != nil {
			return nil, fmt.Errorf("hash APT archive inventory entry %q: %w", entry.Name(), hashErr)
		}
		if statErr != nil || !os.SameFile(info, finalInfo) || finalInfo.Size() != info.Size() {
			return nil, fmt.Errorf("APT archive inventory entry %q changed during inspection", entry.Name())
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close APT archive inventory entry %q: %w", entry.Name(), closeErr)
		}
		artifact := providerstore.ArtifactDescriptor{
			LogicalPath: "debs/" + entry.Name(), Kind: "deb",
			Size:   strconv.FormatInt(info.Size(), 10),
			SHA256: canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))),
		}
		if err := artifact.Validate(); err != nil {
			return nil, fmt.Errorf("describe APT archive inventory entry %q: %w", entry.Name(), err)
		}
		seed, wasSeeded := seeded[entry.Name()]
		unchanged := wasSeeded && seed.Size == artifact.Size && seed.SHA256 == artifact.SHA256
		result = append(result, APTArchiveInventoryEntry{Filename: entry.Name(), HostPath: path, Artifact: artifact, UnchangedSeed: unchanged})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func PrepareAPTResolverWorkspace(store providerstore.Store) (PreparedAPTResolverWorkspace, func(), error) {
	workspace, err := store.NewWorkspace("apt-resolve-*")
	if err != nil {
		return PreparedAPTResolverWorkspace{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(workspace) }
	for _, relative := range []string{"lists", "lists/partial", "archives", "archives/partial", "output"} {
		if err := os.Mkdir(filepath.Join(workspace, relative), 0o700); err != nil {
			cleanup()
			return PreparedAPTResolverWorkspace{}, func() {}, fmt.Errorf("create APT resolver %s directory: %w", relative, err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "apt.conf"), []byte(aptprovider.ResolveAdditiveConfigV1), 0o600); err != nil {
		cleanup()
		return PreparedAPTResolverWorkspace{}, func() {}, fmt.Errorf("write APT resolver additive config: %w", err)
	}
	prepared := PreparedAPTResolverWorkspace{
		HostDir: workspace, ContainerDir: aptprovider.ResolverScratchDirectory,
		SeededArchives: []providerstore.ArtifactDescriptor{},
	}
	if err := validatePreparedAPTResolverWorkspace(prepared); err != nil {
		cleanup()
		return PreparedAPTResolverWorkspace{}, func() {}, err
	}
	return prepared, cleanup, nil
}

func validatePreparedAPTResolverWorkspace(prepared PreparedAPTResolverWorkspace) error {
	return validateAPTResolverWorkspaceState(prepared, true, false)
}

func validateAPTResolverDownloadWorkspace(prepared PreparedAPTResolverWorkspace) error {
	return validateAPTResolverWorkspaceState(prepared, false, true)
}

func validateAPTResolverWorkspaceState(prepared PreparedAPTResolverWorkspace, requireEmptyLists bool, verifySeedDigests bool) error {
	if prepared.HostDir == "" || !filepath.IsAbs(prepared.HostDir) || filepath.Clean(prepared.HostDir) != prepared.HostDir {
		return fmt.Errorf("APT resolver workspace host path must be absolute and clean")
	}
	info, err := os.Lstat(prepared.HostDir)
	if err != nil {
		return fmt.Errorf("inspect APT resolver workspace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !providerInstallFileModeMatches(info.Mode(), 0o700) {
		return fmt.Errorf("APT resolver workspace must be a private real directory")
	}
	if prepared.ContainerDir != aptprovider.ResolverScratchDirectory {
		return fmt.Errorf("APT resolver workspace must use container path %q", aptprovider.ResolverScratchDirectory)
	}
	for _, relative := range []string{"lists", "lists/partial", "archives", "archives/partial", "output"} {
		path := filepath.Join(prepared.HostDir, relative)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !providerInstallFileModeMatches(info.Mode(), 0o700) {
			return fmt.Errorf("APT resolver %s must be a private real directory", relative)
		}
	}
	emptyDirectories := []string{"archives/partial", "output"}
	if requireEmptyLists {
		emptyDirectories = append(emptyDirectories, "lists/partial")
	}
	for _, relative := range emptyDirectories {
		path := filepath.Join(prepared.HostDir, relative)
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			return fmt.Errorf("APT resolver %s directory must be initially empty", relative)
		}
	}
	if err := validateAPTResolverSeededArchives(prepared, verifySeedDigests); err != nil {
		return err
	}
	if requireEmptyLists {
		entries, err := os.ReadDir(filepath.Join(prepared.HostDir, "lists"))
		if err != nil || len(entries) != 1 || entries[0].Name() != "partial" || !entries[0].IsDir() {
			return fmt.Errorf("APT resolver lists directory must contain only its empty partial directory")
		}
	}
	configPath := filepath.Join(prepared.HostDir, "apt.conf")
	configInfo, err := os.Lstat(configPath)
	if err != nil || !configInfo.Mode().IsRegular() || !providerInstallFileModeMatches(configInfo.Mode(), 0o600) {
		return fmt.Errorf("APT resolver additive config must be a private regular file")
	}
	config, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(config, []byte(aptprovider.ResolveAdditiveConfigV1)) {
		return fmt.Errorf("APT resolver additive config does not match profile %q", aptprovider.ResolveProfileV1)
	}
	rootEntries, err := os.ReadDir(prepared.HostDir)
	if err != nil {
		return fmt.Errorf("read APT resolver workspace: %w", err)
	}
	if len(rootEntries) != 4 {
		return fmt.Errorf("APT resolver workspace contains an unexpected entry")
	}
	return nil
}

// SeedAPTResolverArchives copies verified deployment-store .deb objects into
// the disposable archive cache. Copies deliberately do not share inodes with
// immutable store blobs that APT may unlink or otherwise mutate as root.
func SeedAPTResolverArchives(
	ctx context.Context,
	store providerstore.Store,
	prepared PreparedAPTResolverWorkspace,
	reusable []providerstore.ArtifactDescriptor,
) (PreparedAPTResolverWorkspace, error) {
	if ctx == nil {
		return PreparedAPTResolverWorkspace{}, fmt.Errorf("APT archive seed context is required")
	}
	if err := ctx.Err(); err != nil {
		return PreparedAPTResolverWorkspace{}, err
	}
	if err := validatePreparedAPTResolverWorkspace(prepared); err != nil {
		return PreparedAPTResolverWorkspace{}, err
	}
	if len(prepared.SeededArchives) != 0 {
		return PreparedAPTResolverWorkspace{}, fmt.Errorf("APT resolver archives are already seeded")
	}
	type seed struct {
		descriptor providerstore.ArtifactDescriptor
		source     string
		filename   string
	}
	artifacts := append([]providerstore.ArtifactDescriptor{}, reusable...)
	sort.Slice(artifacts, func(left int, right int) bool { return artifacts[left].LogicalPath < artifacts[right].LogicalPath })
	seeds := make([]seed, 0, len(artifacts))
	filenames := map[string]string{}
	for _, artifact := range artifacts {
		if err := artifact.Validate(); err != nil {
			return PreparedAPTResolverWorkspace{}, fmt.Errorf("APT reusable archive: %w", err)
		}
		filename := filepath.Base(filepath.FromSlash(artifact.LogicalPath))
		if artifact.Kind != "deb" || !strings.HasSuffix(strings.ToLower(filename), ".deb") {
			return PreparedAPTResolverWorkspace{}, fmt.Errorf("APT reusable artifact %q must be a .deb", artifact.LogicalPath)
		}
		if previous, exists := filenames[filename]; exists {
			return PreparedAPTResolverWorkspace{}, fmt.Errorf("APT reusable artifacts %q and %q have the same filename", previous, artifact.LogicalPath)
		}
		source, err := store.InspectArtifactPath(artifact)
		if err != nil {
			return PreparedAPTResolverWorkspace{}, fmt.Errorf("inspect APT reusable archive %q: %w", artifact.LogicalPath, err)
		}
		filenames[filename] = artifact.LogicalPath
		seeds = append(seeds, seed{descriptor: artifact, source: source, filename: filename})
	}
	archives := filepath.Join(prepared.HostDir, "archives")
	created := []string{}
	cleanupCreated := func() {
		for _, path := range created {
			_ = os.Remove(path)
		}
	}
	for _, item := range seeds {
		temporary, err := os.CreateTemp(archives, ".reploy-seed-*")
		if err != nil {
			cleanupCreated()
			return PreparedAPTResolverWorkspace{}, fmt.Errorf("create APT archive seed: %w", err)
		}
		temporaryPath := temporary.Name()
		created = append(created, temporaryPath)
		source, err := os.Open(item.source)
		hash := sha256.New()
		if err == nil {
			err = copyAPTSeed(ctx, io.MultiWriter(temporary, hash), source)
		}
		closeSourceErr := error(nil)
		if source != nil {
			closeSourceErr = source.Close()
		}
		if err == nil {
			err = closeSourceErr
		}
		if err == nil {
			err = temporary.Sync()
		}
		if closeErr := temporary.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			got := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
			if got != item.descriptor.SHA256 {
				err = fmt.Errorf("copied archive digest is %s, want %s", got, item.descriptor.SHA256)
			}
		}
		if err == nil {
			err = os.Chmod(temporaryPath, 0o444)
		}
		finalPath := filepath.Join(archives, item.filename)
		if err == nil {
			err = os.Rename(temporaryPath, finalPath)
		}
		if err != nil {
			cleanupCreated()
			return PreparedAPTResolverWorkspace{}, fmt.Errorf("seed APT reusable archive %q: %w", item.descriptor.LogicalPath, err)
		}
		created[len(created)-1] = finalPath
	}
	prepared.SeededArchives = artifacts
	if err := validatePreparedAPTResolverWorkspace(prepared); err != nil {
		cleanupCreated()
		return PreparedAPTResolverWorkspace{}, err
	}
	return prepared, nil
}

func validateAPTResolverSeededArchives(prepared PreparedAPTResolverWorkspace, verifyDigests bool) error {
	if prepared.SeededArchives == nil {
		return fmt.Errorf("APT resolver seeded archives must use an array")
	}
	expected := map[string]providerstore.ArtifactDescriptor{}
	for index, artifact := range prepared.SeededArchives {
		if err := artifact.Validate(); err != nil || artifact.Kind != "deb" {
			return fmt.Errorf("APT resolver seeded archive %d is invalid", index)
		}
		if index > 0 && prepared.SeededArchives[index-1].LogicalPath >= artifact.LogicalPath {
			return fmt.Errorf("APT resolver seeded archives must be unique and sorted by logical path")
		}
		filename := filepath.Base(filepath.FromSlash(artifact.LogicalPath))
		if !strings.HasSuffix(strings.ToLower(filename), ".deb") {
			return fmt.Errorf("APT resolver seeded archive %q must be a .deb", artifact.LogicalPath)
		}
		if _, exists := expected[filename]; exists {
			return fmt.Errorf("APT resolver seeded archive filename %q is duplicated", filename)
		}
		expected[filename] = artifact
	}
	entries, err := os.ReadDir(filepath.Join(prepared.HostDir, "archives"))
	if err != nil || len(entries) != len(expected)+1 {
		return fmt.Errorf("APT resolver archives contain an unexpected entry")
	}
	for _, entry := range entries {
		if entry.Name() == "partial" && entry.IsDir() {
			continue
		}
		artifact, exists := expected[entry.Name()]
		if !exists || entry.IsDir() {
			return fmt.Errorf("APT resolver archive %q is unexpected", entry.Name())
		}
		path := filepath.Join(prepared.HostDir, "archives", entry.Name())
		if verifyDigests {
			err = providerstore.VerifyArtifactFile(path, artifact)
		} else {
			err = providerstore.InspectArtifactFile(path, artifact)
		}
		if err != nil {
			return fmt.Errorf("validate APT resolver archive %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func copyAPTSeed(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			if _, err := destination.Write(buffer[:count]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return ctx.Err()
		}
		if readErr != nil {
			return readErr
		}
	}
}
