package dockerdeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
)

const mappedCloneNamespaceMaskV1 uint64 = 0x7e020080

// This matches the closed Linux v6.6 capability catalog in blueprint. The
// observed kernel limit is a separate caller-supplied qualification fact.
const mappedCapabilityCatalogMaskV1 uint64 = (1 << 41) - 1

// All fields are scalar values. In particular, profile cannot be mutated after
// its identity is computed. The compiler neither grants capabilities nor creates
// or qualifies a container; its caller must authenticate these inputs.
type mappedSeccompPolicyV1 struct {
	abi                  string
	finalCapabilities    uint64
	setupCapabilities    uint64
	kernelLastCapability int
	profile              string
	identity             string
}

type mappedSeccompSpecV1 struct {
	DefaultAction   string                `json:"defaultAction"`
	DefaultErrnoRet *uint32               `json:"defaultErrnoRet"`
	ArchMap         []mappedSeccompArchV1 `json:"archMap"`
	Syscalls        []mappedSeccompRuleV1 `json:"syscalls"`
}

type mappedSeccompArchV1 struct {
	Architecture     string   `json:"architecture"`
	SubArchitectures []string `json:"subArchitectures"`
}

type mappedSeccompRuleV1 struct {
	Names    []string                  `json:"names"`
	Action   string                    `json:"action"`
	Args     []mappedSeccompArgV1      `json:"args,omitempty"`
	Comment  string                    `json:"comment,omitempty"`
	ErrnoRet *uint32                   `json:"errnoRet,omitempty"`
	Includes *mappedSeccompConditionV1 `json:"includes,omitempty"`
	Excludes *mappedSeccompConditionV1 `json:"excludes,omitempty"`
}

type mappedSeccompArgV1 struct {
	Index    uint32 `json:"index"`
	Value    uint64 `json:"value"`
	ValueTwo uint64 `json:"valueTwo,omitempty"`
	Op       string `json:"op"`
}

type mappedSeccompConditionV1 struct {
	Caps      []string `json:"caps,omitempty"`
	Arches    []string `json:"arches,omitempty"`
	MinKernel string   `json:"minKernel,omitempty"`
}

func decodeMappedSeccompSeedV1(seed []byte) (mappedSeccompSpecV1, error) {
	var spec mappedSeccompSpecV1
	decoder := json.NewDecoder(bytes.NewReader(seed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return spec, fmt.Errorf("decode mapped seccomp seed: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return spec, fmt.Errorf("mapped seccomp seed has trailing content")
	}
	if spec.DefaultAction != "SCMP_ACT_ERRNO" || spec.DefaultErrnoRet == nil ||
		*spec.DefaultErrnoRet != 1 || len(spec.ArchMap) == 0 || len(spec.Syscalls) == 0 {
		return spec, fmt.Errorf("mapped seccomp seed requires its pinned default-deny shape")
	}
	for _, arch := range spec.ArchMap {
		if arch.Architecture == "" {
			return spec, fmt.Errorf("mapped seccomp seed has an empty architecture")
		}
	}
	for _, rule := range spec.Syscalls {
		if len(rule.Names) == 0 || rule.Action != "SCMP_ACT_ALLOW" && rule.Action != "SCMP_ACT_ERRNO" ||
			rule.Action == "SCMP_ACT_ALLOW" && rule.ErrnoRet != nil {
			return spec, fmt.Errorf("mapped seccomp seed has an unsupported syscall rule")
		}
		for _, name := range rule.Names {
			if name == "" {
				return spec, fmt.Errorf("mapped seccomp seed has an empty syscall name")
			}
		}
		for _, arg := range rule.Args {
			if arg.Index > 5 || arg.Op != "SCMP_CMP_EQ" && arg.Op != "SCMP_CMP_LT" && arg.Op != "SCMP_CMP_MASKED_EQ" {
				return spec, fmt.Errorf("mapped seccomp seed has an unsupported argument rule")
			}
		}
	}
	return spec, nil
}

func forbiddenMappedSyscallV1(name string) bool {
	switch name {
	case "mount", "umount", "umount2", "pivot_root", "fsopen", "fsconfig", "fsmount", "fspick",
		"open_tree", "move_mount", "mount_setattr", "setns", "unshare", "open_by_handle_at", "name_to_handle_at":
		return true
	}
	return false
}

func compileMappedSeccompV1(seed []byte, abi string, final, setup uint64, kernelLastCapability int) (mappedSeccompPolicyV1, error) {
	fail := func(reason string) (mappedSeccompPolicyV1, error) {
		return mappedSeccompPolicyV1{}, fmt.Errorf("mapped seccomp policy: %s", reason)
	}
	if len(seed) != len(podmanDockerSeccompProfile) {
		return fail("embedded seed integrity mismatch")
	}
	// Hash and decode the same bounded, compiler-owned snapshot.
	seed = bytes.Clone(seed)
	if fmt.Sprintf("%x", sha256.Sum256(seed)) != podmanDockerSeccompSHA256 {
		return fail("embedded seed integrity mismatch")
	}
	var architecture string
	switch abi {
	case "amd64":
		architecture = "SCMP_ARCH_X86_64"
	case "arm64":
		architecture = "SCMP_ARCH_AARCH64"
	default:
		return fail("unsupported native ABI")
	}
	if kernelLastCapability < 0 || kernelLastCapability >= 64 {
		return fail("unsupported kernel capability limit")
	}
	kernelMask := ^uint64(0) >> (63 - uint(kernelLastCapability))
	if (final|setup)&^(kernelMask&mappedCapabilityCatalogMaskV1) != 0 {
		return fail("capabilities exceed kernel or catalog")
	}
	if final&^setup != 0 {
		return fail("setup capability set does not contain the final ceiling")
	}
	spec, err := decodeMappedSeccompSeedV1(seed)
	if err != nil {
		return mappedSeccompPolicyV1{}, err
	}
	foundArchitecture := false
	for _, arch := range spec.ArchMap {
		foundArchitecture = foundArchitecture || arch.Architecture == architecture
	}
	if !foundArchitecture {
		return fail("native architecture is absent from the pinned seed")
	}
	// No compatibility architecture may enter the engine's libseccomp build.
	// Actual engine/ABI qualification remains required by the consuming gate.
	spec.ArchMap = []mappedSeccompArchV1{{Architecture: architecture, SubArchitectures: []string{}}}
	rules := make([]mappedSeccompRuleV1, 0, len(spec.Syscalls)+2)
	for _, rule := range spec.Syscalls {
		names := make([]string, 0, len(rule.Names))
		for _, name := range rule.Names {
			// Replace every clone rule, not just the seed's non-SYS_ADMIN case.
			if name == "clone" || name == "clone3" || rule.Action == "SCMP_ACT_ALLOW" && forbiddenMappedSyscallV1(name) {
				continue
			}
			names = append(names, name)
		}
		if len(names) != 0 {
			rule.Names = names
			rules = append(rules, rule)
		}
	}
	// Both admitted native ABIs place legacy clone flags in argument zero.
	// ENOSYS, rather than EPERM, lets qualified libc implementations fall back.
	enosys := uint32(38)
	spec.Syscalls = append(rules,
		mappedSeccompRuleV1{Names: []string{"clone"}, Action: "SCMP_ACT_ALLOW", Args: []mappedSeccompArgV1{
			{Index: 0, Value: mappedCloneNamespaceMaskV1, Op: "SCMP_CMP_MASKED_EQ"},
		}},
		mappedSeccompRuleV1{Names: []string{"clone3"}, Action: "SCMP_ACT_ERRNO", ErrnoRet: &enosys},
	)
	profile, err := json.Marshal(spec)
	if err != nil {
		return mappedSeccompPolicyV1{}, fmt.Errorf("encode mapped seccomp profile: %w", err)
	}
	bound := struct {
		Version              int             `json:"version"`
		Seed                 string          `json:"seed"`
		ABI                  string          `json:"abi"`
		FinalCapabilities    uint64          `json:"final_capabilities"`
		SetupCapabilities    uint64          `json:"setup_capabilities"`
		KernelLastCapability int             `json:"kernel_last_capability"`
		Profile              json.RawMessage `json:"profile"`
	}{1, podmanDockerSeccompSHA256, abi, final, setup, kernelLastCapability, profile}
	canonical, err := json.Marshal(bound)
	if err != nil {
		return mappedSeccompPolicyV1{}, fmt.Errorf("encode mapped seccomp identity: %w", err)
	}
	return mappedSeccompPolicyV1{
		abi: abi, finalCapabilities: final, setupCapabilities: setup,
		kernelLastCapability: kernelLastCapability, profile: string(profile),
		identity: fmt.Sprintf("sha256:%x", sha256.Sum256(canonical)),
	}, nil
}
