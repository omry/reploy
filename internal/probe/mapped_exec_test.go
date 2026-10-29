package probe

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func mappedSandboxArgumentsV1() []string {
	return []string{"--uid", "0", "--gid", "0", "--groups", "100,201", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/bin/true"}
}

func TestMappedCeilingParsingV1(t *testing.T) {
	args := mappedSandboxArgumentsV1()
	legacy, err := parseMappedSandboxExecPlanV1(args)
	if err != nil || legacy.MappedCapabilities != nil {
		t.Fatal("legacy waiter must remain inhibited", legacy, err)
	}
	for _, text := range []string{"0000000000000000", "00000000000400c1", "ffffffffffffffff"} {
		t.Run(text, func(t *testing.T) {
			mapped := append([]string{mappedCapabilityArgumentV1 + text}, args...)
			plan, err := parseMappedSandboxExecPlanV1(mapped)
			if err != nil || plan.MappedCapabilities == nil || fmt.Sprintf("%016x", *plan.MappedCapabilities) != text || !reflect.DeepEqual(plan.Groups, []uint32{100, 201}) {
				t.Fatal(plan, err)
			}
			if _, err := parseSandboxExecPlanV1(mapped, true); err == nil {
				t.Fatal("ordinary sandbox accepted mapped-only authority")
			}
		})
	}
	for _, text := range []string{"", "0", "c4", "00000000000000C4", "00000000000000000", "+000000000000000", "0x000000000000c4", "00000000000000cg", "00000000000000c4\x00"} {
		t.Run("invalid "+text, func(t *testing.T) {
			if _, err := parseMappedSandboxExecPlanV1(append([]string{mappedCapabilityArgumentV1 + text}, args...)); err == nil {
				t.Fatal("accepted noncanonical ceiling")
			}
		})
	}
	for _, mode := range []string{"duplicate", "wrong position", "nonzero UID", "private environment", "bad ordinary args"} {
		t.Run(mode, func(t *testing.T) {
			a := append([]string{mappedCapabilityArgumentV1 + "00000000000000c4"}, args...)
			switch mode {
			case "duplicate":
				a = append(a[:1], a...)
			case "wrong position":
				a = append(append([]string{}, args[:2]...), append(a[:1], args[2:]...)...)
			case "nonzero UID":
				a[2] = "200"
			case "private environment":
				a = append(a[:1], append([]string{"--private-environment"}, args...)...)
			case "bad ordinary args":
				a = a[:len(a)-2]
			}
			if _, err := parseMappedSandboxExecPlanV1(a); err == nil {
				t.Fatal("accepted unsupported input", a)
			}
		})
	}
	args[1] = "200"
	if plan, err := parseMappedSandboxExecPlanV1(append([]string{mappedCapabilityArgumentV1 + "0000000000000000"}, args...)); err != nil || plan.UID != 200 {
		t.Fatal("empty ceiling at nonzero UID", plan, err)
	}
}

func mappedStatusFixtureV1(plan sandboxExecPlanV1) string {
	groups := []string{}
	for _, group := range plan.Groups {
		groups = append(groups, fmt.Sprint(group))
	}
	return fmt.Sprintf("Name:\tfixture\nUid:\t%d %d %d %d\nGid:\t%d %d %d %d\nGroups:\t%s\nCapPrm:\t%016x\nCapEff:\t%016x\nCapBnd:\t%016x\nCapInh:\t0000000000000000\nCapAmb:\t0000000000000000\nNoNewPrivs:\t1\nSeccomp:\t2\n",
		plan.UID, plan.UID, plan.UID, plan.UID, plan.GID, plan.GID, plan.GID, plan.GID,
		strings.Join(groups, " "), *plan.MappedCapabilities, *plan.MappedCapabilities, *plan.MappedCapabilities)
}

func TestMappedKernelStatusRequiresExactAuthorityV1(t *testing.T) {
	for _, mask := range []uint64{0, 0xc4, 0x400c1} {
		plan := sandboxExecPlanV1{UID: 0, GID: 200, Groups: []uint32{100, 201}, MappedCapabilities: &mask}
		good := mappedStatusFixtureV1(plan)
		if err := verifyMappedKernelStatusV1([]byte(good), plan); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"Uid", "Gid", "Groups", "CapPrm", "CapEff", "CapBnd", "CapInh", "CapAmb", "NoNewPrivs", "Seccomp"} {
			start := strings.Index(good, name+":")
			end := start + strings.Index(good[start:], "\n")
			for _, defect := range []string{"missing", "duplicate", "empty", "different", "malformed"} {
				t.Run(fmt.Sprintf("%x/%s/%s", mask, name, defect), func(t *testing.T) {
					line := good[start:end]
					var bad string
					switch defect {
					case "missing":
						bad = good[:start] + good[end+1:]
					case "duplicate":
						bad = good + line + "\n"
					case "empty":
						bad = good[:start] + name + ":\n" + good[end+1:]
					case "different":
						bad = good[:start] + name + ": 999\n" + good[end+1:]
					case "malformed":
						bad = good[:start] + name + ": nope nope\n" + good[end+1:]
					}
					if err := verifyMappedKernelStatusV1([]byte(bad), plan); err == nil {
						t.Fatal("accepted altered authority", bad)
					}
				})
			}
		}
		for _, kind := range []string{"Uid", "Gid"} {
			for field := range 4 {
				values := strings.Fields(strings.SplitN(strings.SplitN(good, kind+":", 2)[1], "\n", 2)[0])
				values[field] = "201"
				start := strings.Index(good, kind+":")
				end := start + strings.Index(good[start:], "\n")
				bad := good[:start] + kind + ": " + strings.Join(values, " ") + good[end:]
				if err := verifyMappedKernelStatusV1([]byte(bad), plan); err == nil {
					t.Fatalf("accepted changed %s field%d", kind, field)
				}
			}
		}
	}
	mask := uint64(0)
	plan := sandboxExecPlanV1{UID: 200, GID: 200, MappedCapabilities: &mask}
	if err := verifyMappedKernelStatusV1([]byte(mappedStatusFixtureV1(plan)), plan); err != nil {
		t.Fatal("empty groups are valid", err)
	}
	plan.MappedCapabilities = nil
	if err := verifyMappedKernelStatusV1(nil, plan); err == nil {
		t.Fatal("implicit capability ceiling")
	}
}

func TestMappedKernelGroupOrderV1(t *testing.T) {
	mask := uint64(0)
	plan := sandboxExecPlanV1{UID: 200, GID: 201, Groups: []uint32{100, 201}, MappedCapabilities: &mask}
	good := mappedStatusFixtureV1(plan)
	for _, test := range []struct {
		groups string
		pass   bool
	}{
		{"201 100", true}, {"100 201", true},
		{"100 100", false}, {"201 201", false}, {"100", false},
		{"100 201 300", false}, {"100 nope", false},
		{"100 0201", false}, {"100 4294967295", false},
	} {
		content := strings.Replace(good, "Groups:\t100 201\n", "Groups:\t"+test.groups+"\n", 1)
		if err := verifyMappedKernelStatusV1([]byte(content), plan); (err == nil) != test.pass {
			t.Errorf("groups %q: pass=%v, err=%v", test.groups, test.pass, err)
		}
	}
}
