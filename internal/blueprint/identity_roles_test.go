package blueprint

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestIdentityRolesV1CompleteInventoryAndRoundtrip(t *testing.T) {
	raw := policyBlueprintV1(testIdentityPolicyV1)
	raw = strings.Replace(raw, "  mounts:\n", "        unused:\n          source: python\n          binary: demo-server\n          role: app\n  mounts:\n", 1)
	source, err := Decode([]byte(raw))
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
	if !reflect.DeepEqual(document.Environment.Applications["application"].Executables, decoded.Environment.Applications["application"].Executables) || !reflect.DeepEqual(document.Environment.Commands, decoded.Environment.Commands) {
		t.Fatal("resolved application or command inventory changed")
	}
	for _, name := range []string{"server", "unused"} {
		contribution, executable, ok := decoded.Environment.ResolveExecutableProfile("application." + name)
		if !ok || executable.Role != "app" || contribution != ApplicationContributionID("application", "python") {
			t.Fatalf("profile %s: %q, %#v, %v", name, contribution, executable, ok)
		}
		if got := decoded.Environment.Components[contribution].Executables[name]; !reflect.DeepEqual(got, executable) {
			t.Fatalf("provider contribution lost profile %s: %#v", name, got)
		}
	}
	command := decoded.Environment.Commands["serve"]
	_, executable, ok := decoded.Environment.ResolveExecutableProfile(command.Executable)
	if !ok || executable.Role != "app" || command.Executable != "application.server" {
		t.Fatal("command did not inherit the qualified executable role")
	}
	if _, _, ok := decoded.Environment.ResolveExecutableProfile("server"); ok {
		t.Fatal("unqualified executable accepted")
	}
	again, err := EncodeResolvedDocumentV1(decoded)
	if err != nil || again != payload {
		t.Fatalf("roundtrip changed: %v", err)
	}
	before, err := DocumentDigestV1(decoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded.Environment.Runtime.IdentityPolicy.Roles["other"] = IdentityPolicyRoleV1{User: "worker", Capabilities: []string{}}
	application := decoded.Environment.Applications["application"]
	profile := application.Executables["unused"]
	profile.Role = "other"
	application.Executables["unused"] = profile
	after, err := DocumentDigestV1(decoded)
	if err != nil || before == after {
		t.Fatalf("unselected profile role not bound by digest: %v", err)
	}
}

func TestIdentityRolesV1RejectsRawSelectorsAndOverrides(t *testing.T) {
	for _, role := range []string{"null", "~", "", "200", "1.0", "true", "[]", "{}", "*selected_role", `""`, `" app"`, `"app "`, `"App"`, `"${who}"`, `"${runtime.role}"`, "???", `"app/name"`, `"` + strings.Repeat("a", 33) + `"`} {
		t.Run(role, func(t *testing.T) {
			raw := strings.Replace(policyBlueprintV1(testIdentityPolicyV1), "          role: app", "          role: "+role, 1)
			// The alias refers to a valid literal; alias dereferencing is still forbidden.
			raw = strings.Replace(raw, "  id: demo", "  vars: {who: &selected_role app}\n  id: demo", 1)
			if _, err := Decode([]byte(raw)); err == nil {
				t.Fatal("nonliteral or invalid role accepted")
			}
		})
	}
	for _, tc := range []struct{ name, old, replacement string }{
		{"duplicate role", "          role: app", "          role: app\n          role: app"},
		{"unknown profile field", "          role: app", "          role: app\n          capabilities: []"},
		{"merge role", "          role: app", "          <<: {role: app}"},
		{"command role", "      executable: application.server", "      executable: application.server\n      role: app"},
		{"workload role", "    command: serve", "    command: serve\n    role: app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(policyBlueprintV1(testIdentityPolicyV1), tc.old, tc.replacement, 1)
			if _, err := Decode([]byte(raw)); err == nil {
				t.Fatal("ambiguous or unauthorized selector accepted")
			}
		})
	}
}

func TestIdentityRolesV1ResolutionRequiresEveryDeclaredProfile(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"missing selected", strings.Replace(policyBlueprintV1(testIdentityPolicyV1), "          role: app\n", "", 1), "server.role must name a declared role"},
		{"undeclared selected", strings.Replace(policyBlueprintV1(testIdentityPolicyV1), "          role: app", "          role: missing", 1), "server.role references undeclared role"},
		{"role without policy", strings.Replace(minimalBlueprint, "          binary: demo-server", "          binary: demo-server\n          role: app", 1), "server.role requires environment.runtime.identity_policy"},
		{"missing unreferenced", strings.Replace(policyBlueprintV1(testIdentityPolicyV1), "  mounts:\n", "        unused: {source: python, binary: demo-server}\n  mounts:\n", 1), "unused.role must name a declared role"},
		{"undeclared unreferenced", strings.Replace(policyBlueprintV1(testIdentityPolicyV1), "  mounts:\n", "        unused: {source: python, binary: demo-server, role: missing}\n  mounts:\n", 1), "unused.role references undeclared role"},
		{"second application", strings.Replace(policyBlueprintV1(testIdentityPolicyV1), "  mounts:\n", "    second:\n      packages: {python: {requirements: [demo-server]}}\n      executables: {unused: {source: python, binary: demo-server}}\n  mounts:\n", 1), "second.executables.unused.role must name a declared role"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := Decode([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Resolve(source); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Resolve = %v; want %q", err, tc.want)
			}
		})
	}
	// Programmatic syntax cannot bypass literal checking through expansion.
	source, err := Decode([]byte(policyBlueprintV1(testIdentityPolicyV1)))
	if err != nil {
		t.Fatal(err)
	}
	source.Environment.Vars = map[string]any{"who": "app"}
	application := source.Environment.Applications["application"]
	profile := application.Executables["server"]
	profile.Role = "${who}"
	application.Executables["server"] = profile
	if _, err := Resolve(source); err == nil || !strings.Contains(err.Error(), "server.role") {
		t.Fatalf("programmatic interpolation accepted: %v", err)
	}
}

func TestIdentityRolesV1CanonicalInventoryRejectsTampering(t *testing.T) {
	document := resolvePolicyV1(t, testIdentityPolicyV1)
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*Document)
	}{
		{"missing role", func(d *Document) {
			a := d.Environment.Applications["application"]
			p := a.Executables["server"]
			p.Role = ""
			a.Executables["server"] = p
		}},
		{"undeclared role", func(d *Document) {
			a := d.Environment.Applications["application"]
			p := a.Executables["server"]
			p.Role = "unknown"
			a.Executables["server"] = p
		}},
		{"missing policy", func(d *Document) { d.Environment.Runtime.IdentityPolicy = nil }},
		{"dangling command", func(d *Document) {
			c := d.Environment.Commands["serve"]
			c.Executable = "application.missing"
			d.Environment.Commands["serve"] = c
		}},
		{"unqualified command", func(d *Document) {
			c := d.Environment.Commands["serve"]
			c.Executable = "server"
			d.Environment.Commands["serve"] = c
		}},
		{"invalid unused profile", func(d *Document) {
			d.Environment.Applications["application"].Executables["unused"] = Executable{Source: "python", Binary: "demo-server"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := DecodeResolvedDocumentV1(payload)
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(&d)
			if _, err := EncodeResolvedDocumentV1(d); err == nil {
				t.Fatal("invalid programmatic inventory encoded")
			}
			forged, err := json.Marshal(resolvedDocumentEnvelopeV1{Schema: ResolvedDocumentSchemaV1, Document: d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(forged)); err == nil {
				t.Fatal("invalid persisted inventory decoded")
			}
		})
	}
	for _, mutation := range []string{
		strings.Replace(string(payload), `"Role":"app"`, `"Role":null`, 1),
		strings.Replace(string(payload), `"Role":"app"`, `"Role":200`, 1),
		strings.Replace(string(payload), `"Role":"app"`, `"Role":"app","Role":"app"`, 1),
		strings.Replace(string(payload), `"Executable":"application.server"`, `"Role":"app","Executable":"application.server"`, 1),
	} {
		if mutation == string(payload) {
			t.Fatal("test did not mutate payload")
		}
		if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(mutation)); err == nil {
			t.Fatal("noncanonical role payload accepted")
		}
	}
}

func TestIdentityRolesV1AbsentPreservesExecutableWireBytes(t *testing.T) {
	profile := Executable{Source: "python", Binary: "demo"}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	const legacy = `{"Source":"python","Binary":"demo","Order":null,"ArgvPrefix":null,"ArgvSuffix":null}`
	if string(encoded) != legacy {
		t.Fatalf("ordinary executable bytes changed: %s", encoded)
	}
	source, err := Decode([]byte(minimalBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil || strings.Contains(string(payload), `"Role"`) {
		t.Fatalf("ordinary document gained a role field: %v", err)
	}
	if _, err := DecodeResolvedDocumentV1(payload); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityRolesV1InventoryErrorsHaveStableQualifiedPaths(t *testing.T) {
	document := resolvePolicyV1(t, testIdentityPolicyV1)
	profiles := document.Environment.Applications["application"].Executables
	profiles["z_missing"] = Executable{Source: "python", Binary: "demo-server"}
	profiles["a_missing"] = Executable{Source: "python", Binary: "demo-server"}
	for i := 0; i < 20; i++ {
		if _, err := EncodeResolvedDocumentV1(document); err == nil || !strings.Contains(err.Error(), "environment.applications.application.executables.a_missing.role") {
			t.Fatalf("inventory diagnostic is incomplete or nondeterministic: %v", err)
		}
	}
}
