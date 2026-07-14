package python

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	providerapi "github.com/omry/reploy/internal/providers"
)

const preparedBundleManifestName = "reploy-wheelhouse.json"

var requirementNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*`)

// PreparedBundleResolver adapts the retained wheelhouse builder to the new
// provider contract. It reads built metadata rather than trusting filenames.
type PreparedBundleResolver struct {
	Dir          string
	BaseIdentity string
}

func (resolver PreparedBundleResolver) ResolvePython(ctx context.Context, request providerapi.ResolveRequest) (ResolvedSet, error) {
	if resolver.Dir == "" {
		return ResolvedSet{}, fmt.Errorf("prepared Python bundle directory is required")
	}
	if resolver.BaseIdentity == "" {
		return ResolvedSet{}, fmt.Errorf("prepared Python bundle base identity is required")
	}
	entries, err := os.ReadDir(resolver.Dir)
	if err != nil {
		return ResolvedSet{}, err
	}
	artifacts := []providerapi.Artifact{}
	byDistribution := map[string]providerapi.Artifact{}
	consoleScripts := map[string]string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ResolvedSet{}, err
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".whl") {
			continue
		}
		artifact, scripts, err := inspectWheel(filepath.Join(resolver.Dir, entry.Name()))
		if err != nil {
			return ResolvedSet{}, fmt.Errorf("inspect Python wheel %s: %w", entry.Name(), err)
		}
		if existing, exists := byDistribution[artifact.Identifier]; exists {
			return ResolvedSet{}, fmt.Errorf("Python bundle contains duplicate normalized distribution %q in %s and %s", artifact.Identifier, existing.Path, artifact.Path)
		}
		byDistribution[artifact.Identifier] = artifact
		artifacts = append(artifacts, artifact)
		for script := range scripts {
			if owner, exists := consoleScripts[script]; exists {
				return ResolvedSet{}, fmt.Errorf("Python console script %q is provided by both %s and %s", script, owner, artifact.Identifier)
			}
			consoleScripts[script] = artifact.Identifier
		}
	}
	if len(artifacts) == 0 {
		return ResolvedSet{}, fmt.Errorf("prepared Python bundle contains no wheels: %s", resolver.Dir)
	}
	if err := validateRequestedDistributions(request, byDistribution); err != nil {
		return ResolvedSet{}, err
	}
	if err := validateTranslationArtifacts(resolver.Dir, request, byDistribution); err != nil {
		return ResolvedSet{}, err
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return ResolvedSet{BaseIdentity: resolver.BaseIdentity, Artifacts: artifacts, ConsoleScripts: consoleScripts}, nil
}

func inspectWheel(filename string) (providerapi.Artifact, map[string]bool, error) {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return providerapi.Artifact{}, nil, err
	}
	defer archive.Close()
	metadataFiles := []*zip.File{}
	var entryPoints *zip.File
	for _, file := range archive.File {
		if strings.Count(file.Name, "/") == 1 && strings.HasSuffix(file.Name, ".dist-info/METADATA") {
			metadataFiles = append(metadataFiles, file)
		}
		if strings.Count(file.Name, "/") == 1 && strings.HasSuffix(file.Name, ".dist-info/entry_points.txt") {
			entryPoints = file
		}
	}
	if len(metadataFiles) != 1 {
		return providerapi.Artifact{}, nil, fmt.Errorf("wheel must contain exactly one .dist-info/METADATA file")
	}
	name, version, err := readWheelMetadata(metadataFiles[0])
	if err != nil {
		return providerapi.Artifact{}, nil, err
	}
	normalized := NormalizeDistributionName(name)
	filenameRequirement, ok := WheelFilenameRequirement(filepath.Base(filename))
	if !ok {
		return providerapi.Artifact{}, nil, fmt.Errorf("invalid wheel filename")
	}
	wheelName, wheelVersion, _ := strings.Cut(filenameRequirement, "==")
	if wheelName != normalized || wheelVersion != version {
		return providerapi.Artifact{}, nil, fmt.Errorf("wheel filename identifies %s==%s but metadata identifies %s==%s", wheelName, wheelVersion, normalized, version)
	}
	digest, err := fileSHA256(filename)
	if err != nil {
		return providerapi.Artifact{}, nil, err
	}
	scripts := map[string]bool{}
	if entryPoints != nil {
		scripts, err = readConsoleScripts(entryPoints)
		if err != nil {
			return providerapi.Artifact{}, nil, err
		}
	}
	return providerapi.Artifact{
		Identifier: normalized, Version: version, Kind: "wheel",
		Path: filepath.Base(filename), SHA256: digest,
	}, scripts, nil
}

func readWheelMetadata(file *zip.File) (string, string, error) {
	reader, err := file.Open()
	if err != nil {
		return "", "", err
	}
	defer reader.Close()
	name := ""
	version := ""
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		if value, ok := strings.CutPrefix(line, "Name:"); ok {
			name = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "Version:"); ok {
			version = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if name == "" || version == "" {
		return "", "", fmt.Errorf("wheel metadata requires Name and Version")
	}
	return name, version, nil
}

func readConsoleScripts(file *zip.File) (map[string]bool, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	scripts := map[string]bool{}
	inConsoleScripts := false
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inConsoleScripts = line == "[console_scripts]"
			continue
		}
		if !inConsoleScripts || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("invalid console script entry %q", line)
		}
		scripts[name] = true
	}
	return scripts, scanner.Err()
}

func validateRequestedDistributions(request providerapi.ResolveRequest, artifacts map[string]providerapi.Artifact) error {
	translated := map[string]bool{}
	for _, translation := range request.Translations {
		for distribution := range translation.Mappings {
			translated[NormalizeDistributionName(distribution)] = true
		}
	}
	for _, component := range request.Components {
		for _, requirement := range component.Requirements {
			name, err := pythonRequirementName(requirement)
			if err != nil {
				return fmt.Errorf("Python component %q requirement: %w", component.Name, err)
			}
			artifact, exists := artifacts[name]
			if !exists {
				return fmt.Errorf("prepared Python bundle is missing root distribution %q for component %q", name, component.Name)
			}
			if translated[name] {
				if satisfied, checked := requirementAllowsVersion(requirement, artifact.Version); checked && !satisfied {
					return fmt.Errorf("translated Python distribution %q built version %s does not satisfy component %q requirement %q", name, artifact.Version, component.Name, requirement)
				}
			}
		}
	}
	for _, root := range request.DirectRoots {
		name, err := pythonRootName(root)
		if err != nil {
			return err
		}
		if _, exists := artifacts[name]; !exists {
			return fmt.Errorf("prepared Python bundle is missing direct root distribution %q", name)
		}
	}
	return nil
}

func pythonRequirementName(requirement string) (string, error) {
	value := strings.TrimSpace(requirement)
	match := requirementNamePattern.FindString(value)
	if match == "" {
		return "", fmt.Errorf("invalid requirement %q", requirement)
	}
	return NormalizeDistributionName(match), nil
}

func pythonRootName(root string) (string, error) {
	root = strings.TrimSpace(root)
	if strings.HasSuffix(strings.ToLower(root), ".whl") {
		requirement, ok := WheelFilenameRequirement(filepath.Base(root))
		if !ok {
			return "", fmt.Errorf("invalid Python wheel root %q", root)
		}
		name, _, _ := strings.Cut(requirement, "==")
		return name, nil
	}
	return pythonRequirementName(root)
}

func validateTranslationArtifacts(dir string, request providerapi.ResolveRequest, artifacts map[string]providerapi.Artifact) error {
	type source struct {
		Wheel string `json:"wheel"`
	}
	var manifest struct {
		SchemaVersion int               `json:"schema_version"`
		LocalSources  map[string]source `json:"local_sources"`
	}
	content, err := os.ReadFile(filepath.Join(dir, preparedBundleManifestName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(content, &manifest); err != nil {
			return fmt.Errorf("decode %s: %w", preparedBundleManifestName, err)
		}
		if manifest.SchemaVersion != 1 {
			return fmt.Errorf("unsupported %s schema %d", preparedBundleManifestName, manifest.SchemaVersion)
		}
	}
	for _, translation := range request.Translations {
		for distribution := range translation.Mappings {
			artifact, used := artifacts[distribution]
			if !used {
				continue
			}
			built, ok := manifest.LocalSources[distribution]
			if !ok || built.Wheel != artifact.Path {
				return fmt.Errorf("Python translation for %q did not take precedence in the prepared bundle", distribution)
			}
		}
	}
	return nil
}

func fileSHA256(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
