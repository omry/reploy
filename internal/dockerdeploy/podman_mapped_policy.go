package dockerdeploy

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/mappedlaunch"
	"github.com/omry/reploy/internal/probe"
)

type mappedRestartPolicyV1 struct {
	Name              string
	MaximumRetryCount int
}

// These names are in Linux capability-number order, matching the compiler's
// closed v6.6 catalog. Setup authority never becomes an application grant.
var mappedCapabilityNamesV1 = strings.Fields(`CHOWN DAC_OVERRIDE DAC_READ_SEARCH FOWNER FSETID KILL
SETGID SETUID SETPCAP LINUX_IMMUTABLE NET_BIND_SERVICE NET_BROADCAST NET_ADMIN NET_RAW
IPC_LOCK IPC_OWNER SYS_MODULE SYS_RAWIO SYS_CHROOT SYS_PTRACE SYS_PACCT SYS_ADMIN
SYS_BOOT SYS_NICE SYS_RESOURCE SYS_TIME SYS_TTY_CONFIG MKNOD LEASE AUDIT_WRITE
AUDIT_CONTROL SETFCAP MAC_OVERRIDE MAC_ADMIN SYSLOG WAKE_ALARM BLOCK_SUSPEND
AUDIT_READ PERFMON BPF CHECKPOINT_RESTORE`)

// Only this typed overlay can carry mapped compiler output into create. The
// ordinary parser and its capability/profile allowlist stay unchanged.
type mappedPodmanCreateRequestV1 struct {
	body   podmanApplicationCreate
	name   string
	policy mappedSeccompPolicyV1
}

func mappedPodmanApplicationCreateRequestV1(spec CommandSpec, namespace string, policy mappedSeccompPolicyV1) (mappedPodmanCreateRequestV1, error) {
	var result mappedPodmanCreateRequestV1
	compiled, err := compileMappedSeccompV1(podmanDockerSeccompProfile, policy.abi, policy.finalCapabilities, policy.setupCapabilities, policy.kernelLastCapability)
	if err != nil || policy != compiled {
		return result, fmt.Errorf("mapped create requires an exact compiled kernel policy")
	}
	body, name, err := podmanApplicationCreateRequest(spec, namespace)
	if err != nil {
		return result, err
	}
	if body.Tty {
		return result, fmt.Errorf("mapped create requires qualified non-TTY sandbox stdin transport")
	}
	if err := validateMappedCreateStartupV1(body.Cmd, policy); err != nil {
		return result, err
	}
	setup := policy.finalCapabilities
	for _, name := range body.HostConfig.CapAdd {
		index := slices.Index(mappedCapabilityNamesV1, name)
		if index < 0 {
			return result, fmt.Errorf("mapped create has an unknown setup capability")
		}
		setup |= uint64(1) << index
	}
	if setup != policy.setupCapabilities {
		return result, fmt.Errorf("mapped setup capabilities differ from the exact required union")
	}
	body.HostConfig.CapAdd = []string{}
	for bit, name := range mappedCapabilityNamesV1 {
		if setup&(uint64(1)<<bit) != 0 {
			body.HostConfig.CapAdd = append(body.HostConfig.CapAdd, name)
		}
	}
	slices.Sort(body.HostConfig.CapAdd)
	for i, option := range body.HostConfig.SecurityOpt {
		if strings.HasPrefix(option, "seccomp=") {
			body.HostConfig.SecurityOpt[i] = "seccomp=" + policy.profile
		}
	}
	body.HostConfig.AutoRemove = false
	body.HostConfig.RestartPolicy = &mappedRestartPolicyV1{Name: "no", MaximumRetryCount: 0}
	return mappedPodmanCreateRequestV1{body, name, policy}, nil
}

// Validate the two closed startup representations that can reach mapped
// creation. Bare requests carry sandbox-exec directly. The owned waiter wraps
// the same startup after its canonical binding and deadline; validate that
// wrapper before any create rather than trusting the earlier waiter call.
func validateMappedCreateStartupV1(command []string, policy mappedSeccompPolicyV1) error {
	if len(command) == 0 {
		return fmt.Errorf("mapped create requires a qualified startup command")
	}
	if command[0] == "sandbox-exec" {
		_, err := mappedStartupCommandV1(command, policy)
		return err
	}
	if command[0] != mappedlaunch.Command || len(command) < 5 || command[3] != "sandbox-exec" {
		return fmt.Errorf("mapped create requires a fixed mapped-wait startup")
	}
	if _, err := mappedlaunch.DecodeBinding([]byte(command[1])); err != nil {
		return fmt.Errorf("mapped waiter binding is invalid: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, command[2]); err != nil {
		return fmt.Errorf("mapped waiter deadline is invalid: %w", err)
	}
	startup := append([]string{"sandbox-exec"}, command[4:]...)
	if _, err := mappedStartupCommandV1(startup, policy); err != nil {
		return err
	}
	return nil
}

// Require the independent trusted startup argument to match the selected policy.
// Omitted, duplicate or displaced arguments fail the actual probe parser before
// any create or frame. This boundary must never infer a missing authority input.
func mappedStartupCommandV1(command []string, policy mappedSeccompPolicyV1) ([]string, error) {
	if len(command) == 0 || command[0] != "sandbox-exec" {
		return nil, fmt.Errorf("mapped startup requires sandbox-exec")
	}
	args := slices.Clone(command[1:])
	if err := probe.ValidateMappedSandboxArgumentsV1(args, policy.finalCapabilities); err != nil {
		return nil, err
	}
	return append([]string{"sandbox-exec"}, args...), nil
}

// The expected tuple travels with the hashed instance request. F must return
// fresh effective-policy evidence, not merely echo engine policy strings.
type mappedKernelFactsV1 struct {
	Identity             canonical.Digest `json:"identity"`
	ABI                  string           `json:"abi"`
	FinalCapabilities    string           `json:"final_capabilities"`
	SetupCapabilities    string           `json:"setup_capabilities"`
	KernelLastCapability string           `json:"kernel_last_capability"`
}

func (p mappedSeccompPolicyV1) facts() mappedKernelFactsV1 {
	return mappedKernelFactsV1{canonical.Digest(p.identity), p.abi,
		fmt.Sprintf("%016x", p.finalCapabilities), fmt.Sprintf("%016x", p.setupCapabilities),
		fmt.Sprint(p.kernelLastCapability)}
}

func (f mappedKernelFactsV1) validate() error {
	final, err := strconv.ParseUint(f.FinalCapabilities, 16, 64)
	if err != nil || fmt.Sprintf("%016x", final) != f.FinalCapabilities {
		return fmt.Errorf("invalid final kernel capabilities")
	}
	setup, err := strconv.ParseUint(f.SetupCapabilities, 16, 64)
	if err != nil || fmt.Sprintf("%016x", setup) != f.SetupCapabilities {
		return fmt.Errorf("invalid setup kernel capabilities")
	}
	last, err := strconv.Atoi(f.KernelLastCapability)
	if err != nil || strconv.Itoa(last) != f.KernelLastCapability {
		return fmt.Errorf("invalid kernel capability limit")
	}
	p, err := compileMappedSeccompV1(podmanDockerSeccompProfile, f.ABI, final, setup, last)
	if err != nil || p.facts() != f {
		return fmt.Errorf("kernel facts differ from the exact compiler result")
	}
	return nil
}
