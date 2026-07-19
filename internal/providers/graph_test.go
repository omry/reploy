package providers

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func aptPlanNode(component string, outputs ...OutputDeclaration) NodeSpec {
	return NodeSpec{
		ID: "apt", Provider: blueprint.ComponentTypeAPT, Components: []string{component},
		Request:            providerRequest(blueprint.ComponentTypeAPT, "apt-provider-request-v1"),
		OutputDeclarations: append([]OutputDeclaration{}, outputs...),
		Requirements: RequirementDeclaration{
			Executables: []ExecutableRequirement{}, Files: []FileRequirement{}, ProviderData: providerData("apt-requirements-v1"),
		},
	}
}

func TestStableProviderInitializationOrderUsesEdgesThenLayerPriority(t *testing.T) {
	zOutput := OutputDeclaration{SupplierComponent: "z", Name: "tool", Kind: OutputKindExecutable, CandidatePath: "/opt/z/tool", Provenance: providerData("python-output-v1")}
	z := pythonPlanNode("z", ExecutableRequirement{})
	z.OutputDeclarations = []OutputDeclaration{zOutput}
	a := pythonPlanNode("a", ExecutableRequirement{ID: "tool", Command: "tool", Supplier: "z", ValidationPolicy: ValidationPolicyCompatible})
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{aptPlanNode("system"), basePlanNode(), a, z},
		Edges: []ProviderEdgeV1{{
			Supplier: "python/z", Consumer: "python/a", RequirementID: "tool", Output: QualifiedOutput{Component: "z", Name: "tool"},
		}},
	}
	order, err := StableProviderInitializationOrder(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := []NodeID{"base", "apt", "python/z", "python/a"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestCandidateSelectionUsesLowerLayerOrderAndFreezesFirstCompatible(t *testing.T) {
	baseDeclaration := OutputDeclaration{SupplierComponent: "base", Name: "python", Kind: OutputKindExecutable, CandidatePath: "/base/python", Provenance: providerData("base-export-v1")}
	aptDeclaration := OutputDeclaration{SupplierComponent: "system", Name: "python", Kind: OutputKindExecutable, CandidatePath: "/apt/python", Provenance: providerData("apt-export-v1")}
	consumer := pythonPlanNode("application", ExecutableRequirement{ID: "interpreter", Command: "python", ValidationPolicy: ValidationPolicyCompatible})
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{aptPlanNode("system", aptDeclaration), basePlanNode(baseDeclaration), consumer},
		Edges:  []ProviderEdgeV1{},
	}
	baseOutput := catalogOutput("base", "base", "python", "/base/python")
	aptOutput := catalogOutput("apt", "system", "python", "/apt/python")
	groups, err := BuildRequirementCandidates(plan, "python/application", []RealizedOutput{baseOutput, aptOutput})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Outputs) != 2 || groups[0].Outputs[0].SupplierNode != "base" || groups[0].Outputs[1].SupplierNode != "apt" {
		t.Fatalf("groups = %#v", groups)
	}
	visited := []NodeID{}
	selections, err := FreezeExecutableSelections(consumer, groups, func(requirement ExecutableRequirement, output RealizedOutput) (ExecutableEvidence, bool, error) {
		visited = append(visited, output.SupplierNode)
		if output.SupplierNode == "base" {
			return ExecutableEvidence{}, false, nil
		}
		return selectionEvidence(requirement, output), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(visited, []NodeID{"base", "apt"}) || len(selections) != 1 || selections[0].Output.SupplierNode != "apt" {
		t.Fatalf("visited = %#v, selections = %#v", visited, selections)
	}

	// Revalidation checks only the frozen APT choice. A now-compatible base
	// candidate is deliberately unavailable as a fallback.
	err = ValidateFrozenExecutableSelections(consumer, selections, func(_ ExecutableRequirement, selection FrozenExecutableSelection) error {
		if selection.Output.SupplierNode != "apt" {
			t.Fatalf("revalidation switched supplier: %#v", selection)
		}
		return errors.New("observed version drift")
	})
	if err == nil || !strings.Contains(err.Error(), "version drift") {
		t.Fatalf("error = %v", err)
	}
}

func TestExplicitSupplierCandidateGroupIsSingleton(t *testing.T) {
	baseDeclaration := OutputDeclaration{SupplierComponent: "base", Name: "python", Kind: OutputKindExecutable, CandidatePath: "/base/python", Provenance: providerData("base-export-v1")}
	aptDeclaration := OutputDeclaration{SupplierComponent: "system", Name: "python", Kind: OutputKindExecutable, CandidatePath: "/apt/python", Provenance: providerData("apt-export-v1")}
	consumer := pythonPlanNode("application", ExecutableRequirement{ID: "interpreter", Command: "python", Supplier: "system", ValidationPolicy: ValidationPolicyCompatible})
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{aptPlanNode("system", aptDeclaration), basePlanNode(baseDeclaration), consumer},
		Edges: []ProviderEdgeV1{{
			Supplier: "apt", Consumer: "python/application", RequirementID: "interpreter", Output: QualifiedOutput{Component: "system", Name: "python"},
		}},
	}
	groups, err := BuildRequirementCandidates(plan, "python/application", []RealizedOutput{
		catalogOutput("base", "base", "python", "/base/python"),
		catalogOutput("apt", "system", "python", "/apt/python"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups[0].Outputs) != 1 || groups[0].Outputs[0].SupplierNode != "apt" {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestCandidateBuilderRejectsRetroactiveLaterNode(t *testing.T) {
	consumer := pythonPlanNode("application", ExecutableRequirement{ID: "interpreter", Command: "python", ValidationPolicy: ValidationPolicyCompatible})
	later := pythonPlanNode("later", ExecutableRequirement{})
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{basePlanNode(), consumer, later},
		Edges:  []ProviderEdgeV1{},
	}
	_, err := BuildRequirementCandidates(plan, "python/application", []RealizedOutput{
		catalogOutput("python/later", "later", "python", "/later/python"),
	})
	if err == nil || !strings.Contains(err.Error(), "later node") {
		t.Fatalf("error = %v", err)
	}
}

func catalogOutput(node NodeID, component string, name string, invocation string) RealizedOutput {
	qualified := QualifiedOutput{Component: component, Name: name}
	output := RealizedOutput{
		SupplierComponent: component, SupplierNode: node, Name: name,
		Candidate: ExecutableCandidate{InvocationPath: invocation, Provenance: providerData("test-output-v1")},
	}
	output.Evidence = selectionEvidence(ExecutableRequirement{}, output)
	output.Evidence.Output = qualified
	return output
}

func selectionEvidence(requirement ExecutableRequirement, output RealizedOutput) ExecutableEvidence {
	return ExecutableEvidence{
		Schema: ExecutableEvidenceSchemaV1, RequirementID: requirement.ID,
		Output: QualifiedOutput{Component: output.SupplierComponent, Name: output.Name}, InvocationPath: output.Candidate.InvocationPath,
		LinkChain: []LinkEvidence{},
		Terminal: FileEvidence{
			Schema: FileEvidenceSchemaV1, RequirementID: requirement.ID, Path: output.Candidate.InvocationPath, Kind: "regular", Mode: "0755", Size: "1", SHA256: testDigest("a"),
		},
		Access: PortableAccessEvidence{
			Schema: PortableAccessSchemaV1, Profile: PortableOutputAccessV1,
			Paths: []AccessPathEvidence{{Path: output.Candidate.InvocationPath, Kind: "regular", Mode: "0755", Required: "other-read-execute"}},
		},
		Facts: providerData("test-selection-facts-v1"),
	}
}
