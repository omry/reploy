package python

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	ProviderRequestSchemaV1 = "python-provider-request-v1"
	BundleSchemaV1          = "python-bundle-v1"
)

type PythonProviderRequestV1 struct {
	Component    string
	Interpreter  blueprint.CommandRequirement
	Requirements []providers.CanonicalPackageRequest
}

type PythonBundleV1 struct {
	Interpreter providers.ExecutableEvidence
	Script      providerstore.ArtifactDescriptor
	Wheels      []PythonWheelV1
	Outputs     []PythonConsoleScriptV1
	Sources     []providers.ResolvedSourceInput
}

type PythonWheelV1 struct {
	Distribution string
	Version      string
	Tags         []string
	Artifact     providerstore.ArtifactDescriptor
}

type PythonConsoleScriptV1 struct {
	Name         string
	Distribution string
	EntryPoint   string
	Path         string
}

func CanonicalProviderRequestV1(request PythonProviderRequestV1) (providers.CanonicalProviderRequest, error) {
	if err := blueprint.ValidateProviderIdentifier("Python component", request.Component); err != nil {
		return providers.CanonicalProviderRequest{}, err
	}
	if request.Component == "base" {
		return providers.CanonicalProviderRequest{}, fmt.Errorf("Python component cannot use reserved name base")
	}
	if err := request.Interpreter.Validate("Python interpreter"); err != nil {
		return providers.CanonicalProviderRequest{}, err
	}
	if len(request.Requirements) == 0 {
		return providers.CanonicalProviderRequest{}, fmt.Errorf("active Python component %q must contain at least one package requirement", request.Component)
	}
	type encodedRequirement struct {
		value   providers.CanonicalPackageRequest
		encoded []byte
	}
	unique := map[string]encodedRequirement{}
	for _, requirement := range request.Requirements {
		if err := ValidateCanonicalPackageRequestV1(requirement); err != nil {
			return providers.CanonicalProviderRequest{}, err
		}
		encoded, err := providers.CanonicalPackageRequestBytes(requirement)
		if err != nil {
			return providers.CanonicalProviderRequest{}, err
		}
		unique[string(encoded)] = encodedRequirement{value: requirement, encoded: encoded}
	}
	ordered := make([]encodedRequirement, 0, len(unique))
	for _, requirement := range unique {
		ordered = append(ordered, requirement)
	}
	sort.Slice(ordered, func(left int, right int) bool {
		return bytes.Compare(ordered[left].encoded, ordered[right].encoded) < 0
	})
	requirements := make([]any, 0, len(ordered))
	for _, requirement := range ordered {
		requirements = append(requirements, canonical.Object{"schema": requirement.value.Schema, "value": requirement.value.Value})
	}
	interpreter := canonical.Object{"command": request.Interpreter.Command}
	if request.Interpreter.Version != "" {
		interpreter["version"] = request.Interpreter.Version
	}
	if request.Interpreter.Supplier != "" {
		interpreter["supplier"] = request.Interpreter.Supplier
	}
	return providers.CanonicalProviderRequest{
		Schema: ProviderRequestSchemaV1, Provider: blueprint.ComponentTypePython,
		Value: canonical.Object{"component": request.Component, "interpreter": interpreter, "requirements": requirements},
	}, nil
}

func ValidateCanonicalProviderRequestV1(request providers.CanonicalProviderRequest) error {
	_, err := decodeCanonicalProviderRequestV1(request)
	return err
}

func ValidateCanonicalProviderRequestForComponentV1(component string, request providers.CanonicalProviderRequest) error {
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		return err
	}
	if decoded.Component != component {
		return fmt.Errorf("Python provider request component %q does not match %q", decoded.Component, component)
	}
	return nil
}

func decodeCanonicalProviderRequestV1(request providers.CanonicalProviderRequest) (PythonProviderRequestV1, error) {
	if request.Schema != ProviderRequestSchemaV1 || request.Provider != blueprint.ComponentTypePython || len(request.Value) != 3 {
		return PythonProviderRequestV1{}, fmt.Errorf("Python provider request must use schema %q and the exact value shape", ProviderRequestSchemaV1)
	}
	component, componentOK := request.Value["component"].(string)
	interpreterValue, interpreterOK := asCanonicalObject(request.Value["interpreter"])
	requirementValues, requirementsOK := request.Value["requirements"].([]any)
	if !componentOK || !interpreterOK || !requirementsOK {
		return PythonProviderRequestV1{}, fmt.Errorf("Python provider request fields have invalid types")
	}
	command, commandOK := interpreterValue["command"].(string)
	version, _ := interpreterValue["version"].(string)
	supplier, _ := interpreterValue["supplier"].(string)
	if !commandOK || len(interpreterValue) != 1+boolInt(version != "")+boolInt(supplier != "") {
		return PythonProviderRequestV1{}, fmt.Errorf("Python provider request interpreter has unknown or empty fields")
	}
	requirements := make([]providers.CanonicalPackageRequest, 0, len(requirementValues))
	for index, value := range requirementValues {
		object, ok := asCanonicalObject(value)
		if !ok || len(object) != 2 {
			return PythonProviderRequestV1{}, fmt.Errorf("Python provider request requirement %d has invalid shape", index)
		}
		schema, schemaOK := object["schema"].(string)
		packageValue, valueOK := asCanonicalObject(object["value"])
		if !schemaOK || !valueOK {
			return PythonProviderRequestV1{}, fmt.Errorf("Python provider request requirement %d has invalid fields", index)
		}
		requirements = append(requirements, providers.CanonicalPackageRequest{Schema: schema, Value: packageValue})
	}
	normalized, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: component, Interpreter: blueprint.CommandRequirement{Command: command, Version: version, Supplier: supplier}, Requirements: requirements,
	})
	if err != nil {
		return PythonProviderRequestV1{}, err
	}
	actual, err := providers.CanonicalProviderRequestBytes(request)
	if err != nil {
		return PythonProviderRequestV1{}, err
	}
	expected, err := providers.CanonicalProviderRequestBytes(normalized)
	if err != nil {
		return PythonProviderRequestV1{}, err
	}
	if !bytes.Equal(actual, expected) {
		return PythonProviderRequestV1{}, fmt.Errorf("Python provider request is not canonically normalized")
	}
	return PythonProviderRequestV1{
		Component: component, Interpreter: blueprint.CommandRequirement{Command: command, Version: version, Supplier: supplier}, Requirements: requirements,
	}, nil
}

func CanonicalBundleDataV1(component string, bundle PythonBundleV1) (providers.CanonicalProviderData, error) {
	if err := ValidateBundleV1(component, bundle); err != nil {
		return providers.CanonicalProviderData{}, err
	}
	wheels := make([]any, 0, len(bundle.Wheels))
	for _, wheel := range bundle.Wheels {
		wheels = append(wheels, canonical.Object{
			"distribution": wheel.Distribution, "version": wheel.Version, "tags": stringArray(wheel.Tags), "artifact": artifactValue(wheel.Artifact),
		})
	}
	outputs := make([]any, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		outputs = append(outputs, canonical.Object{"name": output.Name, "distribution": output.Distribution, "entry_point": output.EntryPoint, "path": output.Path})
	}
	sources := make([]any, 0, len(bundle.Sources))
	for _, source := range bundle.Sources {
		sources = append(sources, sourceValue(source))
	}
	return providers.CanonicalProviderData{Schema: BundleSchemaV1, Value: canonical.Object{
		"interpreter": executableEvidenceValue(bundle.Interpreter), "script": artifactValue(bundle.Script),
		"wheels": wheels, "outputs": outputs, "sources": sources,
	}}, nil
}

func ValidateBundleV1(component string, bundle PythonBundleV1) error {
	if err := blueprint.ValidateProviderIdentifier("Python bundle component", component); err != nil {
		return err
	}
	if bundle.Interpreter.RequirementID != "interpreter" || bundle.Interpreter.Output.Name != "python" {
		return fmt.Errorf("Python bundle interpreter evidence must satisfy interpreter with python")
	}
	if err := providers.ValidateExecutableEvidence(bundle.Interpreter, providers.ExecutableRequirement{
		ID: "interpreter", Command: "python", ValidationPolicy: providers.ValidationPolicyCompatible,
	}); err != nil {
		return fmt.Errorf("Python bundle interpreter: %w", err)
	}
	if err := bundle.Script.Validate(); err != nil {
		return fmt.Errorf("Python materialization script is invalid: %w", err)
	}
	if bundle.Script != materializationScriptDescriptor() {
		return fmt.Errorf("Python materialization script does not match the provider-owned script")
	}
	if bundle.Wheels == nil || bundle.Outputs == nil || bundle.Sources == nil {
		return fmt.Errorf("Python bundle collections must use arrays")
	}
	if len(bundle.Wheels) == 0 {
		return fmt.Errorf("Python bundle must contain at least one wheel")
	}
	for index, wheel := range bundle.Wheels {
		if index > 0 && compareWheels(bundle.Wheels[index-1], wheel) >= 0 {
			return fmt.Errorf("Python wheels must be unique and canonically sorted")
		}
		if wheel.Distribution == "" || wheel.Version == "" || wheel.Distribution != NormalizeDistributionName(wheel.Distribution) {
			return fmt.Errorf("Python wheel distribution and version are not normalized")
		}
		if wheel.Tags == nil || !uniqueSortedStrings(wheel.Tags) {
			return fmt.Errorf("Python wheel tags must be unique and sorted")
		}
		if err := wheel.Artifact.Validate(); err != nil {
			return fmt.Errorf("Python wheel artifact is invalid: %w", err)
		}
		if wheel.Artifact.Kind != "wheel" {
			return fmt.Errorf("Python wheel artifact kind must be wheel")
		}
		if path.Dir(wheel.Artifact.LogicalPath) != "wheels" {
			return fmt.Errorf("Python wheel artifact must be directly beneath wheels")
		}
	}
	root := "/opt/reploy/providers/python/" + component + "/bin/"
	for index, output := range bundle.Outputs {
		if index > 0 && bundle.Outputs[index-1].Name >= output.Name {
			return fmt.Errorf("Python console scripts must be unique and sorted by name")
		}
		if err := blueprint.ValidateProviderIdentifier("Python console script", output.Name); err != nil {
			return err
		}
		if output.Distribution == "" || output.Distribution != NormalizeDistributionName(output.Distribution) || output.EntryPoint == "" {
			return fmt.Errorf("Python console script metadata is incomplete")
		}
		if output.Path != root+output.Name || path.Clean(output.Path) != output.Path {
			return fmt.Errorf("Python console script %q has path outside its component venv", output.Name)
		}
	}
	for index, source := range bundle.Sources {
		if index > 0 && (bundle.Sources[index-1].Component > source.Component || bundle.Sources[index-1].Component == source.Component && bundle.Sources[index-1].LogicalPackage >= source.LogicalPackage) {
			return fmt.Errorf("Python bundle sources must be unique and sorted")
		}
		if source.Component != component {
			return fmt.Errorf("Python bundle source targets component %q, want %q", source.Component, component)
		}
		if err := ValidateResolvedSourceInputV1(source); err != nil {
			return err
		}
	}
	return nil
}

func asCanonicalObject(value any) (canonical.Object, bool) {
	switch object := value.(type) {
	case canonical.Object:
		return object, true
	case map[string]any:
		return canonical.Object(object), true
	default:
		return nil, false
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func stringArray(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func artifactValue(value providerstore.ArtifactDescriptor) canonical.Object {
	return canonical.Object{"logical_path": value.LogicalPath, "kind": value.Kind, "size": value.Size, "sha256": string(value.SHA256)}
}

func providerDataValue(value providers.CanonicalProviderData) canonical.Object {
	return canonical.Object{"schema": value.Schema, "value": value.Value}
}

func executableEvidenceValue(value providers.ExecutableEvidence) canonical.Object {
	links := make([]any, 0, len(value.LinkChain))
	for _, link := range value.LinkChain {
		item := canonical.Object{"path": link.Path, "target": link.Target, "resolved_path": link.ResolvedPath, "kind": link.Kind}
		if link.Owner != nil {
			item["owner"] = canonical.Object{"provider": link.Owner.Provider, "data": providerDataValue(link.Owner.Data)}
		}
		if link.ProviderDetail != nil {
			item["provider_detail"] = providerDataValue(*link.ProviderDetail)
		}
		links = append(links, item)
	}
	accessPaths := make([]any, 0, len(value.Access.Paths))
	for _, item := range value.Access.Paths {
		accessPaths = append(accessPaths, canonical.Object{"path": item.Path, "kind": item.Kind, "mode": item.Mode, "required": item.Required})
	}
	return canonical.Object{
		"schema": value.Schema, "requirement_id": value.RequirementID,
		"output":          canonical.Object{"component": value.Output.Component, "name": value.Output.Name},
		"invocation_path": value.InvocationPath, "link_chain": links,
		"terminal": fileEvidenceValue(value.Terminal),
		"access":   canonical.Object{"schema": value.Access.Schema, "profile": value.Access.Profile, "paths": accessPaths},
		"facts":    providerDataValue(value.Facts),
	}
}

func fileEvidenceValue(value providers.FileEvidence) canonical.Object {
	result := canonical.Object{
		"schema": value.Schema, "requirement_id": value.RequirementID, "path": value.Path,
		"kind": value.Kind, "mode": value.Mode, "size": value.Size, "sha256": string(value.SHA256),
	}
	if value.Owner != nil {
		result["owner"] = canonical.Object{"provider": value.Owner.Provider, "data": providerDataValue(value.Owner.Data)}
	}
	return result
}

func sourceValue(value providers.ResolvedSourceInput) canonical.Object {
	return canonical.Object{
		"schema": value.Schema, "component": value.Component, "logical_package": value.LogicalPackage,
		"source_manifest_digest": string(value.SourceManifestDigest), "builder_profile": value.BuilderProfile,
		"build_settings": providerDataValue(value.BuildSettings), "ecosystem_metadata": providerDataValue(value.EcosystemMetadata),
		"artifact_digest": string(value.ArtifactDigest),
	}
}

func compareWheels(left PythonWheelV1, right PythonWheelV1) int {
	leftKey := left.Distribution + "\x00" + left.Version + "\x00" + strings.Join(left.Tags, "\x00") + "\x00" + string(left.Artifact.SHA256)
	rightKey := right.Distribution + "\x00" + right.Version + "\x00" + strings.Join(right.Tags, "\x00") + "\x00" + string(right.Artifact.SHA256)
	return strings.Compare(leftKey, rightKey)
}

func uniqueSortedStrings(values []string) bool {
	for index, value := range values {
		if value == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}
