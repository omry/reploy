package probe

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// This bitmap is a private trusted-launch argument, not blueprint syntax.
// Its presence selects the mapped transition; omission keeps the old waiter
// inhibited. Ordinary sandbox/restricted-exec parsing does not accept it.
const mappedCapabilityArgumentV1 = "--mapped-capabilities="

func parseMappedSandboxExecPlanV1(args []string) (sandboxExecPlanV1, error) {
	var ceiling *uint64
	if len(args) > 0 && strings.HasPrefix(args[0], mappedCapabilityArgumentV1) {
		text := strings.TrimPrefix(args[0], mappedCapabilityArgumentV1)
		value, err := strconv.ParseUint(text, 16, 64)
		if err != nil || fmt.Sprintf("%016x", value) != text {
			return sandboxExecPlanV1{}, fmt.Errorf("mapped capability ceiling must be 16 lowercase hexadecimal digits")
		}
		ceiling = &value
		args = args[1:]
	}
	plan, err := parseSandboxExecPlanV1(args, true)
	if err != nil {
		return plan, err
	}
	plan.MappedCapabilities = ceiling
	if ceiling != nil {
		if plan.UID != 0 && *ceiling != 0 {
			return sandboxExecPlanV1{}, fmt.Errorf("nonzero UID with mapped capabilities is not qualified")
		}
		if plan.PrivateEnvironment || plan.RecordExitStatus {
			return sandboxExecPlanV1{}, fmt.Errorf("mapped private environment and observed-exit transitions are not qualified")
		}
	}
	return plan, nil
}

func verifyMappedKernelStatusV1(content []byte, plan sandboxExecPlanV1) error {
	if plan.MappedCapabilities == nil {
		return fmt.Errorf("mapped authority requires an explicit capability ceiling")
	}
	expected := map[string][]string{
		"Uid": {"", "", "", ""}, "Gid": {"", "", "", ""},
		"Groups": {}, "CapPrm": {}, "CapEff": {}, "CapBnd": {},
		"CapInh": {}, "CapAmb": {}, "NoNewPrivs": {"1"}, "Seccomp": {"2"},
	}
	for i := range 4 {
		expected["Uid"][i] = strconv.FormatUint(uint64(plan.UID), 10)
		expected["Gid"][i] = strconv.FormatUint(uint64(plan.GID), 10)
	}
	for _, group := range plan.Groups {
		expected["Groups"] = append(expected["Groups"], strconv.FormatUint(uint64(group), 10))
	}
	seen := make(map[string]bool, len(expected))
	for _, line := range strings.Split(string(content), "\n") {
		name, raw, found := strings.Cut(line, ":")
		want, required := expected[name]
		if !found || !required {
			continue
		}
		if seen[name] {
			return fmt.Errorf("mapped %s appears more than once", name)
		}
		seen[name] = true
		got := strings.Fields(raw)
		if name == "Groups" {
			// Linux orders kernel GIDs, which need not have the same order
			// after translation through an exact container-to-host map.
			groups := make([]uint32, len(got))
			for index, text := range got {
				group, err := parseCredentialV1(text)
				if err != nil {
					return fmt.Errorf("mapped Groups is malformed")
				}
				groups[index] = group
			}
			slices.Sort(groups)
			if !slices.Equal(groups, plan.Groups) {
				return fmt.Errorf("mapped Groups differs from accepted authority")
			}
			continue
		}
		if strings.HasPrefix(name, "Cap") {
			mask := uint64(0)
			if name == "CapPrm" || name == "CapEff" || name == "CapBnd" {
				mask = *plan.MappedCapabilities
			}
			if len(got) != 1 {
				return fmt.Errorf("mapped %s is malformed", name)
			}
			value, err := strconv.ParseUint(got[0], 16, 64)
			if err != nil || value != mask {
				return fmt.Errorf("mapped %s differs from the accepted ceiling", name)
			}
			continue
		}
		if len(got) != len(want) || strings.Join(got, " ") != strings.Join(want, " ") {
			return fmt.Errorf("mapped %s differs from accepted authority", name)
		}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("mapped kernel authority is incomplete")
	}
	return nil
}
