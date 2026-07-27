package dockerdeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

const pythonSourceRelevanceSchemaV1 = "python-source-relevance-v1"

var pythonSourceBuildControlFilesV1 = []string{
	".reploy.yaml",
	"MANIFEST.in",
	"pyproject.toml",
	"setup.cfg",
	"setup.py",
}

type pythonSourceRelevanceV1 struct {
	Schema            string                    `json:"schema"`
	Distribution      string                    `json:"distribution"`
	SourceDir         string                    `json:"source_dir"`
	SourceInputDigest canonical.Digest          `json:"source_input_digest"`
	RootTopology      []pythonSourceRootEntryV1 `json:"root_topology"`
	RelevantDirs      []string                  `json:"relevant_dirs"`
	WatchedRootFiles  []string                  `json:"watched_root_files"`
	RelevantManifest  PythonSourceManifestV1    `json:"relevant_manifest"`
}

type pythonSourceRootEntryV1 struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// ReusablePythonLocalSourcesV1 first checks the learned post-sdist relevance
// map for each locked source. A cache miss or uncertain input triggers the
// complete source observation. When that complete identity still matches the
// retained source artifact, it seeds the next warm check from the sdist.
func ReusablePythonLocalSourcesV1(
	store providerstore.Store,
	overrides []PythonLocalOverrideV1,
	locked []providers.ResolvedSourceInput,
) ([]providers.ResolvedSourceInput, error) {
	if overrides == nil || locked == nil {
		return nil, fmt.Errorf("local Python overrides and locked sources must use arrays")
	}
	lockedByDistribution := make(map[string][]providers.ResolvedSourceInput)
	for _, source := range locked {
		if err := pythonprovider.ValidateResolvedSourceInputV2(source); err != nil {
			return nil, err
		}
		lockedByDistribution[source.LogicalPackage] = append(
			lockedByDistribution[source.LogicalPackage],
			source,
		)
	}
	distributions := make([]string, 0, len(lockedByDistribution))
	for distribution := range lockedByDistribution {
		distributions = append(distributions, distribution)
	}
	sort.Strings(distributions)

	reusable := []providers.ResolvedSourceInput{}
	for _, distribution := range distributions {
		candidates := lockedByDistribution[distribution]
		expected := candidates[0]
		consistent := true
		for _, candidate := range candidates[1:] {
			if candidate.SourceInputDigest != expected.SourceInputDigest ||
				candidate.SourceArtifactDigest != expected.SourceArtifactDigest {
				consistent = false
				break
			}
		}
		sources, err := observeSelectedPythonLocalSources(
			overrides,
			[]string{distribution},
			func(sourceDir string) (PythonSourceManifestV1, canonical.Digest, error) {
				if consistent {
					if relevance, found := readPythonSourceRelevance(store.Root(), distribution, sourceDir); found {
						unchanged, err := pythonSourceRelevanceUnchanged(relevance)
						if err != nil {
							return PythonSourceManifestV1{}, "", err
						}
						if unchanged &&
							relevance.SourceInputDigest == expected.SourceInputDigest {
							return PythonSourceManifestV1{
								Schema:  pythonSourceManifestSchemaV1,
								Entries: []PythonSourceManifestEntryV1{},
							}, relevance.SourceInputDigest, nil
						}
					}
				}
				manifest, digest, err := ObservePythonSourceManifest(sourceDir)
				if err != nil {
					return PythonSourceManifestV1{}, "", err
				}
				if consistent && digest == expected.SourceInputDigest {
					artifact, artifactErr := pythonprovider.SourceArtifactDescriptorV2(expected)
					if artifactErr == nil {
						_ = learnPythonSourceRelevance(store, PythonLocalSource{
							Distribution: distribution, HostDir: sourceDir,
							Manifest: manifest, SourceInputDigest: digest,
						}, artifact)
					}
				}
				return manifest, digest, nil
			},
		)
		if err != nil {
			return nil, err
		}
		if len(sources) == 0 {
			continue
		}
		if sources[0].SourceInputDigest != expected.SourceInputDigest {
			continue
		}
		reusable = append(reusable, candidates...)
	}
	sort.Slice(reusable, func(left int, right int) bool {
		return compareResolvedSources(reusable[left], reusable[right]) < 0
	})
	return reusable, nil
}

func learnPythonSourceRelevance(
	store providerstore.Store,
	source PythonLocalSource,
	artifact providerstore.ArtifactDescriptor,
) error {
	if err := validatePythonLocalSourcesForSnapshot([]PythonLocalSource{source}); err != nil {
		return err
	}
	if err := artifact.Validate(); err != nil {
		return err
	}
	artifactPath, err := store.InspectArtifactPath(artifact)
	if err != nil {
		return err
	}
	sdistPaths, metadata, err := pythonprovider.SourceDistributionRelativePathsV1(artifactPath)
	if err != nil {
		return err
	}
	if metadata.Distribution != source.Distribution {
		return fmt.Errorf(
			"retained Python source distribution is %q, want %q",
			metadata.Distribution,
			source.Distribution,
		)
	}
	byPath := make(map[string]PythonSourceManifestEntryV1, len(source.Manifest.Entries))
	for _, entry := range source.Manifest.Entries {
		byPath[entry.Path] = entry
	}
	relevantDirs := map[string]struct{}{}
	rootFiles := map[string]struct{}{}
	for _, filename := range pythonSourceBuildControlFilesV1 {
		rootFiles[filename] = struct{}{}
	}
	for _, archivePath := range sdistPaths {
		entry, found := byPath[archivePath]
		if !found {
			continue
		}
		first, remainder, nested := strings.Cut(archivePath, "/")
		if !nested {
			if entry.Kind == "directory" {
				relevantDirs[first] = struct{}{}
			} else {
				rootFiles[first] = struct{}{}
			}
			continue
		}
		top, found := byPath[first]
		if !found {
			continue
		}
		if top.Kind == "directory" {
			relevantDirs[first] = struct{}{}
		} else if remainder != "" {
			rootFiles[first] = struct{}{}
		}
	}
	directories := sortedStringSet(relevantDirs)
	watchedFiles := sortedStringSet(rootFiles)
	relevantEntries := []PythonSourceManifestEntryV1{}
	for _, entry := range source.Manifest.Entries {
		if stringInSortedSlice(watchedFiles, entry.Path) ||
			pathHasRelevantTopDirectory(entry.Path, relevantDirs) {
			relevantEntries = append(relevantEntries, entry)
		}
	}
	relevance := pythonSourceRelevanceV1{
		Schema:       pythonSourceRelevanceSchemaV1,
		Distribution: source.Distribution, SourceDir: source.HostDir,
		SourceInputDigest: source.SourceInputDigest,
		RootTopology:      pythonSourceRootTopologyFromManifest(source.Manifest),
		RelevantDirs:      directories, WatchedRootFiles: watchedFiles,
		RelevantManifest: PythonSourceManifestV1{
			Schema:  pythonSourceManifestSchemaV1,
			Entries: relevantEntries,
		},
	}
	if err := validatePythonSourceRelevance(relevance); err != nil {
		return err
	}
	return writePythonSourceRelevance(store.Root(), relevance)
}

func pythonSourceRelevanceUnchanged(relevance pythonSourceRelevanceV1) (bool, error) {
	if err := validatePythonSourceRelevance(relevance); err != nil {
		return false, nil
	}
	topology, err := observePythonSourceRootTopology(relevance.SourceDir)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(topology, relevance.RootTopology) {
		return false, nil
	}
	observed := []PythonSourceManifestEntryV1{}
	for _, directory := range relevance.RelevantDirs {
		rootEntry, found, err := observePythonSourceManifestEntry(
			relevance.SourceDir,
			directory,
		)
		if err != nil || !found || rootEntry.Kind != "directory" {
			return false, err
		}
		observed = append(observed, rootEntry)
		subtree, _, err := ObservePythonSourceManifest(filepath.Join(relevance.SourceDir, directory))
		if err != nil {
			return false, err
		}
		for _, entry := range subtree.Entries {
			entry.Path = path.Join(directory, entry.Path)
			observed = append(observed, entry)
		}
	}
	for _, filename := range relevance.WatchedRootFiles {
		entry, found, err := observePythonSourceManifestEntry(relevance.SourceDir, filename)
		if err != nil {
			return false, err
		}
		if found {
			observed = append(observed, entry)
		}
	}
	sort.Slice(observed, func(left int, right int) bool {
		return observed[left].Path < observed[right].Path
	})
	return reflect.DeepEqual(observed, relevance.RelevantManifest.Entries), nil
}

func observePythonSourceRootTopology(sourceDir string) ([]pythonSourceRootEntryV1, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, err
	}
	result := []pythonSourceRootEntryV1{}
	for _, entry := range entries {
		if ignoredPythonSourceEntry(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		kind := ""
		switch {
		case info.IsDir():
			kind = "directory"
		case info.Mode().IsRegular():
			kind = "file"
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
		default:
			return nil, fmt.Errorf("source root entry %q has unsupported file type %s", entry.Name(), info.Mode().Type())
		}
		result = append(result, pythonSourceRootEntryV1{Path: entry.Name(), Kind: kind})
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].Path < result[right].Path
	})
	return result, nil
}

func observePythonSourceManifestEntry(
	sourceDir string,
	relative string,
) (PythonSourceManifestEntryV1, bool, error) {
	filename := filepath.Join(sourceDir, filepath.FromSlash(relative))
	info, err := os.Lstat(filename)
	if errors.Is(err, fs.ErrNotExist) {
		return PythonSourceManifestEntryV1{}, false, nil
	}
	if err != nil {
		return PythonSourceManifestEntryV1{}, false, err
	}
	entry := PythonSourceManifestEntryV1{Path: relative}
	switch {
	case info.IsDir():
		entry.Kind = "directory"
		entry.Mode = fmt.Sprintf("%04o", info.Mode().Perm())
	case info.Mode().IsRegular():
		entry.Kind = "file"
		entry.Mode = fmt.Sprintf("%04o", info.Mode().Perm())
		digest, err := digestPythonSourceFile(filename)
		if err != nil {
			return PythonSourceManifestEntryV1{}, false, err
		}
		entry.ContentDigest = digest
	case info.Mode()&os.ModeSymlink != 0:
		entry.Kind = "symlink"
		target, err := os.Readlink(filename)
		if err != nil {
			return PythonSourceManifestEntryV1{}, false, err
		}
		if !utf8.ValidString(target) || strings.ContainsRune(target, 0) {
			return PythonSourceManifestEntryV1{}, false, fmt.Errorf("source symlink %q has an invalid target", relative)
		}
		entry.LinkTarget = filepath.ToSlash(target)
	default:
		return PythonSourceManifestEntryV1{}, false, fmt.Errorf(
			"source entry %q has unsupported file type %s",
			relative,
			info.Mode().Type(),
		)
	}
	return entry, true, nil
}

func validatePythonSourceRelevance(relevance pythonSourceRelevanceV1) error {
	if relevance.Schema != pythonSourceRelevanceSchemaV1 {
		return fmt.Errorf("Python source relevance schema must be %q", pythonSourceRelevanceSchemaV1)
	}
	if pythonprovider.NormalizeDistributionName(relevance.Distribution) != relevance.Distribution ||
		relevance.Distribution == "" {
		return fmt.Errorf("Python source relevance distribution is invalid")
	}
	if relevance.SourceDir == "" || !filepath.IsAbs(relevance.SourceDir) ||
		filepath.Clean(relevance.SourceDir) != relevance.SourceDir {
		return fmt.Errorf("Python source relevance directory must be absolute and clean")
	}
	if err := relevance.SourceInputDigest.Validate(); err != nil {
		return err
	}
	if relevance.RootTopology == nil || relevance.RelevantDirs == nil ||
		relevance.WatchedRootFiles == nil {
		return fmt.Errorf("Python source relevance collections must use arrays")
	}
	for index, entry := range relevance.RootTopology {
		if entry.Path == "" || strings.Contains(entry.Path, "/") ||
			(entry.Kind != "directory" && entry.Kind != "file" && entry.Kind != "symlink") {
			return fmt.Errorf("Python source root topology entry is invalid")
		}
		if entry.Kind == "symlink" {
			return fmt.Errorf("Python source relevance cannot safely cache a root symlink")
		}
		if index > 0 && relevance.RootTopology[index-1].Path >= entry.Path {
			return fmt.Errorf("Python source root topology must be unique and sorted")
		}
	}
	for index, directory := range relevance.RelevantDirs {
		if directory == "" || strings.Contains(directory, "/") || path.Clean(directory) != directory {
			return fmt.Errorf("Python source relevant directory is invalid")
		}
		if index > 0 && relevance.RelevantDirs[index-1] >= directory {
			return fmt.Errorf("Python source relevant directories must be unique and sorted")
		}
	}
	for index, filename := range relevance.WatchedRootFiles {
		if filename == "" || strings.Contains(filename, "/") || path.Clean(filename) != filename {
			return fmt.Errorf("Python source watched root file is invalid")
		}
		if index > 0 && relevance.WatchedRootFiles[index-1] >= filename {
			return fmt.Errorf("Python source watched root files must be unique and sorted")
		}
	}
	if err := validatePythonSourceManifestV1(relevance.RelevantManifest); err != nil {
		return fmt.Errorf("Python source relevant manifest: %w", err)
	}
	for _, entry := range relevance.RelevantManifest.Entries {
		if entry.Kind == "symlink" {
			return fmt.Errorf(
				"Python source relevance cannot safely cache relevant symlink %q",
				entry.Path,
			)
		}
	}
	return nil
}

func readPythonSourceRelevance(
	storeRoot string,
	distribution string,
	sourceDir string,
) (pythonSourceRelevanceV1, bool) {
	filename := pythonSourceRelevancePath(storeRoot, distribution, sourceDir)
	parent := filepath.Dir(filename)
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() ||
		info.Mode()&os.ModeSymlink != 0 {
		return pythonSourceRelevanceV1{}, false
	}
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return pythonSourceRelevanceV1{}, false
	}
	content, err := os.ReadFile(filename)
	if err != nil {
		return pythonSourceRelevanceV1{}, false
	}
	var relevance pythonSourceRelevanceV1
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&relevance); err != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		relevance.Distribution != distribution ||
		relevance.SourceDir != sourceDir ||
		validatePythonSourceRelevance(relevance) != nil {
		return pythonSourceRelevanceV1{}, false
	}
	return relevance, true
}

func writePythonSourceRelevance(
	storeRoot string,
	relevance pythonSourceRelevanceV1,
) error {
	if err := validatePythonSourceRelevance(relevance); err != nil {
		return err
	}
	info, err := os.Lstat(storeRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Python source relevance requires a real provider store")
	}
	directory := filepath.Join(storeRoot, "source-relevance")
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err = os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Python source relevance cache directory is not a real directory")
	}
	content, err := json.Marshal(relevance)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".source-relevance-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(
		temporaryPath,
		pythonSourceRelevancePath(storeRoot, relevance.Distribution, relevance.SourceDir),
	)
}

func pythonSourceRelevancePath(storeRoot string, distribution string, sourceDir string) string {
	key := sha256.Sum256([]byte(distribution + "\x00" + sourceDir))
	return filepath.Join(storeRoot, "source-relevance", hex.EncodeToString(key[:])+".json")
}

func pythonSourceRootTopologyFromManifest(
	manifest PythonSourceManifestV1,
) []pythonSourceRootEntryV1 {
	result := []pythonSourceRootEntryV1{}
	for _, entry := range manifest.Entries {
		if strings.Contains(entry.Path, "/") {
			continue
		}
		result = append(result, pythonSourceRootEntryV1{Path: entry.Path, Kind: entry.Kind})
	}
	return result
}

func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func stringInSortedSlice(values []string, value string) bool {
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}

func pathHasRelevantTopDirectory(filename string, relevant map[string]struct{}) bool {
	first, _, nested := strings.Cut(filename, "/")
	if !nested && filename != first {
		return false
	}
	_, found := relevant[first]
	return found
}
