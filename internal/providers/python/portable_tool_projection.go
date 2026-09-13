package python

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	providerapi "github.com/omry/reploy/internal/providers"
)

const PortableToolPythonProjectionSchemaV1 = "portable-tool-python-projection-v1"

// PortableToolPythonProjectionV1 is the provider-owned sidecar produced before
// node planning. Exact portable artifacts remain runtime eligibility
// constraints, not package resolver preferences or acquisition results.
type PortableToolPythonProjectionV1 struct {
	Schema     string                          `json:"schema"`
	Components []PortableToolPythonComponentV1 `json:"components"`
}

type PortableToolPythonComponentV1 struct {
	Component  string                        `json:"component"`
	TestedTags []string                      `json:"tested_tags"`
	Bindings   []PortableToolPythonBindingV1 `json:"bindings"`
}

type PortableToolPythonBindingV1 struct {
	Scope                 string                                    `json:"scope"`
	Component             string                                    `json:"component"`
	SelectedClosureDigest canonical.Digest                          `json:"selected_closure_digest"`
	Distribution          string                                    `json:"distribution"`
	Contract              providerapi.PortableToolRecordReferenceV1 `json:"contract"`
	Artifact              providerapi.PortableToolRecordReferenceV1 `json:"artifact"`
	Requirements          []string                                  `json:"requirements"`
	SupportedPython       []string                                  `json:"supported_python"`
	SupportedTags         []string                                  `json:"supported_tags"`
	CLI                   portabletool.ToolExportV1                 `json:"cli"`
	Wheel                 PortableToolExactWheelConstraintV1        `json:"wheel"`
}

// PortableToolExactWheelConstraintV1 carries identity and compatibility
// metadata only. It is not an index preference, path, or acquisition result.
type PortableToolExactWheelConstraintV1 struct {
	Platform         string           `json:"platform"`
	Filename         string           `json:"filename"`
	Distribution     string           `json:"distribution"`
	EcosystemVersion string           `json:"ecosystem_version"`
	Tags             []string         `json:"tags"`
	Size             string           `json:"size"`
	SHA256           canonical.Digest `json:"sha256"`
	RequiresPython   string           `json:"requires_python"`
}

// ProjectPortableToolPythonBindingsV1 projects selected application-scoped
// binding records into fresh canonical component requests and a deterministic
// provider sidecar. Inputs are validated but never normalized in place.
func ProjectPortableToolPythonBindingsV1(
	plan providerapi.PortableToolPlanV1,
	components []providerapi.ResolvedComponentRequestV1,
) ([]providerapi.ResolvedComponentRequestV1, PortableToolPythonProjectionV1, error) {
	if err := providerapi.ValidatePortableToolPlanV1(plan); err != nil {
		return nil, PortableToolPythonProjectionV1{}, err
	}
	if components == nil {
		return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python projection components must use an array")
	}
	result, err := cloneResolvedComponentsV1(components)
	if err != nil {
		return nil, PortableToolPythonProjectionV1{}, err
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Component < result[right].Component
	})
	byComponent := make(map[string]int, len(result))
	for index := range result {
		component := result[index]
		if index > 0 && result[index-1].Component >= component.Component {
			return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python projection components must be unique")
		}
		if err := blueprint.ValidateContributionReference("portable Python projection component", component.Component); err != nil {
			return nil, PortableToolPythonProjectionV1{}, err
		}
		if component.Request.Provider != component.Provider {
			return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python projection component %q request provider does not match", component.Component)
		}
		if component.Provider == blueprint.ComponentTypePython {
			if err := ValidateCanonicalProviderRequestForComponentV1(component.Component, component.Request); err != nil {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python projection component %q: %w", component.Component, err)
			}
		}
		byComponent[component.Component] = index
	}

	bindings := make(map[string]PortableToolPythonBindingV1)
	for _, tool := range plan.Tools {
		owner, err := portableToolApplicationOwnerV1(tool.Scope)
		if err != nil {
			if len(tool.Responsibilities.BindingContracts) == 0 && len(tool.Responsibilities.BindingArtifacts) == 0 {
				continue
			}
			return nil, PortableToolPythonProjectionV1{}, err
		}
		component := blueprint.ApplicationContributionID(owner, blueprint.ContributionProviderPython)
		contracts := make(map[string]portableToolPythonContractV1)
		for _, selected := range tool.Responsibilities.BindingContracts {
			contract, err := decodePortableToolRecordV1[portabletool.BindingContractV1](selected, portabletool.BindingContractSchemaV1)
			if err != nil {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding contract %q: %w", selected.Reference.ID, err)
			}
			if !portableToolExportSelectedV1(tool.Exports, contract.CLI) {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding contract %q CLI %q is not an exact selected export", contract.ID, contract.CLI.Name)
			}
			contracts[selected.Reference.ID] = portableToolPythonContractV1{
				reference: selected.Reference,
				record:    contract,
			}
		}
		artifactsByContract := make(map[string][]portableToolPythonArtifactV1)
		for _, selected := range tool.Responsibilities.BindingArtifacts {
			artifact, err := decodePortableToolRecordV1[portabletool.BindingArtifactRecordV1](selected, portabletool.BindingArtifactSchemaV1)
			if err != nil {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q: %w", selected.Reference.ID, err)
			}
			contract, found := contracts[artifact.Contract.ID]
			if !found || contract.reference != artifact.Contract {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q does not join an exact selected contract reference", artifact.ID)
			}
			artifactsByContract[artifact.Contract.ID] = append(
				artifactsByContract[artifact.Contract.ID],
				portableToolPythonArtifactV1{reference: selected.Reference, record: artifact},
			)
		}
		for _, id := range sortedMapKeysV1(contracts) {
			selectedContract := contracts[id]
			artifacts := artifactsByContract[id]
			if len(artifacts) != 1 {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding contract %q must have exactly one selected target artifact", id)
			}
			contract := selectedContract.record
			artifact := artifacts[0].record
			if artifact.Binding != contract.Name {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q binding does not match contract %q", artifact.ID, contract.ID)
			}
			if err := portabletool.ValidateBindingBundledComponentAgreementV1(contract.BundledComponents, artifact.BundledComponents); err != nil {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q: %w", artifact.ID, err)
			}
			distribution, err := PackageRootDistributionNameV1(contract.Package)
			if err != nil {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding contract %q package: %w", contract.ID, err)
			}
			artifactDistribution, err := PackageRootDistributionNameV1(artifact.Name)
			if err != nil {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q distribution: %w", artifact.ID, err)
			}
			if artifactDistribution != distribution {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q distribution %q does not match contract package %q", artifact.ID, artifactDistribution, distribution)
			}
			requirements := append([]string{}, contract.Requirements...)
			for _, requirement := range requirements {
				if _, err := PackageRootDistributionNameV1(requirement); err != nil {
					return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding contract %q requirement: %w", contract.ID, err)
				}
			}
			if err := validatePortableToolArtifactVersionAgainstRequirementsV1(
				distribution, artifact.EcosystemVersion, requirements,
			); err != nil {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q: %w", artifact.ID, err)
			}
			eligibleTags := sortedStringIntersectionV1(contract.SupportedTags, artifact.Tags)
			if len(eligibleTags) == 0 {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q has no contract-advertised wheel tag", artifact.ID)
			}
			for _, tag := range eligibleTags {
				if err := ValidatePortableToolWheelTagEnvelopeV1(tag, artifact.Platform); err != nil {
					return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding artifact %q supported tag %q: %w", artifact.ID, tag, err)
				}
			}
			supportedPython, err := providerapi.NormalizeSupportedPythonClaimsV1(contract.SupportedPython)
			if err != nil {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding contract %q supported Python: %w", contract.ID, err)
			}
			binding := PortableToolPythonBindingV1{
				Scope: tool.Scope, Component: component,
				SelectedClosureDigest: tool.SelectedClosureDigest,
				Distribution:          distribution,
				Contract:              selectedContract.reference,
				Artifact:              artifacts[0].reference,
				Requirements:          requirements,
				SupportedPython:       supportedPython,
				SupportedTags:         eligibleTags,
				CLI:                   contract.CLI,
				Wheel: PortableToolExactWheelConstraintV1{
					Platform: artifact.Platform, Filename: artifact.Filename,
					Distribution: artifactDistribution, EcosystemVersion: artifact.EcosystemVersion,
					Tags: append([]string{}, artifact.Tags...), Size: artifact.Size,
					SHA256: artifact.SHA256, RequiresPython: artifact.RequiresPython,
				},
			}
			key := component + "\x00" + distribution
			if previous, found := bindings[key]; found {
				equal, err := portableToolPythonBindingsEqualV1(previous, binding)
				if err != nil {
					return nil, PortableToolPythonProjectionV1{}, err
				}
				if !equal {
					return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding for component %q distribution %q conflicts across selected tools", component, distribution)
				}
				continue
			}
			bindings[key] = binding
		}
	}

	orderedBindings := make([]PortableToolPythonBindingV1, 0, len(bindings))
	for _, binding := range bindings {
		orderedBindings = append(orderedBindings, binding)
	}
	sort.Slice(orderedBindings, func(left, right int) bool {
		if orderedBindings[left].Component != orderedBindings[right].Component {
			return orderedBindings[left].Component < orderedBindings[right].Component
		}
		return orderedBindings[left].Distribution < orderedBindings[right].Distribution
	})
	projection := PortableToolPythonProjectionV1{
		Schema:     PortableToolPythonProjectionSchemaV1,
		Components: []PortableToolPythonComponentV1{},
	}
	for start := 0; start < len(orderedBindings); {
		end := start + 1
		for end < len(orderedBindings) && orderedBindings[end].Component == orderedBindings[start].Component {
			end++
		}
		component := orderedBindings[start].Component
		componentBindings := append([]PortableToolPythonBindingV1{}, orderedBindings[start:end]...)
		tagSet := map[string]struct{}{}
		for _, binding := range componentBindings {
			for _, tag := range binding.SupportedTags {
				tagSet[tag] = struct{}{}
			}
		}
		testedTags := sortedMapKeysV1(tagSet)
		projection.Components = append(projection.Components, PortableToolPythonComponentV1{
			Component: component, TestedTags: testedTags, Bindings: componentBindings,
		})
		index, found := byComponent[component]
		var request PythonProviderRequestV1
		if found {
			if result[index].Provider != blueprint.ComponentTypePython {
				return nil, PortableToolPythonProjectionV1{}, fmt.Errorf("portable Python binding component %q is owned by provider %q", component, result[index].Provider)
			}
			request, err = decodeCanonicalProviderRequestV1(result[index].Request)
			if err != nil {
				return nil, PortableToolPythonProjectionV1{}, err
			}
		} else {
			request = PythonProviderRequestV1{
				Component:    component,
				Interpreter:  blueprint.CommandRequirement{Command: "python"},
				Requirements: []providerapi.CanonicalPackageRequest{},
				Overrides:    []PythonPackageOverrideV1{},
			}
		}
		if err := mergePortableToolPythonRequirementsV1(&request, componentBindings); err != nil {
			return nil, PortableToolPythonProjectionV1{}, err
		}
		canonicalRequest, err := CanonicalProviderRequestV1(request)
		if err != nil {
			return nil, PortableToolPythonProjectionV1{}, err
		}
		resolved := providerapi.ResolvedComponentRequestV1{
			Component: component, Provider: blueprint.ComponentTypePython, Request: canonicalRequest,
		}
		if found {
			result[index] = resolved
		} else {
			result = append(result, resolved)
			byComponent[component] = len(result) - 1
		}
		start = end
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Component < result[right].Component
	})
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err != nil {
		return nil, PortableToolPythonProjectionV1{}, err
	}
	return result, projection, nil
}

// CanonicalPortableToolPythonProjectionBytesV1 validates a projection and
// returns its deterministic canonical-json-v1 representation.
func CanonicalPortableToolPythonProjectionBytesV1(projection PortableToolPythonProjectionV1) ([]byte, error) {
	if projection.Schema != PortableToolPythonProjectionSchemaV1 || projection.Components == nil {
		return nil, fmt.Errorf("portable Python projection must use schema %q and an explicit component array", PortableToolPythonProjectionSchemaV1)
	}
	for index, component := range projection.Components {
		if index > 0 && projection.Components[index-1].Component >= component.Component {
			return nil, fmt.Errorf("portable Python projection components must be unique and sorted")
		}
		if _, ok := blueprint.ApplicationContributionOwner(component.Component, blueprint.ContributionProviderPython); !ok {
			return nil, fmt.Errorf("portable Python projection component %q is not application-owned", component.Component)
		}
		if component.TestedTags == nil || component.Bindings == nil {
			return nil, fmt.Errorf("portable Python projection component %q collections must use arrays", component.Component)
		}
		if len(component.Bindings) == 0 {
			return nil, fmt.Errorf("portable Python projection component %q must contain at least one binding", component.Component)
		}
		if !sortedUniqueStringsV1(component.TestedTags) {
			return nil, fmt.Errorf("portable Python projection component %q tested tags must be unique and sorted", component.Component)
		}
		expectedTags := map[string]struct{}{}
		for bindingIndex, binding := range component.Bindings {
			if binding.Component != component.Component || binding.Requirements == nil || binding.SupportedPython == nil || binding.SupportedTags == nil || binding.Wheel.Tags == nil {
				return nil, fmt.Errorf("portable Python projection component %q binding %d is incomplete", component.Component, bindingIndex)
			}
			if bindingIndex > 0 && component.Bindings[bindingIndex-1].Distribution >= binding.Distribution {
				return nil, fmt.Errorf("portable Python projection bindings must be unique and sorted by distribution")
			}
			owner, err := portableToolApplicationOwnerV1(binding.Scope)
			if err != nil || blueprint.ApplicationContributionID(owner, blueprint.ContributionProviderPython) != component.Component {
				return nil, fmt.Errorf("portable Python binding %q scope does not own component %q", binding.Distribution, component.Component)
			}
			if NormalizeDistributionName(binding.Distribution) != binding.Distribution || binding.Wheel.Distribution != binding.Distribution {
				return nil, fmt.Errorf("portable Python binding distribution %q is not canonical", binding.Distribution)
			}
			if binding.SelectedClosureDigest.Validate() != nil || portabletool.ValidateBindingRecordReferencesV1(binding.Contract, binding.Artifact) != nil {
				return nil, fmt.Errorf("portable Python binding %q exact record references are invalid", binding.Distribution)
			}
			if len(binding.Requirements) == 0 || len(binding.SupportedPython) == 0 || len(binding.SupportedTags) == 0 || len(binding.Wheel.Tags) == 0 {
				return nil, fmt.Errorf("portable Python binding %q collections must not be empty", binding.Distribution)
			}
			if !sortedUniqueStringsV1(binding.Requirements) || !sortedUniqueStringsV1(binding.SupportedTags) || !sortedUniqueStringsV1(binding.Wheel.Tags) {
				return nil, fmt.Errorf("portable Python binding %q string collections must be unique and sorted", binding.Distribution)
			}
			ownsDistributionRoot := false
			for _, requirement := range binding.Requirements {
				distribution, err := PackageRootDistributionNameV1(requirement)
				if err != nil {
					return nil, fmt.Errorf("portable Python binding %q requirement: %w", binding.Distribution, err)
				}
				ownsDistributionRoot = ownsDistributionRoot || distribution == binding.Distribution
			}
			if !ownsDistributionRoot {
				return nil, fmt.Errorf("portable Python binding %q has no matching package root", binding.Distribution)
			}
			if err := validatePortableToolArtifactVersionAgainstRequirementsV1(
				binding.Distribution, binding.Wheel.EcosystemVersion, binding.Requirements,
			); err != nil {
				return nil, fmt.Errorf("portable Python binding %q: %w", binding.Distribution, err)
			}
			normalizedClaims, err := providerapi.NormalizeSupportedPythonClaimsV1(binding.SupportedPython)
			if err != nil || !stringSlicesEqualV1(normalizedClaims, binding.SupportedPython) {
				return nil, fmt.Errorf("portable Python binding %q supported Python claims are not canonical", binding.Distribution)
			}
			if _, err := blueprint.ParsePlatform(binding.Wheel.Platform); err != nil {
				return nil, fmt.Errorf("portable Python binding %q wheel platform: %w", binding.Distribution, err)
			}
			if binding.Wheel.Filename == "" || path.Base(binding.Wheel.Filename) != binding.Wheel.Filename || binding.Wheel.EcosystemVersion == "" || binding.Wheel.RequiresPython == "" || binding.CLI.Name == "" || !path.IsAbs(binding.CLI.Path) || path.Clean(binding.CLI.Path) != binding.CLI.Path {
				return nil, fmt.Errorf("portable Python binding %q exact wheel or CLI fields are invalid", binding.Distribution)
			}
			filename, err := portabletool.ProjectWheelFilenameV1(binding.Wheel.Filename)
			if err != nil {
				return nil, fmt.Errorf("portable Python binding %q exact wheel filename: %w", binding.Distribution, err)
			}
			if filename.Distribution != binding.Wheel.Distribution || filename.EcosystemVersion != binding.Wheel.EcosystemVersion || !stringSlicesEqualV1(filename.Tags, binding.Wheel.Tags) {
				return nil, fmt.Errorf("portable Python binding %q exact wheel filename does not match its distribution, ecosystem version, and tags", binding.Distribution)
			}
			if _, err := strconv.ParseUint(binding.Wheel.Size, 10, 63); err != nil || binding.Wheel.Size == "0" || len(binding.Wheel.Size) > 1 && binding.Wheel.Size[0] == '0' || binding.Wheel.SHA256.Validate() != nil {
				return nil, fmt.Errorf("portable Python binding %q exact wheel size or digest is invalid", binding.Distribution)
			}
			for _, claim := range binding.SupportedPython {
				covered, err := PythonRequiresPythonCoversClaimV1(binding.Wheel.RequiresPython, claim)
				if err != nil {
					return nil, fmt.Errorf("portable Python binding %q requires_python %q for supported Python claim %q: %w", binding.Distribution, binding.Wheel.RequiresPython, claim, err)
				}
				if !covered {
					return nil, fmt.Errorf("portable Python binding %q requires_python %q does not cover supported Python claim %q", binding.Distribution, binding.Wheel.RequiresPython, claim)
				}
			}
			for _, tag := range binding.SupportedTags {
				if !containsSortedStringV1(binding.Wheel.Tags, tag) {
					return nil, fmt.Errorf("portable Python binding %q supported tag %q is outside its exact wheel", binding.Distribution, tag)
				}
				if err := ValidatePortableToolWheelTagEnvelopeV1(tag, binding.Wheel.Platform); err != nil {
					return nil, fmt.Errorf("portable Python binding %q supported tag %q: %w", binding.Distribution, tag, err)
				}
				expectedTags[tag] = struct{}{}
			}
		}
		if !stringSlicesEqualV1(component.TestedTags, sortedMapKeysV1(expectedTags)) {
			return nil, fmt.Errorf("portable Python projection component %q tested tags do not equal its binding tag union", component.Component)
		}
	}
	encoded, err := canonical.Marshal(projection)
	if err != nil {
		return nil, fmt.Errorf("portable Python projection canonical form: %w", err)
	}
	return encoded, nil
}

// ValidatePortableToolWheelTagEnvelopeV1 validates the bounded static tag
// forms admitted before interpreter inspection. Runtime membership remains
// owned by the selected interpreter's pip implementation.
func ValidatePortableToolWheelTagEnvelopeV1(tag, targetPlatform string) error {
	parts := strings.Split(tag, "-")
	if len(parts) != 3 || !portableToolProjectionTagComponentV1(parts[0]) || !portableToolProjectionTagComponentV1(parts[1]) || !portableToolProjectionTagComponentV1(parts[2]) {
		return fmt.Errorf("wheel tag %q is malformed", tag)
	}
	if err := validatePortableToolPythonTagV1(parts[0]); err != nil {
		return err
	}
	if err := validatePortableToolABITagV1(parts[0], parts[1]); err != nil {
		return err
	}
	projection, err := portabletool.ProjectWheelPlatformForTargetV1(parts[2], targetPlatform)
	if err != nil {
		return err
	}
	if projection.Kind == portabletool.WheelPlatformAnyV1 && parts[1] != "none" {
		return fmt.Errorf("wheel tag %q uses an ABI-bearing any platform", tag)
	}
	return nil
}

type portableToolPythonContractV1 struct {
	reference providerapi.PortableToolRecordReferenceV1
	record    portabletool.BindingContractV1
}

type portableToolPythonArtifactV1 struct {
	reference providerapi.PortableToolRecordReferenceV1
	record    portabletool.BindingArtifactRecordV1
}

func portableToolApplicationOwnerV1(scope string) (string, error) {
	const prefix = "application:"
	if !strings.HasPrefix(scope, prefix) {
		return "", fmt.Errorf("portable Python bindings require application:<owner> scope, got %q", scope)
	}
	owner := strings.TrimPrefix(scope, prefix)
	component := blueprint.ApplicationContributionID(owner, blueprint.ContributionProviderPython)
	if _, ok := blueprint.ApplicationContributionOwner(component, blueprint.ContributionProviderPython); !ok {
		return "", fmt.Errorf("portable Python binding scope %q has an invalid application owner", scope)
	}
	return owner, nil
}

func decodePortableToolRecordV1[T any](selected providerapi.PortableToolSelectedRecordV1, schema string) (T, error) {
	var result T
	if selected.Record.Schema != schema {
		return result, fmt.Errorf("record schema must be %q", schema)
	}
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope(selected.Record)); err != nil {
		return result, err
	}
	encoded, err := canonical.Marshal(selected.Record.Value)
	if err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func cloneResolvedComponentsV1(components []providerapi.ResolvedComponentRequestV1) ([]providerapi.ResolvedComponentRequestV1, error) {
	result := make([]providerapi.ResolvedComponentRequestV1, len(components))
	for index, component := range components {
		encoded, err := canonical.Marshal(component)
		if err != nil {
			return nil, fmt.Errorf("clone portable Python projection component %q: %w", component.Component, err)
		}
		if err := json.Unmarshal(encoded, &result[index]); err != nil {
			return nil, fmt.Errorf("clone portable Python projection component %q: %w", component.Component, err)
		}
	}
	return result, nil
}

func portableToolExportSelectedV1(exports []providerapi.PortableToolExportV1, cli portabletool.ToolExportV1) bool {
	for _, exported := range exports {
		if exported.Name == cli.Name && exported.Path == cli.Path {
			return true
		}
	}
	return false
}

func sortedStringIntersectionV1(left, right []string) []string {
	result := []string{}
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		switch strings.Compare(left[leftIndex], right[rightIndex]) {
		case -1:
			leftIndex++
		case 1:
			rightIndex++
		default:
			result = append(result, left[leftIndex])
			leftIndex++
			rightIndex++
		}
	}
	return result
}

func mergePortableToolPythonRequirementsV1(request *PythonProviderRequestV1, bindings []PortableToolPythonBindingV1) error {
	requirementsByDistribution := map[string][]string{}
	rootRequirementsByDistribution := map[string][]string{}
	for _, requirement := range request.Requirements {
		value, ok := requirement.Value["requirement"].(string)
		if !ok {
			return fmt.Errorf("Python package request requirement must be a string")
		}
		distribution, err := RequirementDistributionName(value)
		if err != nil {
			return err
		}
		requirementsByDistribution[distribution] = append(requirementsByDistribution[distribution], value)
		if rootDistribution, err := PackageRootDistributionNameV1(value); err == nil && rootDistribution == distribution {
			rootRequirementsByDistribution[distribution] = append(rootRequirementsByDistribution[distribution], value)
		}
	}
	for _, binding := range bindings {
		for _, value := range binding.Requirements {
			distribution, err := PackageRootDistributionNameV1(value)
			if err != nil {
				return err
			}
			requirementsByDistribution[distribution] = append(requirementsByDistribution[distribution], value)
			rootRequirementsByDistribution[distribution] = append(rootRequirementsByDistribution[distribution], value)
		}
	}
	all := []providerapi.CanonicalPackageRequest{}
	for _, distribution := range sortedMapKeysV1(requirementsByDistribution) {
		values := requirementsByDistribution[distribution]
		unique := map[string]struct{}{}
		for _, value := range values {
			unique[value] = struct{}{}
		}
		values = sortedMapKeysV1(unique)
		compatible, err := PackageRootRequirementsCompatibleV1(rootRequirementsByDistribution[distribution])
		if err != nil {
			return fmt.Errorf("portable Python requirements for %q: %w", distribution, err)
		}
		if !compatible {
			return fmt.Errorf("portable Python requirements for %q conflict", distribution)
		}
		for _, value := range values {
			canonicalRequest, err := CanonicalPackageRequestV1(value)
			if err != nil {
				return err
			}
			all = append(all, canonicalRequest)
		}
	}
	request.Requirements = all
	return nil
}

func validatePortableToolArtifactVersionAgainstRequirementsV1(
	distribution string,
	ecosystemVersion string,
	requirements []string,
) error {
	version, err := pep440.Parse(ecosystemVersion)
	if err != nil {
		return fmt.Errorf("artifact ecosystem version %q is invalid PEP 440", ecosystemVersion)
	}
	found := false
	for _, requirement := range requirements {
		requirementDistribution, err := PackageRootDistributionNameV1(requirement)
		if err != nil {
			return err
		}
		if requirementDistribution != distribution {
			continue
		}
		found = true
		name := requirementNamePattern.FindString(requirement)
		specifiers := strings.TrimPrefix(requirement, name)
		if specifiers == "" {
			continue
		}
		parsed, err := pep440.NewSpecifiers(specifiers)
		if err != nil {
			return fmt.Errorf("contract requirement %q is invalid PEP 440", requirement)
		}
		if !parsed.Check(version) {
			return fmt.Errorf("artifact version %q does not satisfy contract requirement %q", ecosystemVersion, requirement)
		}
	}
	if found {
		return nil
	}
	return fmt.Errorf("contract has no package root for distribution %q", distribution)
}

func portableToolPythonBindingsEqualV1(left, right PortableToolPythonBindingV1) (bool, error) {
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

func validatePortableToolPythonTagV1(value string) error {
	prefix := ""
	if strings.HasPrefix(value, "py") {
		prefix = "py"
	} else if strings.HasPrefix(value, "cp") {
		prefix = "cp"
	} else {
		return fmt.Errorf("wheel Python tag %q is unsupported", value)
	}
	digits := strings.TrimPrefix(value, prefix)
	if digits == "" || strings.TrimLeft(digits, "0123456789") != "" || len(digits) > 1 && digits[0] == '0' || prefix == "cp" && len(digits) < 2 {
		return fmt.Errorf("wheel Python tag %q is unsupported", value)
	}
	return nil
}

func validatePortableToolABITagV1(pythonTag, value string) error {
	if value == "none" {
		return nil
	}
	if value == "abi3" {
		if !strings.HasPrefix(pythonTag, "cp") {
			return fmt.Errorf("wheel ABI tag %q requires a CPython Python tag", value)
		}
		digits := strings.TrimPrefix(pythonTag, "cp")
		if len(digits) < 2 || strings.TrimLeft(digits, "0123456789") != "" {
			return fmt.Errorf("wheel ABI tag %q has an invalid CPython release", value)
		}
		major, _ := strconv.Atoi(digits[:1])
		minor, _ := strconv.Atoi(digits[1:])
		if major < 3 || major == 3 && minor < 2 {
			return fmt.Errorf("wheel ABI tag %q is below the abi3 floor", value)
		}
		return nil
	}
	if !strings.HasPrefix(value, "cp") {
		return fmt.Errorf("wheel ABI tag %q is unsupported", value)
	}
	digits := strings.TrimPrefix(value, "cp")
	index := 0
	for index < len(digits) && digits[index] >= '0' && digits[index] <= '9' {
		index++
	}
	if index < 2 {
		return fmt.Errorf("wheel ABI tag %q is unsupported", value)
	}
	for _, character := range digits[index:] {
		if character < 'a' || character > 'z' {
			return fmt.Errorf("wheel ABI tag %q is unsupported", value)
		}
	}
	return nil
}

func portableToolProjectionTagComponentV1(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func sortedUniqueStringsV1(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			return false
		}
	}
	return true
}

func containsSortedStringV1(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func stringSlicesEqualV1(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sortedMapKeysV1[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
