package dockerdeploy

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

const pythonSourceManifestSchemaV1 = pythonprovider.SourceManifestSchemaV1

// PythonLocalOverrideV1 is an uninterpreted, staging-only physical locator.
// Constructing it never accesses HostDir; source observation is demand-driven
// after a direct request or resolved package closure identifies a matching
// distribution.
type PythonLocalOverrideV1 struct {
	Distribution string
	HostDir      string
}

// PythonLocalSource is a staging-only physical locator paired with the
// path-free source manifest that later Python preparation turns into a wheel.
// HostDir and Manifest never cross into the provider graph or build lock.
type PythonLocalSource struct {
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

// PythonLocalOverridesV1 extracts local Python override locators without
// inspecting any target path.
func PythonLocalOverridesV1(
	overrides deploy.ResolvedPackageOverridesV1,
) ([]PythonLocalOverrideV1, error) {
	packages := overrides.Providers[string(blueprint.ComponentTypePython)]
	result := make([]PythonLocalOverrideV1, 0, len(packages))
	for distribution, choice := range packages {
		if distribution == "" || pythonprovider.NormalizeDistributionName(distribution) != distribution {
			return nil, fmt.Errorf("local Python override distribution is not normalized: %q", distribution)
		}
		if choice.Path == "" {
			continue
		}
		if choice.Version != "" {
			return nil, fmt.Errorf("local Python override %q also contains a version", distribution)
		}
		if !filepath.IsAbs(choice.Path) || filepath.Clean(choice.Path) != choice.Path {
			return nil, fmt.Errorf("local Python override %q path must be absolute and clean", distribution)
		}
		result = append(result, PythonLocalOverrideV1{Distribution: distribution, HostDir: choice.Path})
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].Distribution < result[right].Distribution
	})
	return result, nil
}

// ObserveSelectedPythonLocalSources observes only selected normalized
// distributions. Unselected override paths are never statted or walked.
func ObserveSelectedPythonLocalSources(
	overrides []PythonLocalOverrideV1,
	distributions []string,
) ([]PythonLocalSource, error) {
	if overrides == nil {
		return nil, fmt.Errorf("local Python overrides must use an array")
	}
	if distributions == nil {
		return nil, fmt.Errorf("selected local Python distributions must use an array")
	}
	selected := make(map[string]struct{}, len(distributions))
	for index, distribution := range distributions {
		if distribution == "" || pythonprovider.NormalizeDistributionName(distribution) != distribution {
			return nil, fmt.Errorf("selected local Python distribution %d is not normalized: %q", index, distribution)
		}
		if index > 0 && distributions[index-1] >= distribution {
			return nil, fmt.Errorf("selected local Python distributions must be unique and sorted")
		}
		selected[distribution] = struct{}{}
	}

	sources := make([]PythonLocalSource, 0, len(selected))
	for index, override := range overrides {
		if override.Distribution == "" || pythonprovider.NormalizeDistributionName(override.Distribution) != override.Distribution {
			return nil, fmt.Errorf("local Python override %d distribution is not normalized: %q", index, override.Distribution)
		}
		if index > 0 && overrides[index-1].Distribution >= override.Distribution {
			return nil, fmt.Errorf("local Python overrides must be unique and sorted")
		}
		if override.HostDir == "" || !filepath.IsAbs(override.HostDir) || filepath.Clean(override.HostDir) != override.HostDir {
			return nil, fmt.Errorf("local Python override %q path must be absolute and clean", override.Distribution)
		}
		if _, found := selected[override.Distribution]; !found {
			continue
		}
		hostDir, err := resolveRealPythonSourceDirectory(override.HostDir)
		if err != nil {
			return nil, fmt.Errorf("local Python override %q source: %w", override.Distribution, err)
		}
		manifest, digest, err := ObservePythonSourceManifest(hostDir)
		if err != nil {
			return nil, fmt.Errorf("local Python override %q source manifest: %w", override.Distribution, err)
		}
		sources = append(sources, PythonLocalSource{
			Distribution: override.Distribution, HostDir: hostDir,
			Manifest: manifest, SourceManifestDigest: digest,
		})
	}
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
