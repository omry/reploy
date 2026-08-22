package toolcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

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
  "documentation":"https://example.com/docs",
  "releases":[{"id":"tool:demo/releases/1.2.3/revisions/1/manifest","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]
}`)
	second := []byte(`{"releases":[{"digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","id":"tool:demo/releases/1.2.3/revisions/1/manifest"}],"license":"https://example.com/license","documentation":"https://example.com/docs","source":"https://example.com/source","upstream":"https://example.com/demo","summary":"Demo tool","version_scheme":"semver","name":"demo","id":"tool:demo","schema":"portable-tool-v1"}`)
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
		{name: "unknown field", payload: `{"schema":"portable-tool-v1","id":"tool:demo","name":"demo","version_scheme":"semver","summary":"x","upstream":"https://example.com","source":"https://example.com/source","license":"https://example.com/license","documentation":"https://example.com/docs","releases":[],"extra":true}`, want: "unknown field"},
		{name: "case-variant field", payload: `{"schema":"portable-tool-v1","Schema":"portable-tool-v1","id":"tool:demo","name":"demo","version_scheme":"semver","summary":"x","upstream":"https://example.com","source":"https://example.com/source","license":"https://example.com/license","documentation":"https://example.com/docs","releases":[]}`, want: `unknown field "Schema"`},
		{name: "nested case-variant field", payload: `{"schema":"portable-tool-v1","id":"tool:demo","name":"demo","version_scheme":"semver","summary":"x","upstream":"https://example.com","source":"https://example.com/source","license":"https://example.com/license","documentation":"https://example.com/docs","releases":[{"ID":"tool:demo/releases/1/revisions/1/manifest","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]}`, want: `unknown field "ID"`},
		{name: "null binding", payload: `{"schema":"portable-tool-release-contract-v1","id":"tool:demo/releases/1/contract","contexts":["build"],"supported_reploy":">=0.0.0","binding":null,"selections":{"dimensions":[],"combinations":[]},"exports":[],"resolver_primitives":["https-sha256"],"compatibility_constraints":[]}`, want: "JSON null is not valid"},
		{name: "missing binding options", payload: `{"schema":"portable-tool-release-contract-v1","id":"tool:demo/releases/1/contract","contexts":["build"],"supported_reploy":">=0.0.0","binding":{},"selections":{"dimensions":[],"combinations":[]},"exports":[],"resolver_primitives":["https-sha256"],"compatibility_constraints":[]}`, want: `required field "options" is missing`},
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

func TestValidateSourceURLV1RequiresCanonicalCredentialFreeHTTPS(t *testing.T) {
	for _, valid := range []string{
		"https://example.com/",
		"https://example.com/releases/jdk-21.0.12%2B8/archive.tar.gz",
		"https://example.com/releases/archive%23checksum",
		"https://example.com:8443/archive.tar.gz",
		"https://0xrelease.example.com/archive.tar.gz",
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
		"https://127.1/archive",
		"https://017700000001/archive",
		"https://0x7f000001/archive",
		"https://0x7f.0x0.0x0.0x1/archive",
		"https://[fe80::1%25eth0]/archive",
		"https://example.com/releases/jdk%2b21/archive",
		"https://example.com/releases/../archive",
		"https://éxample.com/archive",
		"https://example.com",
		"https://example.com/%61",
		"https://[2001:0db8:0:0:0:0:0:1]/archive",
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

func TestDecodeValidationEvidenceV1(t *testing.T) {
	evidence := *validValidationEvidenceV1()
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
	// Rejecting a semantically invalid result is evidence validation, which this
	// slice deliberately excludes; its coverage arrives with that validation.
}

func releaseScopedID(name string) string {
	return "tool:demo/releases/1.2.3/payloads/" + name
}

func TestRecordPathsRejectBackslashes(t *testing.T) {
	// path.Clean and path.IsAbs treat a backslash as an ordinary character, so a
	// Windows-style separator would otherwise pass here and fail only later in
	// providerstore.ArtifactDescriptor.Validate, which forbids it explicitly.
	for _, value := range []string{`tools\\demo.zip`, `tools\\demo\\chromium.zip`, `a\\b`} {
		if err := validateRecordPathV1(value, false); err == nil {
			t.Errorf("validateRecordPathV1(%q) accepted a backslash", value)
		}
	}
	for _, value := range []string{`/opt\\demo/bin/demo`, `/opt/demo\\bin`} {
		if err := validateAbsoluteRecordPathV1(value); err == nil {
			t.Errorf("validateAbsoluteRecordPathV1(%q) accepted a backslash", value)
		}
	}
	if err := validateRecordPathV1("tools/demo.zip", false); err != nil {
		t.Errorf("canonical relative path rejected: %v", err)
	}
	if err := validateRecordPathV1(".", true); err != nil {
		t.Errorf("allowed current-directory path rejected: %v", err)
	}
	if err := validateRecordPathV1(".", false); err == nil {
		t.Error("current-directory path accepted when disallowed")
	}
	if err := validateAbsoluteRecordPathV1("/opt/demo/bin/demo"); err != nil {
		t.Errorf("canonical absolute path rejected: %v", err)
	}
}

func TestCanonicalCollectionsAreBoundedSortedAndUnique(t *testing.T) {
	sorted := []RecordReferenceV1{recordTestReference(releaseScopedID("a")), recordTestReference(releaseScopedID("b"))}
	if err := validateReferenceListV1("references", sorted); err != nil {
		t.Fatalf("sorted unique references: %v", err)
	}
	oversized := make([]RecordReferenceV1, maxDefinitionReferences+1)
	for index := range oversized {
		oversized[index] = recordTestReference(releaseScopedID(fmt.Sprintf("p%06d", index)))
	}
	for _, testCase := range []struct {
		name       string
		references []RecordReferenceV1
		want       string
	}{
		{name: "nil", references: nil, want: "must use an array"},
		{name: "over limit", references: oversized, want: "at most"},
		{name: "unsorted", references: []RecordReferenceV1{recordTestReference(releaseScopedID("b")), recordTestReference(releaseScopedID("a"))}, want: "references"},
		{name: "duplicate", references: []RecordReferenceV1{recordTestReference(releaseScopedID("a")), recordTestReference(releaseScopedID("a"))}, want: "references"},
	} {
		err := validateReferenceListV1("references", testCase.references)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("%s references error = %v", testCase.name, err)
		}
	}

	if err := validateSortedUniqueStringsV1("values", []string{"alpha", "beta"}, false); err != nil {
		t.Fatalf("sorted unique strings: %v", err)
	}
	for _, testCase := range []struct {
		name   string
		values []string
	}{
		{name: "nil", values: nil},
		{name: "empty value", values: []string{""}},
		{name: "control character", values: []string{"alpha\nbeta"}},
		{name: "untrimmed", values: []string{" alpha"}},
		{name: "unsorted", values: []string{"beta", "alpha"}},
		{name: "duplicate", values: []string{"alpha", "alpha"}},
	} {
		if err := validateSortedUniqueStringsV1("values", testCase.values, false); err == nil {
			t.Errorf("%s strings error = nil", testCase.name)
		}
	}
	if err := requireNonemptySortedStringsV1("values", []string{"alpha"}); err != nil {
		t.Fatalf("nonempty sorted strings: %v", err)
	}
	if err := requireNonemptySortedStringsV1("values", []string{}); err == nil {
		t.Fatal("empty string collection was accepted as nonempty")
	}
}

func TestCanonicalRecordIdentifiersReferencesAndDecimals(t *testing.T) {
	for _, value := range []string{
		"tool:demo",
		"tool:demo/releases/1.2.3/payloads/demo",
		"tool:demo/releases/1%212/payloads/demo",
	} {
		if err := validateRecordIDV1(value); err != nil {
			t.Errorf("canonical record ID %q: %v", value, err)
		}
	}
	for _, value := range []string{
		"",
		"demo",
		"tool:Demo",
		"tool:demo/releases/1.2.3",
		"tool:demo/releases/1%2f2/payloads/demo",
		"tool:demo/releases/%31/payloads/demo",
		"tool:demo/releases/1.2.3/payloads/%41",
		"tool:demo/releases/1.2.3/../demo",
	} {
		if err := validateRecordIDV1(value); err == nil {
			t.Errorf("noncanonical record ID %q was accepted", value)
		}
	}

	validReference := recordTestReference("tool:demo/releases/1.2.3/payloads/demo")
	if err := validateRecordReferenceV1(validReference); err != nil {
		t.Fatalf("canonical record reference: %v", err)
	}
	invalidDigest := validReference
	invalidDigest.Digest = ""
	if err := validateRecordReferenceV1(invalidDigest); err == nil {
		t.Fatal("record reference with an invalid digest was accepted")
	}

	for _, testCase := range []struct {
		value    string
		positive bool
	}{
		{value: "0"},
		{value: "1", positive: true},
		{value: "9223372036854775807", positive: true},
	} {
		if err := validateCanonicalDecimalV1("value", testCase.value, testCase.positive); err != nil {
			t.Errorf("canonical decimal %q: %v", testCase.value, err)
		}
	}
	for _, testCase := range []struct {
		value    string
		positive bool
	}{
		{value: ""},
		{value: "00"},
		{value: "01"},
		{value: "-1"},
		{value: "0", positive: true},
		{value: "9223372036854775808", positive: true},
	} {
		if err := validateCanonicalDecimalV1("value", testCase.value, testCase.positive); err == nil {
			t.Errorf("noncanonical decimal %q was accepted", testCase.value)
		}
	}
}

func TestCanonicalRecordSegments(t *testing.T) {
	for _, value := range []string{"demo", "demo-1.2+3"} {
		if !validRecordSegmentV1(value) {
			t.Errorf("canonical record segment %q was rejected", value)
		}
	}
	for _, value := range []string{"", ".", "..", "Demo", "demo_name", "demo/name", "demo name"} {
		if validRecordSegmentV1(value) {
			t.Errorf("noncanonical record segment %q was accepted", value)
		}
	}
}
