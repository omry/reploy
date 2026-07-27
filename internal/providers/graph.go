package providers

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
)

type RequirementCandidatesV1 struct {
	RequirementID string
	Outputs       []RealizedOutput
}

type FrozenExecutableSelection struct {
	RequirementID string
	Output        RealizedOutput
	Evidence      ExecutableEvidence
}

type ExecutableCandidateValidator func(ExecutableRequirement, RealizedOutput) (ExecutableEvidence, bool, error)

type FrozenExecutableValidator func(ExecutableRequirement, FrozenExecutableSelection) error

func StableProviderInitializationOrder(plan ProviderPlanV1) ([]NodeID, error) {
	if err := ValidateProviderPlanV1(plan); err != nil {
		return nil, err
	}
	nodes := make([]NodeID, len(plan.Nodes))
	for index, node := range plan.Nodes {
		nodes[index] = node.ID
	}
	return StableProviderNodeOrder(nodes, plan.Edges)
}

// StableProviderNodeOrder returns the deterministic layer order for a
// validated provider graph without requiring its provider-specific node
// payloads. Build-lock validation uses the same ordering rule as execution.
func StableProviderNodeOrder(nodes []NodeID, edges []ProviderEdgeV1) ([]NodeID, error) {
	indegree := make(map[NodeID]int, len(nodes))
	adjacency := make(map[NodeID][]NodeID, len(nodes))
	for _, node := range nodes {
		if _, found := indegree[node]; found {
			return nil, fmt.Errorf("provider graph contains duplicate node %q", node)
		}
		indegree[node] = 0
	}
	for _, edge := range edges {
		if _, found := indegree[edge.Supplier]; !found {
			return nil, fmt.Errorf("provider graph edge supplier %q is missing", edge.Supplier)
		}
		if _, found := indegree[edge.Consumer]; !found {
			return nil, fmt.Errorf("provider graph edge consumer %q is missing", edge.Consumer)
		}
		indegree[edge.Consumer]++
		adjacency[edge.Supplier] = append(adjacency[edge.Supplier], edge.Consumer)
	}
	ready := make([]NodeID, 0, len(nodes))
	for node, count := range indegree {
		if count == 0 {
			ready = append(ready, node)
		}
	}
	order := make([]NodeID, 0, len(nodes))
	for len(ready) != 0 {
		sort.Slice(ready, func(left int, right int) bool { return compareReadyNodes(ready[left], ready[right]) < 0 })
		node := ready[0]
		ready = ready[1:]
		order = append(order, node)
		for _, consumer := range adjacency[node] {
			indegree[consumer]--
			if indegree[consumer] == 0 {
				ready = append(ready, consumer)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, fmt.Errorf("provider plan has no complete initialization order")
	}
	return order, nil
}

func BuildRequirementCandidates(plan ProviderPlanV1, consumerID NodeID, earlierCatalog []RealizedOutput) ([]RequirementCandidatesV1, error) {
	if err := ValidateProviderPlanV1(plan); err != nil {
		return nil, err
	}
	order, err := StableProviderInitializationOrder(plan)
	if err != nil {
		return nil, err
	}
	rank := make(map[NodeID]int, len(order))
	for index, node := range order {
		rank[node] = index
	}
	consumerRank, ranked := rank[consumerID]
	if !ranked {
		return nil, fmt.Errorf("candidate consumer node %q is missing", consumerID)
	}
	var consumer NodeSpec
	found := false
	nodes := make(map[NodeID]bool, len(plan.Nodes))
	for _, node := range plan.Nodes {
		nodes[node.ID] = true
		if node.ID == consumerID {
			consumer = node
			found = true
		}
	}
	if !found || consumerID == "base" {
		return nil, fmt.Errorf("candidate consumer node %q is missing or does not resolve", consumerID)
	}
	for index, output := range earlierCatalog {
		if !nodes[output.SupplierNode] {
			return nil, fmt.Errorf("candidate catalog output %d references unknown supplier node %q", index, output.SupplierNode)
		}
		if output.SupplierNode == consumerID {
			return nil, fmt.Errorf("candidate catalog contains consumer node %q before it is initialized", consumerID)
		}
		if rank[output.SupplierNode] >= consumerRank {
			return nil, fmt.Errorf("candidate catalog contains later node %q for consumer %q", output.SupplierNode, consumerID)
		}
		if index > 0 {
			previous := earlierCatalog[index-1]
			if rank[previous.SupplierNode] > rank[output.SupplierNode] || rank[previous.SupplierNode] == rank[output.SupplierNode] && compareRealizedCatalogOutputs(previous, output) >= 0 {
				return nil, fmt.Errorf("candidate catalog is not in lower-layer output order")
			}
		}
		if err := validateRealizedCatalogOutput(output); err != nil {
			return nil, fmt.Errorf("candidate catalog output %d: %w", index, err)
		}
	}
	groups := make([]RequirementCandidatesV1, 0, len(consumer.Requirements.Executables))
	for _, requirement := range consumer.Requirements.Executables {
		group := RequirementCandidatesV1{RequirementID: requirement.ID, Outputs: []RealizedOutput{}}
		for _, output := range earlierCatalog {
			if output.Name != requirement.Command {
				continue
			}
			if requirement.Supplier != "" && output.SupplierComponent != requirement.Supplier {
				continue
			}
			group.Outputs = append(group.Outputs, output)
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func compareRealizedCatalogOutputs(left RealizedOutput, right RealizedOutput) int {
	if left.SupplierComponent < right.SupplierComponent {
		return -1
	}
	if left.SupplierComponent > right.SupplierComponent {
		return 1
	}
	if left.Name < right.Name {
		return -1
	}
	if left.Name > right.Name {
		return 1
	}
	return 0
}

func FreezeExecutableSelections(node NodeSpec, groups []RequirementCandidatesV1, validate ExecutableCandidateValidator) ([]FrozenExecutableSelection, error) {
	if err := ValidateNodeSpec(node); err != nil {
		return nil, err
	}
	if validate == nil {
		return nil, fmt.Errorf("executable candidate validator is required")
	}
	if len(groups) != len(node.Requirements.Executables) {
		return nil, fmt.Errorf("candidate groups do not match executable declarations")
	}
	selections := make([]FrozenExecutableSelection, 0, len(groups))
	for index, requirement := range node.Requirements.Executables {
		group := groups[index]
		if group.RequirementID != requirement.ID || group.Outputs == nil {
			return nil, fmt.Errorf("candidate group %d does not match requirement %q", index, requirement.ID)
		}
		selected := false
		for _, output := range group.Outputs {
			evidence, compatible, err := validate(requirement, output)
			if err != nil {
				return nil, fmt.Errorf("validate candidate %s.%s for requirement %q: %w", output.SupplierComponent, output.Name, requirement.ID, err)
			}
			if !compatible {
				continue
			}
			if err := validateFrozenEvidence(requirement, output, evidence); err != nil {
				return nil, err
			}
			selections = append(selections, FrozenExecutableSelection{RequirementID: requirement.ID, Output: output, Evidence: evidence})
			selected = true
			break
		}
		if !selected {
			if requirement.Supplier != "" {
				return nil, fmt.Errorf("explicit supplier %q has no compatible output %q for requirement %q", requirement.Supplier, requirement.Command, requirement.ID)
			}
			return nil, fmt.Errorf("no compatible output %q for requirement %q", requirement.Command, requirement.ID)
		}
	}
	return selections, nil
}

func ValidateFrozenExecutableSelections(node NodeSpec, selections []FrozenExecutableSelection, validate FrozenExecutableValidator) error {
	if err := ValidateNodeSpec(node); err != nil {
		return err
	}
	if validate == nil {
		return fmt.Errorf("frozen executable validator is required")
	}
	if len(selections) != len(node.Requirements.Executables) {
		return fmt.Errorf("frozen selections do not match executable declarations")
	}
	for index, requirement := range node.Requirements.Executables {
		selection := selections[index]
		if selection.RequirementID != requirement.ID {
			return fmt.Errorf("frozen selection %d does not match requirement %q", index, requirement.ID)
		}
		if err := validateFrozenEvidence(requirement, selection.Output, selection.Evidence); err != nil {
			return err
		}
		if err := validate(requirement, selection); err != nil {
			return fmt.Errorf("frozen selection %s.%s for requirement %q changed: %w", selection.Output.SupplierComponent, selection.Output.Name, requirement.ID, err)
		}
	}
	return nil
}

func validateRealizedCatalogOutput(output RealizedOutput) error {
	if output.SupplierNode == "" {
		return fmt.Errorf("supplier node is required")
	}
	if err := blueprint.ValidateContributionReference("catalog output contribution", output.SupplierComponent); err != nil {
		return err
	}
	if err := blueprint.ValidateProviderIdentifier("catalog output name", output.Name); err != nil {
		return err
	}
	if err := validateAbsoluteLinuxPath("catalog output invocation path", output.Candidate.InvocationPath); err != nil {
		return err
	}
	if err := validateCanonicalProviderData("catalog output provenance", output.Candidate.Provenance); err != nil {
		return err
	}
	if output.Candidate.InvocationPath != output.Evidence.InvocationPath {
		return fmt.Errorf("candidate and evidence invocation paths differ")
	}
	if output.Evidence.Output != (QualifiedOutput{Component: output.SupplierComponent, Name: output.Name}) {
		return fmt.Errorf("candidate evidence identifies a different output")
	}
	if output.Evidence.Schema != ExecutableEvidenceSchemaV1 || output.Evidence.RequirementID != "" {
		return fmt.Errorf("catalog output evidence must be final exposure evidence with no requirement ID")
	}
	return ValidateFinalExecutableEvidence(output.Evidence)
}

// ValidateRealizedOutput validates one final catalog output without requiring
// the resolver bundle that originally declared it.
func ValidateRealizedOutput(output RealizedOutput) error {
	return validateRealizedCatalogOutput(output)
}

func validateFrozenEvidence(requirement ExecutableRequirement, output RealizedOutput, evidence ExecutableEvidence) error {
	if evidence.RequirementID != requirement.ID {
		return fmt.Errorf("selected evidence requirement ID %q does not match %q", evidence.RequirementID, requirement.ID)
	}
	if evidence.Output != (QualifiedOutput{Component: output.SupplierComponent, Name: output.Name}) || evidence.InvocationPath != output.Candidate.InvocationPath {
		return fmt.Errorf("selected evidence for requirement %q does not match its candidate", requirement.ID)
	}
	return validateExecutableEvidence(evidence, requirement)
}

func compareReadyNodes(left NodeID, right NodeID) int {
	leftPriority := nodeInitializationPriority(left)
	rightPriority := nodeInitializationPriority(right)
	if leftPriority < rightPriority {
		return -1
	}
	if leftPriority > rightPriority {
		return 1
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func nodeInitializationPriority(node NodeID) int {
	switch node {
	case "base":
		return 0
	case "apt":
		return 1
	default:
		return 2
	}
}
