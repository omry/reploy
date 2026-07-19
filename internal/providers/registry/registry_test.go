package registry

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func registryDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func TestPlanBuildsBaseAPTAndPythonGraph(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	base, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image:   "debian:13",
		Exports: map[string]blueprint.BaseExecutableExport{"sh": {Executable: "/bin/sh"}},
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
		Component:    "application",
		Interpreter:  blueprint.CommandRequirement{Command: "python", Supplier: "system"},
		Requirements: []providers.CanonicalPackageRequest{pythonPackage},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(providers.PlanInput{
		BlueprintDigest: registryDigest("a"), Platform: platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: "application", Provider: blueprint.ComponentTypePython, Request: pythonRequest},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: base},
			{Component: "system", Provider: blueprint.ComponentTypeAPT, Request: aptRequest},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := []providers.NodeID{plan.Nodes[0].ID, plan.Nodes[1].ID, plan.Nodes[2].ID}
	if !reflect.DeepEqual(ids, []providers.NodeID{"apt", "base", "python/application"}) {
		t.Fatalf("node IDs = %#v", ids)
	}
	if len(plan.Edges) != 1 || plan.Edges[0].Supplier != "apt" || plan.Edges[0].Consumer != "python/application" || plan.Edges[0].Output.Component != "system" {
		t.Fatalf("edges = %#v", plan.Edges)
	}
	if err := providers.ValidateProviderPlanV1(plan); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRejectsMissingBase(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	input := providers.PlanInput{BlueprintDigest: registryDigest("b"), Platform: platform, Components: []providers.ResolvedComponentRequestV1{}}
	if _, err := Plan(input); err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("missing base error = %v", err)
	}
}

func TestBuildProviderPlanRejectsUnclaimedComponent(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	base, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{}})
	if err != nil {
		t.Fatal(err)
	}
	pythonPackage, err := pythonprovider.CanonicalPackageRequestV1("demo==1")
	if err != nil {
		t.Fatal(err)
	}
	pythonRequest, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{pythonPackage},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := providers.PlanInput{
		BlueprintDigest: registryDigest("c"), Platform: platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: base},
			{Component: "application", Provider: blueprint.ComponentTypePython, Request: pythonRequest},
		},
	}
	if _, err := providers.BuildProviderPlanV1(input); err == nil || !strings.Contains(err.Error(), "not claimed") {
		t.Fatalf("unclaimed component error = %v", err)
	}
}
