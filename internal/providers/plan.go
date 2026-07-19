package providers

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const (
	ProviderPlanSchemaV1       = "provider-plan-v1"
	OutputKindExecutable       = "executable"
	ValidationPolicyCompatible = "compatible"
	ValidationPolicyUnchanged  = "unchanged"
)

type CanonicalProviderData = canonical.Envelope

// CanonicalProviderRequest carries a provider-owned normalized node request.
// The provider implementation remains responsible for validating its schema
// and exact value shape before constructing or consuming this envelope.
type CanonicalProviderRequest struct {
	Schema   string                  `json:"schema"`
	Provider blueprint.ComponentType `json:"provider"`
	Value    canonical.Object        `json:"value"`
}

type ProviderPlanV1 struct {
	Schema string           `json:"schema"`
	Nodes  []NodeSpec       `json:"nodes"`
	Edges  []ProviderEdgeV1 `json:"edges"`
}

type PlanInput struct {
	BlueprintDigest canonical.Digest
	Components      []ResolvedComponentRequestV1
	Platform        blueprint.Platform
}

type ResolvedComponentRequestV1 struct {
	Component string                   `json:"component"`
	Provider  blueprint.ComponentType  `json:"provider"`
	Request   CanonicalProviderRequest `json:"request"`
}

type NodeID string

type NodeSpec struct {
	ID                 NodeID                   `json:"id"`
	Provider           blueprint.ComponentType  `json:"provider"`
	Components         []string                 `json:"components"`
	Request            CanonicalProviderRequest `json:"request"`
	OutputDeclarations []OutputDeclaration      `json:"output_declarations"`
	Requirements       RequirementDeclaration   `json:"requirements"`
}

type RequirementDeclaration struct {
	Executables  []ExecutableRequirement `json:"executables"`
	Files        []FileRequirement       `json:"files"`
	ProviderData CanonicalProviderData   `json:"provider_data"`
}

type OutputDeclaration struct {
	SupplierComponent string                `json:"supplier_component"`
	Name              string                `json:"name"`
	Kind              string                `json:"kind"`
	CandidatePath     string                `json:"candidate_path"`
	Provenance        CanonicalProviderData `json:"provenance"`
}

type ExecutableRequirement struct {
	ID                string `json:"id"`
	Command           string `json:"command"`
	VersionConstraint string `json:"version_constraint"`
	Supplier          string `json:"supplier"`
	ValidationPolicy  string `json:"validation_policy"`
}

type FileRequirement struct {
	ID               string           `json:"id"`
	Path             string           `json:"path"`
	Kind             string           `json:"kind"`
	ExpectedSHA256   canonical.Digest `json:"expected_sha256"`
	ValidationPolicy string           `json:"validation_policy"`
}

type QualifiedOutput struct {
	Component string `json:"component"`
	Name      string `json:"name"`
}

type ProviderEdgeV1 struct {
	Supplier      NodeID          `json:"supplier"`
	Consumer      NodeID          `json:"consumer"`
	RequirementID string          `json:"requirement_id"`
	Output        QualifiedOutput `json:"output"`
}

func CanonicalProviderRequestBytes(request CanonicalProviderRequest) ([]byte, error) {
	if err := validateComponentProvider(request.Provider); err != nil {
		return nil, err
	}
	if request.Schema == "" {
		return nil, fmt.Errorf("canonical provider request schema is required")
	}
	if request.Value == nil {
		return nil, fmt.Errorf("canonical provider request value must be an object")
	}
	encoded, err := canonical.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("canonical provider request %s: %w", request.Schema, err)
	}
	return encoded, nil
}

func ValidateProviderPlanV1(plan ProviderPlanV1) error {
	if plan.Schema != ProviderPlanSchemaV1 {
		return fmt.Errorf("provider plan schema must be %q", ProviderPlanSchemaV1)
	}
	if plan.Nodes == nil || plan.Edges == nil {
		return fmt.Errorf("provider plan nodes and edges must use arrays")
	}
	nodes := make(map[NodeID]NodeSpec, len(plan.Nodes))
	componentNodes := make(map[string]NodeID)
	baseCount := 0
	for index, node := range plan.Nodes {
		if index > 0 && plan.Nodes[index-1].ID >= node.ID {
			return fmt.Errorf("provider plan nodes must be unique and sorted by node ID")
		}
		if err := ValidateNodeSpec(node); err != nil {
			return fmt.Errorf("provider plan node %q: %w", node.ID, err)
		}
		if node.ID == NodeID("base") {
			baseCount++
		}
		nodes[node.ID] = node
		for _, component := range node.Components {
			if previous, exists := componentNodes[component]; exists {
				return fmt.Errorf("component %q belongs to both node %q and node %q", component, previous, node.ID)
			}
			componentNodes[component] = node.ID
		}
	}
	if baseCount != 1 {
		return fmt.Errorf("provider plan must contain exactly one base root")
	}

	explicitRequirements := make(map[string]ExecutableRequirement)
	for _, node := range plan.Nodes {
		for _, requirement := range node.Requirements.Executables {
			if requirement.Supplier != "" {
				explicitRequirements[edgeRequirementKey(node.ID, requirement.ID)] = requirement
			}
		}
	}
	seenEdges := make(map[string]bool, len(plan.Edges))
	adjacency := make(map[NodeID][]NodeID, len(plan.Nodes))
	for index, edge := range plan.Edges {
		if index > 0 && compareProviderEdges(plan.Edges[index-1], edge) >= 0 {
			return fmt.Errorf("provider plan edges must be unique and sorted by supplier, consumer, and requirement ID")
		}
		supplier, supplierExists := nodes[edge.Supplier]
		_, consumerExists := nodes[edge.Consumer]
		if !supplierExists || !consumerExists {
			return fmt.Errorf("provider edge %q -> %q references an unknown node", edge.Supplier, edge.Consumer)
		}
		if edge.Supplier == edge.Consumer {
			return fmt.Errorf("provider edge for requirement %q is a self-edge", edge.RequirementID)
		}
		if edge.Consumer == NodeID("base") {
			return fmt.Errorf("base root cannot consume provider outputs")
		}
		requirement, exists := explicitRequirements[edgeRequirementKey(edge.Consumer, edge.RequirementID)]
		if !exists {
			return fmt.Errorf("provider edge for %q does not match an explicit executable requirement", edge.RequirementID)
		}
		expectedSupplier, exists := componentNodes[requirement.Supplier]
		if !exists || expectedSupplier != edge.Supplier {
			return fmt.Errorf("provider edge for %q does not match supplier component %q", edge.RequirementID, requirement.Supplier)
		}
		if edge.Output.Component != requirement.Supplier || edge.Output.Name != requirement.Command {
			return fmt.Errorf("provider edge for %q does not select %s.%s", edge.RequirementID, requirement.Supplier, requirement.Command)
		}
		if !nodeDeclaresOutput(supplier, edge.Output) {
			return fmt.Errorf("provider edge for %q references undeclared output %s.%s", edge.RequirementID, edge.Output.Component, edge.Output.Name)
		}
		key := edgeRequirementKey(edge.Consumer, edge.RequirementID)
		if seenEdges[key] {
			return fmt.Errorf("explicit requirement %q on node %q has multiple edges", edge.RequirementID, edge.Consumer)
		}
		seenEdges[key] = true
		adjacency[edge.Supplier] = append(adjacency[edge.Supplier], edge.Consumer)
	}
	for key := range explicitRequirements {
		if !seenEdges[key] {
			return fmt.Errorf("explicit executable requirement %q has no structural edge", key)
		}
	}
	if err := rejectProviderPlanCycles(plan.Nodes, adjacency); err != nil {
		return err
	}
	return nil
}

func ValidateNodeSpec(node NodeSpec) error {
	if err := validateNodeID(node.ID, node.Provider, node.Components); err != nil {
		return err
	}
	if node.Components == nil || node.OutputDeclarations == nil || node.Requirements.Executables == nil || node.Requirements.Files == nil {
		return fmt.Errorf("node components, outputs, and requirements must use arrays")
	}
	for index, component := range node.Components {
		if err := blueprint.ValidateProviderIdentifier("component", component); err != nil {
			return err
		}
		if index > 0 && node.Components[index-1] >= component {
			return fmt.Errorf("node components must be unique and sorted")
		}
	}
	if node.Request.Provider != node.Provider {
		return fmt.Errorf("provider request %q does not match node provider %q", node.Request.Provider, node.Provider)
	}
	if _, err := CanonicalProviderRequestBytes(node.Request); err != nil {
		return err
	}
	componentSet := make(map[string]bool, len(node.Components))
	for _, component := range node.Components {
		componentSet[component] = true
	}
	for index, output := range node.OutputDeclarations {
		if index > 0 && compareOutputDeclarations(node.OutputDeclarations[index-1], output) >= 0 {
			return fmt.Errorf("node outputs must be unique and sorted by supplier component and name")
		}
		if !componentSet[output.SupplierComponent] {
			return fmt.Errorf("output %q references component %q outside its node", output.Name, output.SupplierComponent)
		}
		if err := blueprint.ValidateProviderIdentifier("output name", output.Name); err != nil {
			return err
		}
		if output.Kind != OutputKindExecutable {
			return fmt.Errorf("output %q kind must be %q", output.Name, OutputKindExecutable)
		}
		if err := validateAbsoluteLinuxPath("output candidate path", output.CandidatePath); err != nil {
			return err
		}
		if err := validateCanonicalProviderData("output provenance", output.Provenance); err != nil {
			return err
		}
	}
	return ValidateRequirementDeclaration(node.Requirements)
}

func ValidateRequirementDeclaration(declaration RequirementDeclaration) error {
	if declaration.Executables == nil || declaration.Files == nil {
		return fmt.Errorf("requirement declaration executables and files must use arrays")
	}
	if err := validateCanonicalProviderData("requirement provider data", declaration.ProviderData); err != nil {
		return err
	}
	seenRequirements := make(map[string]bool)
	for index, requirement := range declaration.Executables {
		if index > 0 && declaration.Executables[index-1].ID >= requirement.ID {
			return fmt.Errorf("executable requirements must be unique and sorted by ID")
		}
		if err := validateRequirementID(requirement.ID, seenRequirements); err != nil {
			return err
		}
		if err := blueprint.ValidateProviderIdentifier("executable command", requirement.Command); err != nil {
			return err
		}
		if requirement.Supplier != "" {
			if err := blueprint.ValidateProviderIdentifier("executable supplier", requirement.Supplier); err != nil {
				return err
			}
		}
		if err := validateValidationPolicy(requirement.ValidationPolicy); err != nil {
			return fmt.Errorf("executable requirement %q: %w", requirement.ID, err)
		}
	}
	for index, requirement := range declaration.Files {
		if index > 0 && declaration.Files[index-1].ID >= requirement.ID {
			return fmt.Errorf("file requirements must be unique and sorted by ID")
		}
		if err := validateRequirementID(requirement.ID, seenRequirements); err != nil {
			return err
		}
		if err := validateAbsoluteLinuxPath("file requirement path", requirement.Path); err != nil {
			return err
		}
		if err := blueprint.ValidateProviderIdentifier("file requirement kind", requirement.Kind); err != nil {
			return err
		}
		if requirement.ExpectedSHA256 != "" {
			if err := requirement.ExpectedSHA256.Validate(); err != nil {
				return fmt.Errorf("file requirement %q expected digest: %w", requirement.ID, err)
			}
		}
		if err := validateValidationPolicy(requirement.ValidationPolicy); err != nil {
			return fmt.Errorf("file requirement %q: %w", requirement.ID, err)
		}
	}
	return nil
}

func NormalizeProviderPlanV1(plan ProviderPlanV1) ProviderPlanV1 {
	result := plan
	result.Nodes = append([]NodeSpec{}, plan.Nodes...)
	result.Edges = append([]ProviderEdgeV1{}, plan.Edges...)
	sort.Slice(result.Nodes, func(left int, right int) bool { return result.Nodes[left].ID < result.Nodes[right].ID })
	sort.Slice(result.Edges, func(left int, right int) bool {
		return compareProviderEdges(result.Edges[left], result.Edges[right]) < 0
	})
	return result
}

func validateNodeID(id NodeID, provider blueprint.ComponentType, components []string) error {
	if err := validateComponentProvider(provider); err != nil {
		return err
	}
	switch provider {
	case blueprint.ComponentTypeBase:
		if id != NodeID("base") || len(components) != 1 || components[0] != "base" {
			return fmt.Errorf("base provider node must use ID and component %q", "base")
		}
	case blueprint.ComponentTypeAPT:
		if id != NodeID("apt") || len(components) == 0 {
			return fmt.Errorf("APT provider node must use ID %q and contain at least one component", "apt")
		}
		for _, component := range components {
			if component == "base" {
				return fmt.Errorf("APT provider node cannot claim the reserved base component")
			}
		}
	case blueprint.ComponentTypePython:
		if len(components) != 1 || id != NodeID("python/"+components[0]) {
			return fmt.Errorf("Python provider node must use ID python/<component> and contain that one component")
		}
		if components[0] == "base" {
			return fmt.Errorf("Python provider node cannot claim the reserved base component")
		}
	}
	return nil
}

func validateComponentProvider(provider blueprint.ComponentType) error {
	switch provider {
	case blueprint.ComponentTypeBase, blueprint.ComponentTypeAPT, blueprint.ComponentTypePython:
		return nil
	default:
		return fmt.Errorf("unsupported component provider %q", provider)
	}
}

func validateCanonicalProviderData(field string, data CanonicalProviderData) error {
	if data.Schema == "" || data.Value == nil {
		return fmt.Errorf("%s must contain a schema and object value", field)
	}
	if _, err := canonical.Marshal(data); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

func validateRequirementID(id string, seen map[string]bool) error {
	if err := blueprint.ValidateProviderIdentifier("requirement ID", id); err != nil {
		return err
	}
	if seen[id] {
		return fmt.Errorf("requirement ID %q is used more than once", id)
	}
	seen[id] = true
	return nil
}

func validateValidationPolicy(policy string) error {
	if policy != ValidationPolicyCompatible && policy != ValidationPolicyUnchanged {
		return fmt.Errorf("validation policy must be %q or %q", ValidationPolicyCompatible, ValidationPolicyUnchanged)
	}
	return nil
}

func validateAbsoluteLinuxPath(field string, value string) error {
	if value == "" || !path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, `\`) {
		return fmt.Errorf("%s %q must be a normalized absolute Linux path", field, value)
	}
	return nil
}

func compareOutputDeclarations(left OutputDeclaration, right OutputDeclaration) int {
	if left.SupplierComponent < right.SupplierComponent {
		return -1
	}
	if left.SupplierComponent > right.SupplierComponent {
		return 1
	}
	return strings.Compare(left.Name, right.Name)
}

func compareProviderEdges(left ProviderEdgeV1, right ProviderEdgeV1) int {
	if left.Supplier < right.Supplier {
		return -1
	}
	if left.Supplier > right.Supplier {
		return 1
	}
	if left.Consumer < right.Consumer {
		return -1
	}
	if left.Consumer > right.Consumer {
		return 1
	}
	return strings.Compare(left.RequirementID, right.RequirementID)
}

func edgeRequirementKey(consumer NodeID, requirementID string) string {
	return string(consumer) + "\x00" + requirementID
}

func nodeDeclaresOutput(node NodeSpec, qualified QualifiedOutput) bool {
	for _, output := range node.OutputDeclarations {
		if output.SupplierComponent == qualified.Component && output.Name == qualified.Name {
			return true
		}
	}
	return false
}

func rejectProviderPlanCycles(nodes []NodeSpec, adjacency map[NodeID][]NodeID) error {
	const (
		unseen = iota
		visiting
		done
	)
	state := make(map[NodeID]int, len(nodes))
	var visit func(NodeID) error
	visit = func(node NodeID) error {
		switch state[node] {
		case visiting:
			return fmt.Errorf("provider plan contains a structural cycle at node %q", node)
		case done:
			return nil
		}
		state[node] = visiting
		for _, consumer := range adjacency[node] {
			if err := visit(consumer); err != nil {
				return err
			}
		}
		state[node] = done
		return nil
	}
	for _, node := range nodes {
		if err := visit(node.ID); err != nil {
			return err
		}
	}
	return nil
}
