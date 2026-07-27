package python

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

var requirementNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*`)

// InterpreterEvidenceResolver validates one Python interpreter candidate
// against the exact image prefix and returns the consuming Python node's
// observed evidence. It must determine the runtime version rather than trust
// supplier metadata.
type InterpreterEvidenceResolver func(
	context.Context,
	providerapi.ExecutableRequirement,
	[]providerapi.RealizedOutput,
	providerapi.RealizedImageV1,
	blueprint.Platform,
) (providerapi.ExecutableEvidence, error)

// WheelNodeResolver validates one interpreter, runs the backend-owned wheel
// resolver, and ingests its closed output into canonical provider records.
type WheelNodeResolver struct {
	ResolveInterpreter InterpreterEvidenceResolver
	PrepareWheels      func(context.Context, providerapi.ResolveInput, providerapi.ExecutableEvidence) (string, error)
}

type inspectedWheel struct {
	Distribution   string
	Version        string
	Filename       string
	SHA256         string
	Tags           []string
	ConsoleScripts map[string]string
}

// InspectPreparedWheelDistributionsV1 validates a resolver output directory
// and returns its unique normalized distributions without publishing it.
// This is used to discover which optional local overrides are actually part
// of the resolved closure before any local path is inspected.
func InspectPreparedWheelDistributionsV1(ctx context.Context, dir string) ([]string, error) {
	if ctx == nil {
		return nil, fmt.Errorf("inspect prepared Python wheels requires a context")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read Python resolver output: %w", err)
	}
	distributions := make([]string, 0, len(entries))
	owners := make(map[string]string, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".whl") {
			return nil, fmt.Errorf("Python resolver output contains unexpected entry %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect Python resolver output %q: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Python resolver output %q must be a regular wheel", entry.Name())
		}
		wheel, err := inspectWheel(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("inspect Python wheel %s: %w", entry.Name(), err)
		}
		if prior, found := owners[wheel.Distribution]; found {
			return nil, fmt.Errorf(
				"Python resolver output contains duplicate normalized distribution %q in %s and %s",
				wheel.Distribution, prior, wheel.Filename,
			)
		}
		owners[wheel.Distribution] = wheel.Filename
		distributions = append(distributions, wheel.Distribution)
	}
	if len(distributions) == 0 {
		return nil, fmt.Errorf("prepared Python bundle contains no wheels: %s", dir)
	}
	sort.Strings(distributions)
	return distributions, nil
}

func (WheelNodeResolver) Type() blueprint.ComponentType { return blueprint.ComponentTypePython }

func (resolver WheelNodeResolver) Resolve(
	ctx context.Context,
	input providerapi.ResolveInput,
	sink providerapi.ArtifactSink,
) (providerapi.ResolveResult, error) {
	if resolver.ResolveInterpreter == nil {
		return providerapi.ResolveResult{}, fmt.Errorf("prepared Python resolver requires interpreter validation")
	}
	if resolver.PrepareWheels == nil {
		return providerapi.ResolveResult{}, fmt.Errorf("prepared Python resolver requires wheel preparation")
	}
	request, err := decodeCanonicalProviderRequestV1(input.Node.Request)
	if err != nil {
		return providerapi.ResolveResult{}, err
	}
	if len(input.Node.Components) != 1 || input.Node.Components[0] != request.Component {
		return providerapi.ResolveResult{}, fmt.Errorf("Python node components do not match request component %q", request.Component)
	}
	if len(input.Node.Requirements.Executables) != 1 || len(input.Candidates) != 1 {
		return providerapi.ResolveResult{}, fmt.Errorf("Python node must provide one interpreter requirement and candidate group")
	}
	interpreter, err := resolver.ResolveInterpreter(
		ctx,
		input.Node.Requirements.Executables[0],
		append([]providerapi.RealizedOutput{}, input.Candidates[0].Outputs...),
		input.Upstream,
		input.Platform,
	)
	if err != nil {
		return providerapi.ResolveResult{}, fmt.Errorf("validate Python interpreter: %w", err)
	}
	dir, err := resolver.PrepareWheels(ctx, input, interpreter)
	if err != nil {
		return providerapi.ResolveResult{}, fmt.Errorf("prepare Python wheels: %w", err)
	}
	if dir == "" {
		return providerapi.ResolveResult{}, fmt.Errorf("prepare Python wheels returned no output directory")
	}
	wheels, artifacts, outputs, selectedSources, err := publishPreparedWheels(ctx, dir, sink, request, input.SourceCandidates)
	if err != nil {
		return providerapi.ResolveResult{}, err
	}
	profile := providerapi.RequirementProfile{
		Schema:              providerapi.RequirementProfileSchemaV1,
		Provider:            blueprint.ComponentTypePython,
		Declaration:         input.Node.Requirements,
		SelectedExecutables: []providerapi.ExecutableEvidence{interpreter},
		SelectedFiles:       []providerapi.FileEvidence{},
		Platform:            input.Platform,
		Facts:               CanonicalProfileFactsV1(request.Component, selectedSources),
	}
	profileDigest, err := providerapi.RequirementProfileDigest(profile, ValidateRequirementProfileV1)
	if err != nil {
		return providerapi.ResolveResult{}, fmt.Errorf("build Python requirement profile: %w", err)
	}
	script, err := publishMaterializationScript(ctx, sink)
	if err != nil {
		return providerapi.ResolveResult{}, err
	}
	artifacts = append(artifacts, script)
	sort.Slice(artifacts, func(left int, right int) bool { return artifacts[left].LogicalPath < artifacts[right].LogicalPath })
	bundleData, err := CanonicalBundleDataV1(request.Component, PythonBundleV1{
		Interpreter: interpreter,
		Script:      script,
		Wheels:      wheels,
		Outputs:     outputs,
		Sources:     append([]providerapi.ResolvedSourceInput{}, selectedSources...),
	})
	if err != nil {
		return providerapi.ResolveResult{}, fmt.Errorf("build Python bundle data: %w", err)
	}
	resolvedOutputs := make([]providerapi.ResolvedOutput, 0, len(outputs))
	for _, output := range outputs {
		resolvedOutputs = append(resolvedOutputs, providerapi.ResolvedOutput{
			SupplierComponent: request.Component,
			SupplierNode:      input.Node.ID,
			Name:              output.Name,
			Candidate: providerapi.ExecutableCandidate{
				InvocationPath: output.Path,
				Provenance: providerapi.CanonicalProviderData{
					Schema: ConsoleScriptOutputSchemaV1,
					Value:  canonical.Object{"distribution": output.Distribution, "entry_point": output.EntryPoint},
				},
			},
		})
	}
	bundle, err := providerapi.NewResolvedBundle(providerapi.ResolvedBundleIdentityV1{
		Schema:                   providerapi.ResolvedBundleSchemaV1,
		NodeID:                   input.Node.ID,
		Provider:                 blueprint.ComponentTypePython,
		Request:                  input.Node.Request,
		RequirementProfileDigest: profileDigest,
		RecipeVersion:            RecipeVersion,
		Platform:                 input.Platform,
		Upstream:                 input.Upstream,
		SelectedSources:          append([]providerapi.ResolvedSourceInput{}, selectedSources...),
		Artifacts:                artifacts,
		Outputs:                  resolvedOutputs,
		ProviderPayload:          bundleData,
	}, ValidateResolvedBundlePayloadV1)
	if err != nil {
		return providerapi.ResolveResult{}, fmt.Errorf("build Python resolved bundle: %w", err)
	}
	evidence, err := providerapi.NewValidationEvidence(input.Upstream.RootFSSubject, profileDigest)
	if err != nil {
		return providerapi.ResolveResult{}, err
	}
	return providerapi.ResolveResult{
		Bundle: bundle, Profile: profile, Evidence: evidence,
		SelectedSources: append([]providerapi.ResolvedSourceInput{}, selectedSources...),
	}, nil
}

func publishPreparedWheels(
	ctx context.Context,
	dir string,
	sink providerapi.ArtifactSink,
	request PythonProviderRequestV1,
	sources []providerapi.ResolvedSourceInput,
) ([]PythonWheelV1, []providerstore.ArtifactDescriptor, []PythonConsoleScriptV1, []providerapi.ResolvedSourceInput, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	inspected := []inspectedWheel{}
	byDistribution := map[string]inspectedWheel{}
	scriptOwners := map[string]string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".whl") {
			return nil, nil, nil, nil, fmt.Errorf("Python resolver output contains unexpected entry %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("inspect Python resolver output %q: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, nil, nil, nil, fmt.Errorf("Python resolver output %q must be a regular wheel", entry.Name())
		}
		wheel, err := inspectWheel(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("inspect Python wheel %s: %w", entry.Name(), err)
		}
		if existing, exists := byDistribution[wheel.Distribution]; exists {
			return nil, nil, nil, nil, fmt.Errorf("Python bundle contains duplicate normalized distribution %q in %s and %s", wheel.Distribution, existing.Filename, wheel.Filename)
		}
		for script := range wheel.ConsoleScripts {
			if owner, exists := scriptOwners[script]; exists {
				return nil, nil, nil, nil, fmt.Errorf("Python console script %q is provided by both %s and %s", script, owner, wheel.Distribution)
			}
			scriptOwners[script] = wheel.Distribution
		}
		byDistribution[wheel.Distribution] = wheel
		inspected = append(inspected, wheel)
	}
	if len(inspected) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("prepared Python bundle contains no wheels: %s", dir)
	}
	if err := validateCanonicalRequestedDistributions(request, byDistribution); err != nil {
		return nil, nil, nil, nil, err
	}
	selectedSources, err := selectResolvedSourceArtifacts(sources, byDistribution)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sort.Slice(inspected, func(left int, right int) bool { return inspected[left].Filename < inspected[right].Filename })
	wheels := make([]PythonWheelV1, 0, len(inspected))
	artifacts := make([]providerstore.ArtifactDescriptor, 0, len(inspected))
	outputs := []PythonConsoleScriptV1{}
	for _, wheel := range inspected {
		filename := filepath.Join(dir, wheel.Filename)
		file, err := os.Open(filename)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		descriptor, publishErr := sink.Publish(ctx, pathJoin("wheels", wheel.Filename), "wheel", file)
		closeErr := file.Close()
		if publishErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("publish Python wheel %s: %w", wheel.Filename, publishErr)
		}
		if closeErr != nil {
			return nil, nil, nil, nil, closeErr
		}
		expectedDigest := canonical.Digest("sha256:" + wheel.SHA256)
		if err := descriptor.Validate(); err != nil || descriptor.LogicalPath != pathJoin("wheels", wheel.Filename) || descriptor.Kind != "wheel" || descriptor.SHA256 != expectedDigest {
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("published Python wheel %s descriptor: %w", wheel.Filename, err)
			}
			return nil, nil, nil, nil, fmt.Errorf("published Python wheel %s descriptor does not match inspected artifact", wheel.Filename)
		}
		wheels = append(wheels, PythonWheelV1{
			Distribution: wheel.Distribution,
			Version:      wheel.Version,
			Tags:         append([]string{}, wheel.Tags...),
			Artifact:     descriptor,
		})
		for name, entryPoint := range wheel.ConsoleScripts {
			outputs = append(outputs, PythonConsoleScriptV1{
				Name: name, Distribution: wheel.Distribution, EntryPoint: entryPoint,
				Path: InstallRoot + "/" + request.Component + "/bin/" + name,
			})
		}
	}
	sort.Slice(wheels, func(left int, right int) bool { return compareWheels(wheels[left], wheels[right]) < 0 })
	sort.Slice(outputs, func(left int, right int) bool { return outputs[left].Name < outputs[right].Name })
	for _, wheel := range wheels {
		artifacts = append(artifacts, wheel.Artifact)
	}
	sort.Slice(artifacts, func(left int, right int) bool { return artifacts[left].LogicalPath < artifacts[right].LogicalPath })
	return wheels, artifacts, outputs, selectedSources, nil
}

func pathJoin(parts ...string) string {
	return strings.Join(parts, "/")
}

func inspectWheel(filename string) (inspectedWheel, error) {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return inspectedWheel{}, err
	}
	defer archive.Close()
	metadataFiles := []*zip.File{}
	wheelFiles := []*zip.File{}
	entryPointFiles := []*zip.File{}
	for _, file := range archive.File {
		if strings.Count(file.Name, "/") == 1 && strings.HasSuffix(file.Name, ".dist-info/METADATA") {
			metadataFiles = append(metadataFiles, file)
		}
		if strings.Count(file.Name, "/") == 1 && strings.HasSuffix(file.Name, ".dist-info/WHEEL") {
			wheelFiles = append(wheelFiles, file)
		}
		if strings.Count(file.Name, "/") == 1 && strings.HasSuffix(file.Name, ".dist-info/entry_points.txt") {
			entryPointFiles = append(entryPointFiles, file)
		}
	}
	if len(metadataFiles) != 1 {
		return inspectedWheel{}, fmt.Errorf("wheel must contain exactly one .dist-info/METADATA file")
	}
	if len(wheelFiles) != 1 {
		return inspectedWheel{}, fmt.Errorf("wheel must contain exactly one .dist-info/WHEEL file")
	}
	if len(entryPointFiles) > 1 {
		return inspectedWheel{}, fmt.Errorf("wheel must contain at most one .dist-info/entry_points.txt file")
	}
	distInfo := strings.TrimSuffix(metadataFiles[0].Name, "METADATA")
	if wheelFiles[0].Name != distInfo+"WHEEL" || len(entryPointFiles) == 1 && entryPointFiles[0].Name != distInfo+"entry_points.txt" {
		return inspectedWheel{}, fmt.Errorf("wheel metadata files must belong to the same .dist-info directory")
	}
	name, version, err := readWheelMetadata(metadataFiles[0])
	if err != nil {
		return inspectedWheel{}, err
	}
	normalized := NormalizeDistributionName(name)
	filenameRequirement, ok := WheelFilenameRequirement(filepath.Base(filename))
	if !ok {
		return inspectedWheel{}, fmt.Errorf("invalid wheel filename")
	}
	wheelName, wheelVersion, _ := strings.Cut(filenameRequirement, "==")
	if wheelName != normalized || wheelVersion != version {
		return inspectedWheel{}, fmt.Errorf("wheel filename identifies %s==%s but metadata identifies %s==%s", wheelName, wheelVersion, normalized, version)
	}
	digest, err := fileSHA256(filename)
	if err != nil {
		return inspectedWheel{}, err
	}
	tags, err := readWheelTags(wheelFiles[0])
	if err != nil {
		return inspectedWheel{}, err
	}
	scripts := map[string]string{}
	if len(entryPointFiles) == 1 {
		scripts, err = readConsoleScripts(entryPointFiles[0])
		if err != nil {
			return inspectedWheel{}, err
		}
	}
	return inspectedWheel{
		Distribution: normalized, Version: version, Filename: filepath.Base(filename), SHA256: digest,
		Tags: tags, ConsoleScripts: scripts,
	}, nil
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

func readWheelTags(file *zip.File) ([]string, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	seen := map[string]bool{}
	tags := []string{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "Tag:"); ok {
			tag := strings.TrimSpace(value)
			if tag == "" {
				return nil, fmt.Errorf("wheel contains an empty compatibility tag")
			}
			if !seen[tag] {
				seen[tag] = true
				tags = append(tags, tag)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("wheel metadata contains no compatibility tags")
	}
	sort.Strings(tags)
	return tags, nil
}

func readConsoleScripts(file *zip.File) (map[string]string, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	scripts := map[string]string{}
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
		name, target, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		target = strings.TrimSpace(target)
		if !ok || name == "" || target == "" || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("invalid console script entry %q", line)
		}
		if _, duplicate := scripts[name]; duplicate {
			return nil, fmt.Errorf("duplicate console script entry %q", name)
		}
		scripts[name] = target
	}
	return scripts, scanner.Err()
}

func validateCanonicalRequestedDistributions(request PythonProviderRequestV1, artifacts map[string]inspectedWheel) error {
	for _, packageRequest := range request.Requirements {
		if err := ValidateCanonicalPackageRequestV1(packageRequest); err != nil {
			return err
		}
		requirement := packageRequest.Value["requirement"].(string)
		name, err := pythonRequirementName(requirement)
		if err != nil {
			return fmt.Errorf("Python component %q requirement: %w", request.Component, err)
		}
		wheel, exists := artifacts[name]
		if !exists {
			return fmt.Errorf("prepared Python bundle is missing root distribution %q for component %q", name, request.Component)
		}
		if satisfied, checked := requirementAllowsVersion(requirement, wheel.Version); checked && !satisfied {
			return fmt.Errorf("Python distribution %q version %s does not satisfy component %q requirement %q", name, wheel.Version, request.Component, requirement)
		}
	}
	return nil
}

func selectResolvedSourceArtifacts(sources []providerapi.ResolvedSourceInput, artifacts map[string]inspectedWheel) ([]providerapi.ResolvedSourceInput, error) {
	selected := make([]providerapi.ResolvedSourceInput, 0, len(sources))
	for _, source := range sources {
		distribution := NormalizeDistributionName(source.LogicalPackage)
		wheel, exists := artifacts[distribution]
		if !exists {
			continue
		}
		wheelDigest := canonical.Digest("sha256:" + wheel.SHA256)
		if source.ArtifactDigest != wheelDigest {
			return nil, fmt.Errorf("prepared Python source wheel for %q has digest %q, want resolved artifact %q", source.LogicalPackage, wheelDigest, source.ArtifactDigest)
		}
		selected = append(selected, source)
	}
	return selected, nil
}

func pythonRequirementName(requirement string) (string, error) {
	value := strings.TrimSpace(requirement)
	match := requirementNamePattern.FindString(value)
	if match == "" {
		return "", fmt.Errorf("invalid requirement %q", requirement)
	}
	return NormalizeDistributionName(match), nil
}

// RequirementDistributionName returns the normalized distribution named by a
// validated Python requirement. It does not resolve or install the package.
func RequirementDistributionName(requirement string) (string, error) {
	return pythonRequirementName(requirement)
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
