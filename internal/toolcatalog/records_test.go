package toolcatalog

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

const recordTestDigest canonical.Digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func recordTestReference(id string) RecordReferenceV1 {
	return RecordReferenceV1{ID: id, Digest: recordTestDigest}
}

func validRecordValuesV1() []any {
	release := "tool:demo/releases/1.2.3"
	target := release + "/targets/debian/12/amd64"
	return []any{
		&ToolRecordV1{
			Schema: ToolRecordSchemaV1, ID: "tool:demo", Name: "demo", VersionScheme: "semver", Summary: "Demo tool",
			Upstream: "https://example.com/demo", Source: "https://example.com/source", License: "https://example.com/license",
			Documentation: "https://example.com/docs",
			Releases:      []RecordReferenceV1{recordTestReference(release + "/revisions/1/manifest")},
		},
		&ReleaseManifestV1{
			Schema: ReleaseManifestSchemaV1, ID: release + "/revisions/1/manifest", Tool: "demo", Version: "1.2.3", Revision: "1",
			Aliases:  []string{"1.2"},
			Contract: recordTestReference(release + "/contract"), Targets: []RecordReferenceV1{recordTestReference(target)},
			ArtifactSources: []ArtifactSourceMappingV1{}, Provenance: []string{"https://example.com/releases/1.2.3"},
			ValidationProfiles: []RecordReferenceV1{recordTestReference(release + "/validation/profiles/default")},
		},
		&ReleaseContractV1{
			Schema: ReleaseContractSchemaV1, ID: release + "/contract", Contexts: []string{"build"},
			SupportedReploy: ">=0.0.0",
			Binding:         BindingSetSchemaV1{Options: []string{}},
			Selections: SelectionSchemaV1{
				Dimensions: []SelectionDimensionV1{}, Combinations: []SelectionCombinationV1{},
			},
			Exports: []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}}, ResolverPrimitives: []string{"https-sha256"},
			CompatibilityConstraints: []string{"portable-archive-v1"},
		},
		&TargetRecordV1{
			Schema: TargetRecordSchemaV1, ID: target,
			Target: TargetIdentityV1{
				Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
				NativeArchitecture: "amd64", PackageManager: "apt",
			},
			SupportCases: []TargetSupportCaseV1{{Context: "build", Bindings: []string{}, Selections: map[string][]string{}}},
			PackageSets:  []RecordReferenceV1{}, Bindings: []TargetBindingV1{},
			Payloads:   []RecordReferenceV1{recordTestReference(release + "/payloads/demo-linux-amd64")},
			Selections: []TargetSelectionV1{}, Exports: []ToolExportV1{},
			IntegrationFixtures: []RecordReferenceV1{recordTestReference(release + "/validation/fixtures/debian-12-amd64")},
			ValidationProfiles:  []RecordReferenceV1{recordTestReference(release + "/validation/profiles/default")},
		},
		&BindingContractV1{
			Schema: BindingContractSchemaV1, ID: release + "/bindings/python/contract", Name: "python", Package: "demo",
			Requirements: []string{"demo==1.2.3", "support>=1,<2"}, SupportedPython: []string{"3.11", "3.12"}, SupportedTags: []string{"py3-none-manylinux1_x86_64"},
			BundledComponents: []BundledComponentV1{{Name: "nodejs", Version: "24.0.0", Path: "node"}},
			CLI:               ToolExportV1{Name: "demo", Path: "/opt/demo/bin/demo"},
		},
		&BindingArtifactRecordV1{
			Schema: BindingArtifactSchemaV1, ID: release + "/bindings/python/artifacts/linux-amd64", Binding: "python",
			Contract: recordTestReference(release + "/bindings/python/contract"),
			Name:     "demo", EcosystemVersion: "1.2.3",
			Platform: "linux/amd64", Filename: "demo-1.2.3-py3-none-manylinux1_x86_64.whl", Size: "42", SHA256: recordTestDigest,
			Resolver: "https-sha256",
			Tags:     []string{"py3-none-manylinux1_x86_64"}, RequiresPython: ">=3.11",
			BundledComponents: []BundledComponentV1{{Name: "nodejs", Version: "24.0.0", Path: "node"}, {Name: "playwright-core", Version: "1.2.3", Path: "package"}},
		},
		&PayloadRecordV1{
			Schema: PayloadRecordSchemaV1, ID: release + "/payloads/chromium/chromium-linux-amd64", Name: "chromium",
			Revision: "1228", UpstreamVersion: "149.0.0", Platform: "linux/amd64", LogicalPath: "tools/demo/chromium.zip",
			Kind: "playwright-browser-archive", Size: "42", SHA256: recordTestDigest, Resolver: "https-sha256",
			Entries: "2", UnpackedSize: "84",
			InstallDirectory: "chromium-1228", ArchiveRoot: "chrome-linux", Executables: []string{"chrome-linux/chrome", "chrome-linux/chrome-wrapper"},
		},
		&ArtifactSourceRecordV1{
			Schema: ArtifactSourceRecordSchemaV1, ID: release + "/revisions/1/sources/chromium-linux-amd64",
			SHA256:      recordTestDigest,
			Mirrors:     []string{"https://example.com/chromium.zip", "https://mirror.example.com/chromium.zip"},
			Provenance:  []string{"https://example.com/checksums", "https://example.com/releases/1.2.3"},
			Diagnostics: []string{},
		},
		&NativePackageSetV1{
			Schema: NativePackageSetSchemaV1, ID: release + "/package-sets/debian-runtime-amd64", Manager: "apt",
			Requirements: []string{"libasound2", "libnss3"},
			Repositories: []string{}, ValidationMetadata: []string{},
		},
		&IntegrationFixtureRecordV1{
			Schema: IntegrationFixtureSchemaV1, ID: release + "/validation/fixtures/debian-12-amd64", Name: "debian-12-amd64",
			Target: TargetIdentityV1{
				Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
				NativeArchitecture: "amd64", PackageManager: "apt",
			},
			BaseImage: "docker.io/library/debian:12-slim", BaseImageDigest: recordTestDigest,
			Context: "build", Bindings: []string{}, Selections: map[string][]string{},
			ValidationProfiles: []RecordReferenceV1{recordTestReference(release + "/validation/profiles/default")},
		},
		&ValidationProfileRecordV1{
			Schema: ValidationProfileSchemaV1, ID: release + "/validation/profiles/default",
			Tool: "demo", Version: "1.2.3",
			Probes: []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}}},
		},
	}
}

func recordIDV1(value any) string {
	switch record := value.(type) {
	case *ToolRecordV1:
		return record.ID
	case *ReleaseManifestV1:
		return record.ID
	case *ReleaseContractV1:
		return record.ID
	case *TargetRecordV1:
		return record.ID
	case *BindingContractV1:
		return record.ID
	case *BindingArtifactRecordV1:
		return record.ID
	case *PayloadRecordV1:
		return record.ID
	case *ArtifactSourceRecordV1:
		return record.ID
	case *NativePackageSetV1:
		return record.ID
	case *IntegrationFixtureRecordV1:
		return record.ID
	case *ValidationProfileRecordV1:
		return record.ID
	default:
		panic(fmt.Sprintf("unsupported record %T", value))
	}
}

func recordSchemaV1(value any) string {
	switch record := value.(type) {
	case *ToolRecordV1:
		return record.Schema
	case *ReleaseManifestV1:
		return record.Schema
	case *ReleaseContractV1:
		return record.Schema
	case *TargetRecordV1:
		return record.Schema
	case *BindingContractV1:
		return record.Schema
	case *BindingArtifactRecordV1:
		return record.Schema
	case *PayloadRecordV1:
		return record.Schema
	case *ArtifactSourceRecordV1:
		return record.Schema
	case *NativePackageSetV1:
		return record.Schema
	case *IntegrationFixtureRecordV1:
		return record.Schema
	case *ValidationProfileRecordV1:
		return record.Schema
	default:
		panic(fmt.Sprintf("unsupported record %T", value))
	}
}

// validValidationEvidenceV1 is kept separate from validRecordValuesV1 because
// evidence is not decoded by decodeRecordV1's schema dispatch; it has its own
// decode path. It is still a declared record family and needs the same
// construction and clone coverage.
func validValidationEvidenceV1() *ValidationEvidenceV1 {
	return &ValidationEvidenceV1{
		Schema: ValidationEvidenceSchemaV1, Tool: "demo", Version: "1.2.3", Revision: "1",
		ManifestDigest: recordTestDigest, SelectedClosureDigest: recordTestDigest,
		Target: TargetIdentityV1{
			Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
			NativeArchitecture: "amd64", PackageManager: "apt",
		},
		BaseImageDigest: recordTestDigest, Context: "build", Bindings: []string{"python"},
		Selections:       map[string][]string{"browser": {"chromium"}},
		Fixture:          "tool:demo/releases/1.2.3/validation/fixtures/debian-12-amd64",
		ValidatorVersion: "validator-v1", Result: "pass",
		ValidatorOutputDigest: recordTestDigest,
	}
}

func TestValidationEvidenceConstructsAndClonesIndependently(t *testing.T) {
	value := validValidationEvidenceV1()
	if value.Schema != ValidationEvidenceSchemaV1 || value.Tool == "" || value.ValidatorOutputDigest == "" {
		t.Fatalf("evidence fixture is incomplete: %+v", value)
	}
	before := fmt.Sprintf("%+v", value)
	clone := cloneValidationEvidenceV1(value)
	mutateEveryValueV1(reflect.ValueOf(&clone))
	if after := fmt.Sprintf("%+v", value); after != before {
		t.Errorf("clone shares storage with the original evidence\n before: %s\n  after: %s", before, after)
	}
}

func TestValidRecordValuesV1ConstructEveryRecordFamily(t *testing.T) {
	schemas := map[string]bool{
		ToolRecordSchemaV1:           false,
		ReleaseManifestSchemaV1:      false,
		ReleaseContractSchemaV1:      false,
		TargetRecordSchemaV1:         false,
		BindingContractSchemaV1:      false,
		BindingArtifactSchemaV1:      false,
		PayloadRecordSchemaV1:        false,
		ArtifactSourceRecordSchemaV1: false,
		NativePackageSetSchemaV1:     false,
		IntegrationFixtureSchemaV1:   false,
		ValidationProfileSchemaV1:    false,
	}
	identifiers := map[string]bool{}
	for _, value := range validRecordValuesV1() {
		schema := recordSchemaV1(value)
		covered, known := schemas[schema]
		if !known {
			t.Errorf("record %T declares unknown schema %q", value, schema)
			continue
		}
		if covered {
			t.Errorf("schema %q is constructed more than once", schema)
		}
		schemas[schema] = true
		id := recordIDV1(value)
		if id == "" {
			t.Errorf("record %T has an empty ID", value)
		}
		if identifiers[id] {
			t.Errorf("record ID %q is constructed more than once", id)
		}
		identifiers[id] = true
	}
	for schema, covered := range schemas {
		if !covered {
			t.Errorf("record family %q has no construction coverage", schema)
		}
	}
}

func TestTargetRecordExpressesPluralContributionShape(t *testing.T) {
	target := TargetRecordV1{
		Schema: TargetRecordSchemaV1,
		ID:     "tool:demo/target/debian/12/amd64",
		Target: TargetIdentityV1{
			Platform: "linux", OSReleaseID: "debian", VersionID: "12",
			OCIArchitecture: "amd64", NativeArchitecture: "x86_64", PackageManager: "apt",
		},
		SupportCases: []TargetSupportCaseV1{
			{Context: "runtime", Bindings: []string{"node"}, Selections: map[string][]string{"browser": {"firefox"}}},
			{Context: "build", Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}}},
		},
		Bindings: []TargetBindingV1{
			{Name: "python", Contract: recordTestReference("tool:demo/binding/python"), Artifacts: []RecordReferenceV1{
				recordTestReference("tool:demo/binding/python/linux-amd64"),
				recordTestReference("tool:demo/binding/python/linux-amd64-alt"),
			}, ValidationProfiles: []RecordReferenceV1{recordTestReference("tool:demo/profile/python")}},
			{Name: "node", Contract: recordTestReference("tool:demo/binding/node")},
		},
		Selections: []TargetSelectionV1{
			{Dimension: "browser", Value: "chromium", Payloads: []RecordReferenceV1{
				recordTestReference("tool:demo/payload/chromium"),
				recordTestReference("tool:demo/payload/ffmpeg"),
			}, ValidationProfiles: []RecordReferenceV1{recordTestReference("tool:demo/profile/chromium")}},
			{Dimension: "browser", Value: "firefox", Payloads: []RecordReferenceV1{recordTestReference("tool:demo/payload/firefox")}},
		},
		IntegrationFixtures: []RecordReferenceV1{
			recordTestReference("tool:demo/fixture/one"),
			recordTestReference("tool:demo/fixture/two"),
		},
	}
	if len(target.Bindings) < 2 || len(target.Bindings[0].Artifacts) < 2 {
		t.Error("target must carry multiple bindings and multiple binding artifacts")
	}
	if len(target.SupportCases) != 2 || target.SupportCases[0].Bindings[0] != "node" {
		t.Error("target must carry explicit context, binding-set, and selection-map support cases")
	}
	if len(target.Selections) < 2 || target.Selections[0].Dimension != "browser" || len(target.Selections[0].Payloads) < 2 {
		t.Error("target must carry dimensioned selections and multiple coupled payloads")
	}
	if len(target.Bindings[0].ValidationProfiles) == 0 || len(target.Selections[0].ValidationProfiles) == 0 || len(target.IntegrationFixtures) < 2 {
		t.Error("target contributions must carry validation profiles and multiple fixtures")
	}
}

func TestCloneHelpersReturnIndependentValues(t *testing.T) {
	original := &TargetRecordV1{
		Schema:             TargetRecordSchemaV1,
		ID:                 "tool:demo/target/debian/12/amd64",
		SupportCases:       []TargetSupportCaseV1{{Context: "build", Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}}}},
		Selections:         []TargetSelectionV1{{Dimension: "browser", Value: "chromium", Payloads: []RecordReferenceV1{recordTestReference("tool:demo/payload/chromium")}}},
		Exports:            []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}},
		ValidationProfiles: []RecordReferenceV1{recordTestReference("tool:demo/profile/default")},
	}
	original.Bindings = []TargetBindingV1{{
		Name:               "python",
		Artifacts:          []RecordReferenceV1{recordTestReference("tool:demo/binding/python/linux-amd64")},
		Exports:            []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}},
		ValidationProfiles: []RecordReferenceV1{recordTestReference("tool:demo/profile/python")},
	}}
	original.IntegrationFixtures = []RecordReferenceV1{recordTestReference("tool:demo/fixture/one")}

	clone := cloneTargetRecordV1(original)
	clone.SupportCases[0].Bindings[0] = "node"
	clone.SupportCases[0].Selections["browser"][0] = "firefox"
	clone.Selections[0].Value = "firefox"
	clone.Selections[0].Payloads[0] = recordTestReference("tool:demo/payload/firefox")
	clone.Exports[0].Name = "other"
	clone.Bindings[0].Artifacts[0] = recordTestReference("tool:demo/binding/python/other")
	clone.Bindings[0].Exports[0].Name = "other"
	clone.Bindings[0].ValidationProfiles[0] = recordTestReference("tool:demo/profile/other")
	clone.ValidationProfiles[0] = recordTestReference("tool:demo/profile/other")
	clone.IntegrationFixtures[0] = recordTestReference("tool:demo/fixture/other")

	if original.Bindings[0].Artifacts[0].ID != "tool:demo/binding/python/linux-amd64" {
		t.Error("clone shares binding artifact storage with the original record")
	}
	if original.SupportCases[0].Bindings[0] != "python" || original.SupportCases[0].Selections["browser"][0] != "chromium" {
		t.Error("clone shares support-case storage with the original record")
	}
	if original.Bindings[0].Exports[0].Name != "demo" || original.Bindings[0].ValidationProfiles[0].ID != "tool:demo/profile/python" {
		t.Error("clone shares binding export or validation-profile storage with the original record")
	}
	if original.ValidationProfiles[0].ID != "tool:demo/profile/default" {
		t.Error("clone shares target validation-profile storage with the original record")
	}
	if original.IntegrationFixtures[0].ID != "tool:demo/fixture/one" {
		t.Error("clone shares integration fixture storage with the original record")
	}
	if original.Selections[0].Value != "chromium" {
		t.Error("clone shares selection storage with the original record")
	}
	if original.Selections[0].Payloads[0].ID != "tool:demo/payload/chromium" {
		t.Error("clone shares selection payload storage with the original record")
	}
	if original.Exports[0].Name != "demo" {
		t.Error("clone shares export storage with the original record")
	}
}

func TestReleaseContractAndFixtureClonesAreIndependent(t *testing.T) {
	contract := &ReleaseContractV1{
		Schema:  ReleaseContractSchemaV1,
		ID:      "tool:demo/contract/1.2.3",
		Binding: BindingSetSchemaV1{Options: []string{"node", "python"}},
		Selections: SelectionSchemaV1{
			Dimensions:   []SelectionDimensionV1{{Name: "browser", Options: []string{"chromium", "firefox"}}},
			Combinations: []SelectionCombinationV1{{"browser": {"chromium", "firefox"}}},
		},
	}
	contractClone := cloneReleaseContractV1(contract)
	contractClone.Binding.Options[0] = "ruby"
	contractClone.Selections.Dimensions[0].Options[0] = "webkit"
	contractClone.Selections.Combinations[0]["browser"][0] = "webkit"
	if contract.Binding.Options[0] != "node" {
		t.Error("clone shares binding option storage with the original contract")
	}
	if contract.Selections.Dimensions[0].Options[0] != "chromium" {
		t.Error("clone shares selection-dimension option storage with the original contract")
	}
	if contract.Selections.Combinations[0]["browser"][0] != "chromium" {
		t.Error("clone shares selection-combination storage with the original contract")
	}

	fixture := &IntegrationFixtureRecordV1{
		Schema:             IntegrationFixtureSchemaV1,
		ID:                 "tool:demo/fixture/debian-12-amd64",
		Bindings:           []string{"python"},
		Selections:         map[string][]string{"browser": {"chromium"}},
		ValidationProfiles: []RecordReferenceV1{recordTestReference("tool:demo/profile/default")},
	}
	fixtureClone := cloneIntegrationFixtureV1(fixture)
	fixtureClone.Bindings[0] = "node"
	fixtureClone.Selections["browser"][0] = "firefox"
	fixtureClone.ValidationProfiles[0] = recordTestReference("tool:demo/profile/other")
	if fixture.Bindings[0] != "python" || fixture.Selections["browser"][0] != "chromium" {
		t.Error("clone shares fixture binding or selection storage with the original record")
	}
	if fixture.ValidationProfiles[0].ID != "tool:demo/profile/default" {
		t.Error("clone shares fixture validation-profile storage with the original record")
	}
}

func TestRecordModelUsesFinalBindingSelectionAndValidationShape(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		required  []string
		forbidden []string
	}{
		{
			name: "release contract",
			value: ReleaseContractV1{
				Binding: BindingSetSchemaV1{Options: []string{"python"}},
				Selections: SelectionSchemaV1{
					Dimensions:   []SelectionDimensionV1{{Name: "browser", Options: []string{"chromium"}}},
					Combinations: []SelectionCombinationV1{{"browser": {"chromium"}}},
				},
				CompatibilityConstraints: []string{"portable-archive-v1"},
			},
			required:  []string{`"binding":{"options":["python"]}`, `"dimensions"`, `"combinations":[{"browser":["chromium"]}]`, `"compatibility_constraints":["portable-archive-v1"]`},
			forbidden: []string{`"values"`, `"required"`, `"default"`, `"parameters"`, `"probes"`, `"compatibility_groups"`},
		},
		{
			name: "binding CLI export",
			value: BindingContractV1{
				CLI: ToolExportV1{Name: "playwright", Path: "/opt/playwright/bin/playwright"},
			},
			required:  []string{`"cli":{"name":"playwright","path":"/opt/playwright/bin/playwright"}`},
			forbidden: []string{`"cli":"`},
		},
		{
			name: "payload executables",
			value: PayloadRecordV1{
				Executables: []string{"bin/java", "bin/javac"},
			},
			required:  []string{`"executables":["bin/java","bin/javac"]`},
			forbidden: []string{`"executable":`},
		},
		{
			name: "target support case",
			value: TargetSupportCaseV1{
				Context: "build", Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}},
			},
			required:  []string{`"context":"build"`, `"bindings":["python"]`, `"selections":{"browser":["chromium"]}`},
			forbidden: []string{`"values"`},
		},
		{
			name: "target selection",
			value: TargetSelectionV1{
				Dimension: "browser", Value: "chromium",
				ValidationProfiles: []RecordReferenceV1{recordTestReference("tool:demo/profile/chromium")},
			},
			required:  []string{`"dimension":"browser"`, `"value":"chromium"`, `"validation_profiles"`},
			forbidden: []string{`"name"`, `"probes"`},
		},
		{
			name: "artifact source",
			value: ArtifactSourceRecordV1{
				SHA256: recordTestDigest, Mirrors: []string{"https://example.com/archive"},
			},
			required:  []string{`"sha256"`, `"mirrors"`},
			forbidden: []string{`"size"`, `"resolver"`},
		},
		{
			name: "integration fixture",
			value: IntegrationFixtureRecordV1{
				Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}},
			},
			required:  []string{`"bindings":["python"]`, `"selections":{"browser":["chromium"]}`},
			forbidden: []string{`"binding"`, `"parameters"`},
		},
		{
			name: "validation profile",
			value: ValidationProfileRecordV1{
				Probes: []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}}},
			},
			required:  []string{`"probes":[{"path":"/opt/demo/bin/demo","args":["--version"]}]`},
			forbidden: []string{`"validator"`, `"network"`},
		},
		{
			name: "validation evidence",
			value: ValidationEvidenceV1{
				Context:  "build",
				Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}},
				ValidatorOutputDigest: recordTestDigest,
			},
			required:  []string{`"context":"build"`, `"bindings":["python"]`, `"selections":{"browser":["chromium"]}`, `"validator_output_digest"`},
			forbidden: []string{`"binding"`, `"parameters"`, `"probe_digests"`},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := json.Marshal(testCase.value)
			if err != nil {
				t.Fatalf("marshal model fixture: %v", err)
			}
			text := string(encoded)
			for _, required := range testCase.required {
				if !strings.Contains(text, required) {
					t.Errorf("wire model %s is missing %s: %s", testCase.name, required, text)
				}
			}
			for _, forbidden := range testCase.forbidden {
				if strings.Contains(text, forbidden) {
					t.Errorf("wire model %s retains forbidden field %s: %s", testCase.name, forbidden, text)
				}
			}
		})
	}
}

// mutateEveryValueV1 writes through every string reachable in value, including
// strings inside slices and behind pointers. A clone that still shares storage
// with its original therefore shows up as a change to the original.
func mutateEveryValueV1(value reflect.Value) {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			mutateEveryValueV1(value.Elem())
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			mutateEveryValueV1(value.Index(index))
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			item := reflect.New(iterator.Value().Type()).Elem()
			item.Set(iterator.Value())
			mutateEveryValueV1(item)
			value.SetMapIndex(iterator.Key(), item)
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			mutateEveryValueV1(value.Field(index))
		}
	case reflect.String:
		if value.CanSet() {
			value.SetString("mutated")
		}
	}
}

func TestEveryCloneHelperReturnsIndependentStorage(t *testing.T) {
	probes := []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}}}
	exports := []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}}
	references := []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/payloads/one")}

	for _, testCase := range []struct {
		name  string
		clone func() any
	}{
		{"tool", func() any {
			value := &ToolRecordV1{Releases: references}
			result := cloneToolRecordV1(value)
			return [2]any{value, &result}
		}},
		{"release manifest", func() any {
			value := &ReleaseManifestV1{Aliases: []string{"1.2"}, Targets: references,
				ArtifactSources: []ArtifactSourceMappingV1{{Artifact: references[0], Source: references[0]}},
				Provenance:      []string{"https://example.com/releases"}, ValidationProfiles: references}
			result := cloneReleaseManifestV1(value)
			return [2]any{value, &result}
		}},
		{"release contract", func() any {
			value := &ReleaseContractV1{Contexts: []string{"build"},
				Binding: BindingSetSchemaV1{Options: []string{"python"}},
				Selections: SelectionSchemaV1{
					Dimensions:   []SelectionDimensionV1{{Name: "browser", Options: []string{"chromium"}}},
					Combinations: []SelectionCombinationV1{{"browser": {"chromium"}}},
				},
				Exports:                  exports,
				ResolverPrimitives:       []string{"https-sha256"},
				CompatibilityConstraints: []string{"portable-archive-v1"},
				Runtime:                  &RecordRuntimeV1{Environment: []RecordEnvironmentVariableV1{{Name: "DEMO", Value: "1"}}}}
			result := cloneReleaseContractV1(value)
			return [2]any{value, &result}
		}},
		{"target", func() any {
			value := &TargetRecordV1{PackageSets: references, Payloads: references, IntegrationFixtures: references, ValidationProfiles: references,
				SupportCases: []TargetSupportCaseV1{{Context: "build", Bindings: []string{"python"}, Selections: map[string][]string{"browser": {"chromium"}}}},
				Bindings:     []TargetBindingV1{{Name: "python", Artifacts: references, Payloads: references, PackageSets: references, Exports: exports, ValidationProfiles: references}},
				Selections:   []TargetSelectionV1{{Dimension: "browser", Value: "chromium", Payloads: references, PackageSets: references, Exports: exports, ValidationProfiles: references}},
				Exports:      exports}
			result := cloneTargetRecordV1(value)
			return [2]any{value, &result}
		}},
		{"binding contract", func() any {
			value := &BindingContractV1{Requirements: []string{"demo==1"}, SupportedPython: []string{"3.12"}}
			result := cloneBindingContractV1(value)
			return [2]any{value, &result}
		}},
		{"binding artifact", func() any {
			value := &BindingArtifactRecordV1{Tags: []string{"py3-none-any"},
				BundledComponents: []BundledComponentV1{{Name: "nodejs", Version: "24.0.0", Path: "node"}}}
			result := cloneBindingArtifactV1(value)
			return [2]any{value, &result}
		}},
		{"payload", func() any {
			value := &PayloadRecordV1{Name: "chromium", Executables: []string{"chrome-linux/chrome"}}
			result := clonePayloadRecordV1(value)
			return [2]any{value, &result}
		}},
		{"native package set", func() any {
			value := &NativePackageSetV1{Requirements: []string{"libnss3"}}
			result := cloneNativePackageSetV1(value)
			return [2]any{value, &result}
		}},
		{"artifact source", func() any {
			value := &ArtifactSourceRecordV1{Mirrors: []string{"https://example.com/a"}, Provenance: []string{"https://example.com/c"}}
			result := cloneArtifactSourceV1(value)
			return [2]any{value, &result}
		}},
		{"integration fixture", func() any {
			value := &IntegrationFixtureRecordV1{Bindings: []string{"python"},
				Selections: map[string][]string{"browser": {"chromium"}}, ValidationProfiles: references}
			result := cloneIntegrationFixtureV1(value)
			return [2]any{value, &result}
		}},
		{"validation profile", func() any {
			value := &ValidationProfileRecordV1{Probes: probes}
			result := cloneValidationProfileV1(value)
			return [2]any{value, &result}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pair := testCase.clone().([2]any)
			original, clone := pair[0], pair[1]
			before := fmt.Sprintf("%+v", original)
			mutateEveryValueV1(reflect.ValueOf(clone))
			if after := fmt.Sprintf("%+v", original); after != before {
				t.Errorf("clone shares storage with the original record\n before: %s\n  after: %s", before, after)
			}
		})
	}
}
