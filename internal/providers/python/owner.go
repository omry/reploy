package python

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	ProfileFactsSchemaV1        = "python-profile-facts-v1"
	InterpreterFactsSchemaV1    = "python-interpreter-facts-v1"
	ConsoleScriptOutputSchemaV1 = "python-console-script-v1"
)

func CanonicalProfileFactsV1(component string, sources []providers.ResolvedSourceInput) providers.CanonicalProviderData {
	values := make([]any, 0, len(sources))
	for _, source := range sources {
		values = append(values, sourceValue(source))
	}
	return providers.CanonicalProviderData{
		Schema: ProfileFactsSchemaV1,
		Value:  canonical.Object{"component": component, "sources": values},
	}
}

func CanonicalInterpreterFactsV1(version string) providers.CanonicalProviderData {
	return providers.CanonicalProviderData{
		Schema: InterpreterFactsSchemaV1,
		Value:  canonical.Object{"consumer_kind": "python", "version": version},
	}
}

func ValidateRequirementProfileV1(profile providers.RequirementProfile) error {
	if profile.Provider != blueprint.ComponentTypePython {
		return fmt.Errorf("Python profile provider must be %q", blueprint.ComponentTypePython)
	}
	request, err := decodeCanonicalProviderRequestV1(providers.CanonicalProviderRequest{
		Schema: profile.Declaration.ProviderData.Schema, Provider: blueprint.ComponentTypePython, Value: profile.Declaration.ProviderData.Value,
	})
	if err != nil {
		return fmt.Errorf("Python profile request: %w", err)
	}
	component, _, err := decodeProfileFactsV1(profile.Facts)
	if err != nil {
		return err
	}
	if component != request.Component {
		return fmt.Errorf("Python profile facts component %q does not match request component %q", component, request.Component)
	}
	expectedRequirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: request.Interpreter.Command,
		VersionConstraint: request.Interpreter.Version, Supplier: request.Interpreter.Supplier,
		ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	if len(profile.Declaration.Executables) != 1 || profile.Declaration.Executables[0] != expectedRequirement {
		return fmt.Errorf("Python profile interpreter requirement does not match its canonical request")
	}
	if len(profile.Declaration.Files) != 0 || len(profile.SelectedFiles) != 0 {
		return fmt.Errorf("Python profile must not contain file requirements")
	}
	if len(profile.SelectedExecutables) != 1 {
		return fmt.Errorf("Python profile must contain exactly one selected interpreter")
	}
	interpreter := profile.SelectedExecutables[0]
	if interpreter.RequirementID != "interpreter" {
		return fmt.Errorf("Python profile selected executable must be the interpreter")
	}
	if interpreter.Facts.Schema != InterpreterFactsSchemaV1 || len(interpreter.Facts.Value) != 2 {
		return fmt.Errorf("Python interpreter facts must use schema %q and the exact value shape", InterpreterFactsSchemaV1)
	}
	kind, kindOK := interpreter.Facts.Value["consumer_kind"].(string)
	version, versionOK := interpreter.Facts.Value["version"].(string)
	if !kindOK || kind != "python" || !versionOK {
		return fmt.Errorf("Python interpreter facts have invalid fields")
	}
	if _, valid := parseReleaseVersion(version); !valid {
		return fmt.Errorf("Python interpreter version %q is not a normalized release version", version)
	}
	return requireCanonicalData(interpreter.Facts, CanonicalInterpreterFactsV1(version), "Python interpreter facts")
}

// RequirementProfileSelectedSourcesV1 returns the selected local sources
// already bound into a validated Python profile.
func RequirementProfileSelectedSourcesV1(profile providers.RequirementProfile) ([]providers.ResolvedSourceInput, error) {
	if err := ValidateRequirementProfileV1(profile); err != nil {
		return nil, err
	}
	_, sources, err := decodeProfileFactsV1(profile.Facts)
	if err != nil {
		return nil, err
	}
	return append([]providers.ResolvedSourceInput{}, sources...), nil
}

func decodeProfileFactsV1(data providers.CanonicalProviderData) (string, []providers.ResolvedSourceInput, error) {
	if data.Schema != ProfileFactsSchemaV1 || len(data.Value) != 2 {
		return "", nil, fmt.Errorf("Python profile facts must use schema %q and the exact value shape", ProfileFactsSchemaV1)
	}
	var wire struct {
		Component string                          `json:"component"`
		Sources   []providers.ResolvedSourceInput `json:"sources"`
	}
	encoded, err := canonical.Marshal(data.Value)
	if err != nil {
		return "", nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return "", nil, fmt.Errorf("decode Python profile facts: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return "", nil, err
	}
	if err := blueprint.ValidateContributionReference("Python profile contribution", wire.Component); err != nil {
		return "", nil, err
	}
	if wire.Sources == nil {
		return "", nil, fmt.Errorf("Python profile sources must use an array")
	}
	for index, source := range wire.Sources {
		if index > 0 && (wire.Sources[index-1].Component > source.Component || wire.Sources[index-1].Component == source.Component && wire.Sources[index-1].LogicalPackage >= source.LogicalPackage) {
			return "", nil, fmt.Errorf("Python profile sources must be unique and sorted")
		}
		if source.Component != wire.Component {
			return "", nil, fmt.Errorf("Python profile source targets component %q, want %q", source.Component, wire.Component)
		}
		if err := ValidateResolvedSourceInputV2(source); err != nil {
			return "", nil, err
		}
	}
	expected := CanonicalProfileFactsV1(wire.Component, wire.Sources)
	if err := requireCanonicalData(data, expected, "Python profile facts"); err != nil {
		return "", nil, err
	}
	return wire.Component, wire.Sources, nil
}

func ValidateResolvedBundlePayloadV1(payload providers.ResolvedBundleIdentityV1) error {
	if payload.RecipeVersion != RecipeVersion {
		return fmt.Errorf("Python bundle recipe version must be %q", RecipeVersion)
	}
	request, err := decodeCanonicalProviderRequestV1(payload.Request)
	if err != nil {
		return fmt.Errorf("Python bundle request: %w", err)
	}
	expectedNodeID := providers.NodeID("python/" + request.Component)
	if application, ok := blueprint.ApplicationContributionOwner(
		request.Component,
		blueprint.ContributionProviderPython,
	); ok {
		expectedNodeID = providers.NodeID("python/" + blueprint.ApplicationID(application))
	}
	if payload.NodeID != expectedNodeID {
		return fmt.Errorf("Python bundle node does not match request component %q", request.Component)
	}
	bundle, err := DecodeCanonicalBundleDataV1(request.Component, payload.ProviderPayload)
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(bundle.Sources, payload.SelectedSources); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("Python bundle selected sources do not match its provider payload")
	}
	artifacts := make([]providerstore.ArtifactDescriptor, 0, len(bundle.Wheels)+len(bundle.Sources)+1)
	artifacts = append(artifacts, bundle.Script)
	for _, wheel := range bundle.Wheels {
		artifacts = append(artifacts, wheel.Artifact)
	}
	for _, source := range bundle.Sources {
		sourceArtifact, err := SourceArtifactDescriptorV2(source)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, sourceArtifact)
	}
	sort.Slice(artifacts, func(left int, right int) bool { return artifacts[left].LogicalPath < artifacts[right].LogicalPath })
	if equal, err := canonicalEqual(artifacts, payload.Artifacts); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("Python bundle artifacts do not match its wheel and retained-source payload")
	}
	outputs := make([]providers.ResolvedOutput, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		outputs = append(outputs, providers.ResolvedOutput{
			SupplierComponent: request.Component, SupplierNode: payload.NodeID, Name: output.Name,
			Candidate: providers.ExecutableCandidate{
				InvocationPath: output.Path,
				Provenance: providers.CanonicalProviderData{
					Schema: ConsoleScriptOutputSchemaV1,
					Value:  canonical.Object{"distribution": output.Distribution, "entry_point": output.EntryPoint},
				},
			},
		})
	}
	if equal, err := canonicalEqual(outputs, payload.Outputs); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("Python bundle outputs do not match its console-script payload")
	}
	return nil
}

func DecodeCanonicalBundleDataV1(component string, data providers.CanonicalProviderData) (PythonBundleV1, error) {
	if data.Schema != BundleSchemaV1 || len(data.Value) != 5 {
		return PythonBundleV1{}, fmt.Errorf("Python bundle data must use schema %q and the exact value shape", BundleSchemaV1)
	}
	type wheelWire struct {
		Distribution string                           `json:"distribution"`
		Version      string                           `json:"version"`
		Tags         []string                         `json:"tags"`
		Artifact     providerstore.ArtifactDescriptor `json:"artifact"`
	}
	type outputWire struct {
		Name         string `json:"name"`
		Distribution string `json:"distribution"`
		EntryPoint   string `json:"entry_point"`
		Path         string `json:"path"`
	}
	var wire struct {
		Interpreter providers.ExecutableEvidence     `json:"interpreter"`
		Script      providerstore.ArtifactDescriptor `json:"script"`
		Wheels      []wheelWire                      `json:"wheels"`
		Outputs     []outputWire                     `json:"outputs"`
		Sources     []providers.ResolvedSourceInput  `json:"sources"`
	}
	encoded, err := canonical.Marshal(data.Value)
	if err != nil {
		return PythonBundleV1{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return PythonBundleV1{}, fmt.Errorf("decode Python bundle data: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return PythonBundleV1{}, err
	}
	bundle := PythonBundleV1{
		Interpreter: wire.Interpreter,
		Script:      wire.Script,
		Wheels:      make([]PythonWheelV1, 0, len(wire.Wheels)),
		Outputs:     make([]PythonConsoleScriptV1, 0, len(wire.Outputs)),
		Sources:     wire.Sources,
	}
	for _, wheel := range wire.Wheels {
		bundle.Wheels = append(bundle.Wheels, PythonWheelV1{
			Distribution: wheel.Distribution, Version: wheel.Version, Tags: wheel.Tags, Artifact: wheel.Artifact,
		})
	}
	for _, output := range wire.Outputs {
		bundle.Outputs = append(bundle.Outputs, PythonConsoleScriptV1{
			Name: output.Name, Distribution: output.Distribution, EntryPoint: output.EntryPoint, Path: output.Path,
		})
	}
	if err := ValidateBundleV1(component, bundle); err != nil {
		return PythonBundleV1{}, err
	}
	normalized, err := CanonicalBundleDataV1(component, bundle)
	if err != nil {
		return PythonBundleV1{}, err
	}
	if equal, err := canonicalEqual(data, normalized); err != nil {
		return PythonBundleV1{}, err
	} else if !equal {
		return PythonBundleV1{}, fmt.Errorf("Python bundle data is not canonically normalized")
	}
	return bundle, nil
}

func requireCanonicalData(actual providers.CanonicalProviderData, expected providers.CanonicalProviderData, field string) error {
	equal, err := canonicalEqual(actual, expected)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("%s does not use the exact canonical value", field)
	}
	return nil
}

func canonicalEqual(left any, right any) (bool, error) {
	leftBytes, err := canonical.Marshal(left)
	if err != nil {
		return false, err
	}
	rightBytes, err := canonical.Marshal(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftBytes, rightBytes), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("Python bundle data contains trailing JSON")
		}
		return err
	}
	return nil
}
