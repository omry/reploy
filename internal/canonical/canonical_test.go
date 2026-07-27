package canonical

import (
	"reflect"
	"strings"
	"testing"
)

func TestMarshalCanonicalObjectOrderingAndEscaping(t *testing.T) {
	value := Object{
		"ö":      "latin",
		"€":      "euro",
		"\r":     "carriage",
		"1":      "one",
		"😀":      "emoji",
		"\u0080": "control",
		"text":   "<>&/\u2028\"\\\b\t\n\f\r\x00",
	}
	encoded, err := Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"\\r\":\"carriage\",\"1\":\"one\",\"text\":\"<>&/\u2028\\\"\\\\\\b\\t\\n\\f\\r\\u0000\",\"\u0080\":\"control\",\"ö\":\"latin\",\"€\":\"euro\",\"😀\":\"emoji\"}"
	if string(encoded) != want {
		t.Fatalf("canonical JSON mismatch\n got: %s\nwant: %s", encoded, want)
	}
}

func TestMarshalCanonicalStructAndSemanticSets(t *testing.T) {
	type record struct {
		Schema string   `json:"schema"`
		Names  []string `json:"names"`
		Empty  string   `json:"empty,omitempty"`
	}
	encoded, err := Marshal(record{Schema: "demo-v1", Names: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"names":["a","b"],"schema":"demo-v1"}`; got != want {
		t.Fatalf("Marshal() = %s, want %s", got, want)
	}

	first, err := Marshal(Object{"b": true, "a": nil})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(Object{"a": nil, "b": true})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("map insertion order changed canonical output: %s != %s", first, second)
	}
}

func TestMarshalRejectsValuesOutsideCanonicalSchema(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	cyclic := Object{}
	cyclic["self"] = cyclic
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "integer", value: Object{"count": 1}, want: "unsupported numeric type"},
		{name: "float", value: Object{"ratio": 1.5}, want: "unsupported numeric type"},
		{name: "invalid string", value: Object{"value": invalidUTF8}, want: "invalid UTF-8"},
		{name: "invalid key", value: Object{invalidUTF8: "value"}, want: "invalid UTF-8 object key"},
		{name: "non-string map key", value: map[int]string{1: "value"}, want: "unsupported map key type"},
		{name: "cycle", value: cyclic, want: "contains a cycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Marshal(test.value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Marshal() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestMarshalRejectsDuplicateStructFieldNames(t *testing.T) {
	invalidType := reflect.StructOf([]reflect.StructField{
		{Name: "First", Type: reflect.TypeOf(""), Tag: `json:"same"`},
		{Name: "Second", Type: reflect.TypeOf(""), Tag: `json:"same"`},
	})
	invalid := reflect.New(invalidType).Elem()
	invalid.Field(0).SetString("a")
	invalid.Field(1).SetString("b")
	_, err := Marshal(invalid.Interface())
	if err == nil || !strings.Contains(err.Error(), `duplicate field "same"`) {
		t.Fatalf("Marshal() error = %v", err)
	}
}

func TestSumUsesDomainSeparationAndStableDigestGrammar(t *testing.T) {
	value := Object{"schema": "demo-v1", "value": "same"}
	first, err := Sum("bundle", "demo-v1", value)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Sum("transaction", "demo-v1", value)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("identity kind did not domain-separate the digest")
	}
	if len(first) != len("sha256:")+64 || !strings.HasPrefix(string(first), "sha256:") {
		t.Fatalf("Sum() returned invalid digest %q", first)
	}
	if got, want := first, Digest("sha256:072ed1b3bb8a82b3ef8338ea98371ddc586443ba049cfb8729b472f529dedcef"); got != want {
		t.Fatalf("Sum() = %q, want golden %q", got, want)
	}
}

func TestSumRejectsInvalidIdentityTokens(t *testing.T) {
	for _, test := range []struct {
		kind   string
		schema string
	}{
		{kind: "", schema: "demo-v1"},
		{kind: "Bundle", schema: "demo-v1"},
		{kind: "bundle", schema: "demo_v1"},
	} {
		if _, err := Sum(test.kind, test.schema, Object{}); err == nil {
			t.Fatalf("Sum(%q, %q) succeeded", test.kind, test.schema)
		}
	}
}

func TestDigestValidation(t *testing.T) {
	valid := "sha256:" + strings.Repeat("a", 64)
	digest, err := ParseDigest(valid)
	if err != nil {
		t.Fatal(err)
	}
	if digest != Digest(valid) || digest.Validate() != nil {
		t.Fatalf("validated digest = %q", digest)
	}
	for _, invalid := range []string{
		"",
		strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("a", 63),
		"sha256:" + strings.Repeat("A", 64),
		"sha512:" + strings.Repeat("a", 64),
	} {
		if _, err := ParseDigest(invalid); err == nil {
			t.Fatalf("ParseDigest(%q) succeeded", invalid)
		}
	}
}

func TestMarshalOmitemptyAndTagValidation(t *testing.T) {
	type valid struct {
		EmptyMap   map[string]string `json:"empty_map,omitempty"`
		EmptySlice []string          `json:"empty_slice,omitempty"`
		Present    string            `json:"present"`
	}
	encoded, err := Marshal(valid{EmptyMap: map[string]string{}, EmptySlice: []string{}, Present: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"present":"yes"}`; got != want {
		t.Fatalf("Marshal() = %s, want %s", got, want)
	}

	type invalid struct {
		Value string `json:"value,string"`
	}
	if _, err := Marshal(invalid{Value: "value"}); err == nil || !strings.Contains(err.Error(), "unsupported json tag option") {
		t.Fatalf("Marshal() error = %v", err)
	}
}
