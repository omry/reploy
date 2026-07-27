package dockerdeploy

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

const pythonSourceManifestSchemaV1 = pythonprovider.SourceManifestSchemaV1

// PythonWorkspaceSource is a staging-only physical locator paired with the
// path-free source manifest that later Python preparation turns into a wheel.
// HostDir and Manifest never cross into the provider graph or build lock.
type PythonWorkspaceSource struct {
	Distribution         string
	HostDir              string
	Manifest             PythonSourceManifestV1
	SourceManifestDigest canonical.Digest
}

type PythonSourceManifestV1 struct {
	Schema  string                        `json:"schema"`
	Entries []PythonSourceManifestEntryV1 `json:"entries"`
}

type PythonSourceManifestEntryV1 struct {
	Path          string           `json:"path"`
	Kind          string           `json:"kind"`
	Mode          string           `json:"mode"`
	ContentDigest canonical.Digest `json:"content_digest"`
	LinkTarget    string           `json:"link_target"`
}

// ResolvePythonWorkspaceSources resolves auxiliary workspace locators and
// observes deterministic manifests. Callers choose when observation is needed;
// merely loading a blueprint does not walk source trees.
func ResolvePythonWorkspaceSources(
	document blueprint.Document,
	workspaceRoot string,
) ([]PythonWorkspaceSource, error) {
	return resolvePythonWorkspaceSources(document, workspaceRoot, nil)
}

// ResolveSelectedPythonWorkspaceSources observes only the named normalized
// distributions. It still validates declaration names and locator syntax, but
// it does not stat or walk unselected source directories.
func ResolveSelectedPythonWorkspaceSources(
	document blueprint.Document,
	workspaceRoot string,
	distributions []string,
) ([]PythonWorkspaceSource, error) {
	if distributions == nil {
		return nil, fmt.Errorf("selected Python workspace distributions must use an array")
	}
	selected := make(map[string]struct{}, len(distributions))
	for index, distribution := range distributions {
		if distribution == "" || pythonprovider.NormalizeDistributionName(distribution) != distribution {
			return nil, fmt.Errorf("selected Python workspace distribution %d is not normalized: %q", index, distribution)
		}
		if index > 0 && distributions[index-1] >= distribution {
			return nil, fmt.Errorf("selected Python workspace distributions must be unique and sorted")
		}
		selected[distribution] = struct{}{}
	}
	return resolvePythonWorkspaceSources(document, workspaceRoot, selected)
}

// CompletePythonWorkspaceSources adds observations for declarations not
// already present without re-walking the supplied source manifests.
func CompletePythonWorkspaceSources(
	document blueprint.Document,
	workspaceRoot string,
	observed []PythonWorkspaceSource,
) ([]PythonWorkspaceSource, error) {
	if observed == nil {
		return nil, fmt.Errorf("observed Python workspace sources must use an array")
	}
	if err := validatePythonWorkspaceSourcesForSnapshot(observed); err != nil {
		return nil, err
	}
	declared := make(map[string]struct{}, len(document.Environment.Workspace.PythonPackages))
	for name := range document.Environment.Workspace.PythonPackages {
		declared[pythonprovider.NormalizeDistributionName(name)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(observed))
	for _, source := range observed {
		if _, found := declared[source.Distribution]; !found {
			return nil, fmt.Errorf("observed Python workspace source %q is no longer declared", source.Distribution)
		}
		seen[source.Distribution] = struct{}{}
	}
	remaining := make([]string, 0, len(declared)-len(seen))
	for distribution := range declared {
		if distribution != "" {
			if _, found := seen[distribution]; !found {
				remaining = append(remaining, distribution)
			}
		}
	}
	sort.Strings(remaining)
	additional, err := ResolveSelectedPythonWorkspaceSources(document, workspaceRoot, remaining)
	if err != nil {
		return nil, err
	}
	result := append(append([]PythonWorkspaceSource{}, observed...), additional...)
	sort.Slice(result, func(left int, right int) bool { return result[left].Distribution < result[right].Distribution })
	return result, nil
}

func resolvePythonWorkspaceSources(
	document blueprint.Document,
	workspaceRoot string,
	selected map[string]struct{},
) ([]PythonWorkspaceSource, error) {
	packages := document.Environment.Workspace.PythonPackages
	if len(packages) == 0 {
		return []PythonWorkspaceSource{}, nil
	}

	declaredNames := make([]string, 0, len(packages))
	for name := range packages {
		declaredNames = append(declaredNames, name)
	}
	sort.Strings(declaredNames)
	owners := map[string]string{}
	selectedNames := make([]string, 0, len(declaredNames))
	for _, declaredName := range declaredNames {
		if err := blueprint.ValidatePythonDistributionName("Python workspace distribution", declaredName); err != nil {
			return nil, err
		}
		normalized := pythonprovider.NormalizeDistributionName(declaredName)
		if owner, found := owners[normalized]; found {
			return nil, fmt.Errorf("Python workspace distributions %q and %q both normalize to %q", owner, declaredName, normalized)
		}
		owners[normalized] = declaredName

		relative := packages[declaredName]
		cleanRelative := path.Clean(relative)
		if relative == "" || path.IsAbs(relative) || strings.ContainsAny(relative, `\:`) || cleanRelative == ".." || strings.HasPrefix(cleanRelative, "../") {
			return nil, fmt.Errorf("Python workspace distribution %q source must stay within the workspace root", declaredName)
		}
		if selected == nil {
			selectedNames = append(selectedNames, declaredName)
		} else if _, found := selected[normalized]; found {
			selectedNames = append(selectedNames, declaredName)
		}
	}
	if len(selectedNames) == 0 {
		return []PythonWorkspaceSource{}, nil
	}
	if workspaceRoot == "" || !filepath.IsAbs(workspaceRoot) || filepath.Clean(workspaceRoot) != workspaceRoot {
		return nil, fmt.Errorf("Python workspace root must be an absolute clean path")
	}
	realRoot, err := resolveRealPythonSourceDirectory(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("Python workspace root: %w", err)
	}

	sources := make([]PythonWorkspaceSource, 0, len(selectedNames))
	for _, declaredName := range selectedNames {
		normalized := pythonprovider.NormalizeDistributionName(declaredName)
		cleanRelative := path.Clean(packages[declaredName])
		hostDir, err := resolveRealPythonSourceDirectory(filepath.Join(realRoot, filepath.FromSlash(cleanRelative)))
		if err != nil {
			return nil, fmt.Errorf("Python workspace distribution %q source: %w", declaredName, err)
		}
		if err := requirePathWithinPythonWorkspace(realRoot, hostDir); err != nil {
			return nil, fmt.Errorf("Python workspace distribution %q source: %w", declaredName, err)
		}
		manifest, digest, err := ObservePythonSourceManifest(hostDir)
		if err != nil {
			return nil, fmt.Errorf("Python workspace distribution %q source manifest: %w", declaredName, err)
		}
		sources = append(sources, PythonWorkspaceSource{
			Distribution: normalized, HostDir: hostDir,
			Manifest: manifest, SourceManifestDigest: digest,
		})
	}
	sort.Slice(sources, func(left int, right int) bool {
		return sources[left].Distribution < sources[right].Distribution
	})
	return sources, nil
}

// ObservePythonSourceManifest records only source inputs exposed to the v1
// builder snapshot. Generated development directories and bytecode are omitted
// from both the manifest and that later snapshot.
func ObservePythonSourceManifest(sourceDir string) (PythonSourceManifestV1, canonical.Digest, error) {
	realSource, err := resolveRealPythonSourceDirectory(sourceDir)
	if err != nil {
		return PythonSourceManifestV1{}, "", err
	}
	manifest := PythonSourceManifestV1{
		Schema:  pythonSourceManifestSchemaV1,
		Entries: []PythonSourceManifestEntryV1{},
	}
	err = filepath.WalkDir(realSource, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == realSource {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() && ignoredPythonSourceDirectory(name) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && (strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".pyo")) {
			return nil
		}
		relative, err := filepath.Rel(realSource, filename)
		if err != nil {
			return err
		}
		manifestEntry := PythonSourceManifestEntryV1{Path: filepath.ToSlash(relative)}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			manifestEntry.Kind = "directory"
			manifestEntry.Mode = fmt.Sprintf("%04o", info.Mode().Perm())
		case entry.Type().IsRegular():
			manifestEntry.Kind = "file"
			manifestEntry.Mode = fmt.Sprintf("%04o", info.Mode().Perm())
			digest, err := digestPythonSourceFile(filename)
			if err != nil {
				return err
			}
			manifestEntry.ContentDigest = digest
		case entry.Type()&os.ModeSymlink != 0:
			manifestEntry.Kind = "symlink"
			target, err := os.Readlink(filename)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("source symlink %q has an absolute target", manifestEntry.Path)
			}
			resolved, err := filepath.EvalSymlinks(filename)
			if err != nil {
				return fmt.Errorf("resolve source symlink %q: %w", manifestEntry.Path, err)
			}
			if err := requirePathWithinPythonWorkspace(realSource, resolved); err != nil {
				return fmt.Errorf("source symlink %q: %w", manifestEntry.Path, err)
			}
			manifestEntry.LinkTarget = filepath.ToSlash(target)
		default:
			return fmt.Errorf("source entry %q has unsupported file type %s", manifestEntry.Path, info.Mode().Type())
		}
		manifest.Entries = append(manifest.Entries, manifestEntry)
		return nil
	})
	if err != nil {
		return PythonSourceManifestV1{}, "", err
	}
	digest, err := canonical.Sum("python-source-manifest", pythonSourceManifestSchemaV1, manifest)
	if err != nil {
		return PythonSourceManifestV1{}, "", fmt.Errorf("digest Python source manifest: %w", err)
	}
	return manifest, digest, nil
}

func resolveRealPythonSourceDirectory(directory string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", err
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return "", err
	}
	real = filepath.Clean(real)
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", real)
	}
	return real, nil
}

func requirePathWithinPythonWorkspace(root string, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("resolved path %q escapes workspace root", candidate)
	}
	return nil
}

func ignoredPythonSourceDirectory(name string) bool {
	switch name {
	case ".git", ".hg", ".jj", ".mypy_cache", ".nox", ".pytest_cache", ".ruff_cache", ".sl", ".tox", ".venv", "__pycache__", "node_modules":
		return true
	default:
		return strings.HasSuffix(name, ".egg-info")
	}
}

func digestPythonSourceFile(filename string) (canonical.Digest, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))), nil
}
