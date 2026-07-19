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
	if err := validators.Profile(providers.RequirementProfile{}); err == nil || !strings.Contains(err.Error(), "Python profile request") {
		t.Fatalf("profile validator error = %v", err)
	}
	if err := validators.Bundle(providers.ResolvedBundleIdentityV1{}); err == nil || !strings.Contains(err.Error(), "Python bundle recipe version") {
		t.Fatalf("bundle validator error = %v", err)
	}
}

func TestExecutionDispatchRejectsProvidersWithoutSliceThreeExecution(t *testing.T) {
	nodes := executionTestNodes(t)
	for _, provider := range []blueprint.ComponentType{blueprint.ComponentTypeBase, blueprint.ComponentTypeAPT} {
		t.Run(string(provider), func(t *testing.T) {
			if _, err := OwnerValidatorsForNode(nodes[provider]); err == nil {
				t.Fatal("owner validators unexpectedly accepted provider")
			}
			if _, err := MaterializeNode(nodes[provider], providers.MaterializeInput{}); err == nil {
				t.Fatal("materialization unexpectedly accepted provider")
			}
		})
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
