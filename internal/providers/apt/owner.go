package apt

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

const ProfileFactsSchemaV1 = "apt-profile-facts-v1"

func CanonicalProfileFactsV1(base BaseProfileEvidenceV1, executables []providers.ValidatedExecutableInput) (providers.CanonicalProviderData, error) {
	if err := ValidateBaseProfileEvidenceV1(base); err != nil {
		return providers.CanonicalProviderData{}, err
	}
	if err := validateBaseExecutableBindingsV1(base, executables); err != nil {
		return providers.CanonicalProviderData{}, err
	}
	wire := struct {
		Base        BaseProfileEvidenceV1                `json:"base"`
		Executables []providers.ValidatedExecutableInput `json:"executables"`
	}{Base: base, Executables: append([]providers.ValidatedExecutableInput{}, executables...)}
	encoded, err := canonical.Marshal(wire)
	if err != nil {
		return providers.CanonicalProviderData{}, err
	}
	var value canonical.Object
	if err := json.Unmarshal(encoded, &value); err != nil {
		return providers.CanonicalProviderData{}, err
	}
	return providers.CanonicalProviderData{Schema: ProfileFactsSchemaV1, Value: value}, nil
}

func ValidateRequirementProfileV1(profile providers.RequirementProfile) error {
	if profile.Provider != blueprint.ComponentTypeAPT {
		return fmt.Errorf("APT profile provider must be %q", blueprint.ComponentTypeAPT)
	}
	request := providers.CanonicalProviderRequest{
		Schema:   profile.Declaration.ProviderData.Schema,
		Provider: blueprint.ComponentTypeAPT,
		Value:    profile.Declaration.ProviderData.Value,
	}
	if err := ValidateCanonicalProviderRequestV1(request); err != nil {
		return fmt.Errorf("APT profile request: %w", err)
	}
	if len(profile.Declaration.Executables) != 0 || len(profile.Declaration.Files) != 0 || len(profile.SelectedExecutables) != 0 || len(profile.SelectedFiles) != 0 {
		return fmt.Errorf("APT profile must not contain graph-supplied executable or file requirements")
	}
	base, _, err := DecodeProfileFactsV1(profile.Facts)
	if err != nil {
		return err
	}
	if profile.Platform != base.Platform {
		return fmt.Errorf("APT profile platform does not match its base evidence")
	}
	return nil
}

func ValidateResolvedBundlePayloadV1(payload providers.ResolvedBundleIdentityV1) error {
	if payload.RecipeVersion != RecipeVersion {
		return fmt.Errorf("APT bundle recipe version must be %q", RecipeVersion)
	}
	if payload.NodeID != "apt" || payload.Provider != blueprint.ComponentTypeAPT {
		return fmt.Errorf("APT bundle must identify the shared APT node")
	}
	if len(payload.SelectedSources) != 0 {
		return fmt.Errorf("APT bundle does not accept selected source inputs")
	}
	if err := ValidateCanonicalProviderRequestV1(payload.Request); err != nil {
		return fmt.Errorf("APT bundle request: %w", err)
	}
	bundle, err := DecodeCanonicalBundleDataV1(payload.ProviderPayload)
	if err != nil {
		return err
	}
	artifacts := make([]providerstore.ArtifactDescriptor, 0, len(bundle.BundlePackages)+2)
	for _, pkg := range bundle.BundlePackages {
		artifacts = append(artifacts, pkg.Artifact)
	}
	artifacts = append(artifacts, bundle.Script, bundle.StateManifest)
	sort.Slice(artifacts, func(left int, right int) bool { return artifacts[left].LogicalPath < artifacts[right].LogicalPath })
	actualArtifacts, err := canonical.Marshal(payload.Artifacts)
	if err != nil {
		return err
	}
	expectedArtifacts, err := canonical.Marshal(artifacts)
	if err != nil {
		return err
	}
	if !bytes.Equal(actualArtifacts, expectedArtifacts) {
		return fmt.Errorf("APT bundle artifacts do not match its package payload")
	}
	expectedOutputs, err := ResolvedOutputsV1(payload.Request, payload.NodeID, bundle)
	if err != nil {
		return err
	}
	actualOutputs, err := canonical.Marshal(payload.Outputs)
	if err != nil {
		return err
	}
	expectedOutputBytes, err := canonical.Marshal(expectedOutputs)
	if err != nil {
		return err
	}
	if !bytes.Equal(actualOutputs, expectedOutputBytes) {
		return fmt.Errorf("APT bundle outputs do not match its locked package declarations")
	}
	return nil
}

// ResolvedOutputsV1 binds request-derived executable candidates to package
// tuples present in the complete locked APT closure. Filesystem and dpkg owner
// evidence is collected only from the completed materialization layer.
func ResolvedOutputsV1(request providers.CanonicalProviderRequest, nodeID providers.NodeID, bundle BundleV1) ([]providers.ResolvedOutput, error) {
	if nodeID != "apt" {
		return nil, fmt.Errorf("APT resolved outputs require node apt")
	}
	if err := ValidateBundleV1(bundle); err != nil {
		return nil, err
	}
	declarations, err := OutputDeclarationsV1(request)
	if err != nil {
		return nil, err
	}
	closure := make(map[string]PackageTuple, len(bundle.BasePackages)+len(bundle.BundlePackages))
	for _, pkg := range bundle.BasePackages {
		closure[pkg.Tuple.Name] = pkg.Tuple
	}
	for _, pkg := range bundle.BundlePackages {
		closure[pkg.Tuple.Name] = pkg.Tuple
	}
	outputs := make([]providers.ResolvedOutput, 0, len(declarations))
	for _, declaration := range declarations {
		packageName, _ := declaration.Provenance.Value["package"].(string)
		version, _ := declaration.Provenance.Value["version"].(string)
		tuple, found := closure[packageName]
		if !found {
			return nil, fmt.Errorf("APT output %s.%s names package %q outside the locked closure", declaration.SupplierComponent, declaration.Name, packageName)
		}
		if version != "" && tuple.Version != version {
			return nil, fmt.Errorf("APT output %s.%s package %q version does not match the locked closure", declaration.SupplierComponent, declaration.Name, packageName)
		}
		outputs = append(outputs, providers.ResolvedOutput{
			SupplierComponent: declaration.SupplierComponent, SupplierNode: nodeID, Name: declaration.Name,
			Candidate: providers.ExecutableCandidate{InvocationPath: declaration.CandidatePath, Provenance: declaration.Provenance},
		})
	}
	return outputs, nil
}

func DecodeProfileFactsV1(data providers.CanonicalProviderData) (BaseProfileEvidenceV1, []providers.ValidatedExecutableInput, error) {
	if data.Schema != ProfileFactsSchemaV1 || len(data.Value) != 2 {
		return BaseProfileEvidenceV1{}, nil, fmt.Errorf("APT profile facts must use schema %q and the exact value shape", ProfileFactsSchemaV1)
	}
	encoded, err := canonical.Marshal(data.Value)
	if err != nil {
		return BaseProfileEvidenceV1{}, nil, err
	}
	var wire struct {
		Base        BaseProfileEvidenceV1                `json:"base"`
		Executables []providers.ValidatedExecutableInput `json:"executables"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return BaseProfileEvidenceV1{}, nil, fmt.Errorf("decode APT profile facts: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return BaseProfileEvidenceV1{}, nil, fmt.Errorf("decode APT profile facts trailer")
	}
	if err := ValidateBaseProfileEvidenceV1(wire.Base); err != nil {
		return BaseProfileEvidenceV1{}, nil, err
	}
	if err := validateBaseExecutableBindingsV1(wire.Base, wire.Executables); err != nil {
		return BaseProfileEvidenceV1{}, nil, err
	}
	normalized, err := CanonicalProfileFactsV1(wire.Base, wire.Executables)
	if err != nil {
		return BaseProfileEvidenceV1{}, nil, err
	}
	actual, _ := canonical.Marshal(data)
	expected, _ := canonical.Marshal(normalized)
	if !bytes.Equal(actual, expected) {
		return BaseProfileEvidenceV1{}, nil, fmt.Errorf("APT profile facts are not canonically normalized")
	}
	return wire.Base, append([]providers.ValidatedExecutableInput{}, wire.Executables...), nil
}

func validateBaseExecutableBindingsV1(base BaseProfileEvidenceV1, executables []providers.ValidatedExecutableInput) error {
	if executables == nil || len(executables) != len(base.Tools) {
		return fmt.Errorf("APT profile executable evidence must contain the exact base tool set")
	}
	tools := make(map[string]RequiredToolEvidenceV1, len(base.Tools))
	for _, tool := range base.Tools {
		tools[tool.Name] = tool
	}
	for index, input := range executables {
		if err := providers.ValidateValidatedExecutableInput(input); err != nil {
			return fmt.Errorf("APT profile executable %d: %w", index, err)
		}
		if index > 0 && executables[index-1].ID >= input.ID {
			return fmt.Errorf("APT profile executables must be unique and sorted")
		}
		tool, found := tools[input.ID]
		if !found || input.Evidence.InvocationPath != tool.Path || input.Policy != providers.ValidationPolicyCompatible {
			return fmt.Errorf("APT profile executable %q does not match its required base tool", input.ID)
		}
		expectedRole := providers.ExecutableRoleProviderPrerequisite
		expectedComponent := "apt"
		if tool.Name == "sh" {
			expectedRole, expectedComponent = providers.ExecutableRoleCarrier, "backend"
		} else if tool.Name == "env" {
			expectedRole, expectedComponent = providers.ExecutableRoleEnvironmentLauncher, "backend"
		}
		if input.Role != expectedRole || input.Evidence.Output != (providers.QualifiedOutput{Component: expectedComponent, Name: tool.Name}) {
			return fmt.Errorf("APT profile executable %q has the wrong recipe role", input.ID)
		}
		expectedFacts := providers.CanonicalProviderData{Schema: "apt-required-tool-v1", Value: canonical.Object{"interface": tool.Interface, "version": tool.Version}}
		actualFacts, _ := canonical.Marshal(input.Evidence.Facts)
		expectedFactsBytes, _ := canonical.Marshal(expectedFacts)
		if !bytes.Equal(actualFacts, expectedFactsBytes) {
			return fmt.Errorf("APT profile executable %q facts do not match its base tool evidence", input.ID)
		}
	}
	return nil
}
