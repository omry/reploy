package blueprint

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const testIdentityPolicyV1 = `{schema: 1, users: {worker: 200}, groups: {worker: 200}, roles: {app: {user: worker}}}`

func policyBlueprintV1(policy string) string {
	raw := strings.Replace(minimalBlueprint, "  id: demo\n", "  id: demo\n  runtime:\n    identity_policy: "+policy+"\n", 1)
	return strings.Replace(raw, "          binary: demo-server\n", "          binary: demo-server\n          role: app\n", 1)
}

func resolvePolicyV1(t *testing.T, policy string) Document {
	t.Helper()
	source, err := Decode([]byte(policyBlueprintV1(policy)))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestIdentityPolicyV1ShorthandDefaultsAndCanonicalRoundtrip(t *testing.T) {
	variants := []string{
		testIdentityPolicyV1,
		`{roles: {app: {capabilities: [], user: worker}}, groups: {worker: 200}, users: {worker: {uid: 200, primary_group: worker, supplementary_groups: []}}, schema: 1}`,
		`{schema: 1, users: {worker: {uid: 200}}, groups: {worker: 200}, roles: {app: {user: worker}}}`,
	}
	var first ResolvedDocumentV1
	for _, raw := range variants {
		document := resolvePolicyV1(t, raw)
		policy := document.Environment.Runtime.IdentityPolicy
		want := IdentityPolicyUserV1{UID: "200", PrimaryGroup: "worker", SupplementaryGroups: []string{}}
		if !reflect.DeepEqual(policy.Users["worker"], want) || !reflect.DeepEqual(policy.Roles["app"].Capabilities, []string{}) || document.Environment.Runtime.User != DefaultRuntimeUser {
			t.Fatalf("defaults = %#v / %#v", policy.Users["worker"], policy.Roles["app"])
		}
		payload, err := EncodeResolvedDocumentV1(document)
		if err != nil {
			t.Fatal(err)
		}
		if first != "" && payload != first {
			t.Fatal("shorthand or key order changed authority")
		}
		first = payload
		decoded, err := DecodeResolvedDocumentV1(payload)
		if err != nil {
			t.Fatal(err)
		}
		again, err := EncodeResolvedDocumentV1(decoded)
		if err != nil || again != payload {
			t.Fatalf("roundtrip differs: %v", err)
		}
		originalDigest, err := DocumentDigestV1(document)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := DocumentDigestV1(decoded)
		if err != nil || digest != originalDigest {
			t.Fatalf("digest changed: %v", err)
		}
	}
}

func TestIdentityPolicyV1PrimaryOverrideMetadataAndSorting(t *testing.T) {
	document := resolvePolicyV1(t, `{schema: 1, users: {worker: {uid: 200, full_name: "Service Worker", primary_group: staff, supplementary_groups: [zeta, alpha]}}, groups: {staff: 201, zeta: 9, alpha: 300}, roles: {app: {user: worker, capabilities: [CAP_SETUID, CAP_CHOWN]}}}`)
	user := document.Environment.Runtime.IdentityPolicy.Users["worker"]
	if user.PrimaryGroup != "staff" || user.FullName == nil || *user.FullName != "Service Worker" || !reflect.DeepEqual(user.SupplementaryGroups, []string{"alpha", "zeta"}) {
		t.Fatalf("user = %#v", user)
	}
	if !reflect.DeepEqual(document.Environment.Runtime.IdentityPolicy.Roles["app"].Capabilities, []string{"CAP_CHOWN", "CAP_SETUID"}) {
		t.Fatal("capabilities not normalized")
	}
	for _, text := range []string{"", strings.Repeat("é", 128), "Worker ${literal}"} {
		raw := strings.Replace(testIdentityPolicyV1, "worker: 200}", fmt.Sprintf("worker: {uid: 200, full_name: %q}}", text), 1)
		if got := resolvePolicyV1(t, raw).Environment.Runtime.IdentityPolicy.Users["worker"].FullName; got == nil || *got != text {
			t.Fatalf("metadata changed: %v", got)
		}
	}
	// UID and GID namespaces are separate; root is an explicit application identity.
	resolvePolicyV1(t, `{schema: 1, users: {root: 0, worker: 200}, groups: {root: 0, worker: 200}, roles: {app: {user: root}, child: {user: worker}}}`)
	resolvePolicyV1(t, strings.ReplaceAll(testIdentityPolicyV1, "200", "4294967294"))
}

func TestIdentityPolicyV1RejectsHostileRawInput(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"unknown policy field", "schema: 1", "schema: 1, extra: true"},
		{"duplicate policy field", "schema: 1", "schema: 1, schema: 1"},
		{"missing schema", "schema: 1, ", ""},
		{"missing users", "users: {worker: 200}, ", ""},
		{"missing groups", "groups: {worker: 200}, ", ""},
		{"missing roles", ", roles: {app: {user: worker}}", ""},
		{"string schema", "schema: 1", `schema: "1"`},
		{"tagged double-quoted schema", "schema: 1", `schema: !!int "1"`},
		{"tagged single-quoted schema", "schema: 1", `schema: !!int '1'`},
		{"tagged quoted object UID", "users: {worker: 200}", `users: {worker: {uid: !!int "200"}}`},
		{"float schema", "schema: 1", "schema: 1.0"},
		{"null schema", "schema: 1", "schema: null"},
		{"wrong schema", "schema: 1", "schema: 2"},
		{"signed schema", "schema: 1", "schema: +1"},
		{"boolean schema", "schema: 1", "schema: true"},
		{"empty users", "users: {worker: 200}", "users: {}"},
		{"null users", "users: {worker: 200}", "users: null"},
		{"list users", "users: {worker: 200}", "users: []"},
		{"duplicate users", "users: {worker: 200}", "users: {worker: 200, worker: 201}"},
		{"duplicate UIDs", "users: {worker: 200}", "users: {worker: 200, other: {uid: 200, primary_group: worker}}"},
		{"uppercase username", "users: {worker: 200}", "users: {Worker: {uid: 200, primary_group: worker}}"},
		{"invalid username", "users: {worker: 200}", "users: {'bad/name': 200}"},
		{"interpolated username", "users: {worker: 200}", "users: {'${who}': 200}"},
		{"nonstring username", "users: {worker: 200}", "users: {200: 200}"},
		{"empty groups", "groups: {worker: 200}", "groups: {}"},
		{"null groups", "groups: {worker: 200}", "groups: null"},
		{"duplicate groups", "groups: {worker: 200}", "groups: {worker: 200, worker: 201}"},
		{"duplicate GIDs", "groups: {worker: 200}", "groups: {worker: 200, other: 200}"},
		{"group object", "groups: {worker: 200}", "groups: {worker: {gid: 200}}"},
		{"invalid group name", "groups: {worker: 200}", "groups: {Worker: 200}"},
		{"empty roles", "roles: {app: {user: worker}}", "roles: {}"},
		{"null roles", "roles: {app: {user: worker}}", "roles: null"},
		{"duplicate role", "roles: {app: {user: worker}}", "roles: {app: {user: worker}, app: {user: worker}}"},
		{"role scalar", "app: {user: worker}", "app: worker"},
		{"role null", "app: {user: worker}", "app: null"},
		{"role missing user", "app: {user: worker}", "app: {}"},
		{"role unknown field", "user: worker", "user: worker, group: worker"},
		{"role duplicate field", "user: worker", "user: worker, user: worker"},
		{"role unknown user", "user: worker", "user: other"},
		{"role nonstring user", "user: worker", "user: 200"},
		{"role interpolated user", "user: worker", "user: '${who}'"},
		{"invalid role name", "app: {user: worker}", "App: {user: worker}"},
		{"user missing uid", "users: {worker: 200}", "users: {worker: {primary_group: worker}}"},
		{"user unknown field", "users: {worker: 200}", "users: {worker: {uid: 200, name: worker}}"},
		{"user duplicate field", "users: {worker: 200}", "users: {worker: {uid: 200, uid: 200}}"},
		{"user list", "users: {worker: 200}", "users: {worker: []}"},
		{"missing same-name primary", "users: {worker: 200}", "users: {other: 200}"},
		{"unknown primary", "users: {worker: 200}", "users: {worker: {uid: 200, primary_group: absent}}"},
		{"null primary", "users: {worker: 200}", "users: {worker: {uid: 200, primary_group: null}}"},
		{"nonstring primary", "users: {worker: 200}", "users: {worker: {uid: 200, primary_group: 200}}"},
		{"null supplementary", "users: {worker: 200}", "users: {worker: {uid: 200, supplementary_groups: null}}"},
		{"scalar supplementary", "users: {worker: 200}", "users: {worker: {uid: 200, supplementary_groups: worker}}"},
		{"map supplementary", "users: {worker: 200}", "users: {worker: {uid: 200, supplementary_groups: {}}}"},
		{"primary repeated", "users: {worker: 200}", "users: {worker: {uid: 200, supplementary_groups: [worker]}}"},
		{"unknown supplementary", "users: {worker: 200}", "users: {worker: {uid: 200, supplementary_groups: [absent]}}"},
		{"nonstring supplementary", "users: {worker: 200}", "users: {worker: {uid: 200, supplementary_groups: [200]}}"},
		{"null full name", "users: {worker: 200}", "users: {worker: {uid: 200, full_name: null}}"},
		{"numeric full name", "users: {worker: 200}", "users: {worker: {uid: 200, full_name: 200}}"},
		{"null capabilities", "user: worker", "user: worker, capabilities: null"},
		{"scalar capabilities", "user: worker", "user: worker, capabilities: CAP_SETUID"},
		{"map capabilities", "user: worker", "user: worker, capabilities: {}"},
		{"duplicate capabilities", "user: worker", "user: worker, capabilities: [CAP_SETUID, CAP_SETUID]"},
		{"case capability", "user: worker", "user: worker, capabilities: [cap_setuid]"},
		{"empty capability", "user: worker", "user: worker, capabilities: [CAP_]"},
		{"engine defaults", "user: worker", "user: worker, capabilities: [ALL]"},
		{"interpolated capability", "user: worker", "user: worker, capabilities: ['${cap}']"},
		{"nonstring capability", "user: worker", "user: worker, capabilities: [1]"},
		{"root alias", "users: {worker: 200}", "users: {worker: 0}"},
		{"root nonzero", "users: {worker: 200}", "users: {root: {uid: 200, primary_group: worker}}"},
		{"nonroot root group", "groups: {worker: 200}", "groups: {worker: 0}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(testIdentityPolicyV1, tc.old, tc.replacement, 1)
			if raw == testIdentityPolicyV1 {
				t.Fatal("fixture unchanged")
			}
			source, err := Decode([]byte(policyBlueprintV1(raw)))
			if err == nil {
				_, err = Resolve(source)
			}
			if err == nil {
				t.Fatalf("accepted %s", raw)
			}
		})
	}
	for _, id := range []string{`"200"`, `!!int "200"`, `!!int '200'`, "200.0", "true", "null", "???", `"${uid}"`, "-1", "+200", "0200", "0xc8", "4294967295", "4294967296", "[200]", "{}"} {
		for _, field := range []string{"users", "groups"} {
			t.Run(field+"/"+id, func(t *testing.T) {
				raw := strings.Replace(testIdentityPolicyV1, field+": {worker: 200}", field+": {worker: "+id+"}", 1)
				if _, err := Decode([]byte(policyBlueprintV1(raw))); err == nil {
					t.Fatal("accepted invalid source type/ID")
				}
			})
		}
	}
	for _, value := range []string{"null", "[]", "true", `"text"`} {
		if _, err := Decode([]byte(policyBlueprintV1(value))); err == nil {
			t.Fatal("accepted nonmapping policy", value)
		}
	}
}

func TestIdentityPolicyV1BoundsAndGroupInvariants(t *testing.T) {
	for _, name := range []string{strings.Repeat("x", 33), "bad.name", "_bad!"} {
		if _, err := Decode([]byte(policyBlueprintV1(strings.ReplaceAll(testIdentityPolicyV1, "worker", name)))); err == nil {
			t.Fatal("accepted invalid name", name)
		}
	}
	for _, name := range []string{strings.Repeat("x", 257), "bad\x00text", "bad\rtext", "bad\ntext"} {
		raw := strings.Replace(testIdentityPolicyV1, "worker: 200}", fmt.Sprintf("worker: {uid: 200, full_name: %q}}", name), 1)
		if _, err := Decode([]byte(policyBlueprintV1(raw))); err == nil {
			t.Fatal("accepted invalid full_name")
		}
	}
	for _, count := range []int{64, 65} {
		groups := []string{"worker: 200"}
		extra := []string{}
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("g%d", i)
			groups = append(groups, fmt.Sprintf("%s: %d", name, 300+i))
			extra = append(extra, name)
		}
		raw := fmt.Sprintf("{schema: 1, users: {worker: {uid: 200, supplementary_groups: [%s]}}, groups: {%s}, roles: {app: {user: worker}}}", strings.Join(extra, ","), strings.Join(groups, ","))
		_, err := Decode([]byte(policyBlueprintV1(raw)))
		if (err == nil) != (count == 64) {
			t.Fatalf("group count %d: %v", count, err)
		}
	}
	for _, extra := range []string{"[other, other]", "[root]"} {
		raw := `{schema: 1, users: {worker: {uid: 200, supplementary_groups: ` + extra + `}}, groups: {worker: 200, other: 201, root: 0}, roles: {app: {user: worker}}}`
		if _, err := Decode([]byte(policyBlueprintV1(raw))); err == nil {
			t.Fatal("accepted duplicate/root extra groups")
		}
	}
	for _, count := range []int{1022, 1023} {
		groups := []string{"worker: 200"}
		for i := 1; i < count; i++ {
			groups = append(groups, fmt.Sprintf("g%d: %d", i, 200+i))
		}
		raw := strings.Replace(testIdentityPolicyV1, "groups: {worker: 200}", "groups: {"+strings.Join(groups, ",")+"}", 1)
		_, err := Decode([]byte(policyBlueprintV1(raw)))
		if (err == nil) != (count == 1022) {
			t.Fatalf("total count %d: %v", count+2, err)
		}
	}
}

func TestIdentityPolicyV1RejectsExplicitRuntimeUserBeforeExpansion(t *testing.T) {
	for _, user := range []string{"reploy", "worker", "null", `""`, `"${who}"`} {
		raw := strings.Replace(policyBlueprintV1(testIdentityPolicyV1), "  runtime:\n", "  runtime:\n    user: "+user+"\n", 1)
		source, err := Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		source.Environment.Vars = map[string]any{"who": "reploy"}
		if _, err := Resolve(source); err == nil || !strings.Contains(err.Error(), "explicit environment.runtime.user") {
			t.Fatalf("user %s: %v", user, err)
		}
	}
	source, err := Decode([]byte(policyBlueprintV1(testIdentityPolicyV1)))
	if err != nil {
		t.Fatal(err)
	}
	source.Environment.Runtime.User = "reploy"
	if _, err := Resolve(source); err == nil {
		t.Fatal("programmatic explicit user accepted")
	}
}

func TestIdentityPolicyV1CanonicalJSONRejectsTampering(t *testing.T) {
	document := resolvePolicyV1(t, testIdentityPolicyV1)
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ old, new string }{
		{`"schema":"identity-policy-v1"`, `"schema":"other"`},
		{`"schema":"identity-policy-v1"`, `"schema":"identity-policy-v1","schema":"identity-policy-v1"`},
		{`"schema":"identity-policy-v1"`, `"Schema":"identity-policy-v1"`},
		{`"uid":"200"`, `"uid":200`},
		{`"uid":"200"`, `"uid":"0200"`},
		{`"uid":"200"`, `"uid":"200","unknown":true`},
		{`"primary_group":"worker"`, `"primary_group":"absent"`},
		{`"supplementary_groups":[]`, `"supplementary_groups":null`},
		{`,"supplementary_groups":[]`, ``},
		{`"capabilities":[]`, `"capabilities":null`},
		{`,"capabilities":[]`, ``},
		{`"user":"worker"`, `"user":"other"`},
		{`"User":"reploy"`, `"User":"root"`},
		{`"IdentityPolicy":{`, `"IdentityPolicy":{"supervisor":{},`},
	} {
		changed := strings.Replace(string(payload), tc.old, tc.new, 1)
		if changed == string(payload) {
			t.Fatal("unchanged tamper fixture", tc.old)
		}
		if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(changed)); err == nil {
			t.Fatal("accepted tampered JSON", tc)
		}
	}
	// Bypass raw YAML and exercise the common validator on persisted structs.
	mutations := []func(*IdentityPolicyV1){
		func(p *IdentityPolicyV1) { p.Schema = "other" },
		func(p *IdentityPolicyV1) { p.Groups["worker"] = "+200" },
		func(p *IdentityPolicyV1) { p.Groups["Worker"] = "300" },
		func(p *IdentityPolicyV1) { p.Roles["App"] = p.Roles["app"] },
		func(p *IdentityPolicyV1) {
			u := p.Users["worker"]
			u.SupplementaryGroups = []string{"zeta", "alpha"}
			p.Users["worker"] = u
			p.Groups["alpha"] = "201"
			p.Groups["zeta"] = "202"
		},
		func(p *IdentityPolicyV1) {
			u := p.Users["worker"]
			s := string([]byte{0xff})
			u.FullName = &s
			p.Users["worker"] = u
		},
		func(p *IdentityPolicyV1) {
			r := p.Roles["app"]
			r.Capabilities = []string{"CAP_SETUID", "CAP_CHOWN"}
			p.Roles["app"] = r
		},
	}
	for i, mutate := range mutations {
		var copy Document
		if err := json.Unmarshal([]byte(mustPolicyDocumentJSONV1(t, document)), &copy); err != nil {
			t.Fatal(err)
		}
		mutate(copy.Environment.Runtime.IdentityPolicy)
		if _, err := EncodeResolvedDocumentV1(copy); err == nil {
			t.Fatalf("accepted programmatic mutation %d", i)
		}
	}
}

func mustPolicyDocumentJSONV1(t *testing.T, d Document) string {
	t.Helper()
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestIdentityPolicyV1AbsentPreservesExistingCanonicalBytes(t *testing.T) {
	document := Document{}
	encoded, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema":"blueprint-resolved-v1","document":{"Blueprint":{"Schema":0,"Version":"","RequiresReploy":"","Compatibility":{"platforms":null}},"Environment":{"ID":"","ControlScript":"","Vars":null,"Base":{"Image":"","Exports":null},"Packages":{"OS":null},"Applications":null,"AllowConcurrent":"","Runtime":{"User":"","Network":{"Public":"","Local":"","Ambiguous":""}},"Terminal":{"ColorEnv":""},"Install":{"Target":{"DefaultPath":"","DefaultPaths":null},"System":{"Account":{"User":"","Group":"","OnMissing":""}},"AfterInstall":null,"Success":{"Lines":null}},"Mounts":null,"Commands":null,"Workload":null},"Docker":{"Image":"","Mounts":null,"Workload":null}}}`
	if string(encoded) != want {
		t.Fatalf("old canonical bytes changed: %s", encoded)
	}
}

func TestIdentityPolicyV1AliasesAndMergeKeysCannotSupplyAuthority(t *testing.T) {
	for _, raw := range []string{
		`{schema: 1, users: &u {worker: 200}, groups: *u, roles: {app: {user: worker}}}`,
		`{schema: 1, users: {worker: &u 200}, groups: {worker: *u}, roles: {app: {user: worker}}}`,
		`{schema: 1, users: {worker: {uid: 200, <<: {primary_group: worker}}}, groups: {worker: 200}, roles: {app: {user: worker}}}`,
		`{schema: 1, users: {worker: 200}, groups: {worker: 200}, roles: {app: {user: &u worker}, other: {user: *u}}}`,
	} {
		if _, err := Decode([]byte(policyBlueprintV1(raw))); err == nil {
			t.Fatal("accepted aliased/merged policy input", raw)
		}
	}
}

func TestIdentityPolicyV1ProgrammaticResolveRepeatsValidation(t *testing.T) {
	source, err := Decode([]byte(policyBlueprintV1(testIdentityPolicyV1)))
	if err != nil {
		t.Fatal(err)
	}
	u := source.Environment.Runtime.IdentityPolicy.Users["worker"]
	u.UID = "${uid}"
	source.Environment.Runtime.IdentityPolicy.Users["worker"] = u
	source.Environment.Vars = map[string]any{"uid": 200}
	if _, err := Resolve(source); err == nil {
		t.Fatal("generic expansion laundered invalid programmatic policy")
	}
}

func TestIdentityPolicyV1RejectsTaggedBlockNumericScalars(t *testing.T) {
	for _, style := range []string{"|-", ">-"} {
		policy := "\n      schema: 1\n      users:\n        worker: !!int " + style + "\n          200\n      groups: {worker: 200}\n      roles: {app: {user: worker}}"
		if _, err := Decode([]byte(policyBlueprintV1(policy))); err == nil {
			t.Fatal("accepted block numeric scalar", style)
		}
	}
	// An explicit numeric tag on a plain decimal retains the permitted source form.
	resolvePolicyV1(t, strings.ReplaceAll(testIdentityPolicyV1, "200", "!!int 200"))
}
