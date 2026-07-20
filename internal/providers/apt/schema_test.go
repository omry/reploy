package apt

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func aptSchemaDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func TestCanonicalAPTProviderRequestSortsComponentsAndPackages(t *testing.T) {
	request, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{
		{Component: "tools", Packages: []blueprint.APTPackageRequest{{Name: "zlib1g"}, {Name: "curl"}}},
		{Component: "system", Packages: []blueprint.APTPackageRequest{{Name: "ca-certificates"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanonicalProviderRequestV1(request); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCanonicalProviderRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Components) != 2 || decoded.Components[0].Component != "system" || decoded.Components[1].Packages[0].Name != "curl" {
		t.Fatalf("decoded = %#v", decoded)
	}
	request.Value["unknown"] = true
	if err := ValidateCanonicalProviderRequestV1(request); err == nil {
		t.Fatal("unknown APT provider request field was accepted")
	}
}

func TestCanonicalAPTProviderRequestRejectsEmptyAndConflictingComponents(t *testing.T) {
	for name, request := range map[string]APTProviderRequestV1{
		"no components":   {},
		"empty component": {Components: []APTComponentRequestV1{{Component: "system"}}},
		"conflicting package": {Components: []APTComponentRequestV1{{
			Component: "system",
			Packages: []blueprint.APTPackageRequest{
				{Name: "python3"},
				{Name: "python3", Version: "3.11.2-1"},
			},
		}}},
		"cross-component conflict": {Components: []APTComponentRequestV1{
			{Component: "system", Packages: []blueprint.APTPackageRequest{{Name: "python3"}}},
			{Component: "tools", Packages: []blueprint.APTPackageRequest{{Name: "python3", Version: "3.11.2-1"}}},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalProviderRequestV1(request); err == nil {
				t.Fatal("invalid APT provider request was accepted")
			}
		})
	}
}

func TestAPTProviderPlansSharedAuthorityAndDeclaredOutputs(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	systemRequest, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{{
		Component: "system",
		Packages: []blueprint.APTPackageRequest{{Name: "python3", Exports: map[string]blueprint.ExecutableExport{
			"python": {Executable: "/custom/python3"},
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	toolsRequest, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{{
		Component: "tools",
		Packages: []blueprint.APTPackageRequest{{Name: "curl", Exports: map[string]blueprint.ExecutableExport{
			"curl": {Executable: "/usr/bin/curl"},
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := (ComponentProvider{}).Plan(providers.PlanInput{
		Platform: platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: "tools", Provider: blueprint.ComponentTypeAPT, Request: toolsRequest},
			{Component: "system", Provider: blueprint.ComponentTypeAPT, Request: systemRequest},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "apt" || len(nodes[0].Components) != 2 || len(nodes[0].OutputDeclarations) != 2 {
		t.Fatalf("nodes = %#v", nodes)
	}
	pythonOutput := nodes[0].OutputDeclarations[0]
	if pythonOutput.SupplierComponent != "system" || pythonOutput.Name != "python" || pythonOutput.CandidatePath != "/custom/python3" || pythonOutput.Provenance.Schema != WellKnownToolSchemaV1 {
		t.Fatalf("Python output = %#v", pythonOutput)
	}
	if nodes[0].OutputDeclarations[1].Provenance.Schema != ExplicitExportSchemaV1 {
		t.Fatalf("curl output = %#v", nodes[0].OutputDeclarations[1])
	}
}
