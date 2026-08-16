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
			Schema: ToolRecordSchemaV1, ID: "tool:demo", Name: "demo", Summary: "Demo tool",
			Upstream: "https://example.com/demo", Source: "https://example.com/source", License: "https://example.com/license",
			Releases: []RecordReferenceV1{recordTestReference(release + "/revisions/1/manifest")},
		},
		&ReleaseManifestV1{
			Schema: ReleaseManifestSchemaV1, ID: release + "/revisions/1/manifest", Tool: "demo", Version: "1.2.3", Revision: "1",
			Contract: recordTestReference(release + "/contract"), Targets: []RecordReferenceV1{recordTestReference(target)},
			ArtifactSources: []ArtifactSourceMappingV1{}, Provenance: []string{"https://example.com/releases/1.2.3"},
			ValidationProfile: recordTestReference(release + "/validation/profiles/default"),
		},
		&ReleaseContractV1{
			Schema: ReleaseContractSchemaV1, ID: release + "/contract", Contexts: []string{"build"},
			Binding:    BindingRequestV1{Options: []string{}, Required: false, Default: ""},
			Selections: SelectionRequestV1{Options: []string{}, Minimum: "0", Defaults: []string{}},
			Probes:     []RecordProbeV1{{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"}},
			Exports:    []ToolExportV1{{Name: "demo", Path: "/opt/demo/bin/demo"}}, ResolverPrimitives: []string{"https-sha256"},
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
			Schema: PayloadRecordSchemaV1, ID: release + "/payloads/chromium-linux-amd64", Selection: "chromium", Name: "chromium",
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
  "summary":"Demo tool",
  "upstream":"https://example.com/demo",
  "source":"https://example.com/source",
  "license":"https://example.com/license",
  "releases":[{"id":"tool:demo/releases/1.2.3/revisions/1/manifest","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]
}`)
	second := []byte(`{"releases":[{"digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","id":"tool:demo/releases/1.2.3/revisions/1/manifest"}],"license":"https://example.com/license","source":"https://example.com/source","upstream":"https://example.com/demo","summary":"Demo tool","name":"demo","id":"tool:demo","schema":"portable-tool-v1"}`)
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
		{name: "unknown field", payload: `{"schema":"portable-tool-v1","id":"tool:demo","name":"demo","summary":"x","upstream":"https://example.com","source":"https://example.com/source","license":"https://example.com/license","releases":[],"extra":true}`, want: "unknown field"},
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
		{name: "manifest revision", value: func() any { value := *(values[1].(*ReleaseManifestV1)); value.Revision = "01"; return &value }(), want: "canonical decimal"},
		{name: "contract context", value: func() any {
			value := *(values[2].(*ReleaseContractV1))
			value.Contexts = []string{"install"}
			return &value
		}(), want: "unsupported"},
		{name: "target ID", value: func() any { value := *(values[3].(*TargetRecordV1)); value.ID += "-wrong"; return &value }(), want: "must end with"},
		{name: "binding requirements", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.Requirements = []string{"support>=1,<2", "demo==1.2.3"}
			return &value
		}(), want: "unique sorted"},
		{name: "binding artifact size", value: func() any { value := *(values[5].(*BindingArtifactRecordV1)); value.Size = "042"; return &value }(), want: "canonical decimal"},
		{name: "payload escape", value: func() any { value := *(values[6].(*PayloadRecordV1)); value.Executable = "../chrome"; return &value }(), want: "invalid segment"},
		{name: "source duplicate mirror", value: func() any {
			value := *(values[7].(*ArtifactSourceRecordV1))
			value.Mirrors = []string{"https://example.com/a", "https://example.com/b", "https://example.com/a"}
			return &value
		}(), want: "must be unique"},
		{name: "package manager", value: func() any { value := *(values[8].(*NativePackageSetV1)); value.Manager = "dnf"; return &value }(), want: "identity is incomplete"},
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
	validSelections := SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "1", Defaults: []string{}}
	if err := validateSelectionRequestV1(validSelections); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		selections SelectionRequestV1
		want       string
	}{
		{name: "noncanonical minimum", selections: SelectionRequestV1{Options: []string{}, Minimum: "01", Defaults: []string{}}, want: "canonical decimal"},
		{name: "invalid option", selections: SelectionRequestV1{Options: []string{"Chromium"}, Minimum: "0", Defaults: []string{}}, want: "canonical identifiers"},
		{name: "minimum too large", selections: SelectionRequestV1{Options: []string{"chromium"}, Minimum: "2", Defaults: []string{}}, want: "exceeds"},
		{name: "unknown default", selections: SelectionRequestV1{Options: []string{"chromium"}, Minimum: "0", Defaults: []string{"webkit"}}, want: "not a declared option"},
		{name: "insufficient defaults", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "2", Defaults: []string{"chromium"}}, want: "do not satisfy"},
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
		"https://example.com:8443/archive.tar.gz",
	} {
		if err := validateSourceURLV1(valid); err != nil {
			t.Fatalf("valid URL %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"http://example.com/archive",
		"https://user:password@example.com/archive",
		"https://example.com/archive?token=secret",
		"https://example.com/archive#checksum",
		"https://EXAMPLE.com/archive",
		"https://example.com.:443/archive",
		"https://example.com:443/archive",
		"https://example.com/releases/jdk%2b21/archive",
		"https://example.com/releases/../archive",
		"https://éxample.com/archive",
	} {
		if err := validateSourceURLV1(invalid); err == nil {
			t.Fatalf("invalid URL %q was accepted", invalid)
		}
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

func TestRecordReferencesIDsAndQuantitiesAreCanonical(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
		want string
	}{
		{name: "empty tool name", run: func() error { return validateRecordIDV1("tool:") }, want: "invalid tool name"},
		{name: "uppercase ID", run: func() error { return validateRecordIDV1("tool:Demo") }, want: "unsupported character"},
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
