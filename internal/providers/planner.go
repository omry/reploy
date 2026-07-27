package providers

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
)

type NodePlanner interface {
	Plan(PlanInput) ([]NodeSpec, error)
}

func BuildProviderPlanV1(input PlanInput, planners ...NodePlanner) (ProviderPlanV1, error) {
	if err := input.Platform.Validate(); err != nil {
		return ProviderPlanV1{}, fmt.Errorf("provider plan platform: %w", err)
	}
	if input.Components == nil {
		return ProviderPlanV1{}, fmt.Errorf("provider plan components must use an array")
	}

	expected := make(map[string]blueprint.ComponentType, len(input.Components))
	var baseRequest *CanonicalProviderRequest
	for _, component := range input.Components {
		if err := blueprint.ValidateContributionReference("resolved contribution", component.Component); err != nil {
			return ProviderPlanV1{}, err
		}
		if previous, exists := expected[component.Component]; exists {
			return ProviderPlanV1{}, fmt.Errorf("resolved component %q is duplicated with providers %q and %q", component.Component, previous, component.Provider)
		}
		if component.Request.Provider != component.Provider {
			return ProviderPlanV1{}, fmt.Errorf("resolved component %q request provider does not match %q", component.Component, component.Provider)
		}
		if _, err := CanonicalProviderRequestBytes(component.Request); err != nil {
			return ProviderPlanV1{}, fmt.Errorf("resolved component %q: %w", component.Component, err)
		}
		if component.Component == "base" {
			if component.Provider != blueprint.ComponentTypeBase {
				return ProviderPlanV1{}, fmt.Errorf("resolved base component must use provider %q", blueprint.ComponentTypeBase)
			}
			copy := component.Request
			baseRequest = &copy
		} else if component.Provider == blueprint.ComponentTypeBase {
			return ProviderPlanV1{}, fmt.Errorf("base provider cannot own component %q", component.Component)
		}
		expected[component.Component] = component.Provider
	}
	if baseRequest == nil {
		return ProviderPlanV1{}, fmt.Errorf("provider plan requires the resolved base component")
	}

	base, err := BaseNodeSpec(*baseRequest)
	if err != nil {
		return ProviderPlanV1{}, fmt.Errorf("plan base component: %w", err)
	}
	nodes := []NodeSpec{base}
	for _, planner := range planners {
		if planner == nil {
			return ProviderPlanV1{}, fmt.Errorf("provider node planner must not be nil")
		}
		planned, err := planner.Plan(input)
		if err != nil {
			return ProviderPlanV1{}, err
		}
		nodes = append(nodes, planned...)
	}

	claimed := make(map[string]NodeID, len(expected))
	for _, node := range nodes {
		for _, component := range node.Components {
			provider, exists := expected[component]
			if !exists {
				return ProviderPlanV1{}, fmt.Errorf("node %q claims component %q outside the resolved request", node.ID, component)
			}
			if provider != node.Provider {
				return ProviderPlanV1{}, fmt.Errorf("node %q provider %q does not match component %q provider %q", node.ID, node.Provider, component, provider)
			}
			if previous, exists := claimed[component]; exists {
				return ProviderPlanV1{}, fmt.Errorf("component %q is claimed by nodes %q and %q", component, previous, node.ID)
			}
			claimed[component] = node.ID
		}
	}
	for component := range expected {
		if _, exists := claimed[component]; !exists {
			return ProviderPlanV1{}, fmt.Errorf("resolved component %q was not claimed by a provider node", component)
		}
	}

	componentNodes := make(map[string]NodeID, len(claimed))
	for component, node := range claimed {
		componentNodes[component] = node
	}
	edges := []ProviderEdgeV1{}
	for _, consumer := range nodes {
		for _, requirement := range consumer.Requirements.Executables {
			if requirement.Supplier == "" {
				continue
			}
			supplier, exists := componentNodes[requirement.Supplier]
			if !exists {
				return ProviderPlanV1{}, fmt.Errorf("node %q requirement %q names missing supplier component %q", consumer.ID, requirement.ID, requirement.Supplier)
			}
			edges = append(edges, ProviderEdgeV1{
				Supplier: supplier, Consumer: consumer.ID, RequirementID: requirement.ID,
				Output: QualifiedOutput{Component: requirement.Supplier, Name: requirement.Command},
			})
		}
	}
	plan := NormalizeProviderPlanV1(ProviderPlanV1{Schema: ProviderPlanSchemaV1, Nodes: nodes, Edges: edges})
	if err := ValidateProviderPlanV1(plan); err != nil {
		return ProviderPlanV1{}, err
	}
	return plan, nil
}
