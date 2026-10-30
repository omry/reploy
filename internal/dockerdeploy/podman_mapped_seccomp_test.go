package dockerdeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// These tests inspect the compiler contract independently of its typed parser.
// They do not qualify an engine/libseccomp build or a live runtime tuple.
type mappedPolicyDocumentForTest struct {
	DefaultAction   string                       `json:"defaultAction"`
	DefaultErrnoRet uint32                       `json:"defaultErrnoRet"`
	ArchMap         []map[string]any             `json:"archMap"`
	Syscalls        []map[string]json.RawMessage `json:"syscalls"`
}

func mappedPolicyForTest(t *testing.T, abi string, final, setup uint64) (mappedSeccompPolicyV1, mappedPolicyDocumentForTest) {
	t.Helper()
	policy, err := compileMappedSeccompV1(podmanDockerSeccompProfile, abi, final, setup, 40)
	if err != nil {
		t.Fatal(err)
	}
	var document mappedPolicyDocumentForTest
	if err := json.Unmarshal([]byte(policy.profile), &document); err != nil {
		t.Fatal(err)
	}
	return policy, document
}

func mappedRuleNamesForTest(t *testing.T, rule map[string]json.RawMessage) []string {
	t.Helper()
	var names []string
	if err := json.Unmarshal(rule["names"], &names); err != nil {
		t.Fatal(err)
	}
	return names
}

func mappedRuleActionForTest(t *testing.T, rule map[string]json.RawMessage) string {
	t.Helper()
	var action string
	if err := json.Unmarshal(rule["action"], &action); err != nil {
		t.Fatal(err)
	}
	return action
}

func TestMappedSeccompCompilerRejectsUnqualifiedInputs(t *testing.T) {
	changedSeed := bytes.Clone(podmanDockerSeccompProfile)
	changedSeed[bytes.IndexByte(changedSeed, '\n')] = ' '
	if !json.Valid(changedSeed) {
		t.Fatal("same-length corruption fixture must remain valid JSON")
	}
	tests := []struct {
		name         string
		seed         []byte
		abi          string
		final, setup uint64
		last         int
	}{
		{"missing seed", nil, "amd64", 0, 0, 40},
		{"changed seed", append(bytes.Clone(podmanDockerSeccompProfile), '\n'), "amd64", 0, 0, 40},
		{"same-length changed seed", changedSeed, "amd64", 0, 0, 40},
		{"empty ABI", podmanDockerSeccompProfile, "", 0, 0, 40},
		{"compat x86", podmanDockerSeccompProfile, "386", 0, 0, 40},
		{"compat ARM", podmanDockerSeccompProfile, "arm", 0, 0, 40},
		{"different clone ABI", podmanDockerSeccompProfile, "s390x", 0, 0, 40},
		{"unknown ABI", podmanDockerSeccompProfile, "riscv64", 0, 0, 40},
		{"negative kernel limit", podmanDockerSeccompProfile, "amd64", 0, 0, -1},
		{"unrepresentable kernel", podmanDockerSeccompProfile, "amd64", 0, 0, 64},
		{"unknown final bit", podmanDockerSeccompProfile, "amd64", 1 << 41, 1 << 41, 63},
		{"unknown setup bit", podmanDockerSeccompProfile, "amd64", 0, 1 << 41, 63},
		{"final outside setup", podmanDockerSeccompProfile, "amd64", 1 << 7, 1 << 6, 40},
		{"final outside kernel", podmanDockerSeccompProfile, "amd64", 1 << 40, 1 << 40, 39},
		{"setup outside kernel", podmanDockerSeccompProfile, "amd64", 0, 1 << 40, 39},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := compileMappedSeccompV1(test.seed, test.abi, test.final, test.setup, test.last)
			if err == nil || got != (mappedSeccompPolicyV1{}) {
				t.Fatalf("unqualified input returned usable policy: %+v, %v", got, err)
			}
		})
	}
	for _, last := range []int{0, 40, 63} {
		if _, err := compileMappedSeccompV1(podmanDockerSeccompProfile, "amd64", 1, 1, last); err != nil {
			t.Fatalf("valid bounded input with kernel last=%d: %v", last, err)
		}
	}
}

func TestMappedSeccompSeedDecoderRejectsUnknownShapes(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"unknown top field": func(m map[string]any) { m["listenerPath"] = "/tmp/listener" },
		"allow default":     func(m map[string]any) { m["defaultAction"] = "SCMP_ACT_ALLOW" },
		"missing errno":     func(m map[string]any) { delete(m, "defaultErrnoRet") },
		"different errno":   func(m map[string]any) { m["defaultErrnoRet"] = 0 },
		"missing arches":    func(m map[string]any) { m["archMap"] = []any{} },
		"missing rules":     func(m map[string]any) { m["syscalls"] = []any{} },
		"unknown arch field": func(m map[string]any) {
			m["archMap"].([]any)[0].(map[string]any)["flags"] = []any{}
		},
		"empty arch": func(m map[string]any) {
			m["archMap"].([]any)[0].(map[string]any)["architecture"] = ""
		},
		"unknown rule field": func(m map[string]any) {
			m["syscalls"].([]any)[0].(map[string]any)["flags"] = []any{}
		},
		"unknown action": func(m map[string]any) {
			m["syscalls"].([]any)[0].(map[string]any)["action"] = "SCMP_ACT_TRACE"
		},
		"empty names": func(m map[string]any) {
			m["syscalls"].([]any)[0].(map[string]any)["names"] = []string{}
		},
		"empty name": func(m map[string]any) {
			m["syscalls"].([]any)[0].(map[string]any)["names"] = []string{""}
		},
		"allow with errno": func(m map[string]any) {
			m["syscalls"].([]any)[0].(map[string]any)["errnoRet"] = 0
		},
		"unknown condition": func(m map[string]any) {
			m["syscalls"].([]any)[0].(map[string]any)["includes"] = map[string]any{"other": true}
		},
		"unknown arg": func(m map[string]any) {
			m["syscalls"].([]any)[0].(map[string]any)["args"] = []any{map[string]any{"index": 0, "value": 0, "op": "SCMP_CMP_GE"}}
		},
		"bad arg index": func(m map[string]any) {
			m["syscalls"].([]any)[0].(map[string]any)["args"] = []any{map[string]any{"index": 6, "value": 0, "op": "SCMP_CMP_EQ"}}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal(podmanDockerSeccompProfile, &raw); err != nil {
				t.Fatal(err)
			}
			mutate(raw)
			seed, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeMappedSeccompSeedV1(seed); err == nil {
				t.Fatal("accepted unsupported seed shape")
			}
		})
	}
	for _, suffix := range []string{"{}", " trailing", "null"} {
		if _, err := decodeMappedSeccompSeedV1(append(bytes.Clone(podmanDockerSeccompProfile), []byte(suffix)...)); err == nil {
			t.Fatalf("accepted trailing %q", suffix)
		}
	}
}

func TestMappedSeccompCompilerRemovesEveryEscapeAllowance(t *testing.T) {
	forbidden := []string{"mount", "umount", "umount2", "pivot_root", "fsopen", "fsconfig", "fsmount", "fspick",
		"open_tree", "move_mount", "mount_setattr", "setns", "unshare", "open_by_handle_at", "name_to_handle_at"}
	for _, abi := range []string{"amd64", "arm64"} {
		for _, caps := range []uint64{0, 1 << 21, 1 << 2, (1 << 41) - 1} {
			t.Run(fmt.Sprintf("%s-%x", abi, caps), func(t *testing.T) {
				_, doc := mappedPolicyForTest(t, abi, caps, caps)
				for _, rule := range doc.Syscalls {
					if mappedRuleActionForTest(t, rule) != "SCMP_ACT_ALLOW" {
						continue
					}
					for _, name := range mappedRuleNamesForTest(t, rule) {
						for _, blocked := range forbidden {
							if name == blocked {
								t.Fatalf("allow for %s survived with includes=%s excludes=%s", name, rule["includes"], rule["excludes"])
							}
						}
					}
				}
			})
		}
	}
}

func TestMappedSeccompCompilerNativeClonePolicy(t *testing.T) {
	arches := map[string]string{"amd64": "SCMP_ARCH_X86_64", "arm64": "SCMP_ARCH_AARCH64"}
	namespaceBits := []uint64{0x00020000, 0x02000000, 0x04000000, 0x08000000, 0x10000000, 0x20000000, 0x40000000, 0x80}
	for abi, native := range arches {
		for _, caps := range []uint64{0, 1 << 21, (1 << 41) - 1} {
			_, doc := mappedPolicyForTest(t, abi, caps, caps)
			if len(doc.ArchMap) != 1 || doc.ArchMap[0]["architecture"] != native {
				t.Fatalf("wrong native architecture: %+v", doc.ArchMap)
			}
			sub, ok := doc.ArchMap[0]["subArchitectures"].([]any)
			if !ok || len(sub) != 0 {
				t.Fatalf("compat architectures retained: %+v", doc.ArchMap)
			}
			var clone, clone3 []map[string]json.RawMessage
			for _, rule := range doc.Syscalls {
				for _, name := range mappedRuleNamesForTest(t, rule) {
					if name == "clone" {
						clone = append(clone, rule)
					}
					if name == "clone3" {
						clone3 = append(clone3, rule)
					}
				}
			}
			if len(clone) != 1 || len(clone3) != 1 {
				t.Fatalf("conditional clone rule survived: clone=%d clone3=%d", len(clone), len(clone3))
			}
			for _, rule := range []map[string]json.RawMessage{clone[0], clone3[0]} {
				if _, ok := rule["includes"]; ok {
					t.Fatal("replacement has includes condition")
				}
				if _, ok := rule["excludes"]; ok {
					t.Fatal("replacement has excludes condition")
				}
			}
			if mappedRuleActionForTest(t, clone[0]) != "SCMP_ACT_ALLOW" || mappedRuleActionForTest(t, clone3[0]) != "SCMP_ACT_ERRNO" || string(clone3[0]["errnoRet"]) != "38" {
				t.Fatal("clone actions or clone3 fallback errno changed")
			}
			if _, ok := clone3[0]["args"]; ok {
				t.Fatal("clone3 ENOSYS must be unconditional")
			}
			var args []struct {
				Index    uint32 `json:"index"`
				Value    uint64 `json:"value"`
				ValueTwo uint64 `json:"valueTwo"`
				Op       string `json:"op"`
			}
			if err := json.Unmarshal(clone[0]["args"], &args); err != nil {
				t.Fatal(err)
			}
			if len(args) != 1 || args[0].Index != 0 || args[0].Op != "SCMP_CMP_MASKED_EQ" || args[0].ValueTwo != 0 {
				t.Fatalf("wrong native clone argument: %+v", args)
			}
			var mask uint64
			for _, bit := range namespaceBits {
				mask |= bit
			}
			if args[0].Value != mask {
				t.Fatalf("namespace mask=%x, want %x", args[0].Value, mask)
			}
			for _, ordinary := range []uint64{17, 0x4111, 0x3d0f00} { // fork, vfork, pthread-style clone
				if ordinary&args[0].Value != args[0].ValueTwo {
					t.Fatalf("ordinary clone flags %x denied", ordinary)
				}
				for combination := 1; combination < 1<<len(namespaceBits); combination++ {
					flags := ordinary
					for i, bit := range namespaceBits {
						if combination&(1<<i) != 0 {
							flags |= bit
						}
					}
					if flags&args[0].Value == args[0].ValueTwo {
						t.Fatalf("namespace combination %x allowed", flags)
					}
				}
			}
		}
	}
}

func TestMappedSeccompCompilerPreservesUnrelatedRules(t *testing.T) {
	var seed mappedPolicyDocumentForTest
	if err := json.Unmarshal(podmanDockerSeccompProfile, &seed); err != nil {
		t.Fatal(err)
	}
	_, doc := mappedPolicyForTest(t, "amd64", 0, (1<<41)-1)
	if doc.DefaultAction != seed.DefaultAction || doc.DefaultErrnoRet != seed.DefaultErrnoRet {
		t.Fatal("default-deny semantics changed")
	}
	removed := map[string]bool{"clone": true, "clone3": true, "mount": true, "umount": true, "umount2": true,
		"pivot_root": true, "fsopen": true, "fsconfig": true, "fsmount": true, "fspick": true, "open_tree": true,
		"move_mount": true, "mount_setattr": true, "setns": true, "unshare": true, "open_by_handle_at": true, "name_to_handle_at": true}
	var want []map[string]json.RawMessage
	for _, rule := range seed.Syscalls {
		var names []string
		for _, name := range mappedRuleNamesForTest(t, rule) {
			if !removed[name] {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}
		rule["names"], _ = json.Marshal(names)
		want = append(want, rule)
	}
	if len(doc.Syscalls) != len(want)+2 {
		t.Fatal("unexpected unrelated rule count")
	}
	for i, rule := range want {
		// Compare parsed values, allowing canonical encoding to remove whitespace.
		before, after := map[string]any{}, map[string]any{}
		b, _ := json.Marshal(rule)
		a, _ := json.Marshal(doc.Syscalls[i])
		if json.Unmarshal(b, &before) != nil || json.Unmarshal(a, &after) != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("unrelated rule %d changed: before=%s after=%s", i, b, a)
		}
	}
	for _, required := range []string{"fork", "vfork", "chroot", "setuid", "setgid", "setgroups", "setresuid", "setresgid"} {
		found := false
		for _, rule := range doc.Syscalls {
			for _, name := range mappedRuleNamesForTest(t, rule) {
				found = found || name == required && mappedRuleActionForTest(t, rule) == "SCMP_ACT_ALLOW"
			}
		}
		if !found {
			t.Fatalf("ordinary positive control %s missing", required)
		}
	}
}

func TestMappedSeccompCompilerBindsImmutableCanonicalResult(t *testing.T) {
	seed := bytes.Clone(podmanDockerSeccompProfile)
	original := sha256.Sum256(seed)
	base, err := compileMappedSeccompV1(seed, "amd64", 0, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(seed) != original || sha256.Sum256(podmanDockerSeccompProfile) != original {
		t.Fatal("compiler modified pinned seed")
	}
	again, _ := compileMappedSeccompV1(seed, "amd64", 0, 1, 40)
	if again != base || !json.Valid([]byte(base.profile)) || strings.Contains(base.profile, "\n") {
		t.Fatal("noncanonical or nondeterministic result")
	}
	canonical := fmt.Sprintf(`{"version":1,"seed":%q,"abi":"amd64","final_capabilities":0,"setup_capabilities":1,"kernel_last_capability":40,"profile":%s}`, podmanDockerSeccompSHA256, base.profile)
	if want := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(canonical))); base.identity != want {
		t.Fatalf("identity did not bind exact profile/input bytes: %s != %s", base.identity, want)
	}
	for _, change := range []struct {
		abi          string
		final, setup uint64
		last         int
	}{{"arm64", 0, 1, 40}, {"amd64", 1, 1, 40}, {"amd64", 0, 3, 40}, {"amd64", 0, 1, 63}} {
		got, err := compileMappedSeccompV1(seed, change.abi, change.final, change.setup, change.last)
		if err != nil || got.identity == base.identity {
			t.Fatalf("input change not bound: %+v %v", change, err)
		}
	}
	seed[0] ^= 1
	copyOfResult := base
	copyOfResult.profile = "changed"
	if base != again || copyOfResult == base {
		t.Fatal("caller changes altered the original immutable result")
	}
}
