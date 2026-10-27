package deploy

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func identityPolicyBuildLockFixtureV1(t *testing.T) BuildLockV1 {
	t.Helper()
	lock := validBuildLock(t)
	policy, _, _, files := imageIdentityFixtureV1(t)
	evidence, err := VerifyImageIdentityFilesV1(policy, lock.FinalImage, lock.Platform, files)
	if err != nil {
		t.Fatal(err)
	}
	lock.IdentityPolicy = &IdentityPolicyLockV1{Schema: IdentityPolicyLockSchemaV1, Policy: *policy, Evidence: evidence}
	return lock
}

func TestIdentityPolicyLockV1CanonicalBindingAndOmission(t *testing.T) {
	ordinary := validBuildLock(t)
	before, err := EncodeBuildLockV1(ordinary, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte("identity_policy")) {
		t.Fatal("ordinary lock acquired a policy field")
	}
	lock := identityPolicyBuildLockFixtureV1(t)
	payload, err := EncodeBuildLockV1(lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBuildLockV1(payload, acceptBuildLockProfile)
	if err != nil || !reflect.DeepEqual(decoded, lock) {
		t.Fatalf("round trip: %v", err)
	}
	ordinaryDigest, _ := BuildLockDigestV1(ordinary, acceptBuildLockProfile)
	policyDigest, _ := BuildLockDigestV1(lock, acceptBuildLockProfile)
	if ordinaryDigest == policyDigest {
		t.Fatal("generation lock digest did not bind identity evidence")
	}
	lock.IdentityPolicy = nil
	after, err := EncodeBuildLockV1(lock, acceptBuildLockProfile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("omission changed ordinary canonical bytes: %v", err)
	}
}

func TestIdentityPolicyLockV1RejectsStaleOrMalformedNestedBindings(t *testing.T) {
	for name, mutate := range map[string]func(*IdentityPolicyLockV1){
		"schema": func(p *IdentityPolicyLockV1) { p.Schema = "other" },
		"policy": func(p *IdentityPolicyLockV1) { p.Policy.Schema = "other" },
		"role capabilities": func(p *IdentityPolicyLockV1) {
			p.Policy.Roles["app"] = blueprint.IdentityPolicyRoleV1{User: "worker", Capabilities: []string{"CAP_SETUID"}}
		},
		"image digest":         func(p *IdentityPolicyLockV1) { p.Evidence.Image.Digest = buildLockTestDigest("e") },
		"config":               func(p *IdentityPolicyLockV1) { p.Evidence.Image.ConfigDigest = buildLockTestDigest("e") },
		"rootfs":               func(p *IdentityPolicyLockV1) { p.Evidence.Image.RootFSSubject = buildLockTestDigest("e") },
		"platform":             func(p *IdentityPolicyLockV1) { p.Evidence.Platform.Architecture = "arm64" },
		"file metadata":        func(p *IdentityPolicyLockV1) { delete(p.Evidence.Files, "etc/passwd") },
		"account relationship": func(p *IdentityPolicyLockV1) { p.Evidence.Users[0].UID = "123" },
	} {
		t.Run(name, func(t *testing.T) {
			lock := identityPolicyBuildLockFixtureV1(t)
			mutate(lock.IdentityPolicy)
			if _, err := EncodeBuildLockV1(lock, acceptBuildLockProfile); err == nil {
				t.Fatal("stale or malformed identity evidence accepted")
			}
		})
	}
}

func TestIdentityPolicyLockV1ClosedRequiredNestedObjects(t *testing.T) {
	payload, err := EncodeBuildLockV1(identityPolicyBuildLockFixtureV1(t), acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"policy", "evidence"} {
		for _, mode := range []string{"missing", "null", "unknown"} {
			t.Run(field+"/"+mode, func(t *testing.T) {
				var root map[string]any
				if err := json.Unmarshal(payload, &root); err != nil {
					t.Fatal(err)
				}
				p := root["identity_policy"].(map[string]any)
				switch mode {
				case "missing":
					delete(p, field)
				case "null":
					p[field] = nil
				case "unknown":
					p[field].(map[string]any)["unrecognized"] = "value"
				}
				changed, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := DecodeBuildLockV1(changed, acceptBuildLockProfile); err == nil {
					t.Fatal("invalid closed identity-policy object accepted")
				}
			})
		}
	}
}
