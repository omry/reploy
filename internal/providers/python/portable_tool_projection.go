package python

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
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
	normalizedPlan, err := cloneAndNormalizePortableToolPlanV1(plan)
	if err != nil {
		return nil, PortableToolPythonProjectionV1{}, err
	}
	if err := providerapi.ValidatePortableToolPlanV1(normalizedPlan); err != nil {
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
	for _, tool := range normalizedPlan.Tools {
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
		if len(component.TestedTags) > portabletool.RecordArrayMaxEntriesV1 {
			return nil, fmt.Errorf("portable Python projection component %q tested tags exceed the portable-tool record limit", component.Component)
		}
		expectedTags := map[string]struct{}{}
		componentRequirementsByDistribution := map[string][]string{}
		var commonSupportedPython []string
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
			if len(binding.Requirements) > portabletool.RecordArrayMaxEntriesV1 || len(binding.SupportedPython) > portabletool.RecordArrayMaxEntriesV1 || len(binding.SupportedTags) > portabletool.RecordArrayMaxEntriesV1 || len(binding.Wheel.Tags) > portabletool.RecordArrayMaxEntriesV1 {
				return nil, fmt.Errorf("portable Python binding %q collections exceed the portable-tool record limit", binding.Distribution)
			}
			if !sortedUniqueStringsV1(binding.Requirements) || !sortedUniqueStringsV1(binding.SupportedTags) || !sortedUniqueStringsV1(binding.Wheel.Tags) {
				return nil, fmt.Errorf("portable Python binding %q string collections must be unique and sorted", binding.Distribution)
			}
			if err := portabletool.ValidatePythonPackageRootRequirementsV1(binding.Requirements); err != nil {
				return nil, fmt.Errorf("portable Python binding %q requirements: %w", binding.Distribution, err)
			}
			ownsDistributionRoot := false
			for _, requirement := range binding.Requirements {
				distribution, err := PackageRootDistributionNameV1(requirement)
				if err != nil {
					return nil, fmt.Errorf("portable Python binding %q requirement: %w", binding.Distribution, err)
				}
				componentRequirementsByDistribution[distribution] = append(componentRequirementsByDistribution[distribution], requirement)
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
			if commonSupportedPython == nil {
				commonSupportedPython = append([]string{}, normalizedClaims...)
			} else {
				commonSupportedPython, err = providerapi.IntersectSupportedPythonClaimsV1(commonSupportedPython, normalizedClaims)
				if err != nil {
					return nil, fmt.Errorf("portable Python projection component %q supported Python claims: %w", component.Component, err)
				}
				if len(commonSupportedPython) == 0 {
					return nil, fmt.Errorf("portable Python projection component %q bindings have no common supported Python claim", component.Component)
				}
			}
			if _, err := blueprint.ParsePlatform(binding.Wheel.Platform); err != nil {
				return nil, fmt.Errorf("portable Python binding %q wheel platform: %w", binding.Distribution, err)
			}
			if err := portabletool.ValidateBindingArtifactReferencePlatformV1(binding.Artifact, binding.Wheel.Platform); err != nil {
				return nil, fmt.Errorf("portable Python binding %q: %w", binding.Distribution, err)
			}
			if binding.Wheel.Filename == "" || path.Base(binding.Wheel.Filename) != binding.Wheel.Filename || binding.Wheel.EcosystemVersion == "" {
				return nil, fmt.Errorf("portable Python binding %q exact wheel fields are invalid", binding.Distribution)
			}
			if err := portabletool.ValidateBindingCLIExportV1(binding.CLI); err != nil {
				return nil, fmt.Errorf("portable Python binding %q CLI: %w", binding.Distribution, err)
			}
			if err := portabletool.ValidatePythonRequiresPythonV1(binding.Wheel.RequiresPython); err != nil {
				return nil, fmt.Errorf("portable Python binding %q: %w", binding.Distribution, err)
			}
			filename, err := portabletool.ProjectWheelFilenameV1(binding.Wheel.Filename)
			if err != nil {
				return nil, fmt.Errorf("portable Python binding %q exact wheel filename: %w", binding.Distribution, err)
			}
			if filename.Distribution != binding.Wheel.Distribution || filename.EcosystemVersion != binding.Wheel.EcosystemVersion || !stringSlicesEqualV1(filename.Tags, binding.Wheel.Tags) {
				return nil, fmt.Errorf("portable Python binding %q exact wheel filename does not match its distribution, ecosystem version, and tags", binding.Distribution)
			}
			if err := portabletool.ValidateBindingWheelTagsForPlatformV1(binding.Wheel.Tags, binding.Wheel.Platform); err != nil {
				return nil, fmt.Errorf("portable Python binding %q: %w", binding.Distribution, err)
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
		exactWheelDistributions := map[string]struct{}{}
		for _, binding := range component.Bindings {
			if err := validatePortableToolArtifactVersionAgainstRequirementsV1(
				binding.Distribution, binding.Wheel.EcosystemVersion,
				componentRequirementsByDistribution[binding.Distribution],
			); err != nil {
				return nil, fmt.Errorf("portable Python component %q exact wheel for distribution %q: %w", component.Component, binding.Distribution, err)
			}
			exactWheelDistributions[binding.Distribution] = struct{}{}
		}
		for _, distribution := range sortedMapKeysV1(componentRequirementsByDistribution) {
			if _, found := exactWheelDistributions[distribution]; found {
				continue
			}
			compatible, err := PackageRootRequirementsCompatibleV1(componentRequirementsByDistribution[distribution])
			if err != nil {
				return nil, fmt.Errorf("portable Python projection requirements for %q: %w", distribution, err)
			}
			if !compatible {
				return nil, fmt.Errorf("portable Python projection requirements for %q conflict", distribution)
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

func cloneAndNormalizePortableToolPlanV1(plan providerapi.PortableToolPlanV1) (providerapi.PortableToolPlanV1, error) {
	encoded, err := canonical.Marshal(plan)
	if err != nil {
		return providerapi.PortableToolPlanV1{}, fmt.Errorf("clone portable Python projection plan: %w", err)
	}
	var result providerapi.PortableToolPlanV1
	if err := json.Unmarshal(encoded, &result); err != nil {
		return providerapi.PortableToolPlanV1{}, fmt.Errorf("clone portable Python projection plan: %w", err)
	}
	sort.Slice(result.Tools, func(left, right int) bool {
		if result.Tools[left].Scope != result.Tools[right].Scope {
			return result.Tools[left].Scope < result.Tools[right].Scope
		}
		return result.Tools[left].Provenance.Tool < result.Tools[right].Provenance.Tool
	})
	for index := range result.Tools {
		responsibilities := &result.Tools[index].Responsibilities
		sort.Slice(responsibilities.BindingContracts, func(left, right int) bool {
			return portableToolRecordReferenceLessV1(responsibilities.BindingContracts[left].Reference, responsibilities.BindingContracts[right].Reference)
		})
		sort.Slice(responsibilities.BindingArtifacts, func(left, right int) bool {
			return portableToolRecordReferenceLessV1(responsibilities.BindingArtifacts[left].Reference, responsibilities.BindingArtifacts[right].Reference)
		})
	}
	return result, nil
}

func portableToolRecordReferenceLessV1(left, right providerapi.PortableToolRecordReferenceV1) bool {
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return string(left.Digest) < string(right.Digest)
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
	bindingsByDistribution := make(map[string]PortableToolPythonBindingV1, len(bindings))
	for _, binding := range bindings {
		bindingsByDistribution[binding.Distribution] = binding
	}
	for _, override := range request.Overrides {
		binding, found := bindingsByDistribution[override.Distribution]
		if !found {
			continue
		}
		switch override.Kind {
		case "local":
			return fmt.Errorf("local Python package override for portable distribution %q conflicts with its selected exact wheel", override.Distribution)
		case "version":
			comparison, err := ComparePackageVersionsV1(override.Version, binding.Wheel.EcosystemVersion)
			if err != nil {
				return fmt.Errorf("version Python package override for portable distribution %q: %w", override.Distribution, err)
			}
			if comparison != 0 {
				return fmt.Errorf("version Python package override for portable distribution %q selects %q, conflicting with exact wheel version %q", override.Distribution, override.Version, binding.Wheel.EcosystemVersion)
			}
		}
	}
	requirementsByDistribution := map[string][]string{}
	rootRequirementsByDistribution := map[string][]string{}
	bindingRootRequirementsByDistribution := map[string][]string{}
	for _, requirement := range request.Requirements {
		value, ok := requirement.Value["requirement"].(string)
		if !ok {
			return fmt.Errorf("Python package request requirement must be a string")
		}
		distribution, sourceKind, sourceRoot, err := portableToolPythonRequirementIdentityAndSourceRootV1(value)
		if err != nil {
			return err
		}
		if sourceKind != "" && distribution == "" {
			return fmt.Errorf("unverifiable %s Python package requirement cannot be proven distinct from selected portable wheels", sourceKind)
		}
		binding, bound := bindingsByDistribution[distribution]
		if bound && sourceKind != "" {
			return fmt.Errorf("%s Python package requirement for portable distribution %q conflicts with its selected exact wheel", sourceKind, distribution)
		}
		if bound {
			compatible, checked := requirementAllowsVersion(value, binding.Wheel.EcosystemVersion)
			if !checked {
				var err error
				compatible, err = portableToolPythonRequirementAllowsExactVersionV1(value, binding.Wheel.EcosystemVersion)
				if err != nil {
					return fmt.Errorf("Python package requirement for portable distribution %q cannot be checked against its exact wheel", distribution)
				}
			}
			if !compatible {
				return fmt.Errorf("portable Python requirements for %q conflict", distribution)
			}
		}
		requirementsByDistribution[distribution] = append(requirementsByDistribution[distribution], value)
		if sourceKind != "" {
			rootRequirementsByDistribution[distribution] = append(rootRequirementsByDistribution[distribution], sourceRoot)
		} else {
			rootRequirement, err := portableToolPythonCompatibilityRootV1(value)
			if err != nil {
				return fmt.Errorf("Python package requirement for %q cannot be checked against portable binding requirements", distribution)
			}
			rootRequirementsByDistribution[distribution] = append(rootRequirementsByDistribution[distribution], rootRequirement)
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
			bindingRootRequirementsByDistribution[distribution] = append(bindingRootRequirementsByDistribution[distribution], value)
		}
	}
	for _, override := range request.Overrides {
		if override.Kind != "version" || len(bindingRootRequirementsByDistribution[override.Distribution]) == 0 {
			continue
		}
		requirements := append([]string{}, bindingRootRequirementsByDistribution[override.Distribution]...)
		requirements = append(requirements, override.Distribution+"=="+override.Version)
		compatible, err := PackageRootRequirementsCompatibleV1(requirements)
		if err != nil {
			return fmt.Errorf("version Python package override for portable dependency %q: %w", override.Distribution, err)
		}
		if !compatible {
			return fmt.Errorf("version Python package override for portable dependency %q conflicts with binding requirements", override.Distribution)
		}
	}
	for _, binding := range bindings {
		if err := validatePortableToolArtifactVersionAgainstRequirementsV1(
			binding.Distribution,
			binding.Wheel.EcosystemVersion,
			rootRequirementsByDistribution[binding.Distribution],
		); err != nil {
			return fmt.Errorf("portable Python exact wheel for distribution %q: %w", binding.Distribution, err)
		}
	}
	for _, distribution := range sortedMapKeysV1(bindingRootRequirementsByDistribution) {
		if _, found := bindingsByDistribution[distribution]; found {
			continue
		}
		compatible, err := PackageRootRequirementsCompatibleV1(rootRequirementsByDistribution[distribution])
		if err != nil {
			return fmt.Errorf("portable Python requirements for %q: %w", distribution, err)
		}
		if !compatible {
			return fmt.Errorf("portable Python requirements for %q conflict", distribution)
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

func portableToolPythonRequirementIdentityV1(requirement string) (string, string, error) {
	distribution, sourceKind, _, err := portableToolPythonRequirementIdentityAndSourceRootV1(requirement)
	return distribution, sourceKind, err
}

func portableToolPythonRequirementIdentityAndSourceRootV1(requirement string) (string, string, string, error) {
	value := strings.TrimSpace(requirement)
	body := portableToolPythonRequirementBodyV1(value)
	name := requirementNamePattern.FindString(body)
	if name != "" {
		remainder := strings.TrimSpace(strings.TrimPrefix(body, name))
		if strings.HasPrefix(remainder, "[") {
			end := strings.IndexByte(remainder, ']')
			if end >= 0 {
				remainder = strings.TrimSpace(remainder[end+1:])
			}
		}
		if strings.HasPrefix(remainder, "@") {
			location := strings.TrimSpace(strings.TrimPrefix(remainder, "@"))
			if marker := strings.Index(location, " ;"); marker >= 0 {
				location = strings.TrimSpace(location[:marker])
			}
			distribution, sourceRoot, err := portableToolPythonSourceLocationIdentityV1(location)
			if err != nil {
				return "", "direct URL", "", err
			}
			declared := NormalizeDistributionName(name)
			if distribution == "" {
				return declared, "direct URL", "", nil
			}
			if declared != distribution {
				return "", "direct URL", "", fmt.Errorf("Python package source requirement declared distribution %q does not match wheel distribution %q", declared, distribution)
			}
			return distribution, "direct URL", sourceRoot, nil
		}
		if portableToolPythonOrdinaryRequirementRemainderV1(remainder) && !portableToolPythonBareSourceFilenameV1(body) {
			return NormalizeDistributionName(name), "", "", nil
		}
	}
	parsed, err := url.Parse(body)
	if err == nil && portableToolPythonDirectURLSchemeV1(parsed.Scheme) {
		distribution, sourceRoot, err := portableToolPythonSourceLocationIdentityV1(body)
		return distribution, "direct URL", sourceRoot, err
	}
	if portableToolPythonLocalPathV1(body) {
		distribution, sourceRoot, err := portableToolPythonSourceLocationIdentityV1(body)
		return distribution, "local path", sourceRoot, err
	}
	if err != nil {
		return "", "", "", fmt.Errorf("Python package requirement has invalid syntax")
	}
	distribution, nameErr := RequirementDistributionName(body)
	if nameErr != nil {
		return "", "", "", fmt.Errorf("Python package requirement has invalid syntax")
	}
	return distribution, "", "", nil
}

// portableToolPythonRequirementBodyV1 removes a PEP 508 environment marker
// before source classification. A named direct reference requires whitespace
// before its marker delimiter, so semicolons within its URL remain part of the
// source location. Ordinary requirements use their first semicolon delimiter.
func portableToolPythonRequirementBodyV1(value string) string {
	firstMarker := strings.IndexByte(value, ';')
	if firstMarker < 0 {
		return value
	}
	if colon := strings.IndexByte(value[:firstMarker], ':'); colon > 0 {
		scheme := value[:colon]
		if portableToolPythonValidURLSchemeV1(scheme) && portableToolPythonDirectURLSchemeV1(scheme) {
			return portableToolPythonDirectReferenceBodyV1(value, firstMarker)
		}
	}
	name := requirementNamePattern.FindString(value)
	if name != "" {
		remainder := strings.TrimSpace(strings.TrimPrefix(value, name))
		if strings.HasPrefix(remainder, "[") {
			if end := strings.IndexByte(remainder, ']'); end >= 0 {
				remainder = strings.TrimSpace(remainder[end+1:])
			}
		}
		if strings.HasPrefix(remainder, "@") {
			return portableToolPythonDirectReferenceBodyV1(value, firstMarker)
		}
	}
	return strings.TrimSpace(value[:firstMarker])
}

func portableToolPythonDirectReferenceBodyV1(value string, firstSemicolon int) string {
	for index := firstSemicolon; index < len(value); index++ {
		if value[index] == ';' && index > 0 && (value[index-1] == ' ' || value[index-1] == '\t') {
			return strings.TrimSpace(value[:index])
		}
	}
	return value
}

func portableToolPythonRequirementAllowsExactVersionV1(requirement, ecosystemVersion string) (bool, error) {
	value := strings.TrimSpace(requirement)
	name := requirementNamePattern.FindString(value)
	if name == "" {
		return false, fmt.Errorf("Python package requirement has invalid syntax")
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(value, name))
	if strings.HasPrefix(remainder, "[") {
		end := strings.IndexByte(remainder, ']')
		if end < 0 {
			return false, fmt.Errorf("Python package requirement has invalid syntax")
		}
		remainder = strings.TrimSpace(remainder[end+1:])
	}
	if marker := strings.IndexByte(remainder, ';'); marker >= 0 {
		remainder = strings.TrimSpace(remainder[:marker])
	}
	if strings.HasPrefix(remainder, "(") && strings.HasSuffix(remainder, ")") {
		remainder = strings.TrimSpace(remainder[1 : len(remainder)-1])
	}
	if remainder == "" {
		return true, nil
	}
	if strings.HasPrefix(remainder, "@") {
		return false, fmt.Errorf("Python package source requirement cannot be checked as a version constraint")
	}
	version, err := pep440.Parse(ecosystemVersion)
	if err != nil {
		return false, fmt.Errorf("exact wheel ecosystem version is invalid PEP 440")
	}
	specifiers, err := pep440.NewSpecifiers(remainder)
	if err != nil {
		return false, fmt.Errorf("Python package requirement has invalid PEP 440 specifiers")
	}
	return specifiers.Check(version), nil
}

// portableToolPythonCompatibilityRootV1 reduces an ordinary PEP 508
// requirement to the package-root form used by the bounded compatibility
// checker. Extras and environment markers do not change the distribution or
// version constraint; the original requirement remains in provider output.
func portableToolPythonCompatibilityRootV1(requirement string) (string, error) {
	body := portableToolPythonRequirementBodyV1(strings.TrimSpace(requirement))
	name := requirementNamePattern.FindString(body)
	if name == "" {
		return "", fmt.Errorf("Python package requirement has invalid syntax")
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(body, name))
	if strings.HasPrefix(remainder, "[") {
		end := strings.IndexByte(remainder, ']')
		if end < 0 {
			return "", fmt.Errorf("Python package requirement has invalid syntax")
		}
		remainder = strings.TrimSpace(remainder[end+1:])
	}
	if strings.HasPrefix(remainder, "(") && strings.HasSuffix(remainder, ")") {
		remainder = strings.TrimSpace(remainder[1 : len(remainder)-1])
	}
	root := NormalizeDistributionName(name)
	if remainder == "" {
		return root, nil
	}
	if strings.HasPrefix(remainder, "@") {
		return "", fmt.Errorf("Python package source requirement is not an ordinary package root")
	}
	specifiers, err := pep440.NewSpecifiers(remainder)
	if err != nil {
		return "", fmt.Errorf("Python package requirement has invalid PEP 440 specifiers")
	}
	return root + specifiers.String(), nil
}

func portableToolPythonSourceLocationIdentityV1(location string) (string, string, error) {
	parsed, err := url.Parse(location)
	if err != nil {
		return "", "", fmt.Errorf("Python package source requirement has an invalid location")
	}
	filename := path.Base(strings.ReplaceAll(parsed.Path, `\`, "/"))
	if strings.HasSuffix(strings.ToLower(filename), ".whl") {
		wheelRequirement, ok := WheelFilenameRequirement(filename)
		if !ok {
			return "", "", fmt.Errorf("Python package source requirement has an invalid wheel filename")
		}
		distribution, err := RequirementDistributionName(wheelRequirement)
		return distribution, wheelRequirement, err
	}
	eggs, err := portableToolPythonFragmentValuesV1(parsed.Fragment, "egg")
	if err != nil {
		return "", "", fmt.Errorf("Python package source requirement has an invalid location")
	}
	if len(eggs) == 1 {
		egg := eggs[0]
		if portableToolPythonValidDistributionIdentifierV1(egg) {
			return NormalizeDistributionName(egg), "", nil
		}
	}
	return "", "", nil
}

// portableToolPythonFragmentValuesV1 decodes ampersand-separated fragment
// parameters without treating a legal URI semicolon as a query separator.
func portableToolPythonFragmentValuesV1(fragment, key string) ([]string, error) {
	values := []string{}
	for _, field := range strings.Split(fragment, "&") {
		pair := strings.SplitN(field, "=", 2)
		name, err := url.QueryUnescape(pair[0])
		if err != nil {
			return nil, err
		}
		value := ""
		if len(pair) == 2 {
			value, err = url.QueryUnescape(pair[1])
			if err != nil {
				return nil, err
			}
		}
		if name == key {
			values = append(values, value)
		}
	}
	return values, nil
}

func portableToolPythonValidDistributionIdentifierV1(value string) bool {
	if value == "" || !portableToolPythonASCIIAlphaNumericV1(value[0]) || !portableToolPythonASCIIAlphaNumericV1(value[len(value)-1]) {
		return false
	}
	return requirementNamePattern.FindString(value) == value
}

func portableToolPythonASCIIAlphaNumericV1(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func portableToolPythonValidURLSchemeV1(value string) bool {
	if value == "" || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !portableToolPythonASCIIAlphaNumericV1(character) && character != '+' && character != '-' && character != '.' {
			return false
		}
	}
	return true
}

func portableToolPythonOrdinaryRequirementRemainderV1(remainder string) bool {
	if remainder == "" {
		return true
	}
	return strings.ContainsRune("[;=<>!~(", rune(remainder[0]))
}

func portableToolPythonBareSourceFilenameV1(value string) bool {
	if strings.ContainsAny(value, `/\`) {
		return false
	}
	return portableToolPythonArchiveFilenameV1(value)
}

func portableToolPythonLocalPathV1(value string) bool {
	if strings.ContainsAny(value, `/\`) || strings.HasPrefix(value, ".") {
		return true
	}
	return portableToolPythonArchiveFilenameV1(value)
}

func portableToolPythonArchiveFilenameV1(value string) bool {
	lower := strings.ToLower(value)
	for _, suffix := range []string{
		".zip", ".whl", ".tar.bz2", ".tbz", ".tar.gz", ".tgz",
		".tar", ".tar.xz", ".txz", ".tlz", ".tar.lz", ".tar.lzma",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func portableToolPythonDirectURLSchemeV1(scheme string) bool {
	scheme = strings.ToLower(scheme)
	return scheme == "http" || scheme == "https" || scheme == "file" || scheme == "ftp" || strings.Contains(scheme, "+")
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
