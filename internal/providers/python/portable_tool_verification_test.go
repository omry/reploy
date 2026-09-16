package python

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/wheelinventory"
)

func TestVerifyPortableToolPythonBindingV1RetainsAuthenticatedObservations(t *testing.T) {
	input, wantDescriptor := portableToolPythonBindingVerificationFixtureV1(t, "")
	verified, err := VerifyPortableToolPythonBindingV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Descriptor != wantDescriptor || verified.Inspection.Artifact != wantDescriptor ||
		!reflect.DeepEqual(verified.EligibleFilenameTags, []string{"py3-none-manylinux1_x86_64"}) ||
		verified.ConsoleScript != (WheelConsoleScriptV1{Name: "demo", Target: "demo.cli:main"}) {
		t.Fatalf("verified result = %#v", verified)
	}

	// Returned observations are detached from the caller's slices so carrying
	// them into a later lock stage cannot mutate the verification input.
	verified.EligibleFilenameTags[0] = "changed"
	verified.Inspection.InternalTags[0] = "changed"
	if input.Inspection.InternalTags[0] == "changed" {
		t.Fatal("verification result aliases input inspection")
	}
}

// The catalog-pinned Playwright wheel declares py3-none-any in its WHEEL
// metadata, while its filename declares py3-none-manylinux1_x86_64.
// Verify the real selected records and observed wheel at the same boundary.
// The optional exact-artifact branch can be run with the catalog-pinned wheel
// without storing a 47 MB fixture in the repository.
func TestVerifyPortableToolPythonBindingV1SelectedPlaywrightWheel(t *testing.T) {
	contractRecord := portableToolProjectionDefinitionRecordForTest(t,
		"../../toolcatalog/definitions/playwright/releases/1.61.0/bindings/python/contract.json")
	artifactRecord := portableToolProjectionDefinitionRecordForTest(t,
		"../../toolcatalog/definitions/playwright/releases/1.61.0/bindings/python/linux-amd64.json")
	contractReference := portableToolProjectionReferenceForTest(t, contractRecord.Value["id"].(string), contractRecord)
	artifactReference := portableToolProjectionReferenceForTest(t, artifactRecord.Value["id"].(string), artifactRecord)
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: "application:browser", SelectedClosureDigest: portableToolProjectionTestDigest,
			Provenance: providers.PortableToolReleaseProvenanceV1{
				Tool: "playwright", Version: "1.61.0", Revision: "1", ManifestDigest: portableToolProjectionTestDigest,
			},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{{Reference: contractReference, Record: contractRecord}},
				BindingArtifacts: []providers.PortableToolSelectedRecordV1{{Reference: artifactReference, Record: artifactRecord}},
				Payloads:         []providers.PortableToolSelectedRecordV1{}, NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports:            []providers.PortableToolExportV1{{Name: "playwright", Path: "/opt/reploy/tools/playwright/bin/playwright"}},
			ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	component := projection.Components[0]
	binding := component.Bindings[0]
	descriptor := providerstore.ArtifactDescriptor{
		LogicalPath: "wheels/" + binding.Wheel.Filename, Kind: "wheel",
		Size: binding.Wheel.Size, SHA256: binding.Wheel.SHA256,
	}
	facts := portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp312")
	facts.Version = "3.12.2"
	facts.CompatibleTags = append([]string{}, binding.SupportedTags...)
	input := PortableToolPythonBindingVerificationInputV1{
		Component: component, Binding: binding,
		ContractRecord: plan.Tools[0].Responsibilities.BindingContracts[0],
		ArtifactRecord: plan.Tools[0].Responsibilities.BindingArtifacts[0],
		Interpreter:    providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)},
		Descriptor:     descriptor,
		// These observations come from the pinned 1.61.0 Linux wheel. Include
		// both declared component paths to isolate the tag disagreement.
		Inspection: WheelInspectionV1{
			Artifact: descriptor, Distribution: "playwright", Version: "1.61.0",
			RequiresPython: ">=3.10", Filename: binding.Wheel.Filename,
			FilenameTags: []string{"py3-none-manylinux1_x86_64"},
			InternalTags: []string{"py3-none-any"}, RootIsPurelib: true,
			ConsoleScripts: []WheelConsoleScriptV1{{Name: "playwright", Target: "playwright.__main__:main"}},
			Inventory: []WheelInventoryPathV1{
				{Path: "playwright/driver/node", Kind: wheelinventory.Regular, UncompressedSize: 123459976},
				{Path: "playwright/driver/package/LICENSE", Kind: wheelinventory.Regular, UncompressedSize: 11601},
			},
		},
	}
	assertPlaywrightVerified := func(t *testing.T, input PortableToolPythonBindingVerificationInputV1) {
		t.Helper()
		verified, err := VerifyPortableToolPythonBindingV1(input)
		if err != nil {
			t.Fatalf("Playwright verification: %v", err)
		}
		if !reflect.DeepEqual(verified.EligibleFilenameTags, []string{"py3-none-manylinux1_x86_64"}) ||
			verified.ConsoleScript != (WheelConsoleScriptV1{Name: "playwright", Target: "playwright.__main__:main"}) {
			t.Fatalf("Playwright verification result = %#v", verified)
		}
	}
	assertPlaywrightVerified(t, input)

	if wheelPath := os.Getenv("REPLOY_TEST_PLAYWRIGHT_WHEEL"); wheelPath != "" {
		t.Run("exact artifact", func(t *testing.T) {
			file, err := os.Open(wheelPath)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			input.Inspection, err = InspectWheelReaderV1(context.Background(), file, info.Size(), binding.Wheel.Filename, descriptor)
			if err != nil {
				t.Fatal(err)
			}
			assertPlaywrightVerified(t, input)
		})
	}
}

func TestVerifyPortableToolPythonBindingV1RejectsBoundaryMismatches(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*PortableToolPythonBindingVerificationInputV1)
		want   string
	}{
		{
			name: "descriptor digest",
			mutate: func(input *PortableToolPythonBindingVerificationInputV1) {
				input.Descriptor.SHA256 = canonical.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			},
			want: "acquired descriptor",
		},
		{
			name: "descriptor logical path",
			mutate: func(input *PortableToolPythonBindingVerificationInputV1) {
				input.Descriptor.LogicalPath = "other/" + input.Binding.Wheel.Filename
			},
			want: "acquired descriptor",
		},
		{
			name: "core requires python",
			mutate: func(input *PortableToolPythonBindingVerificationInputV1) {
				input.Inspection.RequiresPython = ">=3.13"
			},
			want: "core metadata",
		},
		{
			name: "internal tags disjoint",
			mutate: func(input *PortableToolPythonBindingVerificationInputV1) {
				input.Inspection.InternalTags = []string{"py2-none-manylinux1_x86_64"}
			},
			want: "do not agree",
		},
		{
			name: "selected CLI absent",
			mutate: func(input *PortableToolPythonBindingVerificationInputV1) {
				input.Inspection.ConsoleScripts = nil
			},
			want: "exactly one console script",
		},
		{
			name: "record reference",
			mutate: func(input *PortableToolPythonBindingVerificationInputV1) {
				input.ArtifactRecord.Reference.Digest = canonical.Digest("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			},
			want: "selected record reference",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, _ := portableToolPythonBindingVerificationFixtureV1(t, "")
			test.mutate(&input)
			if _, err := VerifyPortableToolPythonBindingV1(input); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verification error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyPortableToolPythonBindingV1RequiresBundledPaths(t *testing.T) {
	input, _ := portableToolPythonBindingVerificationFixtureV1(t, "playwright/driver/node")
	if _, err := VerifyPortableToolPythonBindingV1(input); err == nil || !strings.Contains(err.Error(), "absent or empty") {
		t.Fatalf("missing bundled path error = %v", err)
	}

	// Add one regular file under the declared directory and refresh the exact
	// artifact record. This exercises directory-prefix acceptance without any
	// filesystem access in the verifier itself.
	input, _ = portableToolPythonBindingVerificationFixtureV1(t, "playwright/driver/node")
	input.Inspection.Inventory = append(input.Inspection.Inventory, WheelInventoryPathV1{
		Path: "playwright/driver/node/node", Kind: wheelinventory.Regular, UncompressedSize: 1,
	})
	sort.Slice(input.Inspection.Inventory, func(left, right int) bool {
		return input.Inspection.Inventory[left].Path < input.Inspection.Inventory[right].Path
	})
	if _, err := VerifyPortableToolPythonBindingV1(input); err != nil {
		t.Fatalf("directory bundled path error = %v", err)
	}

	// Directory entries alone do not prove that the declared prefix contains
	// materialized component bytes.
	input, _ = portableToolPythonBindingVerificationFixtureV1(t, "playwright/driver/node")
	input.Inspection.Inventory = append(input.Inspection.Inventory,
		WheelInventoryPathV1{Path: "playwright/driver/node/empty", Kind: wheelinventory.Directory},
	)
	sort.Slice(input.Inspection.Inventory, func(left, right int) bool {
		return input.Inspection.Inventory[left].Path < input.Inspection.Inventory[right].Path
	})
	if _, err := VerifyPortableToolPythonBindingV1(input); err == nil || !strings.Contains(err.Error(), "absent or empty") {
		t.Fatalf("empty bundled directory error = %v", err)
	}
}

func TestVerifyPortableToolPythonBindingV1UsesEligibleCompressedFilenameTags(t *testing.T) {
	input := portableToolPythonCompressedTagVerificationFixtureV1(t, "Tag: py2-none-manylinux1_x86_64\n")
	if _, err := VerifyPortableToolPythonBindingV1(input); err == nil || !strings.Contains(err.Error(), "do not agree with eligible filename tags") {
		t.Fatalf("ineligible compressed filename member error = %v", err)
	}

	// The inspected wheel may advertise additional internal tags, but one
	// intersection with the filename tags accepted by the interpreter suffices.
	input = portableToolPythonCompressedTagVerificationFixtureV1(t,
		"Tag: py3-none-manylinux1_x86_64\nTag: py3-none-any\n")
	verified, err := VerifyPortableToolPythonBindingV1(input)
	if err != nil {
		t.Fatalf("overlapping unequal internal tags: %v", err)
	}
	if !reflect.DeepEqual(verified.EligibleFilenameTags, []string{"py3-none-manylinux1_x86_64"}) {
		t.Fatalf("eligible tags = %#v", verified.EligibleFilenameTags)
	}
}

func TestVerifyPortableToolPythonBindingV1BoundsGenericInternalTag(t *testing.T) {
	input, _ := portableToolPythonBindingVerificationFixtureV1(t, "")
	input.Inspection.InternalTags = []string{"py3-none-any"}
	if _, err := VerifyPortableToolPythonBindingV1(input); err != nil {
		t.Fatalf("generic internal platform tag: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*PortableToolPythonBindingVerificationInputV1)
	}{
		{"different Python tag", func(input *PortableToolPythonBindingVerificationInputV1) {
			input.Inspection.InternalTags = []string{"py2-none-any"}
		}},
		{"different platform tag", func(input *PortableToolPythonBindingVerificationInputV1) {
			input.Inspection.InternalTags = []string{"py3-none-win_amd64"}
		}},
		{"not purelib", func(input *PortableToolPythonBindingVerificationInputV1) {
			input.Inspection.RootIsPurelib = false
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := input
			test.mutate(&candidate)
			if _, err := VerifyPortableToolPythonBindingV1(candidate); err == nil ||
				!strings.Contains(err.Error(), "internal wheel tags do not agree") {
				t.Fatalf("generic internal tag error = %v", err)
			}
		})
	}
}

func TestVerifyPortableToolPythonBindingV1RejectsBundledPortablePathCollision(t *testing.T) {
	input, _ := portableToolPythonBindingVerificationFixtureV1(t, "playwright/driver/node")
	addBundledComponentForVerificationTest(t, &input, "nodejs-alt", "Playwright/driver/node")
	if _, err := VerifyPortableToolPythonBindingV1(input); err == nil || !strings.Contains(err.Error(), "bundled component paths") {
		t.Fatalf("bundled path collision error = %v", err)
	}
}

func TestVerifyPortableToolPythonBindingV1RejectsCaseFoldedBundledAncestor(t *testing.T) {
	input, _ := portableToolPythonBindingVerificationFixtureV1(t, "Playwright/driver")
	addBundledComponentForVerificationTest(t, &input, "nodejs-alt", "playwright/driver/node")
	if _, err := VerifyPortableToolPythonBindingV1(input); err == nil || !strings.Contains(err.Error(), "bundled component paths") {
		t.Fatalf("case-folded bundled ancestor error = %v", err)
	}
}

func addBundledComponentForVerificationTest(
	t *testing.T,
	input *PortableToolPythonBindingVerificationInputV1,
	name, componentPath string,
) {
	t.Helper()
	for _, selected := range []*providers.PortableToolSelectedRecordV1{&input.ContractRecord, &input.ArtifactRecord} {
		selected.Record.Value["bundled_components"] = append(
			selected.Record.Value["bundled_components"].([]any),
			map[string]any{
				"name": name, "version": "24.17.0", "path": componentPath,
			},
		)
		portableToolProjectionRebindRecordForTest(t, selected)
	}
	input.ArtifactRecord.Record.Value["contract"] = map[string]any{
		"id":     input.ContractRecord.Reference.ID,
		"digest": string(input.ContractRecord.Reference.Digest),
	}
	portableToolProjectionRebindRecordForTest(t, &input.ArtifactRecord)
	input.Binding.Contract = input.ContractRecord.Reference
	input.Binding.Artifact = input.ArtifactRecord.Reference
	input.Component.Bindings[0].Contract = input.Binding.Contract
	input.Component.Bindings[0].Artifact = input.Binding.Artifact
}

func TestPortableToolPythonBindingEligibleFilenameTagsV1SharesGateDecision(t *testing.T) {
	component := portablePythonEligibilityComponentForTest("py3-none-any", "linux/amd64")
	binding := component.Bindings[0]
	binding.Wheel.Filename = "demo-1-py2.py3-none-any.whl"
	binding.Wheel.Tags = []string{"py2-none-any", "py3-none-any"}
	binding.SupportedTags = []string{"py2-none-any", "py3-none-any"}
	facts := portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp313")
	facts.TestedTags = []string{"py2-none-any", "py3-none-any"}
	facts.CompatibleTags = []string{"py2-none-any", "py3-none-any"}
	eligible, err := portableToolPythonBindingEligibleFilenameTagsV1(binding, facts)
	if err != nil || !reflect.DeepEqual(eligible, []string{"py2-none-any", "py3-none-any"}) {
		t.Fatalf("eligible filename tags = %#v, %v", eligible, err)
	}

	// A compatible tag outside the contract-advertised subset cannot make the
	// exact wheel eligible; this is the distinction the verifier relies on for
	// compressed filename sets.
	binding.SupportedTags = []string{"py3-none-any"}
	facts.CompatibleTags = []string{"py2-none-any"}
	if eligible, err := portableToolPythonBindingEligibleFilenameTagsV1(binding, facts); err == nil || len(eligible) != 0 {
		t.Fatalf("incompatible filename member yielded tags = %#v, err = %v", eligible, err)
	}
}

func portableToolPythonBindingVerificationFixtureV1(
	t *testing.T,
	bundledPath string,
) (PortableToolPythonBindingVerificationInputV1, providerstore.ArtifactDescriptor) {
	t.Helper()
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1.0.0")
	const filename = "demo-1.0.0-py3-none-manylinux1_x86_64.whl"
	if bundledPath != "" {
		bundle := map[string]any{"name": "nodejs", "version": "24.17.0", "path": bundledPath}
		contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
		artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
		contract.Record.Value["bundled_components"] = []any{bundle}
		portableToolProjectionRebindRecordForTest(t, contract)
		artifact.Record.Value["bundled_components"] = []any{bundle}
		artifact.Record.Value["contract"] = map[string]any{
			"id": contract.Reference.ID, "digest": contract.Reference.Digest,
		}
		portableToolProjectionRebindRecordForTest(t, artifact)
	}
	content := testInspectionWheel(t, filename, "demo", "1.0.0",
		"Name: demo\nVersion: 1.0.0\nRequires-Python: >=3.12,<3.13\n",
		"Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-manylinux1_x86_64\n",
		"[console_scripts]\ndemo = demo.cli:main\n")
	descriptor := testInspectionDescriptor(content, filename)
	artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["size"] = descriptor.Size
	artifact.Record.Value["sha256"] = descriptor.SHA256
	portableToolProjectionRebindRecordForTest(t, artifact)
	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	binding := projection.Components[0].Bindings[0]
	inspection, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	facts := portablePythonEligibilityFactsForTest(projection.Components[0].TestedTags, "cpython", "cp312")
	facts.Version = "3.12.2"
	facts.CompatibleTags = append([]string{}, binding.SupportedTags...)
	return PortableToolPythonBindingVerificationInputV1{
		Component: projection.Components[0], Binding: binding,
		ContractRecord: plan.Tools[0].Responsibilities.BindingContracts[0],
		ArtifactRecord: plan.Tools[0].Responsibilities.BindingArtifacts[0],
		Interpreter:    providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)},
		Descriptor:     descriptor, Inspection: inspection,
	}, descriptor
}

func portableToolPythonCompressedTagVerificationFixtureV1(
	t *testing.T,
	internalTags string,
) PortableToolPythonBindingVerificationInputV1 {
	t.Helper()
	plan := portableToolPythonPlanForTest(t, "application:demo", "one", "demo", "demo==1.0.0")
	const (
		py2Tag   = "py2-none-manylinux1_x86_64"
		py3Tag   = "py3-none-manylinux1_x86_64"
		filename = "demo-1.0.0-py2.py3-none-manylinux1_x86_64.whl"
	)
	contract := &plan.Tools[0].Responsibilities.BindingContracts[0]
	contract.Record.Value["supported_tags"] = []any{py2Tag, py3Tag}
	portableToolProjectionRebindRecordForTest(t, contract)
	artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["contract"] = map[string]any{
		"id": contract.Reference.ID, "digest": string(contract.Reference.Digest),
	}
	artifact.Record.Value["filename"] = filename
	artifact.Record.Value["tags"] = []any{py2Tag, py3Tag}
	content := testInspectionWheel(t, filename, "demo", "1.0.0",
		"Name: demo\nVersion: 1.0.0\nRequires-Python: >=3.12,<3.13\n",
		"Wheel-Version: 1.0\nRoot-Is-Purelib: true\n"+internalTags,
		"[console_scripts]\ndemo = demo.cli:main\n")
	descriptor := testInspectionDescriptor(content, filename)
	artifact.Record.Value["size"] = descriptor.Size
	artifact.Record.Value["sha256"] = descriptor.SHA256
	portableToolProjectionRebindRecordForTest(t, artifact)
	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	binding := projection.Components[0].Bindings[0]
	inspection, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	facts := portablePythonEligibilityFactsForTest(projection.Components[0].TestedTags, "cpython", "cp312")
	facts.Version = "3.12.2"
	// The filename advertises both py2 and py3, but the interpreter accepts
	// only py3. Each caller supplies the WHEEL metadata tags for its case.
	facts.CompatibleTags = []string{py3Tag}
	return PortableToolPythonBindingVerificationInputV1{
		Component: projection.Components[0], Binding: binding,
		ContractRecord: plan.Tools[0].Responsibilities.BindingContracts[0],
		ArtifactRecord: plan.Tools[0].Responsibilities.BindingArtifacts[0],
		Interpreter:    providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)},
		Descriptor:     descriptor, Inspection: inspection,
	}
}
