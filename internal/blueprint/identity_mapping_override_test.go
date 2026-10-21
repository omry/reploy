package blueprint

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func mappingOverrideDocumentV1(t *testing.T, mapping, variables string) Document {
	t.Helper()
	source, err := Decode([]byte(mappingBlueprintV1(mapping, variables)))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	document, err = DecodeResolvedDocumentV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func decodeMappingOverrideV1(t *testing.T, text string) *IdentityMappingOverrideV1 {
	t.Helper()
	var override IdentityMappingOverrideV1
	if err := yaml.Unmarshal([]byte(text), &override); err != nil {
		t.Fatal(err)
	}
	return &override
}

func TestMappingOverrideStrictSparseInput(t *testing.T) {
	for _, text := range []string{
		`{}`, `{schema: 1}`, `{profile: exact}`, `{profile: "???"}`,
		`{uids: [{container_start: 0, host_start: "{{ host }}", count: 1}]}`,
	} {
		override := decodeMappingOverrideV1(t, text)
		content, err := yaml.Marshal(override)
		if err != nil {
			t.Fatal(err)
		}
		decoded := decodeMappingOverrideV1(t, string(content))
		if !reflect.DeepEqual(override, decoded) {
			t.Fatalf("sparse input changed across YAML: %s", text)
		}
	}
	for _, text := range []string{
		`[]`, `{extra: 1}`, `{schema: 1, schema: 1}`,
		`{schema: null}`, `{schema: "1"}`, `{schema: 1.0}`, `{schema: true}`, `{schema: 2}`,
		`{profile: null}`, `{profile: false}`, `{profile: 1}`, `{profile: other}`,
		`{uids: null}`, `{uids: {}}`, `{uids: []}`, `{uids: [null]}`,
		`{uids: [{container_start: 0, count: 1}]}`,
		`{uids: [{container_start: 0, host_start: 1, count: null}]}`,
		`{uids: [{container_start: 0, host_start: "1", count: 1}]}`,
		`{uids: [{container_start: 0, host_start: 1.0, count: 1}]}`,
		`{uids: [{container_start: 0, host_start: 0, count: 1}]}`,
		`{uids: [{container_start: 0, host_start: 1, count: 0}]}`,
		`{uids: [{container_start: 0, host_start: 1, count: 1, extra: 0}]}`,
		`{gids: [{container_start: 0, host_start: 1, count: 1, count: 1}]}`,
		`{uids: [{container_start: 4294967294, host_start: 1, count: 2}]}`,
		`{uids: [{container_start: 0, host_start: 10, count: 2}, {container_start: 1, host_start: 20, count: 1}]}`,
		`{profile: bounded, uids: [{container_start: 0, host_start: 10, count: 1}, {container_start: 2, host_start: 20, count: 1}]}`,
	} {
		t.Run(text, func(t *testing.T) {
			var override IdentityMappingOverrideV1
			if err := yaml.Unmarshal([]byte(text), &override); err == nil {
				t.Fatalf("accepted malformed sparse override %s", text)
			}
		})
	}
}

func TestMappingOverrideReplacesBeforeResolvingAndPreservesPortableAuthority(t *testing.T) {
	mapping := `{schema: 1, profile: "???", uids: [{container_start: "{{ nonexistent }}", host_start: "???", count: "???"}], gids: [{container_start: 0, host_start: "???", count: 1}]}`
	document := mappingOverrideDocumentV1(t, mapping, "  vars:\n    unused_host: 100000\n")
	before, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	override := decodeMappingOverrideV1(t, `{profile: exact, uids: [{container_start: 4, host_start: " {{ unused_host }} ", count: 2}], gids: [{container_start: 0, host_start: 200000, count: 1}]}`)
	savedOverride, _ := yaml.Marshal(override)
	result, err := ResolveIdentityMappingV1(document, override)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UIDs) != 1 || result.UIDs[0].ContainerStart.Value != "4" || result.UIDs[0].HostStart != (MappingScalarV1{"integer", "100000"}) || result.Variables != nil {
		t.Fatalf("incorrect complete-array replacement/resolution: %#v", result)
	}
	result.UIDs[0].Count.Value = "999"
	after, _ := EncodeResolvedDocumentV1(document)
	afterOverride, _ := yaml.Marshal(override)
	if before != after || string(savedOverride) != string(afterOverride) {
		t.Fatal("candidate modified blueprint or caller-owned override")
	}
	firstDigest, _ := ResolvedDocumentDigestV1(before)
	secondDigest, _ := DocumentDigestV1(document)
	if firstDigest != secondDigest {
		t.Fatal("deployment host selection changed portable authority")
	}
}

func TestMappingOverrideOriginalVariableTypesAfterWireRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{"100000", true}, {`"100000"`, false}, {"100000.0", false}, {"true", false},
		{"null", false}, {`"???"`, false}, {`[100000]`, false}, {`{id: 100000}`, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			document := mappingOverrideDocumentV1(t, mappingDeclarationV1, "  vars:\n    unused: "+tc.value+"\n")
			override := decodeMappingOverrideV1(t, `{uids: [{container_start: 0, host_start: "{{ unused }}", count: 1}]}`)
			_, err := ResolveIdentityMappingV1(document, override)
			if (err == nil) != tc.ok {
				t.Fatalf("original %s type: error = %v", tc.value, err)
			}
			if err != nil && !strings.Contains(err.Error(), "environment.runtime.identity_mapping.uids[0].host_start") {
				t.Fatalf("error lacks exact consumer path: %v", err)
			}
		})
	}
}

func TestMappingOverrideCollectsMissingPathsWithoutPrivateValues(t *testing.T) {
	document := mappingOverrideDocumentV1(t, `{schema: 1, profile: "{{ missing_profile }}", uids: [{container_start: 0, host_start: "???", count: "{{ count }}"}], gids: [{container_start: 0, host_start: "{{ user.gid }}", count: 1}]}`, "  vars:\n    count: '???'\n    private: do-not-print-this\n")
	_, err := ResolveIdentityMappingV1(document, nil)
	if err == nil {
		t.Fatal("accepted unresolved mapping")
	}
	for _, path := range []string{".profile", ".uids[0].host_start", ".uids[0].count", ".gids[0].host_start"} {
		if !strings.Contains(err.Error(), identityMappingPathV1+path) {
			t.Errorf("missing path %s in %v", path, err)
		}
	}
	if !strings.Contains(err.Error(), `environment "demo"`) || strings.Contains(err.Error(), "do-not-print-this") {
		t.Fatalf("incorrect diagnostic context/privacy: %v", err)
	}
}

func TestMappingOverrideEffectiveValidationAndCanonicalOrder(t *testing.T) {
	document := mappingOverrideDocumentV1(t, mappingDeclarationV1, "  vars:\n    first: 2\n    second: 10\n")
	for _, order := range []string{
		`[{container_start: "{{ second }}", host_start: 200000, count: 1}, {container_start: "{{ first }}", host_start: 100000, count: 1}]`,
		`[{container_start: "{{ first }}", host_start: 100000, count: 1}, {container_start: "{{ second }}", host_start: 200000, count: 1}]`,
	} {
		mapping, err := ResolveIdentityMappingV1(document, decodeMappingOverrideV1(t, "{uids: "+order+"}"))
		if err != nil || mapping.UIDs[0].ContainerStart.Value != "2" || mapping.UIDs[1].ContainerStart.Value != "10" {
			t.Fatalf("canonical effective numeric order: %#v / %v", mapping, err)
		}
	}
	for _, leaf := range []string{`"{{ first }}0"`, `"{{ missing }}"`, `"{{ runtime.uid }}"`} {
		_, err := ResolveIdentityMappingV1(document, decodeMappingOverrideV1(t, fmt.Sprintf(`{uids: [{container_start: 0, host_start: %s, count: 1}]}`, leaf)))
		if err == nil || !strings.Contains(err.Error(), ".uids[0].host_start") {
			t.Fatalf("accepted coerced or unresolved ID %s: %v", leaf, err)
		}
	}
	// Literal starts do not overlap, but their original typed bindings do.
	_, err := ResolveIdentityMappingV1(document, decodeMappingOverrideV1(t, `{uids: [{container_start: "{{ first }}", host_start: 100000, count: 10}, {container_start: "{{ second }}", host_start: 200000, count: 1}]}`))
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("accepted effective overlap: %v", err)
	}
}

func TestMappingOverrideRequiresDeclarationAndRetainsOrdinaryBehavior(t *testing.T) {
	source, err := Decode([]byte(minimalBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if mapping, err := ResolveIdentityMappingV1(document, nil); err != nil || mapping != nil {
		t.Fatalf("ordinary document changed: %#v / %v", mapping, err)
	}
	if _, err := ResolveIdentityMappingV1(document, decodeMappingOverrideV1(t, `{}`)); err == nil || !strings.Contains(err.Error(), "declaration") {
		t.Fatalf("undeclared override accepted: %v", err)
	}
}
