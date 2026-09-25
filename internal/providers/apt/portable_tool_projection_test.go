package apt

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
)

const portableAPTTestDigest = canonical.Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111")

func TestProjectPortableToolAPTRootsV1SelectedTargetPackageSets(t *testing.T) {
	for _, test := range []struct {
		name, selected, present, absent string
	}{
		{"debian", "debian-12-amd64", "libasound2", "libasound2t64"},
		{"ubuntu", "ubuntu-t64-amd64", "libasound2t64", "libasound2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := portableAPTPlanForTest(t, test.selected)
			components, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{})
			if err != nil {
				t.Fatal(err)
			}
			if len(components) != 1 || components[0].Component != "application/browser/os" {
				t.Fatalf("projected components = %#v", components)
			}
			roots, err := ResolveRootOperandsV1(components[0].Request)
			if err != nil {
				t.Fatal(err)
			}
			selected := plan.Tools[0].Responsibilities.NativePackageSets[0]
			var record portabletool.NativePackageSetV1
			encoded, err := canonical.Marshal(selected.Record.Value)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &record); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roots, record.Requirements) {
				t.Fatalf("APT roots differ from exact selected %s record:\n got %v\nwant %v", test.selected, roots, record.Requirements)
			}
			if !containsAPTRoot(roots, test.present) || containsAPTRoot(roots, test.absent) {
				t.Fatalf("wrong target roots: %v", roots)
			}
			again, err := ProjectPortableToolAPTRootsV1(plan, components)
			if err != nil {
				t.Fatal(err)
			}
			firstBytes, _ := canonical.Marshal(components)
			againBytes, _ := canonical.Marshal(again)
			if !bytes.Equal(firstBytes, againBytes) {
				t.Fatal("projecting the same selected roots twice changed the APT request")
			}
		})
	}
}

func TestProjectPortableToolAPTRootsV1MergesOrdinaryRootsWithoutMutatingInput(t *testing.T) {
	plan := portableAPTPlanForTest(t, "debian-12-amd64")
	input := []providers.ResolvedComponentRequestV1{portableAPTComponentForTest(t, "application/browser/os", "curl", "xvfb")}
	ordinary, err := decodeCanonicalProviderRequestV1(input[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	for index := range ordinary.Components[0].Packages {
		if ordinary.Components[0].Packages[index].Name == "xvfb" {
			ordinary.Components[0].Packages[index].Exports["display"] = blueprint.ExecutableExport{Executable: "/usr/bin/Xvfb"}
		}
	}
	input[0].Request, err = CanonicalProviderRequestV1(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	before, err := canonical.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := ProjectPortableToolAPTRootsV1(plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 {
		t.Fatalf("projected components = %#v", projected)
	}
	roots, err := ResolveRootOperandsV1(projected[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAPTRoot(roots, "curl") || !containsAPTRoot(roots, "xvfb") || len(roots) != 34 {
		t.Fatalf("merged APT roots = %v", roots)
	}
	merged, err := decodeCanonicalProviderRequestV1(projected[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range merged.Components[0].Packages {
		if pkg.Name == "xvfb" && pkg.Exports["display"].Executable != "/usr/bin/Xvfb" {
			t.Fatalf("matching selected root discarded ordinary exports: %#v", pkg)
		}
	}
	again, err := ProjectPortableToolAPTRootsV1(plan, projected)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := canonical.Marshal(projected)
	againBytes, _ := canonical.Marshal(again)
	if !bytes.Equal(firstBytes, againBytes) {
		t.Fatal("projecting matching roots again changed ordinary exports")
	}
	after, err := canonical.Marshal(input)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("projection mutated input: %v", err)
	}
}

func TestProjectPortableToolAPTRootsV1RejectsSharedTransactionConflict(t *testing.T) {
	plan := portableAPTPlanForTest(t, "debian-12-amd64")
	other := portableAPTComponentForTest(t, "environment/os", "libasound2=1.0")
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{other}); err == nil || !strings.Contains(err.Error(), "conflicting declarations") {
		t.Fatalf("shared APT conflict was accepted: %v", err)
	}
	sameComponent := portableAPTComponentForTest(t, "application/browser/os", "xvfb=1.0")
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{sameComponent}); err == nil || !strings.Contains(err.Error(), "conflicting declarations") {
		t.Fatalf("different-version APT root was accepted: %v", err)
	}
}

func TestProjectPortableToolAPTRootsV1RejectsWrongScopeAndRecord(t *testing.T) {
	plan := portableAPTPlanForTest(t, "debian-12-amd64")
	plan.Tools[0].Scope = "source-builder:browser"
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), "application:<owner>") {
		t.Fatalf("non-application package root passed: %v", err)
	}
	plan = portableAPTPlanForTest(t, "debian-12-amd64")
	plan.Tools[0].Responsibilities.NativePackageSets[0].Record.Value["requirements"] = []any{"foreign-package"}
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil {
		t.Fatal("substituted package-set bytes passed selected record validation")
	}
}

func TestProjectPortableToolAPTRootsV1RejectsUnsupportedRepository(t *testing.T) {
	plan := portableAPTPlanForTest(t, "debian-12-amd64")
	selected := &plan.Tools[0].Responsibilities.NativePackageSets[0]
	selected.Record.Value["repositories"] = []any{"https://example.invalid/apt"}
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, selected.Record.Value)
	if err != nil {
		t.Fatal(err)
	}
	selected.Reference.Digest = digest
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{}); err == nil || !strings.Contains(err.Error(), "unsupported manager or repositories") {
		t.Fatalf("custom package repository was accepted: %v", err)
	}
}

func TestProjectPortableToolAPTRootsV1ChecksComponentOwnership(t *testing.T) {
	plan := portableAPTPlanForTest(t, "debian-12-amd64")
	valid := portableAPTComponentForTest(t, "application/browser/os", "curl")
	if _, err := ProjectPortableToolAPTRootsV1(plan, nil); err == nil || !strings.Contains(err.Error(), "array") {
		t.Fatalf("nil component list passed: %v", err)
	}
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{valid, valid}); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate component passed: %v", err)
	}
	wrongProvider := valid
	wrongProvider.Provider = blueprint.ComponentTypePython
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{wrongProvider}); err == nil || !strings.Contains(err.Error(), "provider does not match") {
		t.Fatalf("mismatched provider passed: %v", err)
	}
	other := valid
	other.Component = "environment/os"
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{other}); err == nil {
		t.Fatal("component with a request owned by another component passed")
	}
	occupied := valid
	occupied.Provider = blueprint.ComponentTypePython
	occupied.Request.Provider = blueprint.ComponentTypePython
	if _, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{occupied}); err == nil || !strings.Contains(err.Error(), "is owned by") {
		t.Fatalf("browser APT owner collision passed: %v", err)
	}
}

func TestProjectPortableToolAPTRootsV1LeavesUnselectedPlanAlone(t *testing.T) {
	plan := portableAPTPlanForTest(t, "debian-12-amd64")
	plan.Tools[0].Responsibilities.NativePackageSets = []providers.PortableToolSelectedRecordV1{}
	component := portableAPTComponentForTest(t, "environment/os", "curl")
	projected, err := ProjectPortableToolAPTRootsV1(plan, []providers.ResolvedComponentRequestV1{component})
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 || projected[0].Component != "environment/os" {
		t.Fatalf("unselected APT plan changed components: %#v", projected)
	}
}

func portableAPTPlanForTest(t *testing.T, name string) providers.PortableToolPlanV1 {
	t.Helper()
	data, err := os.ReadFile("../../toolcatalog/definitions/playwright/releases/1.61.0/package-sets/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var value canonical.Object
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, value)
	if err != nil {
		t.Fatal(err)
	}
	selected := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: value["id"].(string), Digest: digest},
		Record:    providers.CanonicalProviderData{Schema: portabletool.NativePackageSetSchemaV1, Value: value},
	}
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: "application:browser", SelectedClosureDigest: portableAPTTestDigest,
			Provenance: providers.PortableToolReleaseProvenanceV1{Tool: "playwright", Version: "1.61.0", Revision: "1", ManifestDigest: portableAPTTestDigest},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{}, BindingArtifacts: []providers.PortableToolSelectedRecordV1{},
				Payloads: []providers.PortableToolSelectedRecordV1{}, NativePackageSets: []providers.PortableToolSelectedRecordV1{selected},
			},
			Exports: []providers.PortableToolExportV1{}, ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
	if err := providers.ValidatePortableToolPlanV1(plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func portableAPTComponentForTest(t *testing.T, component string, roots ...string) providers.ResolvedComponentRequestV1 {
	t.Helper()
	packages := make([]blueprint.APTPackageRequest, 0, len(roots))
	for _, root := range roots {
		pkg, err := blueprint.ParseAPTPackageRequest(root)
		if err != nil {
			t.Fatal(err)
		}
		packages = append(packages, pkg)
	}
	request, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{{Component: component, Packages: packages}}})
	if err != nil {
		t.Fatal(err)
	}
	return providers.ResolvedComponentRequestV1{Component: component, Provider: blueprint.ComponentTypeAPT, Request: request}
}

func containsAPTRoot(roots []string, want string) bool {
	for _, root := range roots {
		if root == want {
			return true
		}
	}
	return false
}
