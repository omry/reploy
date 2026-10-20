package blueprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const mappingDeclarationV1 = `{schema: 1, profile: exact, uids: [{container_start: 0, host_start: 100000, count: 16}], gids: [{container_start: 0, host_start: 200000, count: 16}]}`

func mappingBlueprintV1(mapping, variables string) string {
	return strings.Replace(minimalBlueprint, "  id: demo\n", "  id: demo\n"+variables+"  runtime:\n    identity_mapping: "+mapping+"\n", 1)
}

func TestIdentityMappingV1StrictRealBlueprintDecode(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"unknown mapping field", "schema: 1", "schema: 1, extra: true"},
		{"duplicate mapping field", "schema: 1", "schema: 1, schema: 1"},
		{"missing schema", "schema: 1, ", ""},
		{"missing profile", "profile: exact, ", ""},
		{"missing uids", "uids: [{container_start: 0, host_start: 100000, count: 16}], ", ""},
		{"missing gids", ", gids: [{container_start: 0, host_start: 200000, count: 16}]", ""},
		{"string schema", "schema: 1", `schema: "1"`},
		{"float schema", "schema: 1", "schema: 1.0"},
		{"boolean schema", "schema: 1", "schema: true"},
		{"null schema", "schema: 1", "schema: null"},
		{"required schema", "schema: 1", `schema: "???"`},
		{"wrong schema", "schema: 1", "schema: 2"},
		{"bad profile", "profile: exact", "profile: other"},
		{"integer profile", "profile: exact", "profile: 1"},
		{"null profile", "profile: exact", "profile: null"},
		{"empty uids", "[{container_start: 0, host_start: 100000, count: 16}]", "[]"},
		{"null uids", "[{container_start: 0, host_start: 100000, count: 16}]", "null"},
		{"object uids", "[{container_start: 0, host_start: 100000, count: 16}]", "{}"},
		{"scalar interval", "{container_start: 0, host_start: 100000, count: 16}", "17"},
		{"null interval", "{container_start: 0, host_start: 100000, count: 16}", "null"},
		{"missing interval leaf", "container_start: 0, host_start: 100000", "host_start: 100000"},
		{"unknown interval leaf", "host_start: 100000", "host_start: 100000, extra: 1"},
		{"duplicate interval leaf", "host_start: 100000", "host_start: 100000, host_start: 100000"},
		{"string integer", "host_start: 100000", `host_start: "100000"`},
		{"float integer", "host_start: 100000", "host_start: 100000.0"},
		{"boolean integer", "host_start: 100000", "host_start: true"},
		{"null integer", "host_start: 100000", "host_start: null"},
		{"array integer", "host_start: 100000", "host_start: [100000]"},
		{"negative start", "host_start: 100000", "host_start: -1"},
		{"host root", "host_start: 100000", "host_start: 0"},
		{"sentinel", "host_start: 100000", "host_start: 4294967295"},
		{"too large", "host_start: 100000", "host_start: 4294967296"},
		{"overflow", "host_start: 100000", "host_start: 4294967294"},
		{"zero count", "count: 16", "count: 0"},
		{"negative count", "count: 16", "count: -1"},
		{"string count", "count: 16", `count: "16"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapping := strings.Replace(mappingDeclarationV1, tc.old, tc.replacement, 1)
			if mapping == mappingDeclarationV1 {
				t.Fatal("fixture did not change")
			}
			if _, err := Decode([]byte(mappingBlueprintV1(mapping, ""))); err == nil {
				t.Fatalf("accepted invalid mapping: %s", mapping)
			}
		})
	}
	for _, mapping := range []string{"null", "[]", `"???"`, `"text"`, "true"} {
		if _, err := Decode([]byte(mappingBlueprintV1(mapping, ""))); err == nil {
			t.Fatalf("accepted non-mapping declaration %s", mapping)
		}
	}
}

func TestIdentityMappingV1LiteralGeometryAndMissingLeaves(t *testing.T) {
	for _, tc := range []struct {
		name, profile, uids string
		ok                  bool
	}{
		{"adjacent exact", "exact", "[{container_start: 16, host_start: 100016, count: 16}, {container_start: 0, host_start: 100000, count: 16}]", true},
		{"container overlap", "exact", "[{container_start: 0, host_start: 100000, count: 16}, {container_start: 15, host_start: 100016, count: 16}]", false},
		{"host overlap", "exact", "[{container_start: 0, host_start: 100000, count: 16}, {container_start: 16, host_start: 100015, count: 16}]", false},
		{"duplicate assignment", "exact", "[{container_start: 0, host_start: 100000, count: 16}, {container_start: 0, host_start: 100000, count: 16}]", false},
		{"bounded multiple", "bounded", "[{container_start: 0, host_start: 100000, count: 16}, {container_start: 16, host_start: 100016, count: 16}]", false},
		{"bounded single", "bounded", "[{container_start: 0, host_start: 100000, count: 16}]", true},
		{"last usable ID", "exact", "[{container_start: 4294967294, host_start: 4294967294, count: 1}]", true},
		{"container overflow", "exact", "[{container_start: 4294967294, host_start: 100000, count: 2}]", false},
		{"required values", `"???"`, `[{container_start: "???", host_start: "???", count: "???"}]`, true},
		{"lazy unknown", `"{{ profile }}"`, `[{container_start: 0, host_start: "{{ absent }}", count: 16}]`, true},
		{"lazy namespace", "exact", `[{container_start: 0, host_start: "{{ user.uid }}", count: 16}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapping := fmt.Sprintf("{schema: 1, profile: %s, uids: %s, gids: [{container_start: 0, host_start: 200000, count: 16}]}", tc.profile, tc.uids)
			source, err := Decode([]byte(mappingBlueprintV1(mapping, "")))
			if !tc.ok {
				if err == nil {
					t.Fatal("invalid literal geometry accepted")
				}
				return
			}
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
			decoded, err := DecodeResolvedDocumentV1(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Environment.Runtime.IdentityMapping, document.Environment.Runtime.IdentityMapping) {
				t.Fatal("portable mapping changed during real resolved-document round trip")
			}
		})
	}
}

func TestIdentityMappingV1RejectsInvalidPersistedVariableBindings(t *testing.T) {
	source, err := Decode([]byte(mappingBlueprintV1(mappingDeclarationV1, "")))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value MappingScalarV1
	}{
		{"", MappingScalarV1{"integer", "1"}},
		{"user.uid", MappingScalarV1{"integer", "1"}},
		{"selected", MappingScalarV1{"integer", "01"}},
		{"selected", MappingScalarV1{"integer", "18446744073709551616"}},
		{"selected", MappingScalarV1{"float", "NaN"}},
		{"selected", MappingScalarV1{"float", "+Inf"}},
		{"selected", MappingScalarV1{"float", "text"}},
		{"selected", MappingScalarV1{"bool", "True"}},
		{"selected", MappingScalarV1{"null", "null"}},
		{"selected", MappingScalarV1{"invalid", "secret"}},
		{"selected", MappingScalarV1{"missing", "text"}},
		{"selected", MappingScalarV1{"expression", "literal"}},
		{"selected", MappingScalarV1{"other", ""}},
	} {
		t.Run(tc.name+"/"+tc.value.Kind+"/"+tc.value.Value, func(t *testing.T) {
			document.Environment.Runtime.IdentityMapping.Variables = map[string]MappingScalarV1{tc.name: tc.value}
			if _, err := EncodeResolvedDocumentV1(document); err == nil {
				t.Fatal("accepted invalid persisted type binding")
			}
		})
	}
}

func TestIdentityMappingV1LiteralOrderHasOnePersistedIdentity(t *testing.T) {
	first := "{container_start: 2, host_start: 100002, count: 1}"
	second := "{container_start: 10, host_start: 100010, count: 1}"
	var canonical ResolvedDocumentV1
	for i, entries := range []string{first + ", " + second, second + ", " + first} {
		mapping := strings.Replace(mappingDeclarationV1, "[{container_start: 0, host_start: 100000, count: 16}]", "["+entries+"]", 1)
		source, err := Decode([]byte(mappingBlueprintV1(mapping, "")))
		if err != nil {
			t.Fatal(err)
		}
		before := source.Environment.Runtime.IdentityMapping.UIDs[0]
		document, err := Resolve(source)
		if err != nil {
			t.Fatal(err)
		}
		if source.Environment.Runtime.IdentityMapping.UIDs[0] != before {
			t.Fatal("resolution changed original syntax order")
		}
		payload, err := EncodeResolvedDocumentV1(document)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			canonical = payload
		} else if payload != canonical {
			t.Fatal("literal interval order changed persisted blueprint authority")
		}
		if document.Environment.Runtime.IdentityMapping.UIDs[0].ContainerStart.Value != "2" {
			t.Fatal("literal intervals were not sorted numerically")
		}
	}
}

func TestIdentityMappingV1WireOrderHasOneAuthority(t *testing.T) {
	mapping := strings.Replace(mappingDeclarationV1, "[{container_start: 0, host_start: 100000, count: 16}]",
		"[{container_start: 2, host_start: 100002, count: 1}, {container_start: 10, host_start: 100010, count: 1}]", 1)
	mapping = strings.Replace(mapping, "[{container_start: 0, host_start: 200000, count: 16}]",
		"[{container_start: 2, host_start: 200002, count: 1}, {container_start: 10, host_start: 200010, count: 1}]", 1)
	source, err := Decode([]byte(mappingBlueprintV1(mapping, "")))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := ResolvedDocumentDigestV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"UIDs", "GIDs"} {
		copy := *document.Environment.Runtime.IdentityMapping
		values := copy.UIDs
		if field == "GIDs" {
			values = copy.GIDs
		}
		reversed := []MappingIntervalV1{values[1], values[0]}
		before := append([]MappingIntervalV1{}, reversed...)
		forwardJSON, _ := json.Marshal(values)
		reversedJSON, _ := json.Marshal(reversed)
		if field == "UIDs" {
			copy.UIDs = reversed
		} else {
			copy.GIDs = reversed
		}
		input := document
		input.Environment.Runtime.IdentityMapping = &copy
		t.Run(field+"/encoder", func(t *testing.T) {
			encoded, err := EncodeResolvedDocumentV1(input)
			if err != nil || encoded != canonical {
				t.Fatalf("direct encoder retained noncanonical literal order: %v", err)
			}
			if !reflect.DeepEqual(reversed, before) {
				t.Fatal("wire encoding changed caller-owned intervals")
			}
			if digest, err := DocumentDigestV1(input); err != nil || digest != wantDigest {
				t.Fatalf("interval order changed document authority: %s %v", digest, err)
			}
		})
		t.Run(field+"/wire", func(t *testing.T) {
			bad := strings.Replace(string(canonical), `"`+field+`":`+string(forwardJSON), `"`+field+`":`+string(reversedJSON), 1)
			if bad == string(canonical) {
				t.Fatal("wire fixture did not change interval order")
			}
			if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(bad)); err == nil || !strings.Contains(err.Error(), "canonical wire form") {
				t.Fatalf("reordered literal wire mapping was accepted: %v", err)
			}
		})
	}
}

func TestIdentityMappingV1VariableTypesSurviveCanonicalRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, yaml, kind, value string }{
		{"integer", "100000", "integer", "100000"},
		{"integral float", "1.0", "float", "1"},
		{"fractional float", "1.5", "float", "1.5"},
		{"numeric string", `"100000"`, "string", "100000"},
		{"boolean", "true", "bool", "true"},
		{"null", "null", "null", ""},
		{"required", `"???"`, "missing", "???"},
		{"sequence", "[100000]", "invalid", ""},
		{"object", "{nested: 100000}", "invalid", ""},
		{"negative integer", "-1", "integer", "-1"},
		{"integer beyond mapping IDs", "4294967296", "integer", "4294967296"},
		{"lazy value", `"{{ user.uid }}"`, "expression", "{{ user.uid }}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapping := strings.Replace(mappingDeclarationV1, "host_start: 100000", `host_start: "{{ selected }}"`, 1)
			source, err := Decode([]byte(mappingBlueprintV1(mapping, "  vars:\n    selected: "+tc.yaml+"\n")))
			if err != nil {
				t.Fatal(err)
			}
			document, err := Resolve(source)
			if err != nil {
				t.Fatal(err)
			}
			if source.Environment.Runtime.IdentityMapping.Variables != nil {
				t.Fatal("Resolve mutated input syntax")
			}
			payload, err := EncodeResolvedDocumentV1(document)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeResolvedDocumentV1(payload)
			if err != nil {
				t.Fatal(err)
			}
			want := MappingScalarV1{tc.kind, tc.value}
			got := decoded.Environment.Runtime.IdentityMapping.Variables["selected"]
			if got != want {
				t.Fatalf("type binding = %#v, want %#v", got, want)
			}
			if decoded.Environment.Runtime.IdentityMapping.UIDs[0].HostStart != (MappingScalarV1{"expression", "{{ selected }}"}) {
				t.Fatal("mapping expression evaluated before deployment overrides")
			}
			reencoded, err := EncodeResolvedDocumentV1(decoded)
			if err != nil || reencoded != payload {
				t.Fatal("noncanonical round trip", err)
			}
		})
	}
}

func TestIdentityMappingV1KeepsOrdinaryRuntimeAndRequiredStringMeaning(t *testing.T) {
	for _, runtime := range []string{"", "  runtime: null\n", "  runtime: {user: null, network: null}\n", "  runtime: {network: {public: deny, local: deny, ambiguous: require-both}}\n"} {
		text := strings.Replace(minimalBlueprint, "  id: demo\n", "  id: demo\n"+runtime, 1)
		text = strings.Replace(text, "argv: [serve]", `argv: ["???"]`, 1)
		source, err := Decode([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		document, err := Resolve(source)
		if err != nil {
			t.Fatal(err)
		}
		if document.Environment.Commands["serve"].Argv[0] != "???" {
			t.Fatal("ordinary marker spelling was reserved")
		}
		payload, err := EncodeResolvedDocumentV1(document)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "IdentityMapping") {
			t.Fatal("ordinary wire form gained mapping metadata")
		}
		if document.Environment.Runtime.User != DefaultRuntimeUser {
			t.Fatal("ordinary runtime default changed")
		}
	}
	for _, extra := range []string{"    unknown: true\n", "    network: {unknown: allow}\n", "    network: {public: allow, public: deny}\n"} {
		text := strings.Replace(minimalBlueprint, "  id: demo\n", "  id: demo\n  runtime:\n"+extra, 1)
		if _, err := Decode([]byte(text)); err == nil {
			t.Fatal("lost ordinary strict runtime/network field validation")
		}
	}
}

func TestIdentityMappingV1StrictOldRuntimeReaderAndTamperRejection(t *testing.T) {
	source, err := Decode([]byte(mappingBlueprintV1(mappingDeclarationV1, "")))
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
	var envelope struct {
		Document struct {
			Environment struct{ Runtime json.RawMessage }
		}
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		t.Fatal(err)
	}
	var oldRuntime struct {
		User    string
		Network RuntimeNetwork
	}
	oldReader := json.NewDecoder(bytes.NewReader(envelope.Document.Environment.Runtime))
	oldReader.DisallowUnknownFields()
	if err := oldReader.Decode(&oldRuntime); err == nil || !strings.Contains(err.Error(), "IdentityMapping") {
		t.Fatalf("strict old runtime reader silently accepted opt-in: %v", err)
	}
	for _, tc := range []struct{ old, replacement string }{
		{`"IdentityMapping":{"Schema":1`, `"IdentityMapping":{"Schema":2`},
		{`"kind":"integer"`, `"kind":"forged"`},
		{`"value":"16"`, `"value":"0"`},
		{`"Profile":{"kind":"string","value":"exact"}`, `"Profile":{"kind":"missing","value":"invalid"}`},
		{`"IdentityMapping":{"Schema":1`, `"IdentityMapping":{"Schema":1,"unknown":true`},
		{`"IdentityMapping":{"Schema":1`, `"IdentityMapping":{"Schema":1,"Schema":1`},
	} {
		bad := strings.Replace(string(payload), tc.old, tc.replacement, 1)
		if bad == string(payload) {
			t.Fatal("tamper fixture did not change")
		}
		if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(bad)); err == nil {
			t.Fatalf("accepted tampered document: %s", tc.replacement)
		}
	}
	if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(strings.Replace(string(payload), string(envelope.Document.Environment.Runtime), `{"User":"reploy","Network":{"Public":"deny","Local":"deny","Ambiguous":"require-both"},"IdentityMapping":null}`, 1))); err == nil {
		t.Fatal("accepted noncanonical explicit-null mapping")
	}
}
