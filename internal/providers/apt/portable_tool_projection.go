package apt

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
)

// ProjectPortableToolAPTRootsV1 adds only selected native package-set roots to
// the ordinary APT transaction. It neither consults the catalog nor invokes a
// tool-specific dependency installer.
func ProjectPortableToolAPTRootsV1(
	plan providers.PortableToolPlanV1,
	components []providers.ResolvedComponentRequestV1,
) ([]providers.ResolvedComponentRequestV1, error) {
	if err := providers.ValidatePortableToolPlanV1(plan); err != nil {
		return nil, fmt.Errorf("portable APT projection plan: %w", err)
	}
	if components == nil {
		return nil, fmt.Errorf("portable APT projection components must use an array")
	}
	encoded, err := canonical.Marshal(components)
	if err != nil {
		return nil, fmt.Errorf("clone portable APT projection components: %w", err)
	}
	var result []providers.ResolvedComponentRequestV1
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("clone portable APT projection components: %w", err)
	}
	byComponent := make(map[string]int, len(result))
	for index, component := range result {
		if err := blueprint.ValidateContributionReference("portable APT projection component", component.Component); err != nil {
			return nil, err
		}
		if component.Provider != component.Request.Provider {
			return nil, fmt.Errorf("portable APT projection component %q provider does not match request", component.Component)
		}
		if _, exists := byComponent[component.Component]; exists {
			return nil, fmt.Errorf("portable APT projection component %q is duplicated", component.Component)
		}
		if component.Provider == blueprint.ComponentTypeAPT {
			if err := ValidateCanonicalProviderRequestForComponentV1(component.Component, component.Request); err != nil {
				return nil, fmt.Errorf("portable APT projection component %q: %w", component.Component, err)
			}
		}
		byComponent[component.Component] = index
	}

	for _, tool := range plan.Tools {
		if len(tool.Responsibilities.NativePackageSets) == 0 {
			continue
		}
		owner, ok := strings.CutPrefix(tool.Scope, "application:")
		component := blueprint.ApplicationContributionID(owner, blueprint.ContributionProviderOS)
		if !ok || owner == "" {
			return nil, fmt.Errorf("portable APT roots require application:<owner> scope, got %q", tool.Scope)
		}
		if _, valid := blueprint.ApplicationContributionOwner(component, blueprint.ContributionProviderOS); !valid {
			return nil, fmt.Errorf("portable APT root scope %q has an invalid application owner", tool.Scope)
		}
		request := APTProviderRequestV1{Components: []APTComponentRequestV1{{Component: component}}}
		if index, exists := byComponent[component]; exists {
			if result[index].Provider != blueprint.ComponentTypeAPT {
				return nil, fmt.Errorf("portable APT root component %q is owned by %q", component, result[index].Provider)
			}
			request, err = decodeCanonicalProviderRequestV1(result[index].Request)
			if err != nil {
				return nil, fmt.Errorf("portable APT root component %q: %w", component, err)
			}
		}
		for _, selected := range tool.Responsibilities.NativePackageSets {
			if selected.Record.Schema != portabletool.NativePackageSetSchemaV1 {
				return nil, fmt.Errorf("selected native package set %q has invalid schema", selected.Reference.ID)
			}
			if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope(selected.Record)); err != nil {
				return nil, fmt.Errorf("selected native package set %q: %w", selected.Reference.ID, err)
			}
			value, err := canonical.Marshal(selected.Record.Value)
			if err != nil {
				return nil, err
			}
			var packageSet portabletool.NativePackageSetV1
			if err := json.Unmarshal(value, &packageSet); err != nil {
				return nil, fmt.Errorf("decode selected native package set %q: %w", selected.Reference.ID, err)
			}
			if packageSet.Manager != "apt" || len(packageSet.Repositories) != 0 {
				return nil, fmt.Errorf("selected native package set %q requires unsupported manager or repositories", selected.Reference.ID)
			}
			for _, requirement := range packageSet.Requirements {
				pkg, err := blueprint.ParseAPTPackageRequest(requirement)
				if err != nil {
					return nil, fmt.Errorf("selected native package set %q root %q: %w", selected.Reference.ID, requirement, err)
				}
				request.Components[0].Packages = append(request.Components[0].Packages, pkg)
			}
		}
		canonicalRequest, err := CanonicalProviderRequestV1(request)
		if err != nil {
			return nil, fmt.Errorf("selected portable APT roots for %q: %w", tool.Scope, err)
		}
		entry := providers.ResolvedComponentRequestV1{Component: component, Provider: blueprint.ComponentTypeAPT, Request: canonicalRequest}
		if index, exists := byComponent[component]; exists {
			result[index] = entry
		} else {
			byComponent[component] = len(result)
			result = append(result, entry)
		}
	}

	// Reuse the APT provider's shared-transaction conflict check, including
	// collisions with ordinary roots from other components.
	all := APTProviderRequestV1{Components: []APTComponentRequestV1{}}
	for _, component := range result {
		if component.Provider != blueprint.ComponentTypeAPT {
			continue
		}
		request, err := decodeCanonicalProviderRequestV1(component.Request)
		if err != nil {
			return nil, err
		}
		all.Components = append(all.Components, request.Components...)
	}
	if len(all.Components) != 0 {
		if _, err := CanonicalProviderRequestV1(all); err != nil {
			return nil, fmt.Errorf("portable APT shared transaction: %w", err)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Component < result[right].Component })
	return result, nil
}
