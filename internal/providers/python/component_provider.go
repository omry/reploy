package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	providerapi "github.com/omry/reploy/internal/providers"
)

const (
	RecipeVersion                = "python-v1"
	MaterializationRecipeVersion = "python-materialize-v1"
	InstallRoot                  = "/opt/reploy/providers/python"
	BundleMount                  = "/reploy-bundle"
)

type ComponentProvider struct{}

func (ComponentProvider) Type() blueprint.ComponentType { return blueprint.ComponentTypePython }

func (ComponentProvider) RecipeVersion() string { return RecipeVersion }

func (ComponentProvider) Plan(input providerapi.PlanInput) ([]providerapi.NodeSpec, error) {
	if err := input.Platform.Validate(); err != nil {
		return nil, fmt.Errorf("Python plan platform: %w", err)
	}
	components := append([]providerapi.ResolvedComponentRequestV1{}, input.Components...)
	sort.Slice(components, func(left int, right int) bool { return components[left].Component < components[right].Component })
	nodes := []providerapi.NodeSpec{}
	for _, component := range components {
		if component.Provider != blueprint.ComponentTypePython {
			continue
		}
		request, err := decodeCanonicalProviderRequestV1(component.Request)
		if err != nil {
			return nil, fmt.Errorf("plan Python component %q: %w", component.Component, err)
		}
		if request.Component != component.Component {
			return nil, fmt.Errorf("Python request component %q does not match resolved component %q", request.Component, component.Component)
		}
		application, ok := blueprint.ApplicationContributionOwner(
			component.Component,
			blueprint.ContributionProviderPython,
		)
		nodeOwner := component.Component
		if ok {
			nodeOwner = blueprint.ApplicationID(application)
		}
		node := providerapi.NodeSpec{
			ID:                 providerapi.NodeID("python/" + nodeOwner),
			Provider:           blueprint.ComponentTypePython,
			Components:         []string{component.Component},
			Request:            component.Request,
			OutputDeclarations: []providerapi.OutputDeclaration{},
			Requirements: providerapi.RequirementDeclaration{
				Executables: []providerapi.ExecutableRequirement{{
					ID: "interpreter", Command: request.Interpreter.Command, VersionConstraint: request.Interpreter.Version,
					Supplier: request.Interpreter.Supplier, ValidationPolicy: providerapi.ValidationPolicyCompatible,
				}},
				Files: []providerapi.FileRequirement{},
				ProviderData: providerapi.CanonicalProviderData{
					Schema: component.Request.Schema, Value: component.Request.Value,
				},
			},
		}
		if err := providerapi.ValidateNodeSpec(node); err != nil {
			return nil, fmt.Errorf("plan Python component %q: %w", component.Component, err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func ValidatePythonVersion(output string) error {
	value := strings.TrimSpace(output)
	if !strings.HasPrefix(value, "Python 3.") {
		return fmt.Errorf("Python provider requires Python 3 in the selected base image; probe returned %q", value)
	}
	return nil
}
