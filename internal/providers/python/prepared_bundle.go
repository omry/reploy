package python

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

var requirementNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*`)

const (
	maxWheelMetadataFieldNameBytes = 256
	maxWheelMetadataFieldBytes     = 1 << 20
)

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
	ResolveInterpreter     InterpreterEvidenceResolver
	PrepareWheels          func(context.Context, providerapi.ResolveInput, providerapi.ExecutableEvidence) (string, error)
	SelectedPortableWheels []PortableToolVerifiedWheelInputV1
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
		wheel, err := inspectWheel(ctx, filepath.Join(dir, entry.Name()))
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
	wheels, artifacts, outputs, selectedSources, err := publishPreparedWheels(ctx, dir, sink, request, input.SourceCandidates, resolver.SelectedPortableWheels)
	if err != nil {
		return providerapi.ResolveResult{}, err
	}
	for _, source := range selectedSources {
		sourceArtifact, err := SourceArtifactDescriptorV2(source)
		if err != nil {
			return providerapi.ResolveResult{}, err
		}
		artifacts = append(artifacts, sourceArtifact)
	}
	profile := providerapi.RequirementProfile{
		Schema:              providerapi.RequirementProfileSchemaV1,
		Provider:            blueprint.ComponentTypePython,
		Declaration:         input.Node.Requirements,
		SelectedExecutables: []providerapi.ExecutableEvidence{interpreter},
		SelectedFiles:       []providerapi.FileEvidence{},
		Platform:            input.Platform,
		Facts:               CanonicalProfileFactsV2(request.Component, selectedSources),
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
	selected []PortableToolVerifiedWheelInputV1,
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
		wheel, err := inspectWheel(ctx, filepath.Join(dir, entry.Name()))
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
	if err := validateSelectedPortableWheelOutputs(ctx, dir, byDistribution, selected); err != nil {
		return nil, nil, nil, nil, err
	}
	selectedSources, err := selectResolvedSourceArtifacts(sources, byDistribution)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	runtimeRoot, err := RuntimeRootV1(request.Component)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("resolve Python runtime root: %w", err)
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
				Path: runtimeRoot + "/bin/" + name,
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

func validateSelectedPortableWheelOutputs(
	ctx context.Context,
	dir string,
	outputs map[string]inspectedWheel,
	selected []PortableToolVerifiedWheelInputV1,
) error {
	seen := make(map[string]struct{}, len(selected))
	for _, input := range selected {
		distribution := NormalizeDistributionName(input.Inspection.Distribution)
		if distribution == "" || distribution != input.Inspection.Distribution {
			return fmt.Errorf("selected portable wheel has invalid normalized distribution %q", input.Inspection.Distribution)
		}
		if _, exists := seen[distribution]; exists {
			return fmt.Errorf("selected portable wheel distribution %q is duplicated", distribution)
		}
		seen[distribution] = struct{}{}
		output, found := outputs[distribution]
		if !found {
			return fmt.Errorf("selected portable wheel distribution %q is missing from resolver output", distribution)
		}
		if output.Filename != input.Inspection.Filename ||
			output.SHA256 != strings.TrimPrefix(string(input.Descriptor.SHA256), "sha256:") {
			return fmt.Errorf("selected portable wheel distribution %q has different bytes or filename", distribution)
		}
		observed, err := inspectWheelPath(ctx, filepath.Join(dir, output.Filename), input.Descriptor.LogicalPath, &input.Descriptor)
		if err != nil {
			return fmt.Errorf("inspect selected portable wheel output %q: %w", distribution, err)
		}
		if !reflect.DeepEqual(observed, input.Inspection) {
			return fmt.Errorf("selected portable wheel distribution %q has different inspected metadata", distribution)
		}
		matchedScript := false
		for _, script := range observed.ConsoleScripts {
			if script == input.ConsoleScript {
				matchedScript = true
				break
			}
		}
		if !matchedScript {
			return fmt.Errorf("selected portable wheel distribution %q is missing its selected console script", distribution)
		}
	}
	return nil
}

func pathJoin(parts ...string) string {
	return strings.Join(parts, "/")
}

func inspectWheel(ctx context.Context, filename string) (inspectedWheel, error) {
	observed, err := inspectWheelPath(ctx, filename, "wheels/"+filepath.Base(filename), nil)
	if err != nil {
		return inspectedWheel{}, err
	}
	scripts := make(map[string]string, len(observed.ConsoleScripts))
	for _, script := range observed.ConsoleScripts {
		scripts[script.Name] = script.Target
	}
	return inspectedWheel{Distribution: observed.Distribution, Version: observed.Version,
		Filename: observed.Filename, SHA256: strings.TrimPrefix(string(observed.Artifact.SHA256), "sha256:"),
		Tags: observed.InternalTags, ConsoleScripts: scripts}, nil
}

// InspectWheelDeclaredDependenciesV1 reads the shared verified wheel inspection
// and returns only dependency names present in the resolved distribution set.
func InspectWheelDeclaredDependenciesV1(filename string, resolvedDistributions []string) ([]string, error) {
	observed, err := inspectWheelPath(context.Background(), filename, "wheels/"+filepath.Base(filename), nil)
	if err != nil {
		return nil, err
	}
	return selectedWheelDependencies(observed.DeclaredDependencies, resolvedDistributions)
}

// InspectWheelDeclaredDependenciesReaderV1 inspects the exact descriptor-bound
// wheel. The caller owns reader and keeps it open through the call.
func InspectWheelDeclaredDependenciesReaderV1(reader io.ReaderAt, size int64, descriptor providerstore.ArtifactDescriptor, resolvedDistributions []string) ([]string, error) {
	observed, err := InspectWheelReaderV1(context.Background(), reader, size, filepath.Base(descriptor.LogicalPath), descriptor)
	if err != nil {
		return nil, err
	}
	return selectedWheelDependencies(observed.DeclaredDependencies, resolvedDistributions)
}

func selectedWheelDependencies(dependencies, resolvedDistributions []string) ([]string, error) {
	allowed := make(map[string]bool, len(resolvedDistributions))
	for index, distribution := range resolvedDistributions {
		if distribution == "" || NormalizeDistributionName(distribution) != distribution {
			return nil, fmt.Errorf("resolved Python distribution %d is not normalized", index)
		}
		if index > 0 && resolvedDistributions[index-1] >= distribution {
			return nil, fmt.Errorf("resolved Python distributions must be unique and sorted")
		}
		allowed[distribution] = true
	}
	result := []string{}
	for _, name := range dependencies {
		if allowed[name] {
			result = append(result, name)
		}
	}
	return result, nil
}

type selectedWheelMetadata struct {
	Name    string
	Version string
}

func readSelectedCoreMetadata(reader io.Reader, subject string, onRequirement func(string) error) (selectedWheelMetadata, error) {
	var identity selectedWheelMetadata
	seen := map[string]bool{}
	err := readMetadataFields(reader, subject, func(name string) bool {
		return name == "name" || name == "version" || name == "requires-dist" && onRequirement != nil
	}, func(name, value string) error {
		if name != "requires-dist" && seen[name] {
			return fmt.Errorf("%s contains duplicate %s fields", subject, canonicalWheelMetadataFieldName(name))
		}
		seen[name] = true
		switch name {
		case "name":
			identity.Name = value
		case "version":
			identity.Version = value
		case "requires-dist":
			return onRequirement(value)
		}
		return nil
	})
	return identity, err
}

// readMetadataFields streams unknown fields without retaining their values.
func readMetadataFields(reader io.Reader, subject string, selected func(string) bool, onField func(string, string) error) error {
	buffered := bufio.NewReader(reader)
	currentName := ""
	currentValue := []byte(nil)
	currentSelected := false

	flush := func() error {
		if currentName == "" {
			return nil
		}
		if !currentSelected {
			currentName = ""
			currentValue = nil
			return nil
		}
		value := string(bytes.TrimSpace(currentValue))
		if err := onField(currentName, value); err != nil {
			return err
		}

		currentName = ""
		currentValue = nil
		currentSelected = false
		return nil
	}
	appendValue := func(fragment []byte) error {
		if !currentSelected {
			return nil
		}
		if len(currentValue)+len(fragment) > maxWheelMetadataFieldBytes {
			return fmt.Errorf(
				"%s %s field exceeds %d bytes",
				subject, canonicalWheelMetadataFieldName(currentName), maxWheelMetadataFieldBytes,
			)
		}
		currentValue = append(currentValue, fragment...)
		return nil
	}

	for {
		fragment, more, err := buffered.ReadLine()
		if err != nil {
			if err == io.EOF {
				if flushErr := flush(); flushErr != nil {
					return flushErr
				}
				return nil
			}
			return err
		}
		if len(fragment) == 0 && !more {
			if err := flush(); err != nil {
				return err
			}
			return nil
		}

		continuation := fragment[0] == ' ' || fragment[0] == '\t'
		if continuation {
			if currentName == "" {
				return fmt.Errorf("%s contains a continuation without a field", subject)
			}
			if currentSelected && len(currentValue) != 0 {
				if err := appendValue([]byte{' '}); err != nil {
					return err
				}
			}
			if err := appendValue(bytes.TrimLeft(fragment, " \t")); err != nil {
				return err
			}
			for more {
				fragment, more, err = buffered.ReadLine()
				if err != nil {
					return err
				}
				if err := appendValue(fragment); err != nil {
					return err
				}
			}
			continue
		}

		if err := flush(); err != nil {
			return err
		}
		name := make([]byte, 0, 32)
		foundColon := false
		for {
			if !foundColon {
				if colon := bytes.IndexByte(fragment, ':'); colon >= 0 {
					if len(name)+colon > maxWheelMetadataFieldNameBytes {
						return fmt.Errorf("%s field name is too long", subject)
					}
					name = append(name, fragment[:colon]...)
					if !validWheelMetadataFieldName(name) {
						return fmt.Errorf("%s contains invalid field name %q", subject, name)
					}
					currentName = strings.ToLower(string(name))
					currentSelected = selected(currentName)
					foundColon = true
					if err := appendValue(fragment[colon+1:]); err != nil {
						return err
					}
				} else {
					if len(name)+len(fragment) > maxWheelMetadataFieldNameBytes {
						return fmt.Errorf("%s field name is too long", subject)
					}
					name = append(name, fragment...)
				}
			} else if err := appendValue(fragment); err != nil {
				return err
			}
			if !more {
				break
			}
			fragment, more, err = buffered.ReadLine()
			if err != nil {
				return err
			}
		}
		if !foundColon {
			return fmt.Errorf("%s line is missing a field separator", subject)
		}
	}
}

func validWheelMetadataFieldName(name []byte) bool {
	if len(name) == 0 {
		return false
	}
	for _, char := range name {
		if char <= 0x20 || char >= 0x7f || strings.ContainsRune(`()<>@,;:\"/[]?={}`, rune(char)) {
			return false
		}
	}
	return true
}

func canonicalWheelMetadataFieldName(name string) string {
	switch name {
	case "name":
		return "Name"
	case "version":
		return "Version"
	case "requires-dist":
		return "Requires-Dist"
	default:
		return name
	}
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
		if source.OutputArtifactDigest != wheelDigest {
			return nil, fmt.Errorf("prepared Python source wheel for %q has digest %q, want resolved artifact %q", source.LogicalPackage, wheelDigest, source.OutputArtifactDigest)
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
