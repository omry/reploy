package python

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
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

func TestProjectPortableToolPythonBindingsV1NormalizesOrderWithoutMutatingInputs(t *testing.T) {
	canonicalPlan := portableToolPythonTwoBindingPlanForTest(t)
	canonicalComponents, _, err := ProjectPortableToolPythonBindingsV1(canonicalPlan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	reversedPlan := portableToolPythonTwoBindingPlanForTest(t)
	for index := range reversedPlan.Tools {
		reversedPlan.Tools[index].Responsibilities.BindingContracts = append([]providers.PortableToolSelectedRecordV1{}, reversedPlan.Tools[index].Responsibilities.BindingContracts...)
		reversedPlan.Tools[index].Responsibilities.BindingArtifacts = append([]providers.PortableToolSelectedRecordV1{}, reversedPlan.Tools[index].Responsibilities.BindingArtifacts...)
		reversePortableToolProjectionRecordsForTest(reversedPlan.Tools[index].Responsibilities.BindingContracts)
		reversePortableToolProjectionRecordsForTest(reversedPlan.Tools[index].Responsibilities.BindingArtifacts)
	}
	for left, right := 0, len(reversedPlan.Tools)-1; left < right; left, right = left+1, right-1 {
		reversedPlan.Tools[left], reversedPlan.Tools[right] = reversedPlan.Tools[right], reversedPlan.Tools[left]
	}
	reversedComponents := []providers.ResolvedComponentRequestV1{canonicalComponents[1], canonicalComponents[0]}

	canonicalPlanBefore, err := canonical.Marshal(canonicalPlan)
	if err != nil {
		t.Fatal(err)
	}
	canonicalComponentsBefore, err := canonical.Marshal(canonicalComponents)
	if err != nil {
		t.Fatal(err)
	}
	reversedPlanBefore, err := canonical.Marshal(reversedPlan)
	if err != nil {
		t.Fatal(err)
	}
	reversedComponentsBefore, err := canonical.Marshal(reversedComponents)
	if err != nil {
		t.Fatal(err)
	}

	projectedCanonical, projectionCanonical, err := ProjectPortableToolPythonBindingsV1(canonicalPlan, canonicalComponents)
	if err != nil {
		t.Fatal(err)
	}
	projectedReversed, projectionReversed, err := ProjectPortableToolPythonBindingsV1(reversedPlan, reversedComponents)
	if err != nil {
		t.Fatal(err)
	}
	canonicalProjectedBytes, err := canonical.Marshal(projectedCanonical)
	if err != nil {
		t.Fatal(err)
	}
	reversedProjectedBytes, err := canonical.Marshal(projectedReversed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonicalProjectedBytes, reversedProjectedBytes) {
		t.Fatalf("projected component requests differ:\n%s\n%s", canonicalProjectedBytes, reversedProjectedBytes)
	}
	canonicalSidecar, err := CanonicalPortableToolPythonProjectionBytesV1(projectionCanonical)
	if err != nil {
		t.Fatal(err)
	}
	reversedSidecar, err := CanonicalPortableToolPythonProjectionBytesV1(projectionReversed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonicalSidecar, reversedSidecar) {
		t.Fatalf("projection sidecars differ:\n%s\n%s", canonicalSidecar, reversedSidecar)
	}

	for name, item := range map[string]struct {
		value  any
		before []byte
	}{
		"canonical plan":       {canonicalPlan, canonicalPlanBefore},
		"canonical components": {canonicalComponents, canonicalComponentsBefore},
		"reversed plan":        {reversedPlan, reversedPlanBefore},
		"reversed components":  {reversedComponents, reversedComponentsBefore},
	} {
		after, err := canonical.Marshal(item.value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(item.before, after) {
			t.Fatalf("projection mutated caller-owned %s", name)
		}
	}
}

func TestCanonicalPortableToolPythonProjectionBytesV1RejectsDisjointComponentPythonClaims(t *testing.T) {
	_, projection, err := ProjectPortableToolPythonBindingsV1(
		portableToolPythonTwoBindingPlanForTest(t),
		[]providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		t.Fatal(err)
	}
	component := &projection.Components[0]
	if component.Component != "application/demo/python" || len(component.Bindings) != 2 {
		t.Fatalf("projection component = %#v, want two demo bindings", component)
	}
	component.Bindings[1].SupportedPython = []string{"3.13"}
	component.Bindings[1].Wheel.RequiresPython = ">=3.13,<3.14"

	_, err = CanonicalPortableToolPythonProjectionBytesV1(projection)
	if err == nil || !strings.Contains(err.Error(), "bindings have no common supported Python claim") {
		t.Fatalf("error = %v, want empty component-wide supported-Python intersection", err)
	}
}

func TestProjectPortableToolPythonBindingsV1ChecksDirectWheelDependencyRoots(t *testing.T) {
	plan := portableToolPythonSmokePlanForTest(t)
	contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
	contract.Record.Value["requirements"] = []any{"demo>=1,<2", "shared<2"}
	portableToolProjectionSmokeRebindRecordForTest(t, contract)
	artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["contract"] = map[string]any{
		"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
	}
	portableToolProjectionSmokeRebindRecordForTest(t, artifact)

	for _, test := range []struct {
		name        string
		requirement string
		wantError   string
	}{
		{name: "compatible", requirement: "shared @ https://example.invalid/shared-1.5-py3-none-any.whl"},
		{name: "compatible URL semicolon", requirement: "shared @ https://example.invalid/cache;v=1/shared-1.5-py3-none-any.whl"},
		{name: "compatible URL semicolon and marker", requirement: "shared @ https://example.invalid/cache;v=1/shared-1.5-py3-none-any.whl ; python_version >= '3.12'"},
		{name: "conflicting", requirement: "shared @ https://example.invalid/shared-3.0-py3-none-any.whl", wantError: "portable Python requirements for \"shared\" conflict"},
		{name: "conflicting URL semicolon", requirement: "shared @ https://example.invalid/cache;v=1/shared-3.0-py3-none-any.whl", wantError: "portable Python requirements for \"shared\" conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requirement, err := CanonicalPackageRequestV1(test.requirement)
			if err != nil {
				t.Fatal(err)
			}
			request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
				Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
				Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
				Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
			}})
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("compatible direct wheel failed: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("direct wheel conflict error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestPortableToolPythonRequirementBodyV1SeparatesMarkersFromDirectReferenceURLs(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "ordinary requirement marker",
			value: "shared<2; python_version >= '3.12'",
			want:  "shared<2",
		},
		{
			name:  "ordinary marker containing URL-like text",
			value: "shared<2; platform_version == 'build+tag:1'",
			want:  "shared<2",
		},
		{
			name:  "URL semicolon",
			value: "shared @ https://example.invalid/cache;v=1/shared-1.5-py3-none-any.whl",
			want:  "shared @ https://example.invalid/cache;v=1/shared-1.5-py3-none-any.whl",
		},
		{
			name:  "URL semicolon before marker",
			value: "shared @ https://example.invalid/cache;v=1/shared-1.5-py3-none-any.whl ; python_version >= '3.12'",
			want:  "shared @ https://example.invalid/cache;v=1/shared-1.5-py3-none-any.whl",
		},
		{
			name:  "bare VCS URL semicolon",
			value: "git+https://example.invalid/repo.git;variant=one#egg=shared",
			want:  "git+https://example.invalid/repo.git;variant=one#egg=shared",
		},
		{
			name:  "bare VCS URL semicolon before marker",
			value: "git+https://example.invalid/repo.git;variant=one#egg=shared ; python_version >= '3.12'",
			want:  "git+https://example.invalid/repo.git;variant=one#egg=shared",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := portableToolPythonRequirementBodyV1(test.value); got != test.want {
				t.Fatalf("body = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProjectPortableToolPythonBindingsV1KeepsOrdinaryMarkersSeparateFromDirectURLs(t *testing.T) {
	const value = "existing<2; platform_version == 'build+tag:1'"
	components, err := projectPortableToolPythonRequirementForTest(t, portableToolPythonSmokePlanForTest(t), value)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := decodeCanonicalProviderRequestV1(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range merged.Requirements {
		if candidate.Value["requirement"] == value {
			return
		}
	}
	t.Fatalf("merged requirements = %#v, want preserved %q", merged.Requirements, value)
}

func TestProjectPortableToolPythonBindingsV1PreservesNamedNonWheelDirectReferences(t *testing.T) {
	plan := portableToolPythonSmokePlanForTest(t)
	for _, value := range []string{
		"existing @ https://example.invalid/existing-1.tar.gz",
		"existing @ git+https://example.invalid/repo.git",
		"existing @ git+https://example.invalid/repo.git#subdirectory=packages/existing;variant=one",
		"existing @ git+https://example.invalid/repo.git#subdirectory=packages/existing;variant=one ; python_version >= '3.12'",
		"git+https://example.invalid/repo.git#egg=existing",
		"git+https://example.invalid/repo.git#egg=Existing_Pkg",
		"git+https://example.invalid/repo.git;variant=one#egg=existing",
		"git+https://example.invalid/repo.git;variant=one#egg=existing ; python_version >= '3.12'",
	} {
		t.Run(value, func(t *testing.T) {
			components, err := projectPortableToolPythonRequirementForTest(t, plan, value)
			if err != nil {
				t.Fatal(err)
			}
			merged, err := decodeCanonicalProviderRequestV1(components[0].Request)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, candidate := range merged.Requirements {
				if candidate.Value["requirement"] == value {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("merged requirements = %#v, want preserved %q", merged.Requirements, value)
			}
		})
	}

	for _, value := range []string{
		"git+https://example.invalid/repo.git#egg=existing-",
		"git+https://example.invalid/repo.git#egg=existing.",
		"git+https://example.invalid/repo.git#egg=existing_",
		"git+https://example.invalid/repo.git#egg=%20existing%20",
		"git+https://example.invalid/repo.git#egg=existing&egg=other",
	} {
		t.Run("reject "+value, func(t *testing.T) {
			_, err := projectPortableToolPythonRequirementForTest(t, plan, value)
			if err == nil || !strings.Contains(err.Error(), "unverifiable direct URL") {
				t.Fatalf("error = %v, want unverifiable direct URL", err)
			}
		})
	}

	t.Run("selected wheel collision", func(t *testing.T) {
		_, err := projectPortableToolPythonRequirementForTest(t, plan, "git+https://example.invalid/repo.git#egg=DEMO")
		if err == nil || !strings.Contains(err.Error(), "direct URL Python package requirement for portable distribution \"demo\" conflicts") {
			t.Fatalf("error = %v, want selected wheel conflict", err)
		}
	})

	t.Run("dependency overlap requires source version", func(t *testing.T) {
		dependencyPlan := portableToolPythonSmokePlanForTest(t)
		contract := &dependencyPlan.Tools[0].Responsibilities.BindingContracts[0]
		contract.Record.Value["requirements"] = []any{"demo>=1,<2", "shared<2"}
		portableToolProjectionSmokeRebindRecordForTest(t, contract)
		artifact := &dependencyPlan.Tools[0].Responsibilities.BindingArtifacts[0]
		artifact.Record.Value["contract"] = map[string]any{
			"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
		}
		portableToolProjectionSmokeRebindRecordForTest(t, artifact)

		_, err := projectPortableToolPythonRequirementForTest(t, dependencyPlan, "git+https://example.invalid/repo.git#egg=shared")
		if err == nil || !strings.Contains(err.Error(), "portable Python requirements for \"shared\"") {
			t.Fatalf("error = %v, want dependency compatibility proof failure", err)
		}
	})
}

func projectPortableToolPythonRequirementForTest(
	t *testing.T,
	plan providers.PortableToolPlanV1,
	value string,
) ([]providers.ResolvedComponentRequestV1, error) {
	t.Helper()
	requirement, err := CanonicalPackageRequestV1(value)
	if err != nil {
		return nil, err
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application/demo/python", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement}, Overrides: []PythonPackageOverrideV1{},
	})
	if err != nil {
		return nil, err
	}
	components, _, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{{
		Component: "application/demo/python", Provider: blueprint.ComponentTypePython, Request: request,
	}})
	return components, err
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

func portableToolPythonTwoBindingPlanForTest(t *testing.T) providers.PortableToolPlanV1 {
	t.Helper()
	plan := portableToolPythonSmokePlanForTest(t)
	const namespace = "tool:demo/releases/1.0.0"
	contract := portabletool.BindingContractV1{
		Schema: portabletool.BindingContractSchemaV1, ID: namespace + "/bindings/zpython/contract",
		Name: "zpython", Package: "other", Requirements: []string{"other>=1,<2"},
		SupportedPython: []string{"3.12"}, SupportedTags: []string{"py3-none-manylinux1_x86_64"},
		BundledComponents: []portabletool.BundledComponentV1{},
		CLI:               portabletool.ToolExportV1{Name: "zother", Path: "/opt/demo/bin/zother"},
	}
	contractRecord := portableToolProjectionSmokeRecordForTest(t, contract)
	contractReference := portableToolProjectionSmokeReferenceForTest(t, contract.ID, contractRecord)
	artifact := portabletool.BindingArtifactRecordV1{
		Schema: portabletool.BindingArtifactSchemaV1, ID: namespace + "/bindings/zpython/artifacts/linux-amd64",
		Binding: "zpython", Contract: contractReference, Name: "other", EcosystemVersion: "1.0.0",
		Platform: "linux/amd64", Filename: "other-1.0.0-py3-none-manylinux1_x86_64.whl",
		Size: "7", SHA256: portableToolProjectionSmokeDigest, Resolver: "https-sha256",
		Tags: []string{"py3-none-manylinux1_x86_64"}, RequiresPython: ">=3.12,<3.13",
		BundledComponents: []portabletool.BundledComponentV1{},
	}
	artifactRecord := portableToolProjectionSmokeRecordForTest(t, artifact)
	artifactReference := portableToolProjectionSmokeReferenceForTest(t, artifact.ID, artifactRecord)
	plan.Tools[0].Responsibilities.BindingContracts = append(
		plan.Tools[0].Responsibilities.BindingContracts,
		providers.PortableToolSelectedRecordV1{Reference: contractReference, Record: contractRecord},
	)
	plan.Tools[0].Responsibilities.BindingArtifacts = append(
		plan.Tools[0].Responsibilities.BindingArtifacts,
		providers.PortableToolSelectedRecordV1{Reference: artifactReference, Record: artifactRecord},
	)
	plan.Tools[0].Exports = append(plan.Tools[0].Exports, providers.PortableToolExportV1{Name: "zother", Path: "/opt/demo/bin/zother"})
	second := portableToolPythonSmokePlanForTest(t).Tools[0]
	second.Scope = "application:other"
	plan.Tools = append(plan.Tools, second)
	return plan
}

func reversePortableToolProjectionRecordsForTest(records []providers.PortableToolSelectedRecordV1) {
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
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

func portableToolProjectionSmokeRebindRecordForTest(t *testing.T, selected *providers.PortableToolSelectedRecordV1) {
	t.Helper()
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, selected.Record.Value)
	if err != nil {
		t.Fatal(err)
	}
	selected.Reference.Digest = digest
}
