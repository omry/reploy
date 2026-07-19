package providers

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func providerData(schema string) CanonicalProviderData {
	return CanonicalProviderData{Schema: schema, Value: canonical.Object{}}
}

func providerRequest(provider blueprint.ComponentType, schema string) CanonicalProviderRequest {
	return CanonicalProviderRequest{Schema: schema, Provider: provider, Value: canonical.Object{}}
}

func basePlanNode(outputs ...OutputDeclaration) NodeSpec {
	return NodeSpec{
		ID:                 "base",
		Provider:           blueprint.ComponentTypeBase,
		Components:         []string{"base"},
		Request:            providerRequest(blueprint.ComponentTypeBase, "base-provider-request-v1"),
		OutputDeclarations: append([]OutputDeclaration{}, outputs...),
		Requirements: RequirementDeclaration{
			Executables:  []ExecutableRequirement{},
			Files:        []FileRequirement{},
			ProviderData: providerData("base-requirements-v1"),
		},
	}
}

func pythonPlanNode(component string, requirement ExecutableRequirement) NodeSpec {
	executables := []ExecutableRequirement{}
	if requirement.ID != "" {
		executables = append(executables, requirement)
	}
	return NodeSpec{
		ID:                 NodeID("python/" + component),
		Provider:           blueprint.ComponentTypePython,
		Components:         []string{component},
		Request:            providerRequest(blueprint.ComponentTypePython, "python-provider-request-v1"),
		OutputDeclarations: []OutputDeclaration{},
		Requirements: RequirementDeclaration{
			Executables:  executables,
			Files:        []FileRequirement{},
			ProviderData: providerData("python-requirements-v1"),
		},
	}
}

func TestCanonicalProviderRequestBytes(t *testing.T) {
	request := providerRequest(blueprint.ComponentTypePython, "python-provider-request-v1")
	first, err := CanonicalProviderRequestBytes(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalProviderRequestBytes(request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || !strings.Contains(string(first), `"provider":"python"`) {
		t.Fatalf("canonical request = %s", first)
	}
	for _, invalid := range []CanonicalProviderRequest{
		{Provider: blueprint.ComponentTypePython, Value: canonical.Object{}},
		{Schema: "python-provider-request-v1", Provider: blueprint.ComponentTypePython},
		{Schema: "unknown-v1", Provider: "unknown", Value: canonical.Object{}},
		{Schema: "python-provider-request-v1", Provider: blueprint.ComponentTypePython, Value: canonical.Object{"count": 1}},
	} {
		if _, err := CanonicalProviderRequestBytes(invalid); err == nil {
			t.Fatalf("invalid request succeeded: %#v", invalid)
		}
	}
}

func TestValidateProviderPlanV1AcceptsExplicitSupplierEdge(t *testing.T) {
	baseOutput := OutputDeclaration{
		SupplierComponent: "base",
		Name:              "python",
		Kind:              OutputKindExecutable,
		CandidatePath:     "/usr/local/bin/python",
		Provenance:        providerData("base-export-v1"),
	}
	requirement := ExecutableRequirement{
		ID:               "interpreter",
		Command:          "python",
		Supplier:         "base",
		ValidationPolicy: ValidationPolicyCompatible,
	}
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{basePlanNode(baseOutput), pythonPlanNode("application", requirement)},
		Edges: []ProviderEdgeV1{{
			Supplier:      "base",
			Consumer:      "python/application",
			RequirementID: "interpreter",
			Output:        QualifiedOutput{Component: "base", Name: "python"},
		}},
	}
	if err := ValidateProviderPlanV1(plan); err != nil {
		t.Fatal(err)
	}
}

func TestValidateProviderPlanV1RejectsMalformedRecords(t *testing.T) {
	baseOutput := OutputDeclaration{
		SupplierComponent: "base", Name: "python", Kind: OutputKindExecutable,
		CandidatePath: "/usr/bin/python3", Provenance: providerData("base-export-v1"),
	}
	requirement := ExecutableRequirement{
		ID: "interpreter", Command: "python", Supplier: "base", ValidationPolicy: ValidationPolicyCompatible,
	}
	valid := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{basePlanNode(baseOutput), pythonPlanNode("application", requirement)},
		Edges: []ProviderEdgeV1{{
			Supplier: "base", Consumer: "python/application", RequirementID: "interpreter",
			Output: QualifiedOutput{Component: "base", Name: "python"},
		}},
	}
	tests := []struct {
		name   string
		mutate func(*ProviderPlanV1)
		want   string
	}{
		{name: "schema", mutate: func(plan *ProviderPlanV1) { plan.Schema = "provider-plan-v2" }, want: "schema"},
		{name: "missing base", mutate: func(plan *ProviderPlanV1) { plan.Nodes = plan.Nodes[1:] }, want: "base root"},
		{name: "node order", mutate: func(plan *ProviderPlanV1) { plan.Nodes[0], plan.Nodes[1] = plan.Nodes[1], plan.Nodes[0] }, want: "sorted"},
		{name: "wrong node union", mutate: func(plan *ProviderPlanV1) { plan.Nodes[1].ID = "python/other" }, want: "python/<component>"},
		{name: "relative output", mutate: func(plan *ProviderPlanV1) { plan.Nodes[0].OutputDeclarations[0].CandidatePath = "usr/bin/python3" }, want: "absolute Linux path"},
		{name: "missing explicit edge", mutate: func(plan *ProviderPlanV1) { plan.Edges = []ProviderEdgeV1{} }, want: "no structural edge"},
		{name: "wrong output", mutate: func(plan *ProviderPlanV1) { plan.Edges[0].Output.Name = "python3" }, want: "does not select"},
		{name: "unknown supplier", mutate: func(plan *ProviderPlanV1) { plan.Edges[0].Supplier = "apt" }, want: "unknown node"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneProviderPlanForTest(valid)
			test.mutate(&candidate)
			if err := ValidateProviderPlanV1(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateProviderPlanV1RejectsStructuralCycle(t *testing.T) {
	firstRequirement := ExecutableRequirement{ID: "first", Command: "tool", Supplier: "second", ValidationPolicy: ValidationPolicyCompatible}
	secondRequirement := ExecutableRequirement{ID: "second", Command: "tool", Supplier: "first", ValidationPolicy: ValidationPolicyCompatible}
	first := pythonPlanNode("first", firstRequirement)
	first.OutputDeclarations = []OutputDeclaration{{SupplierComponent: "first", Name: "tool", Kind: OutputKindExecutable, CandidatePath: "/opt/first/tool", Provenance: providerData("python-output-v1")}}
	second := pythonPlanNode("second", secondRequirement)
	second.OutputDeclarations = []OutputDeclaration{{SupplierComponent: "second", Name: "tool", Kind: OutputKindExecutable, CandidatePath: "/opt/second/tool", Provenance: providerData("python-output-v1")}}
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{basePlanNode(), first, second},
		Edges: []ProviderEdgeV1{
			{Supplier: "python/first", Consumer: "python/second", RequirementID: "second", Output: QualifiedOutput{Component: "first", Name: "tool"}},
			{Supplier: "python/second", Consumer: "python/first", RequirementID: "first", Output: QualifiedOutput{Component: "second", Name: "tool"}},
		},
	}
	if err := ValidateProviderPlanV1(plan); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeProviderPlanV1SortsWithoutMutatingInput(t *testing.T) {
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1,
		Nodes:  []NodeSpec{pythonPlanNode("z", ExecutableRequirement{}), basePlanNode(), pythonPlanNode("a", ExecutableRequirement{})},
		Edges: []ProviderEdgeV1{
			{Supplier: "python/z", Consumer: "python/a", RequirementID: "z"},
			{Supplier: "base", Consumer: "python/a", RequirementID: "a"},
		},
	}
	normalized := NormalizeProviderPlanV1(plan)
	if got := []NodeID{normalized.Nodes[0].ID, normalized.Nodes[1].ID, normalized.Nodes[2].ID}; !reflect.DeepEqual(got, []NodeID{"base", "python/a", "python/z"}) {
		t.Fatalf("node order = %#v", got)
	}
	if normalized.Edges[0].Supplier != "base" || plan.Nodes[0].ID != "python/z" {
		t.Fatalf("normalize result = %#v; input = %#v", normalized, plan)
	}
}

func TestNormalizeProviderPlanV1PreservesEmptyCollections(t *testing.T) {
	plan := ProviderPlanV1{
		Schema: ProviderPlanSchemaV1, Nodes: []NodeSpec{basePlanNode()}, Edges: []ProviderEdgeV1{},
	}
	normalized := NormalizeProviderPlanV1(plan)
	if normalized.Nodes == nil || normalized.Edges == nil {
		t.Fatalf("normalized collections = nodes %#v, edges %#v", normalized.Nodes, normalized.Edges)
	}
	if err := ValidateProviderPlanV1(normalized); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNodeSpecRejectsReservedBaseComponent(t *testing.T) {
	node := pythonPlanNode("base", ExecutableRequirement{})
	if err := ValidateNodeSpec(node); err == nil || !strings.Contains(err.Error(), "reserved base") {
		t.Fatalf("error = %v", err)
	}
}

func cloneProviderPlanForTest(plan ProviderPlanV1) ProviderPlanV1 {
	result := plan
	result.Nodes = append([]NodeSpec(nil), plan.Nodes...)
	for index := range result.Nodes {
		result.Nodes[index].Components = append([]string{}, plan.Nodes[index].Components...)
		result.Nodes[index].OutputDeclarations = append([]OutputDeclaration{}, plan.Nodes[index].OutputDeclarations...)
		result.Nodes[index].Requirements.Executables = append([]ExecutableRequirement{}, plan.Nodes[index].Requirements.Executables...)
		result.Nodes[index].Requirements.Files = append([]FileRequirement{}, plan.Nodes[index].Requirements.Files...)
	}
	result.Edges = append([]ProviderEdgeV1(nil), plan.Edges...)
	return result
}
