package toolcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
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
			Selections: []TargetSelectionV1{}, Probes: []RecordProbeV1{},
			IntegrationFixture: recordTestReference(release + "/validation/fixtures/debian-12-amd64"),
			ValidationProfile:  recordTestReference(release + "/validation/profiles/default"),
		},
		&BindingContractV1{
			Schema: BindingContractSchemaV1, ID: release + "/bindings/python/contract", Name: "python", Package: "demo",
			Requirements: []string{"demo==1.2.3", "support>=1,<2"}, SupportedPython: []string{"3.11", "3.12"},
			CLI: "/opt/demo/bin/demo",
		},
		&BindingArtifactRecordV1{
			Schema: BindingArtifactSchemaV1, ID: release + "/bindings/python/artifacts/linux-amd64", Binding: "python",
			Platform: "linux/amd64", Filename: "demo-1.2.3-py3-none-manylinux1_x86_64.whl", Size: "42", SHA256: recordTestDigest,
			Tags: []string{"py3-none-manylinux1_x86_64"}, RequiresPython: ">=3.11",
			BundledComponents: []BundledComponentV1{{Name: "nodejs", Version: "24.0.0", Path: "node"}, {Name: "playwright-core", Version: "1.2.3", Path: "package"}},
		},
		&PayloadRecordV1{
			Schema: PayloadRecordSchemaV1, ID: release + "/payloads/chromium/chromium-linux-amd64", Selection: "chromium", Name: "chromium",
			Revision: "1228", UpstreamVersion: "149.0.0", Platform: "linux/amd64", LogicalPath: "tools/demo/chromium.zip",
			Kind: "playwright-browser-archive", Size: "42", SHA256: recordTestDigest, Entries: "2", UnpackedSize: "84",
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
			Schema: IntegrationFixtureSchemaV1, ID: release + "/validation/fixtures/debian-12-amd64",
			Target: TargetIdentityV1{
				Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
				NativeArchitecture: "amd64", PackageManager: "apt",
			},
			BaseImage: "docker.io/library/debian:12-slim", BaseImageDigest: recordTestDigest,
			Context: "build", Binding: "", Selections: []string{},
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

func TestDecodeRecordV1AcceptsEverySchema(t *testing.T) {
	for _, value := range validRecordValuesV1() {
		value := value
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			payload, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			record, err := decodeRecordV1("record.json", payload)
			if err != nil {
				t.Fatal(err)
			}
			if record.ID != recordIDV1(value) || record.Schema == "" || record.Digest == "" || record.Value == nil {
				t.Fatalf("decoded record = %#v", record)
			}
		})
	}
}

func TestDecodeRecordV1UsesCanonicalSemanticIdentity(t *testing.T) {
	first := []byte(`{
  "schema":"portable-tool-v1",
  "id":"tool:demo",
  "name":"demo",
  "version_scheme":"semver",
  "summary":"Demo tool",
  "upstream":"https://example.com/demo",
  "source":"https://example.com/source",
  "license":"https://example.com/license",
  "releases":[{"id":"tool:demo/releases/1.2.3/revisions/1/manifest","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]
}`)
	second := []byte(`{"releases":[{"digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","id":"tool:demo/releases/1.2.3/revisions/1/manifest"}],"license":"https://example.com/license","source":"https://example.com/source","upstream":"https://example.com/demo","summary":"Demo tool","version_scheme":"semver","name":"demo","id":"tool:demo","schema":"portable-tool-v1"}`)
	left, err := decodeRecordV1("first.json", first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := decodeRecordV1("second.json", second)
	if err != nil {
		t.Fatal(err)
	}
	if left.Digest != right.Digest {
		t.Fatalf("semantic digests differ: %s != %s", left.Digest, right.Digest)
	}
}

func TestDecodeRecordV1RejectsUnknownSchemaFieldAndMissingHeader(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		want    string
	}{
		{name: "unknown schema", payload: `{"schema":"portable-tool-future-v1","id":"tool:demo"}`, want: "unsupported schema"},
		{name: "unknown field", payload: `{"schema":"portable-tool-v1","id":"tool:demo","name":"demo","version_scheme":"semver","summary":"x","upstream":"https://example.com","source":"https://example.com/source","license":"https://example.com/license","releases":[],"extra":true}`, want: "unknown field"},
		{name: "case-variant field", payload: `{"schema":"portable-tool-v1","Schema":"portable-tool-v1","id":"tool:demo","name":"demo","version_scheme":"semver","summary":"x","upstream":"https://example.com","source":"https://example.com/source","license":"https://example.com/license","releases":[]}`, want: `unknown field "Schema"`},
		{name: "nested case-variant field", payload: `{"schema":"portable-tool-v1","id":"tool:demo","name":"demo","version_scheme":"semver","summary":"x","upstream":"https://example.com","source":"https://example.com/source","license":"https://example.com/license","releases":[{"ID":"tool:demo/releases/1/revisions/1/manifest","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]}`, want: `unknown field "ID"`},
		{name: "null boolean", payload: `{"schema":"portable-tool-release-contract-v1","id":"tool:demo/releases/1/contract","contexts":["build"],"supported_reploy":">=0.0.0","binding":{"options":[],"required":null,"default":""},"selections":{"options":[],"minimum":"0","maximum":"0","defaults":[],"compatibility_groups":[]},"probes":[],"exports":[],"resolver_primitives":["https-sha256"]}`, want: "JSON null is not valid"},
		{name: "missing boolean", payload: `{"schema":"portable-tool-release-contract-v1","id":"tool:demo/releases/1/contract","contexts":["build"],"supported_reploy":">=0.0.0","binding":{"options":[],"default":""},"selections":{"options":[],"minimum":"0","maximum":"0","defaults":[],"compatibility_groups":[]},"probes":[],"exports":[],"resolver_primitives":["https-sha256"]}`, want: `required field "required" is missing`},
		{name: "missing schema", payload: `{"id":"tool:demo"}`, want: "schema is required"},
		{name: "missing ID", payload: `{"schema":"portable-tool-v1"}`, want: "ID is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeRecordV1("record.json", []byte(test.payload))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateStrictJSONV1RejectsAmbiguousAndOversizedInput(t *testing.T) {
	tooManyValues := "[" + strings.Repeat("null,", maxDefinitionJSONMembers) + "null]"
	tooDeep := strings.Repeat("[", maxDefinitionJSONDepth+2) + "null" + strings.Repeat("]", maxDefinitionJSONDepth+2)
	tests := []struct {
		name    string
		payload []byte
		want    string
	}{
		{name: "empty", payload: nil, want: "record size"},
		{name: "invalid UTF-8", payload: []byte{'{', '"', 0xff, '"', ':', 'n', 'u', 'l', 'l', '}'}, want: "valid UTF-8"},
		{name: "duplicate member", payload: []byte(`{"a":null,"a":null}`), want: "duplicate object member"},
		{name: "number", payload: []byte(`{"size":1}`), want: "JSON numbers are not supported"},
		{name: "unpaired high surrogate", payload: []byte(`{"value":"\ud800"}`), want: "unpaired UTF-16 surrogate"},
		{name: "unpaired low surrogate", payload: []byte(`{"value":"\udc00"}`), want: "unpaired UTF-16 surrogate"},
		{name: "high surrogate without low", payload: []byte(`{"value":"\ud800\u0041"}`), want: "unpaired UTF-16 surrogate"},
		{name: "trailing value", payload: []byte(`{} {}`), want: "trailing JSON token"},
		{name: "too deep", payload: []byte(tooDeep), want: "JSON nesting exceeds"},
		{name: "too many members", payload: []byte(tooManyValues), want: "JSON member count exceeds"},
		{name: "long string", payload: []byte(`{"value":"` + strings.Repeat("x", maxDefinitionJSONStringBytes+1) + `"}`), want: "JSON string exceeds"},
		{name: "large file", payload: bytes.Repeat([]byte{' '}, maxDefinitionFileBytes+1), want: "record size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateStrictJSONV1(test.payload)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	for _, payload := range [][]byte{[]byte(`{"value":"\ud83d\ude00"}`), []byte(`{"value":"�"}`)} {
		if err := validateStrictJSONV1(payload); err != nil {
			t.Fatalf("valid Unicode payload %q: %v", payload, err)
		}
	}
}

func TestValidateLoadedRecordV1RejectsInvalidFieldsBySchema(t *testing.T) {
	values := validRecordValuesV1()
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "tool URL query", value: func() any { value := *(values[0].(*ToolRecordV1)); value.Source += "?token=secret"; return &value }(), want: "credential-free HTTPS"},
		{name: "tool version scheme", value: func() any { value := *(values[0].(*ToolRecordV1)); value.VersionScheme = "debian"; return &value }(), want: "version scheme is unsupported"},
		{name: "ordered default version", value: func() any { value := *(values[0].(*ToolRecordV1)); value.DefaultVersion = "1.2.3"; return &value }(), want: "must not declare"},
		{name: "opaque default version", value: func() any {
			value := *(values[0].(*ToolRecordV1))
			value.VersionScheme = "opaque"
			return &value
		}(), want: "requires a canonical default"},
		{name: "manifest revision", value: func() any { value := *(values[1].(*ReleaseManifestV1)); value.Revision = "01"; return &value }(), want: "canonical decimal"},
		{name: "manifest duplicate alias", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Aliases = []string{"1.2", "1.2"}
			return &value
		}(), want: "unique sorted"},
		{name: "manifest exact alias", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Aliases = []string{"1.2.3"}
			return &value
		}(), want: "redundantly equals"},
		{name: "manifest unencoded version ID", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Version = "1!2"
			return &value
		}(), want: "release manifest ID must be"},
		{name: "manifest duplicate alias", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Aliases = []string{"1", "1"}
			return &value
		}(), want: "unique, sorted"},
		{name: "manifest exact-version alias", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Aliases = []string{"1.2.3"}
			return &value
		}(), want: "different from the exact version"},
		{name: "manifest equivalent provenance", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Provenance = []string{"https://example.com/a", "https://example.com/%61"}
			return &value
		}(), want: "must be unique"},
		{name: "contract context", value: func() any {
			value := *(values[2].(*ReleaseContractV1))
			value.Contexts = []string{"install"}
			return &value
		}(), want: "unsupported"},
		{name: "contract supported Reploy", value: func() any {
			value := *(values[2].(*ReleaseContractV1))
			value.SupportedReploy = ">= 0.0"
			return &value
		}(), want: "canonical SemVer"},
		{name: "contract ID", value: func() any {
			value := *(values[2].(*ReleaseContractV1))
			value.ID = "tool:demo/releases/1.2.3/payloads/contract"
			return &value
		}(), want: "release contract ID must use"},
		{name: "target ID", value: func() any { value := *(values[3].(*TargetRecordV1)); value.ID += "-wrong"; return &value }(), want: "must end with"},
		{name: "empty target selection", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.Selections = []TargetSelectionV1{{Name: "browser", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, Probes: []RecordProbeV1{}}}
			return &value
		}(), want: "must contribute"},
		{name: "binding requirements", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.Requirements = []string{"support>=1,<2", "demo==1.2.3"}
			return &value
		}(), want: "unique sorted"},
		{name: "binding artifact size", value: func() any { value := *(values[5].(*BindingArtifactRecordV1)); value.Size = "042"; return &value }(), want: "canonical decimal"},
		{name: "payload escape", value: func() any { value := *(values[6].(*PayloadRecordV1)); value.Executable = "../chrome"; return &value }(), want: "invalid segment"},
		{name: "payload ID", value: func() any {
			value := *(values[6].(*PayloadRecordV1))
			value.ID = "tool:demo/releases/1.2.3/bindings/chromium"
			return &value
		}(), want: "release payload namespace"},
		{name: "source duplicate mirror", value: func() any {
			value := *(values[7].(*ArtifactSourceRecordV1))
			value.Mirrors = []string{"https://example.com/a", "https://example.com/b", "https://example.com/a"}
			return &value
		}(), want: "must be unique"},
		{name: "source equivalent mirror", value: func() any {
			value := *(values[7].(*ArtifactSourceRecordV1))
			value.Mirrors = []string{"https://example.com/a", "https://example.com/%61"}
			return &value
		}(), want: "must be unique"},
		{name: "source equivalent provenance", value: func() any {
			value := *(values[7].(*ArtifactSourceRecordV1))
			value.Provenance = []string{"https://example.com/a", "https://example.com/%61"}
			return &value
		}(), want: "must be unique"},
		{name: "package manager", value: func() any { value := *(values[8].(*NativePackageSetV1)); value.Manager = "dnf"; return &value }(), want: "identity is incomplete"},
		{name: "fixture base image", value: func() any {
			value := *(values[9].(*IntegrationFixtureRecordV1))
			value.BaseImage = "https://example.com/image:tag"
			return &value
		}(), want: "canonical tagged OCI reference"},
		{name: "profile network", value: func() any {
			value := *(values[10].(*ValidationProfileRecordV1))
			value.Network = "default"
			return &value
		}(), want: "disable networking"},
		{name: "profile validator version", value: func() any {
			value := *(values[10].(*ValidationProfileRecordV1))
			value.ValidatorVersion = ""
			return &value
		}(), want: "validator version"},
		{name: "profile missing probes", value: func() any {
			value := *(values[10].(*ValidationProfileRecordV1))
			value.Probes = []RecordProbeV1{}
			return &value
		}(), want: "nonempty bounded array"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := loadedRecordV1{ID: recordIDV1(test.value), Schema: recordSchemaV1(test.value), Value: test.value}
			err := validateLoadedRecordV1(record)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	valid := values[0].(*ToolRecordV1)
	if err := validateLoadedRecordV1(loadedRecordV1{ID: valid.ID, Schema: ReleaseContractSchemaV1, Value: valid}); err == nil || !strings.Contains(err.Error(), "identity is inconsistent") {
		t.Fatalf("mismatched loaded schema error = %v", err)
	}
}

func TestReleaseVersionSegmentsAreReversibleAndCanonical(t *testing.T) {
	for version, want := range map[string]string{
		"1.2.3": "1.2.3",
		"1!2":   "1%212",
		"A_B":   "A_B",
		".":     "%2E",
		"..":    "%2E%2E",
		"雪":     "%E9%9B%AA",
	} {
		encoded, err := encodeToolVersionSegmentV1(version)
		if err != nil || encoded != want {
			t.Fatalf("encode %q = %q, %v; want %q", version, encoded, err, want)
		}
		decoded, err := decodeToolVersionSegmentV1(encoded)
		if err != nil || decoded != version {
			t.Fatalf("decode %q = %q, %v; want %q", encoded, decoded, err, version)
		}
	}
	for _, encoded := range []string{"", "1%", "1%2f2", "1%312", ".", "..", "%FF"} {
		if _, err := decodeToolVersionSegmentV1(encoded); err == nil {
			t.Fatalf("noncanonical encoded version %q was accepted", encoded)
		}
	}
}

func TestReleaseManifestAcceptsEncodedSchemeNativeVersion(t *testing.T) {
	value := *(validRecordValuesV1()[1].(*ReleaseManifestV1))
	value.Version = "1!2"
	value.ID = "tool:demo/releases/1%212/revisions/1/manifest"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseContractAcceptsEncodedSchemeNativeVersion(t *testing.T) {
	value := *(validRecordValuesV1()[2].(*ReleaseContractV1))
	value.ID = "tool:demo/releases/1%212/contract"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestToolRecordAcceptsOpaqueDefaultVersion(t *testing.T) {
	value := *(validRecordValuesV1()[0].(*ToolRecordV1))
	value.VersionScheme = "opaque"
	value.DefaultVersion = "latest!vetted"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateToolReleaseIndexV1(t *testing.T) {
	baseTool := *(validRecordValuesV1()[0].(*ToolRecordV1))
	baseManifest := *(validRecordValuesV1()[1].(*ReleaseManifestV1))
	if err := validateToolReleaseIndexV1(&baseTool, []*ReleaseManifestV1{&baseManifest}); err != nil {
		t.Fatal(err)
	}
	pepTool := baseTool
	pepTool.VersionScheme = "pep440"
	pepManifest := baseManifest
	pepManifest.Version = "1!2.0"
	pepManifest.Aliases = []string{}
	if err := validateToolReleaseIndexV1(&pepTool, []*ReleaseManifestV1{&pepManifest}); err != nil {
		t.Fatal(err)
	}
	opaqueTool := baseTool
	opaqueTool.VersionScheme = "opaque"
	opaqueTool.DefaultVersion = "stable!v1"
	opaqueManifest := baseManifest
	opaqueManifest.Version = opaqueTool.DefaultVersion
	opaqueManifest.Aliases = []string{}
	if err := validateToolReleaseIndexV1(&opaqueTool, []*ReleaseManifestV1{&opaqueManifest}); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		scheme    string
		version   string
		aliases   []string
		other     *ReleaseManifestV1
		defaultV  string
		wantError string
	}{
		{name: "noncanonical semver", scheme: "semver", version: "1.2", aliases: []string{}, wantError: "canonical SemVer"},
		{name: "noncanonical pep440", scheme: "pep440", version: "01.2", aliases: []string{}, wantError: "canonical PEP 440"},
		{name: "noncanonical integer", scheme: "integer", version: "021", aliases: []string{}, wantError: "canonical decimal"},
		{name: "opaque default absent", scheme: "opaque", version: "stable", aliases: []string{}, defaultV: "latest", wantError: "not an advertised exact release"},
		{name: "alias collision", scheme: "semver", version: "1.2.3", aliases: []string{"2"}, other: &ReleaseManifestV1{Tool: "demo", Version: "2.0.0", Aliases: []string{"2"}}, wantError: "maps to both"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := baseTool
			tool.VersionScheme = test.scheme
			tool.DefaultVersion = test.defaultV
			manifest := baseManifest
			manifest.Version = test.version
			manifest.Aliases = test.aliases
			manifests := []*ReleaseManifestV1{&manifest}
			if test.other != nil {
				other := *test.other
				manifests = append(manifests, &other)
			}
			err := validateToolReleaseIndexV1(&tool, manifests)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestTargetSelectionV1PreservesScopedContributions(t *testing.T) {
	value := *(validRecordValuesV1()[3].(*TargetRecordV1))
	value.Selections = []TargetSelectionV1{{
		Name:        "browser",
		Payloads:    []RecordReferenceV1{},
		PackageSets: []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/package-sets/browser")},
		Exports:     []ToolExportV1{{Name: "browser", Path: "/opt/demo/bin/browser"}},
		Probes:      []RecordProbeV1{{Path: "/opt/demo/bin/browser", Args: []string{"--version"}, Network: "none"}},
	}}
	payload, err := json.Marshal(&value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRecordV1("target.json", payload)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := decoded.Value.(*TargetRecordV1)
	if !ok || len(target.Selections) != 1 || len(target.Selections[0].PackageSets) != 1 || len(target.Selections[0].Exports) != 1 || len(target.Selections[0].Probes) != 1 {
		t.Fatalf("decoded target selection lost scoped contributions: %#v", decoded.Value)
	}
}

func TestPayloadIDsEncodeSelectionAndPlatform(t *testing.T) {
	selected := *(validRecordValuesV1()[6].(*PayloadRecordV1))
	if err := validateLoadedRecordV1(loadedRecordV1{ID: selected.ID, Schema: selected.Schema, Value: &selected}); err != nil {
		t.Fatal(err)
	}
	unconditional := selected
	unconditional.Selection = ""
	unconditional.ID = "tool:demo/releases/1.2.3/payloads/chromium-linux-amd64"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: unconditional.ID, Schema: unconditional.Schema, Value: &unconditional}); err != nil {
		t.Fatal(err)
	}
}

func TestValidationProfileAcceptsEncodedSchemeNativeVersion(t *testing.T) {
	value := *(validRecordValuesV1()[10].(*ValidationProfileRecordV1))
	value.Version = "1!2"
	value.ID = "tool:demo/releases/1%212/validation/profiles/default"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequestPoliciesV1(t *testing.T) {
	validBinding := BindingRequestV1{Options: []string{"python"}, Required: true, Default: ""}
	if err := validateBindingRequestV1(validBinding); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		binding BindingRequestV1
		want    string
	}{
		{name: "nil options", binding: BindingRequestV1{Options: nil}, want: "must use an array"},
		{name: "required empty", binding: BindingRequestV1{Options: []string{}, Required: true}, want: "at least one option"},
		{name: "invalid option", binding: BindingRequestV1{Options: []string{"Python"}}, want: "canonical identifiers"},
		{name: "unknown default", binding: BindingRequestV1{Options: []string{"python"}, Default: "node"}, want: "default binding"},
	} {
		t.Run("binding "+test.name, func(t *testing.T) {
			err := validateBindingRequestV1(test.binding)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	validSelections := SelectionRequestV1{
		Options: []string{"chromium", "webkit"}, Minimum: "1", Maximum: "2", Defaults: []string{},
		CompatibilityGroups: [][]string{{"chromium", "webkit"}},
	}
	if err := validateSelectionRequestV1(validSelections); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		selections SelectionRequestV1
		want       string
	}{
		{name: "noncanonical minimum", selections: SelectionRequestV1{Options: []string{}, Minimum: "01", Maximum: "0", Defaults: []string{}, CompatibilityGroups: [][]string{}}, want: "canonical decimal"},
		{name: "noncanonical maximum", selections: SelectionRequestV1{Options: []string{}, Minimum: "0", Maximum: "01", Defaults: []string{}, CompatibilityGroups: [][]string{}}, want: "canonical decimal"},
		{name: "invalid option", selections: SelectionRequestV1{Options: []string{"Chromium"}, Minimum: "0", Maximum: "1", Defaults: []string{}, CompatibilityGroups: [][]string{{"Chromium"}}}, want: "canonical identifiers"},
		{name: "minimum too large", selections: SelectionRequestV1{Options: []string{"chromium"}, Minimum: "2", Maximum: "1", Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium"}}}, want: "cardinality"},
		{name: "uncovered option", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "0", Maximum: "2", Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium"}}}, want: "do not cover"},
		{name: "nonmaximal groups", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "0", Maximum: "2", Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium"}, {"chromium", "webkit"}}}, want: "must be maximal"},
		{name: "unknown default", selections: SelectionRequestV1{Options: []string{"chromium"}, Minimum: "0", Maximum: "1", Defaults: []string{"webkit"}, CompatibilityGroups: [][]string{{"chromium"}}}, want: "not a declared option"},
		{name: "insufficient defaults", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "2", Maximum: "2", Defaults: []string{"chromium"}, CompatibilityGroups: [][]string{{"chromium", "webkit"}}}, want: "do not satisfy"},
		{name: "incompatible defaults", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "1", Maximum: "2", Defaults: []string{"chromium", "webkit"}, CompatibilityGroups: [][]string{{"chromium"}, {"webkit"}}}, want: "do not satisfy"},
	} {
		t.Run("selections "+test.name, func(t *testing.T) {
			err := validateSelectionRequestV1(test.selections)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateSourceURLV1RequiresCanonicalCredentialFreeHTTPS(t *testing.T) {
	for _, valid := range []string{
		"https://example.com",
		"https://example.com/releases/jdk-21.0.12%2B8/archive.tar.gz",
		"https://example.com/releases/archive%23checksum",
		"https://example.com:8443/archive.tar.gz",
	} {
		if err := validateSourceURLV1(valid); err != nil {
			t.Fatalf("valid URL %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"http://example.com/archive",
		"https://user:password@example.com/archive",
		"https://example.com/archive?",
		"https://example.com/archive?token=secret",
		"https://example.com/archive#",
		"https://example.com/archive#checksum",
		"https://EXAMPLE.com/archive",
		"https://example.com:/archive",
		"https://example.com.:443/archive",
		"https://example.com:443/archive",
		"https://example.com:0443/archive",
		"https://127.000.000.001/archive",
		"https://[fe80::1%25eth0]/archive",
		"https://example.com/releases/jdk%2b21/archive",
		"https://example.com/releases/../archive",
		"https://éxample.com/archive",
	} {
		if err := validateSourceURLV1(invalid); err == nil {
			t.Fatalf("invalid URL %q was accepted", invalid)
		}
	}
	canonicalPlain, err := canonicalSourceURLV1("https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	canonicalEscaped, err := canonicalSourceURLV1("https://example.com/%61")
	if err != nil {
		t.Fatal(err)
	}
	if canonicalPlain != canonicalEscaped {
		t.Fatalf("equivalent URL identities differ: %q != %q", canonicalPlain, canonicalEscaped)
	}
	canonicalRoot, err := canonicalSourceURLV1("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	canonicalEmptyPath, err := canonicalSourceURLV1("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if canonicalRoot != canonicalEmptyPath {
		t.Fatalf("root URL identities differ: %q != %q", canonicalRoot, canonicalEmptyPath)
	}
	canonicalIPv6, err := canonicalSourceURLV1("https://[2001:db8::1]/archive")
	if err != nil {
		t.Fatal(err)
	}
	canonicalExpandedIPv6, err := canonicalSourceURLV1("https://[2001:0db8:0:0:0:0:0:1]/archive")
	if err != nil {
		t.Fatal(err)
	}
	if canonicalIPv6 != canonicalExpandedIPv6 {
		t.Fatalf("IPv6 URL identities differ: %q != %q", canonicalIPv6, canonicalExpandedIPv6)
	}
	canonicalReserved, err := canonicalSourceURLV1("https://example.com/%2B")
	if err != nil {
		t.Fatal(err)
	}
	if canonicalReserved == "https://example.com/+" {
		t.Fatal("reserved percent escape was normalized")
	}
}

func TestRecordCollectionLimitsV1(t *testing.T) {
	if err := validateReferenceListV1("references", nil); err == nil {
		t.Fatal("nil reference list was accepted")
	}
	references := make([]RecordReferenceV1, maxDefinitionReferences+1)
	if err := validateReferenceListV1("references", references); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("oversized reference error = %v", err)
	}
	values := validRecordValuesV1()
	source := *(values[7].(*ArtifactSourceRecordV1))
	source.Mirrors = make([]string, maxDefinitionArtifactMirrors+1)
	if err := validateLoadedRecordV1(loadedRecordV1{ID: source.ID, Schema: source.Schema, Value: &source}); err == nil || !strings.Contains(err.Error(), "between 1 and") {
		t.Fatalf("oversized mirror error = %v", err)
	}
	packages := *(values[8].(*NativePackageSetV1))
	packages.Requirements = []string{"bad\nrequirement"}
	if err := validateLoadedRecordV1(loadedRecordV1{ID: packages.ID, Schema: packages.Schema, Value: &packages}); err == nil || !strings.Contains(err.Error(), "canonical values") {
		t.Fatalf("control-character requirement error = %v", err)
	}
	binding := *(values[4].(*BindingContractV1))
	binding.Package = "bad package"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: binding.ID, Schema: binding.Schema, Value: &binding}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("invalid binding package error = %v", err)
	}
	payload := *(values[6].(*PayloadRecordV1))
	payload.Revision = "bad\nrevision"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: payload.ID, Schema: payload.Schema, Value: &payload}); err == nil || !strings.Contains(err.Error(), "identity is incomplete") {
		t.Fatalf("invalid payload revision error = %v", err)
	}
}

func TestDecodeValidationEvidenceV1(t *testing.T) {
	evidence := ValidationEvidenceV1{
		Schema: ValidationEvidenceSchemaV1, Tool: "demo", Version: "1.2.3", Revision: "1",
		ManifestDigest: recordTestDigest, SelectedClosureDigest: recordTestDigest,
		Target: TargetIdentityV1{
			Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
			NativeArchitecture: "amd64", PackageManager: "apt",
		},
		BaseImageDigest: recordTestDigest, Binding: "python", Selections: []string{"chromium"},
		Fixture: "fixtures/demo-debian-12-amd64", ValidatorVersion: "validator-v1", Result: "pass",
		ProbeDigests: []canonical.Digest{recordTestDigest},
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeValidationEvidenceV1("evidence.json", payload); err != nil {
		t.Fatal(err)
	}
	caseVariantPayload := bytes.Replace(payload, []byte(`"schema"`), []byte(`"Schema"`), 1)
	if _, err := decodeValidationEvidenceV1("evidence.json", caseVariantPayload); err == nil || !strings.Contains(err.Error(), `unknown field "Schema"`) {
		t.Fatalf("case-variant evidence error = %v", err)
	}
	evidence.Result = "trusted"
	payload, err = json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeValidationEvidenceV1("evidence.json", payload); err == nil || !strings.Contains(err.Error(), "result is invalid") {
		t.Fatalf("invalid evidence error = %v", err)
	}
}

func TestValidateRuntimeV1RejectsInconsistentContracts(t *testing.T) {
	valid := RecordRuntimeV1{
		InstallRoot: "/opt/demo", Environment: []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "/opt/demo"}},
	}
	if err := validateRuntimeV1([]string{"build", "runtime"}, &valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		contexts []string
		mutate   func(*RecordRuntimeV1)
		want     string
	}{
		{name: "missing runtime context", contexts: []string{"build"}, want: "inconsistent with contexts"},
		{name: "relative root", contexts: []string{"runtime"}, mutate: func(value *RecordRuntimeV1) { value.InstallRoot = "opt/demo" }, want: "inconsistent with contexts"},
		{name: "nil environment", contexts: []string{"runtime"}, mutate: func(value *RecordRuntimeV1) { value.Environment = nil }, want: "bounded array"},
		{name: "invalid environment name", contexts: []string{"runtime"}, mutate: func(value *RecordRuntimeV1) { value.Environment[0].Name = "demo_home" }, want: "unique and sorted"},
		{name: "environment NUL", contexts: []string{"runtime"}, mutate: func(value *RecordRuntimeV1) { value.Environment[0].Value = "bad\x00value" }, want: "unique and sorted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.Environment = append([]RecordEnvironmentVariableV1{}, valid.Environment...)
			if test.mutate != nil {
				test.mutate(&value)
			}
			err := validateRuntimeV1(test.contexts, &value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	if err := validateRuntimeV1([]string{"runtime"}, nil); err == nil || !strings.Contains(err.Error(), "requires a runtime contract") {
		t.Fatalf("missing runtime error = %v", err)
	}
}

func TestValidateProbeV1RequiresOfflineCanonicalExecution(t *testing.T) {
	valid := RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"}
	if err := validateProbeV1(valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*RecordProbeV1)
		want   string
	}{
		{name: "relative path", mutate: func(value *RecordProbeV1) { value.Path = "demo" }, want: "absolute path"},
		{name: "nil args", mutate: func(value *RecordProbeV1) { value.Args = nil }, want: "argument array"},
		{name: "network", mutate: func(value *RecordProbeV1) { value.Network = "host" }, want: "network=none"},
		{name: "NUL", mutate: func(value *RecordProbeV1) { value.Args = []string{"bad\x00arg"} }, want: "control characters"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.Args = append([]string{}, valid.Args...)
			test.mutate(&value)
			err := validateProbeV1(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateValidationEvidenceV1RejectsUnboundClaims(t *testing.T) {
	valid := ValidationEvidenceV1{
		Schema: ValidationEvidenceSchemaV1, Tool: "demo", Version: "1.2.3", Revision: "1",
		ManifestDigest: recordTestDigest, SelectedClosureDigest: recordTestDigest,
		Target: TargetIdentityV1{
			Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
			NativeArchitecture: "amd64", PackageManager: "apt",
		},
		BaseImageDigest: recordTestDigest, Binding: "python", Selections: []string{"chromium"},
		Fixture: "fixtures/demo", ValidatorVersion: "validator-v1", Result: "pass", ProbeDigests: []canonical.Digest{recordTestDigest},
	}
	for _, test := range []struct {
		name   string
		mutate func(*ValidationEvidenceV1)
		want   string
	}{
		{name: "manifest digest", mutate: func(value *ValidationEvidenceV1) { value.ManifestDigest = "bad" }, want: "manifest digest"},
		{name: "closure digest", mutate: func(value *ValidationEvidenceV1) { value.SelectedClosureDigest = "bad" }, want: "selected closure digest"},
		{name: "base image digest", mutate: func(value *ValidationEvidenceV1) { value.BaseImageDigest = "bad" }, want: "base image digest"},
		{name: "target", mutate: func(value *ValidationEvidenceV1) { value.Target.NativeArchitecture = "arm64" }, want: "target"},
		{name: "binding", mutate: func(value *ValidationEvidenceV1) { value.Binding = "Python" }, want: "binding is invalid"},
		{name: "selections", mutate: func(value *ValidationEvidenceV1) { value.Selections = nil }, want: "must use an array"},
		{name: "fixture", mutate: func(value *ValidationEvidenceV1) { value.Fixture = " fixture " }, want: "fixture, validator, or result"},
		{name: "probe digests", mutate: func(value *ValidationEvidenceV1) { value.ProbeDigests = nil }, want: "nonempty bounded array"},
		{name: "bad probe digest", mutate: func(value *ValidationEvidenceV1) { value.ProbeDigests = []canonical.Digest{"bad"} }, want: "probe digest 0"},
		{name: "duplicate probe digest", mutate: func(value *ValidationEvidenceV1) {
			value.ProbeDigests = []canonical.Digest{recordTestDigest, recordTestDigest}
		}, want: "unique and sorted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.Selections = append([]string{}, valid.Selections...)
			value.ProbeDigests = append([]canonical.Digest{}, valid.ProbeDigests...)
			test.mutate(&value)
			err := validateValidationEvidenceV1(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidationEvidenceAcceptsSchemeNativeVersion(t *testing.T) {
	value := ValidationEvidenceV1{
		Schema: ValidationEvidenceSchemaV1, Tool: "demo", Version: "1!2", Revision: "1",
		ManifestDigest: recordTestDigest, SelectedClosureDigest: recordTestDigest,
		Target: TargetIdentityV1{
			Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12", OCIArchitecture: "amd64",
			NativeArchitecture: "amd64", PackageManager: "apt",
		},
		BaseImageDigest: recordTestDigest, Binding: "python", Selections: []string{"chromium"},
		Fixture: "fixtures/demo", ValidatorVersion: "validator-v1", Result: "pass", ProbeDigests: []canonical.Digest{recordTestDigest},
	}
	if err := validateValidationEvidenceV1(value); err != nil {
		t.Fatal(err)
	}
}

func TestRecordReferencesIDsAndQuantitiesAreCanonical(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
		want string
	}{
		{name: "empty tool name", run: func() error { return validateRecordIDV1("tool:") }, want: "invalid tool name"},
		{name: "uppercase tool name", run: func() error { return validateRecordIDV1("tool:Demo") }, want: "invalid tool name"},
		{name: "empty segment", run: func() error { return validateRecordIDV1("tool:demo//contract") }, want: "invalid path segment"},
		{name: "bad digest", run: func() error {
			return validateRecordReferenceV1(RecordReferenceV1{ID: "tool:demo", Digest: "sha256:ABC"})
		}, want: "digest"},
		{name: "leading zero", run: func() error { return validateCanonicalDecimalV1("size", "01", true) }, want: "canonical decimal"},
		{name: "zero", run: func() error { return validateCanonicalDecimalV1("size", "0", true) }, want: "positive decimal"},
		{name: "overflow", run: func() error { return validateCanonicalDecimalV1("size", "9223372036854775808", true) }, want: "positive decimal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}
