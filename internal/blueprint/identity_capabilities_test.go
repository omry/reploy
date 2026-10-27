package blueprint

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func capabilityPolicyV1(capabilities string) string {
	return strings.Replace(testIdentityPolicyV1, "user: worker", "user: worker, capabilities: "+capabilities, 1)
}

func TestIdentityCapabilitiesV1RecognizesPortableRequests(t *testing.T) {
	// Include old and newer UAPI names, plus names outside the mail use case.
	// The non-root user is intentional: recognition does not qualify a grant.
	for _, name := range []string{
		"CAP_CHOWN", "CAP_AUDIT_READ", "CAP_BPF", "CAP_CHECKPOINT_RESTORE",
		"CAP_NET_BIND_SERVICE", "CAP_SETFCAP", "CAP_SETGID", "CAP_SETUID",
		"CAP_SYS_ADMIN", "CAP_SYS_CHROOT",
	} {
		t.Run(name, func(t *testing.T) {
			document := resolvePolicyV1(t, capabilityPolicyV1("["+name+"]"))
			role := document.Environment.Runtime.IdentityPolicy.Roles["app"]
			if role.User != "worker" || !reflect.DeepEqual(role.Capabilities, []string{name}) {
				t.Fatalf("request changed: %#v", role)
			}
			payload, err := EncodeResolvedDocumentV1(document)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeResolvedDocumentV1(payload)
			if err != nil || !reflect.DeepEqual(decoded.Environment.Runtime.IdentityPolicy, document.Environment.Runtime.IdentityPolicy) {
				t.Fatalf("request lost during persistence: %v", err)
			}
		})
	}
}

func TestIdentityCapabilitiesV1RejectsRawUnknownAliasesAndInterpolation(t *testing.T) {
	for _, capabilities := range []string{
		"[CAP_NOT_REAL]", "[CAP_LAST_CAP]", "[CAP_ALL]", "[ALL]",
		"[CHOWN]", "[cap_chown]", "[Cap_Chown]", "[CAP_chown]",
		"[CAP_CHOWN, CAP_CHOWN]", "[CAP_CHOWN, CAP_NOT_REAL]",
		"['CAP_CHOWN ']", "[' CAP_CHOWN']", "[\"CAP_CHOWN\\n\"]",
		"['{{capability}}']", "['CAP_{{suffix}}']", "['${capability}']",
		"[1]", "[true]", "[null]", "['']", "null", "CAP_CHOWN",
	} {
		t.Run(capabilities, func(t *testing.T) {
			// Decode validates the raw policy before variable expansion can run.
			_, err := Decode([]byte(policyBlueprintV1(capabilityPolicyV1(capabilities))))
			if err == nil || !strings.Contains(err.Error(), "capabilities") {
				t.Fatalf("accepted or misdiagnosed raw capability input: %v", err)
			}
		})
	}
}

func TestIdentityCapabilitiesV1CanonicalPolicyLocksEveryRole(t *testing.T) {
	const policy = `{schema: 1, users: {worker: 200}, groups: {worker: 200}, roles: {app: {user: worker, capabilities: [CAP_SETUID, CAP_CHOWN]}, unused: {user: worker, capabilities: [CAP_BPF]}}}`
	document := resolvePolicyV1(t, policy)
	roles := document.Environment.Runtime.IdentityPolicy.Roles
	if !reflect.DeepEqual(roles["app"].Capabilities, []string{"CAP_CHOWN", "CAP_SETUID"}) || !reflect.DeepEqual(roles["unused"].Capabilities, []string{"CAP_BPF"}) {
		t.Fatalf("complete role policy not normalized: %#v", roles)
	}
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	ordered := strings.Replace(policy, "[CAP_SETUID, CAP_CHOWN]", "[CAP_CHOWN, CAP_SETUID]", 1)
	other, err := EncodeResolvedDocumentV1(resolvePolicyV1(t, ordered))
	if err != nil || other != payload {
		t.Fatalf("source capability order changed canonical policy: %v", err)
	}
	before, err := DocumentDigestV1(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app", "unused"} {
		t.Run(name, func(t *testing.T) {
			copy, err := DecodeResolvedDocumentV1(payload)
			if err != nil {
				t.Fatal(err)
			}
			role := copy.Environment.Runtime.IdentityPolicy.Roles[name]
			role.Capabilities = []string{"CAP_SETGID"}
			copy.Environment.Runtime.IdentityPolicy.Roles[name] = role
			after, err := DocumentDigestV1(copy)
			if err != nil || after == before {
				t.Fatalf("digest did not bind %s capability policy: %v", name, err)
			}
		})
	}
}

func TestIdentityCapabilitiesV1RevalidatesProgrammaticAndPersistedPolicy(t *testing.T) {
	for _, capabilities := range [][]string{
		nil, {"CAP_NOT_REAL"}, {"CAP_LAST_CAP"}, {"ALL"}, {"cap_chown"},
		{"CAP_CHOWN", "CAP_CHOWN"}, {"CAP_SETUID", "CAP_CHOWN"},
	} {
		t.Run(strings.Join(capabilities, ","), func(t *testing.T) {
			source, err := Decode([]byte(policyBlueprintV1(testIdentityPolicyV1)))
			if err != nil {
				t.Fatal(err)
			}
			role := source.Environment.Runtime.IdentityPolicy.Roles["app"]
			role.Capabilities = slices.Clone(capabilities)
			source.Environment.Runtime.IdentityPolicy.Roles["app"] = role
			if _, err := Resolve(source); err == nil {
				t.Fatal("resolved invalid programmatic capability policy")
			}
			document := resolvePolicyV1(t, testIdentityPolicyV1)
			document.Environment.Runtime.IdentityPolicy.Roles["app"] = role
			if _, err := EncodeResolvedDocumentV1(document); err == nil {
				t.Fatal("encoded invalid capability policy")
			}
			forged, err := json.Marshal(resolvedDocumentEnvelopeV1{Schema: ResolvedDocumentSchemaV1, Document: document})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeResolvedDocumentV1(ResolvedDocumentV1(forged)); err == nil {
				t.Fatal("decoded invalid persisted capability policy")
			}
		})
	}
}
