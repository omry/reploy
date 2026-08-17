package toolcatalog

import (
	"fmt"
	"reflect"
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
			Releases: []RecordReferenceV1{recordTestReference(release + "/revisions/1/manifest")},
		},
		&ReleaseManifestV1{
			Schema: ReleaseManifestSchemaV1, ID: release + "/revisions/1/manifest", Tool: "demo", Version: "1.2.3", Revision: "1",
			Aliases:  []string{"1.2"},
			Contract: recordTestReference(release + "/contract"), Targets: []RecordReferenceV1{recordTestReference(target)},
			ArtifactSources: []ArtifactSourceMappingV1{}, Provenance: []string{"https://example.com/releases/1.2.3"},
			ValidationProfile: recordTestReference(release + "/validation/profiles/default"),
		},
		&ReleaseContractV1{
			Schema: ReleaseContractSchemaV1, ID: release + "/contract", Contexts: []string{"build"},
			SupportedReploy: ">=0.0.0",
			Binding:         BindingRequestV1{Options: []string{}, Required: false, Default: ""},
			Selections:      SelectionRequestV1{Options: []string{}, Minimum: "0", Maximum: "0", Defaults: []string{}, CompatibilityGroups: [][]string{}},
			Parameters:      []ParameterSchemaV1{},
			Probes:          []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"}},
			Exports:         []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}}, ResolverPrimitives: []string{"https-sha256"},
		},
		&TargetRecordV1{
			Schema: TargetRecordSchemaV1, ID: target,
			Target: TargetIdentityV1{
				Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
				NativeArchitecture: "amd64", PackageManager: "apt",
			},
			PackageSets: []RecordReferenceV1{}, Bindings: []TargetBindingV1{},
			Payloads:   []RecordReferenceV1{recordTestReference(release + "/payloads/demo-linux-amd64")},
			Selections: []TargetSelectionV1{}, Parameters: []TargetParameterConstraintV1{}, Exports: []ToolExportV1{}, Probes: []RecordProbeV1{},
			IntegrationFixtures: []RecordReferenceV1{recordTestReference(release + "/validation/fixtures/debian-12-amd64")},
			ValidationProfile:   recordTestReference(release + "/validation/profiles/default"),
		},
		&BindingContractV1{
			Schema: BindingContractSchemaV1, ID: release + "/bindings/python/contract", Name: "python", Package: "demo",
			Requirements: []string{"demo==1.2.3", "support>=1,<2"}, SupportedPython: []string{"3.11", "3.12"},
			CLI: "/opt/demo/bin/demo",
		},
		&BindingArtifactRecordV1{
			Schema: BindingArtifactSchemaV1, ID: release + "/bindings/python/artifacts/linux-amd64", Binding: "python",
			Platform: "linux/amd64", Filename: "demo-1.2.3-py3-none-manylinux1_x86_64.whl", Size: "42", SHA256: recordTestDigest,
			Resolver: "https-sha256",
			Tags:     []string{"py3-none-manylinux1_x86_64"}, RequiresPython: ">=3.11",
			BundledComponents: []BundledComponentV1{{Name: "nodejs", Version: "24.0.0", Path: "node"}, {Name: "playwright-core", Version: "1.2.3", Path: "package"}},
		},
		&PayloadRecordV1{
			Schema: PayloadRecordSchemaV1, ID: release + "/payloads/chromium/chromium-linux-amd64", Selection: "chromium", Name: "chromium",
			Revision: "1228", UpstreamVersion: "149.0.0", Platform: "linux/amd64", LogicalPath: "tools/demo/chromium.zip",
			Kind: "playwright-browser-archive", Size: "42", SHA256: recordTestDigest, Resolver: "https-sha256",
			Entries: "2", UnpackedSize: "84",
			InstallDirectory: "chromium-1228", ArchiveRoot: "chrome-linux", Executable: "chrome-linux/chrome",
		},
		&ArtifactSourceRecordV1{
			Schema: ArtifactSourceRecordSchemaV1, ID: release + "/revisions/1/sources/chromium-linux-amd64",
			SHA256: recordTestDigest, Size: "42", Resolver: "https-sha256",
			Mirrors:    []string{"https://example.com/chromium.zip", "https://mirror.example.com/chromium.zip"},
			Provenance: []string{"https://example.com/checksums", "https://example.com/releases/1.2.3"},
		},
		&NativePackageSetV1{
			Schema: NativePackageSetSchemaV1, ID: release + "/package-sets/debian-runtime-amd64", Manager: "apt",
			Requirements: []string{"libasound2", "libnss3"},
		},
		&IntegrationFixtureRecordV1{
			Schema: IntegrationFixtureSchemaV1, ID: release + "/validation/fixtures/debian-12-amd64", Name: "debian-12-amd64",
			Target: TargetIdentityV1{
				Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
				NativeArchitecture: "amd64", PackageManager: "apt",
			},
			BaseImage: "docker.io/library/debian:12-slim", BaseImageDigest: recordTestDigest,
			Context: "build", Binding: "", Selections: []string{}, Parameters: []ParameterValueV1{},
		},
		&ValidationProfileRecordV1{
			Schema: ValidationProfileSchemaV1, ID: release + "/validation/profiles/default",
			Tool: "demo", Version: "1.2.3", Validator: "java-jdk", ValidatorVersion: "1",
			Probes: []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"}}, Network: "none",
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
		Bindings: []TargetBindingV1{
			{Name: "python", Contract: recordTestReference("tool:demo/binding/python"), Artifacts: []RecordReferenceV1{
				recordTestReference("tool:demo/binding/python/linux-amd64"),
				recordTestReference("tool:demo/binding/python/linux-amd64-alt"),
			}},
			{Name: "node", Contract: recordTestReference("tool:demo/binding/node")},
		},
		Selections: []TargetSelectionV1{
			{Name: "chromium", Payloads: []RecordReferenceV1{
				recordTestReference("tool:demo/payload/chromium"),
				recordTestReference("tool:demo/payload/ffmpeg"),
			}},
			{Name: "firefox", Payloads: []RecordReferenceV1{recordTestReference("tool:demo/payload/firefox")}},
		},
		Parameters: []TargetParameterConstraintV1{{Name: "channel"}, {Name: "locale"}},
		IntegrationFixtures: []RecordReferenceV1{
			recordTestReference("tool:demo/fixture/one"),
			recordTestReference("tool:demo/fixture/two"),
		},
	}
	if len(target.Bindings) < 2 || len(target.Bindings[0].Artifacts) < 2 {
		t.Error("target must carry multiple bindings and multiple binding artifacts")
	}
	if len(target.Selections) < 2 || len(target.Selections[0].Payloads) < 2 {
		t.Error("target must carry multiple selections and multiple coupled payloads")
	}
	if len(target.Parameters) < 2 || len(target.IntegrationFixtures) < 2 {
		t.Error("target must carry multiple typed parameters and multiple fixtures")
	}
}

func TestCloneHelpersReturnIndependentValues(t *testing.T) {
	original := &TargetRecordV1{
		Schema:     TargetRecordSchemaV1,
		ID:         "tool:demo/target/debian/12/amd64",
		Selections: []TargetSelectionV1{{Name: "chromium", Payloads: []RecordReferenceV1{recordTestReference("tool:demo/payload/chromium")}}},
		Exports:    []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}},
		Probes:     []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"}},
	}
	original.Bindings = []TargetBindingV1{{
		Name:      "python",
		Artifacts: []RecordReferenceV1{recordTestReference("tool:demo/binding/python/linux-amd64")},
		Exports:   []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}},
		Probes:    []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"}},
	}}
	original.Parameters = []TargetParameterConstraintV1{{Name: "channel", Values: []string{"stable"}}}
	original.IntegrationFixtures = []RecordReferenceV1{recordTestReference("tool:demo/fixture/one")}

	clone := cloneTargetRecordV1(original)
	clone.Selections[0].Name = "firefox"
	clone.Selections[0].Payloads[0] = recordTestReference("tool:demo/payload/firefox")
	clone.Exports[0].Name = "other"
	clone.Probes[0].Args[0] = "--help"
	clone.Bindings[0].Artifacts[0] = recordTestReference("tool:demo/binding/python/other")
	clone.Bindings[0].Exports[0].Name = "other"
	clone.Bindings[0].Probes[0].Args[0] = "--help"
	clone.Parameters[0].Values[0] = "beta"
	clone.IntegrationFixtures[0] = recordTestReference("tool:demo/fixture/other")

	if original.Bindings[0].Artifacts[0].ID != "tool:demo/binding/python/linux-amd64" {
		t.Error("clone shares binding artifact storage with the original record")
	}
	if original.Bindings[0].Exports[0].Name != "demo" || original.Bindings[0].Probes[0].Args[0] != "--version" {
		t.Error("clone shares binding export or probe storage with the original record")
	}
	if original.Parameters[0].Values[0] != "stable" {
		t.Error("clone shares target parameter storage with the original record")
	}
	if original.IntegrationFixtures[0].ID != "tool:demo/fixture/one" {
		t.Error("clone shares integration fixture storage with the original record")
	}
	if original.Selections[0].Name != "chromium" {
		t.Error("clone shares selection storage with the original record")
	}
	if original.Selections[0].Payloads[0].ID != "tool:demo/payload/chromium" {
		t.Error("clone shares selection payload storage with the original record")
	}
	if original.Exports[0].Name != "demo" {
		t.Error("clone shares export storage with the original record")
	}
	if original.Probes[0].Args[0] != "--version" {
		t.Error("clone shares probe argument storage with the original record")
	}
}

func TestReleaseContractAndFixtureClonesAreIndependent(t *testing.T) {
	defaultValue := "stable"
	contract := &ReleaseContractV1{
		Schema:     ReleaseContractSchemaV1,
		ID:         "tool:demo/contract/1.2.3",
		Parameters: []ParameterSchemaV1{{Name: "channel", Default: &defaultValue, Values: []string{"stable", "beta"}}},
		Selections: SelectionRequestV1{
			Options:             []string{"chromium", "firefox"},
			CompatibilityGroups: [][]string{{"chromium", "ffmpeg"}},
		},
	}
	contractClone := cloneReleaseContractV1(contract)
	*contractClone.Parameters[0].Default = "beta"
	contractClone.Parameters[0].Values[0] = "nightly"
	contractClone.Selections.CompatibilityGroups[0][0] = "firefox"
	if *contract.Parameters[0].Default != "stable" {
		t.Error("clone shares the parameter default pointer with the original contract")
	}
	if contract.Parameters[0].Values[0] != "stable" {
		t.Error("clone shares parameter value storage with the original contract")
	}
	if contract.Selections.CompatibilityGroups[0][0] != "chromium" {
		t.Error("clone shares compatibility group storage with the original contract")
	}

	fixture := &IntegrationFixtureRecordV1{
		Schema:     IntegrationFixtureSchemaV1,
		ID:         "tool:demo/fixture/debian-12-amd64",
		Selections: []string{"chromium"},
		Parameters: []ParameterValueV1{{Name: "channel", Value: "stable"}},
	}
	fixtureClone := cloneIntegrationFixtureV1(fixture)
	fixtureClone.Parameters[0].Value = "beta"
	if fixture.Parameters[0].Value != "stable" {
		t.Error("clone shares fixture parameter storage with the original record")
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
	probes := []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"}}
	exports := []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}}
	references := []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/payloads/one")}
	defaultValue := "stable"

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
				Provenance:      []string{"https://example.com/releases"}}
			result := cloneReleaseManifestV1(value)
			return [2]any{value, &result}
		}},
		{"release contract", func() any {
			value := &ReleaseContractV1{Contexts: []string{"build"},
				Binding:            BindingRequestV1{Options: []string{"python"}},
				Selections:         SelectionRequestV1{Options: []string{"chromium"}, Defaults: []string{"chromium"}, CompatibilityGroups: [][]string{{"chromium"}}},
				Parameters:         []ParameterSchemaV1{{Name: "channel", Default: &defaultValue, Values: []string{"stable"}}},
				Probes:             probes,
				Exports:            exports,
				ResolverPrimitives: []string{"https-sha256"},
				Runtime:            &RecordRuntimeV1{Environment: []RecordEnvironmentVariableV1{{Name: "DEMO", Value: "1"}}}}
			result := cloneReleaseContractV1(value)
			return [2]any{value, &result}
		}},
		{"target", func() any {
			value := &TargetRecordV1{PackageSets: references, Payloads: references, IntegrationFixtures: references,
				Bindings:   []TargetBindingV1{{Name: "python", Artifacts: references, PackageSets: references, Exports: exports, Probes: probes}},
				Selections: []TargetSelectionV1{{Name: "chromium", Payloads: references, PackageSets: references, Exports: exports, Probes: probes}},
				Parameters: []TargetParameterConstraintV1{{Name: "channel", Values: []string{"stable"}}},
				Exports:    exports, Probes: probes}
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
			value := &PayloadRecordV1{Name: "chromium"}
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
			value := &IntegrationFixtureRecordV1{Selections: []string{"chromium"},
				Parameters: []ParameterValueV1{{Name: "channel", Value: "stable"}}}
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
