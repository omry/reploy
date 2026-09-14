package python

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
)

const portableToolProjectionSmokeDigest = canonical.Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111")

func TestProjectPortableToolPythonBindingsV1CreatesAndMergesCanonicalRequest(t *testing.T) {
	plan := portableToolPythonSmokePlanForTest(t)
	components, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 1 || components[0].Component != "application/demo/python" {
		t.Fatalf("components = %#v", components)
	}
	request, err := decodeCanonicalProviderRequestV1(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if request.Interpreter.Command != "python" || len(request.Requirements) != 1 {
		t.Fatalf("created request = %#v", request)
	}
	if len(projection.Components) != 1 || !reflect.DeepEqual(projection.Components[0].TestedTags, []string{"py3-none-manylinux1_x86_64"}) {
		t.Fatalf("projection = %#v", projection)
	}

	existingRequirement, err := CanonicalPackageRequestV1("existing==1")
	if err != nil {
		t.Fatal(err)
	}
	existingRequest, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component:    "application/demo/python",
		Interpreter:  blueprint.CommandRequirement{Command: "python313", Version: ">=3.13,<3.14", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{existingRequirement},
		Overrides:    []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := []providers.ResolvedComponentRequestV1{{
		Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: existingRequest,
	}}
	original, err := canonical.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	components, _, err = ProjectPortableToolPythonBindingsV1(plan, input)
	if err != nil {
		t.Fatal(err)
	}
	request, err = decodeCanonicalProviderRequestV1(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if request.Interpreter.Command != "python313" || request.Interpreter.Version != ">=3.13,<3.14" || request.Interpreter.Supplier != "base" || len(request.Requirements) != 2 {
		t.Fatalf("merged request = %#v", request)
	}
	after, err := canonical.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, after) {
		t.Fatal("projection mutated caller-owned component requests")
	}
}

func portableToolPythonSmokePlanForTest(t *testing.T) providers.PortableToolPlanV1 {
	t.Helper()
	const namespace = "tool:demo/releases/1.0.0"
	contract := portabletool.BindingContractV1{
		Schema: portabletool.BindingContractSchemaV1, ID: namespace + "/bindings/python/contract",
		Name: "python", Package: "demo", Requirements: []string{"demo>=1,<2"},
		SupportedPython: []string{"3.12"}, SupportedTags: []string{"py3-none-manylinux1_x86_64"},
		BundledComponents: []portabletool.BundledComponentV1{},
		CLI:               portabletool.ToolExportV1{Name: "demo", Path: "/opt/demo/bin/demo"},
	}
	contractRecord := portableToolProjectionSmokeRecordForTest(t, contract)
	contractReference := portableToolProjectionSmokeReferenceForTest(t, contract.ID, contractRecord)
	artifact := portabletool.BindingArtifactRecordV1{
		Schema: portabletool.BindingArtifactSchemaV1, ID: namespace + "/bindings/python/artifacts/linux-amd64",
		Binding: "python", Contract: contractReference, Name: "demo", EcosystemVersion: "1.0.0",
		Platform: "linux/amd64", Filename: "demo-1.0.0-py3-none-manylinux1_x86_64.whl",
		Size: "7", SHA256: portableToolProjectionSmokeDigest, Resolver: "https-sha256",
		Tags: []string{"py3-none-manylinux1_x86_64"}, RequiresPython: ">=3.12,<3.13",
		BundledComponents: []portabletool.BundledComponentV1{},
	}
	artifactRecord := portableToolProjectionSmokeRecordForTest(t, artifact)
	artifactReference := portableToolProjectionSmokeReferenceForTest(t, artifact.ID, artifactRecord)
	return providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: "application:demo", SelectedClosureDigest: portableToolProjectionSmokeDigest,
			Provenance: providers.PortableToolReleaseProvenanceV1{Tool: "demo", Version: "1.0.0", Revision: "1", ManifestDigest: portableToolProjectionSmokeDigest},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{{Reference: contractReference, Record: contractRecord}},
				BindingArtifacts: []providers.PortableToolSelectedRecordV1{{Reference: artifactReference, Record: artifactRecord}},
				Payloads:         []providers.PortableToolSelectedRecordV1{}, NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports:            []providers.PortableToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}},
			ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
}

func portableToolProjectionSmokeRecordForTest(t *testing.T, value any) providers.CanonicalProviderData {
	t.Helper()
	encoded, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object canonical.Object
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	schema, ok := object["schema"].(string)
	if !ok {
		t.Fatal("record schema is missing")
	}
	return providers.CanonicalProviderData{Schema: schema, Value: object}
}

func portableToolProjectionSmokeReferenceForTest(t *testing.T, id string, record providers.CanonicalProviderData) providers.PortableToolRecordReferenceV1 {
	t.Helper()
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, record.Value)
	if err != nil {
		t.Fatal(err)
	}
	return providers.PortableToolRecordReferenceV1{ID: id, Digest: digest}
}
