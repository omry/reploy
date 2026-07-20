package registry

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func executionTestNodes(t *testing.T) map[blueprint.ComponentType]providers.NodeSpec {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	base, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{},
	})
	if err != nil {
		t.Fatal(err)
	}
	aptRequest, err := apt.CanonicalProviderRequestV1(apt.APTProviderRequestV1{Components: []apt.APTComponentRequestV1{{
		Component: "system", Packages: []blueprint.APTPackageRequest{{Name: "python3"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	pythonPackage, err := pythonprovider.CanonicalPackageRequestV1("demo==1")
	if err != nil {
		t.Fatal(err)
	}
	pythonRequest, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "system"},
		Requirements: []providers.CanonicalPackageRequest{pythonPackage},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(providers.PlanInput{
		BlueprintDigest: registryDigest("e"), Platform: platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: "application", Provider: blueprint.ComponentTypePython, Request: pythonRequest},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: base},
			{Component: "system", Provider: blueprint.ComponentTypeAPT, Request: aptRequest},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	nodes := make(map[blueprint.ComponentType]providers.NodeSpec, len(plan.Nodes))
	for _, node := range plan.Nodes {
		nodes[node.Provider] = node
	}
	return nodes
}

func TestOwnerValidatorsForNodeDispatchesPython(t *testing.T) {
	node := executionTestNodes(t)[blueprint.ComponentTypePython]
	validators, err := OwnerValidatorsForNode(node)
	if err != nil {
		t.Fatal(err)
	}
	if validators.Profile == nil || validators.Bundle == nil {
		t.Fatalf("Python validators = %#v", validators)
	}
	if err := validators.Profile(providers.RequirementProfile{Provider: blueprint.ComponentTypePython}); err == nil || !strings.Contains(err.Error(), "Python profile request") {
		t.Fatalf("profile validator error = %v", err)
	}
	if err := validators.Bundle(providers.ResolvedBundleIdentityV1{}); err == nil || !strings.Contains(err.Error(), "Python bundle recipe version") {
		t.Fatalf("bundle validator error = %v", err)
	}
}

func TestValidateRequirementProfileV1DispatchesMixedProviderOwners(t *testing.T) {
	aptProfile := providers.RequirementProfile{Provider: blueprint.ComponentTypeAPT, Declaration: providers.RequirementDeclaration{
		ProviderData: providers.CanonicalProviderData{Schema: apt.ProviderRequestSchemaV1},
	}}
	if err := ValidateRequirementProfileV1(aptProfile); err == nil || !strings.Contains(err.Error(), "APT profile request") {
		t.Fatalf("APT profile dispatch error = %v", err)
	}
	pythonProfile := providers.RequirementProfile{Provider: blueprint.ComponentTypePython, Declaration: providers.RequirementDeclaration{
		ProviderData: providers.CanonicalProviderData{Schema: pythonprovider.ProviderRequestSchemaV1},
	}}
	if err := ValidateRequirementProfileV1(pythonProfile); err == nil || !strings.Contains(err.Error(), "Python profile request") {
		t.Fatalf("Python profile dispatch error = %v", err)
	}
	unknown := providers.RequirementProfile{Provider: blueprint.ComponentType("other"), Declaration: providers.RequirementDeclaration{
		ProviderData: providers.CanonicalProviderData{Schema: "other-provider-request-v1"},
	}}
	if err := ValidateRequirementProfileV1(unknown); err == nil || !strings.Contains(err.Error(), "unsupported requirement profile") {
		t.Fatalf("unknown profile dispatch error = %v", err)
	}
}

func TestExecutionDispatchRejectsBaseExecution(t *testing.T) {
	nodes := executionTestNodes(t)
	if _, err := OwnerValidatorsForNode(nodes[blueprint.ComponentTypeBase]); err == nil {
		t.Fatal("owner validators unexpectedly accepted base")
	}
	if _, err := MaterializeNode(nodes[blueprint.ComponentTypeBase], providers.MaterializeInput{}); err == nil {
		t.Fatal("materialization unexpectedly accepted base")
	}
}

func TestExecutionDispatchesAPT(t *testing.T) {
	node := executionTestNodes(t)[blueprint.ComponentTypeAPT]
	validators, err := OwnerValidatorsForNode(node)
	if err != nil || validators.Profile == nil || validators.Bundle == nil {
		t.Fatalf("APT validators = %#v, err = %v", validators, err)
	}
	if _, err := MaterializeNode(node, providers.MaterializeInput{}); err == nil || !strings.Contains(err.Error(), "materialize APT input") {
		t.Fatalf("APT materialization dispatch error = %v", err)
	}
}

func TestMaterializeNodeDispatchesPython(t *testing.T) {
	node := executionTestNodes(t)[blueprint.ComponentTypePython]
	if _, err := MaterializeNode(node, providers.MaterializeInput{}); err == nil || !strings.Contains(err.Error(), "materialize Python input") {
		t.Fatalf("Python materialization dispatch error = %v", err)
	}
}

func TestValidateMaterializationNodeBindingRejectsPlanDrift(t *testing.T) {
	node := executionTestNodes(t)[blueprint.ComponentTypePython]
	input := providers.MaterializeInput{Bundle: providers.ResolvedBundle{Payload: providers.ResolvedBundleIdentityV1{
		NodeID: node.ID, Provider: node.Provider, Request: node.Request,
	}}}
	if err := validateMaterializationNodeBinding(node, input); err != nil {
		t.Fatal(err)
	}

	input.Bundle.Payload.NodeID = "python/other"
	if err := validateMaterializationNodeBinding(node, input); err == nil || !strings.Contains(err.Error(), "does not match planned node") {
		t.Fatalf("node drift error = %v", err)
	}
}
