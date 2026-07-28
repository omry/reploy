package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

const BlueprintManifestGlob = "*.blueprint.yaml"

type LoadedBlueprint struct {
	Document         blueprint.Document
	Ref              PackRef
	RequestedRef     PackRef
	ResolvedArtifact *ResolvedPackArtifact
	ManifestPath     string
	BlueprintSource  string
}

// LoadBlueprint resolves and validates one environment blueprint. It does not
// project the document through the removed app/install/bundle schema.
func LoadBlueprint(ref PackRef) (LoadedBlueprint, error) {
	requested := ref
	resolved := ref
	manifestPath := ""
	var manifestContent []byte
	hasManifestContent := false
	var artifact *ResolvedPackArtifact
	var err error
	switch ref.Scheme {
	case "file":
		manifestPath, err = fileBlueprintManifestPath(ref.Source)
		if err == nil {
			resolved.Source = manifestPath
			resolved.Raw = "file:" + manifestPath
		}
	case "pypi":
		resolved, manifestPath, manifestContent, artifact, err = resolvePyPIBlueprint(ref)
		hasManifestContent = err == nil
	case "git":
		var checkoutRoot, subdir string
		resolved, checkoutRoot, subdir, artifact, err = resolveGitBlueprint(ref)
		if err == nil {
			manifestPath, _, err = sourceBlueprintManifestPath(checkoutRoot, subdir)
		}
	default:
		err = fmt.Errorf("blueprint scheme is not implemented yet: %s", ref.Scheme)
	}
	if err != nil {
		return LoadedBlueprint{}, err
	}
	content, document, err := loadEnvironmentBlueprintManifest(manifestPath, manifestContent, hasManifestContent)
	if err != nil {
		return LoadedBlueprint{}, err
	}
	return LoadedBlueprint{
		Document: document, Ref: resolved, RequestedRef: requested, ResolvedArtifact: artifact,
		ManifestPath: manifestPath, BlueprintSource: string(content),
	}, nil
}

func loadEnvironmentBlueprintManifest(
	manifestPath string,
	content []byte,
	hasContent bool,
) ([]byte, blueprint.Document, error) {
	if !hasContent {
		var err error
		content, err = os.ReadFile(manifestPath)
		if err != nil {
			return nil, blueprint.Document{}, fmt.Errorf("read blueprint manifest: %w", err)
		}
	}
	source, err := blueprint.Decode(content)
	if err != nil {
		return nil, blueprint.Document{}, fmt.Errorf("parse blueprint manifest: %w", err)
	}
	document, err := blueprint.Resolve(source)
	if err != nil {
		return nil, blueprint.Document{}, fmt.Errorf("resolve blueprint manifest: %w", err)
	}
	return content, document, nil
}

func fileBlueprintManifestPath(source string) (string, error) {
	if !filepath.IsAbs(source) {
		absolute, err := filepath.Abs(source)
		if err != nil {
			return "", err
		}
		source = absolute
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return findBlueprintManifest(source)
	}
	if !isBlueprintManifestPath(source) {
		return "", fmt.Errorf("blueprint reference is not a %s file: %s", BlueprintManifestGlob, source)
	}
	return source, nil
}

func sourceBlueprintManifestPath(sourceRoot string, requestedSubdir string) (string, string, error) {
	blueprintPath := strings.Trim(requestedSubdir, "/")
	if blueprintPath == "" {
		projectName, err := sourceProjectName(sourceRoot)
		if err != nil {
			return "", "", err
		}
		blueprintPath = defaultSourceBlueprintSubdir(projectName)
	}
	cleanPath := filepath.Clean(filepath.FromSlash(blueprintPath))
	if cleanPath == "." || filepath.IsAbs(cleanPath) || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("blueprint path must stay inside the source root: %s", requestedSubdir)
	}
	manifestPath, err := fileBlueprintManifestPath(filepath.Join(sourceRoot, cleanPath))
	if err != nil {
		return "", "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve blueprint source root: %w", err)
	}
	resolvedManifest, err := filepath.EvalSymlinks(manifestPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve blueprint manifest: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedManifest)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("blueprint path must stay inside the source root: %s", requestedSubdir)
	}
	return manifestPath, filepath.ToSlash(cleanPath), nil
}

func sourceProjectName(sourceRoot string) (string, error) {
	content, err := os.ReadFile(filepath.Join(sourceRoot, "pyproject.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("blueprint ref without #PATH requires pyproject.toml with [project].name: %s", sourceRoot)
		}
		return "", err
	}
	inProject := false
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inProject = trimmed == "[project]"
			continue
		}
		if !inProject || !strings.HasPrefix(trimmed, "name") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(key) == "name" {
			if name := strings.Trim(strings.TrimSpace(value), `"'`); name != "" {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("blueprint ref without #PATH requires pyproject.toml with [project].name: %s", sourceRoot)
}

func findBlueprintManifest(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, BlueprintManifestGlob))
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("blueprint manifest not found in %s; expected exactly one %s file", dir, BlueprintManifestGlob)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, match := range matches {
			names = append(names, filepath.Base(match))
		}
		return "", fmt.Errorf("multiple blueprint manifests found in %s; choose one explicitly: %s", dir, strings.Join(names, ", "))
	}
}

func isBlueprintManifestPath(path string) bool {
	return strings.HasSuffix(filepath.Base(path), ".blueprint.yaml")
}
