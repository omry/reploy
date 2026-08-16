package dockerdeploy

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

const pythonSourceSnapshotDirectory = "sources"

type PreparedPythonSourceSnapshot struct {
	Distribution      string
	HostDir           string
	ContainerDir      string
	SourceInputDigest canonical.Digest
}

// StagePythonLocalSourceSnapshots copies exactly the entries covered by
// each recorded manifest into the resolver's existing read-only input mount.
// The live checkout is never mounted into the resolver container.
func StagePythonLocalSourceSnapshots(
	prepared PreparedPythonResolverArtifacts,
	sources []PythonLocalSource,
) (snapshots []PreparedPythonSourceSnapshot, err error) {
	if err := validatePreparedPythonResolverArtifacts(prepared); err != nil {
		return nil, err
	}
	if sources == nil {
		return nil, fmt.Errorf("local Python sources must use an array")
	}
	if len(sources) == 0 {
		return []PreparedPythonSourceSnapshot{}, nil
	}
	if err := validatePythonLocalSourcesForSnapshot(sources); err != nil {
		return nil, err
	}
	if err := os.Chmod(prepared.InputHostDir, 0o700); err != nil {
		return nil, fmt.Errorf("make Python resolver input writable for source snapshots: %w", err)
	}
	snapshotRoot := filepath.Join(prepared.InputHostDir, pythonSourceSnapshotDirectory)
	rootCreated := false
	created := []string{}
	defer func() {
		if err != nil {
			for index := len(created) - 1; index >= 0; index-- {
				makePythonResolverWorkspaceRemovable(created[index])
				err = errors.Join(err, os.RemoveAll(created[index]))
			}
			if rootCreated {
				err = errors.Join(err, os.Remove(snapshotRoot))
			}
		}
		if protectErr := os.Chmod(snapshotRoot, 0o500); protectErr != nil && !errors.Is(protectErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("protect Python source snapshot root: %w", protectErr))
			snapshots = nil
		}
		if protectErr := os.Chmod(prepared.InputHostDir, 0o500); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("restore Python resolver input protection: %w", protectErr))
			snapshots = nil
		}
	}()
	previousDistributionRoot := filepath.Join(prepared.InputHostDir, pythonSourceDistributionDirectory)
	makePythonResolverWorkspaceRemovable(previousDistributionRoot)
	if err := os.RemoveAll(previousDistributionRoot); err != nil {
		return nil, fmt.Errorf("clear prior retained Python source distribution inputs: %w", err)
	}
	info, statErr := os.Lstat(snapshotRoot)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		if err := os.Mkdir(snapshotRoot, 0o700); err != nil {
			return nil, fmt.Errorf("create Python source snapshot root: %w", err)
		}
		rootCreated = true
	case statErr != nil:
		return nil, fmt.Errorf("inspect Python source snapshot root: %w", statErr)
	case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("Python source snapshot root must be a directory")
	default:
		if err := os.Chmod(snapshotRoot, 0o700); err != nil {
			return nil, fmt.Errorf("make Python source snapshot root writable: %w", err)
		}
	}

	snapshots = make([]PreparedPythonSourceSnapshot, 0, len(sources))
	for _, source := range sources {
		hostDir := filepath.Join(snapshotRoot, source.Distribution)
		if _, statErr := os.Lstat(hostDir); statErr == nil {
			return nil, fmt.Errorf("Python source snapshot %q already exists", source.Distribution)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect Python source snapshot %q: %w", source.Distribution, statErr)
		}
		created = append(created, hostDir)
		if err := stageOnePythonSourceSnapshot(source, hostDir); err != nil {
			return nil, fmt.Errorf("stage Python source snapshot %q: %w", source.Distribution, err)
		}
		snapshots = append(snapshots, PreparedPythonSourceSnapshot{
			Distribution: source.Distribution, HostDir: hostDir,
			ContainerDir:      path.Join(prepared.InputContainerDir, pythonSourceSnapshotDirectory, source.Distribution),
			SourceInputDigest: source.SourceInputDigest,
		})
	}
	return snapshots, nil
}

func validatePythonLocalSourcesForSnapshot(sources []PythonLocalSource) error {
	for index, source := range sources {
		if err := blueprint.ValidatePythonDistributionName("local Python source distribution", source.Distribution); err != nil {
			return fmt.Errorf("local Python source %d: %w", index, err)
		}
		if pythonprovider.NormalizeDistributionName(source.Distribution) != source.Distribution {
			return fmt.Errorf("local Python source %d has noncanonical distribution %q", index, source.Distribution)
		}
		if index > 0 && sources[index-1].Distribution >= source.Distribution {
			return fmt.Errorf("local Python sources must be unique and sorted by distribution")
		}
		if source.HostDir == "" || !filepath.IsAbs(source.HostDir) || filepath.Clean(source.HostDir) != source.HostDir {
			return fmt.Errorf("local Python source %q host directory must be absolute and clean", source.Distribution)
		}
		real, err := resolveRealPythonSourceDirectory(source.HostDir)
		if err != nil {
			return fmt.Errorf("local Python source %q host directory: %w", source.Distribution, err)
		}
		if real != source.HostDir {
			return fmt.Errorf("local Python source %q host directory must be fully resolved", source.Distribution)
		}
		if err := validatePythonSourceManifestV1(source.Manifest); err != nil {
			return fmt.Errorf(
				"prepare immutable snapshot for local Python source %q from %q: invalid source manifest: %w",
				source.Distribution,
				source.HostDir,
				err,
			)
		}
		digest, err := canonical.Sum("python-source-manifest", pythonSourceManifestSchemaV1, source.Manifest)
		if err != nil {
			return fmt.Errorf("local Python source %q manifest digest: %w", source.Distribution, err)
		}
		if digest != source.SourceInputDigest {
			return fmt.Errorf("local Python source %q manifest digest does not match its entries", source.Distribution)
		}
	}
	return nil
}

func validatePythonSourceManifestV1(manifest PythonSourceManifestV1) error {
	if manifest.Schema != pythonSourceManifestSchemaV1 {
		return fmt.Errorf("schema must be %q", pythonSourceManifestSchemaV1)
	}
	if manifest.Entries == nil {
		return fmt.Errorf("entries must use an array")
	}
	if manifest.Exclude == nil {
		return fmt.Errorf("exclude must use an array")
	}
	exclusions, err := deploy.NormalizePackageOverrideExclusionsV1(manifest.Exclude)
	if err != nil {
		return fmt.Errorf("exclusions: %w", err)
	}
	if !slices.Equal(exclusions, manifest.Exclude) {
		return fmt.Errorf("exclusions must be unique and sorted")
	}
	directories := map[string]struct{}{".": {}}
	for index, entry := range manifest.Entries {
		if entry.Path == "" || entry.Path == "." || path.IsAbs(entry.Path) || path.Clean(entry.Path) != entry.Path ||
			strings.ContainsAny(entry.Path, `\:`) || entry.Path == ".." || strings.HasPrefix(entry.Path, "../") {
			return fmt.Errorf("entry %d has unsafe path %q", index, entry.Path)
		}
		if index > 0 && manifest.Entries[index-1].Path >= entry.Path {
			return fmt.Errorf(
				"entry %d path %q is not strictly after entry %d path %q; entries must be unique and sorted so the local source has a stable content digest",
				index,
				entry.Path,
				index-1,
				manifest.Entries[index-1].Path,
			)
		}
		if _, found := directories[path.Dir(entry.Path)]; !found {
			return fmt.Errorf("entry %q has no recorded parent directory", entry.Path)
		}
		switch entry.Kind {
		case "directory":
			if _, err := parsePythonSourceMode(entry.Mode); err != nil {
				return fmt.Errorf("directory %q: %w", entry.Path, err)
			}
			if entry.ContentDigest != "" || entry.LinkTarget != "" {
				return fmt.Errorf("directory %q has file or link data", entry.Path)
			}
			directories[entry.Path] = struct{}{}
		case "file":
			if _, err := parsePythonSourceMode(entry.Mode); err != nil {
				return fmt.Errorf("file %q: %w", entry.Path, err)
			}
			if err := entry.ContentDigest.Validate(); err != nil {
				return fmt.Errorf("file %q content digest: %w", entry.Path, err)
			}
			if entry.LinkTarget != "" {
				return fmt.Errorf("file %q has a link target", entry.Path)
			}
		case "symlink":
			if entry.Mode != "" || entry.ContentDigest != "" {
				return fmt.Errorf("symlink %q has file metadata", entry.Path)
			}
			if entry.LinkTarget == "" || !utf8.ValidString(entry.LinkTarget) || strings.ContainsRune(entry.LinkTarget, 0) {
				return fmt.Errorf("symlink %q has an invalid target", entry.Path)
			}
		default:
			return fmt.Errorf("entry %q has unsupported kind %q", entry.Path, entry.Kind)
		}
	}
	return nil
}

func parsePythonSourceMode(value string) (os.FileMode, error) {
	if len(value) != 4 || value[0] != '0' {
		return 0, fmt.Errorf("mode %q must be four octal permission digits", value)
	}
	parsed, err := strconv.ParseUint(value, 8, 9)
	if err != nil {
		return 0, fmt.Errorf("mode %q must be four octal permission digits", value)
	}
	return os.FileMode(parsed), nil
}

func stageOnePythonSourceSnapshot(source PythonLocalSource, destination string) error {
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	sourceRoot, err := os.OpenRoot(source.HostDir)
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	destinationRoot, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer destinationRoot.Close()

	for _, entry := range source.Manifest.Entries {
		if entry.Kind != "directory" {
			continue
		}
		if err := destinationRoot.Mkdir(filepath.FromSlash(entry.Path), 0o700); err != nil {
			return err
		}
	}
	for _, entry := range source.Manifest.Entries {
		name := filepath.FromSlash(entry.Path)
		switch entry.Kind {
		case "file":
			if err := copyPythonSourceSnapshotFile(sourceRoot, destinationRoot, name, entry); err != nil {
				return err
			}
		case "symlink":
			target, err := sourceRoot.Readlink(name)
			if err != nil {
				return err
			}
			if filepath.ToSlash(target) != entry.LinkTarget {
				return fmt.Errorf("source symlink %q changed after manifest observation", entry.Path)
			}
			if err := destinationRoot.Symlink(filepath.FromSlash(entry.LinkTarget), name); err != nil {
				return err
			}
		}
	}
	for index := len(source.Manifest.Entries) - 1; index >= 0; index-- {
		entry := source.Manifest.Entries[index]
		if entry.Kind != "directory" {
			continue
		}
		mode, _ := parsePythonSourceMode(entry.Mode)
		if err := destinationRoot.Chmod(filepath.FromSlash(entry.Path), mode); err != nil {
			return err
		}
	}
	if err := os.Chmod(destination, 0o555); err != nil {
		return err
	}
	observed, digest, err := ObservePythonSourceManifestWithExclusions(destination, source.Manifest.Exclude)
	if err != nil {
		return err
	}
	if digest != source.SourceInputDigest || !reflect.DeepEqual(observed, source.Manifest) {
		return fmt.Errorf("prepared snapshot does not match its source manifest")
	}
	return nil
}

func copyPythonSourceSnapshotFile(
	sourceRoot *os.Root,
	destinationRoot *os.Root,
	name string,
	entry PythonSourceManifestEntryV1,
) error {
	info, err := sourceRoot.Lstat(name)
	if err != nil {
		return err
	}
	mode, _ := parsePythonSourceMode(entry.Mode)
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return fmt.Errorf("source file %q changed after manifest observation", entry.Path)
	}
	source, err := sourceRoot.Open(name)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := destinationRoot.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(destination, hash), source)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	digest := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
	if digest != entry.ContentDigest {
		return fmt.Errorf("source file %q content changed after manifest observation", entry.Path)
	}
	if err := destinationRoot.Chmod(name, mode); err != nil {
		return err
	}
	return nil
}

func makePythonResolverWorkspaceRemovable(root string) {
	_ = filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			_ = os.Chmod(filename, 0o700)
		}
		return nil
	})
}
