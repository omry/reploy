package dockerdeploy

import (
	"fmt"
	"path"
	"slices"
	"strconv"
)

const applicationSeccompProfileBuiltinV1 = "builtin"

type ApplicationKernelPolicyV1 struct {
	DropAllCapabilities bool
	NoNewPrivileges     bool
	SeccompProfile      string
	Privileged          bool
	HostNamespaces      []string
	HostDevices         []string
}

// ApplicationSandboxPlanV1 is the common security boundary consumed by every
// application-container renderer. It contains only policies that Reploy
// currently enforces; later sandbox slices extend this plan rather than adding
// renderer-specific flags.
type ApplicationSandboxPlanV1 struct {
	RuntimeUser   RuntimeUserPlan
	ReadOnlyRoot  bool
	TemporaryHome string
	Kernel        ApplicationKernelPolicyV1
}

func newApplicationSandboxPlanV1(runtimeUser RuntimeUserPlan) ApplicationSandboxPlanV1 {
	return ApplicationSandboxPlanV1{
		RuntimeUser:   runtimeUser,
		ReadOnlyRoot:  true,
		TemporaryHome: environmentTemporaryHome,
		Kernel: ApplicationKernelPolicyV1{
			DropAllCapabilities: true,
			NoNewPrivileges:     true,
			SeccompProfile:      applicationSeccompProfileBuiltinV1,
			HostNamespaces:      []string{},
			HostDevices:         []string{},
		},
	}
}

func ValidateApplicationSandboxPlanV1(plan ApplicationSandboxPlanV1) error {
	if plan.RuntimeUser.UID < 0 || plan.RuntimeUser.GID < 0 {
		return fmt.Errorf("application sandbox requires a non-negative numeric UID and GID")
	}
	wantUser := strconv.Itoa(plan.RuntimeUser.UID) + ":" + strconv.Itoa(plan.RuntimeUser.GID)
	if plan.RuntimeUser.DockerUser != wantUser {
		return fmt.Errorf("application sandbox Docker user must match its numeric UID and GID")
	}
	wantGroups, err := normalizeSupplementaryGIDsV1(plan.RuntimeUser.GID, plan.RuntimeUser.SupplementaryGIDs)
	if err != nil {
		return fmt.Errorf("application sandbox supplementary groups: %w", err)
	}
	if !slices.Equal(plan.RuntimeUser.SupplementaryGIDs, wantGroups) {
		return fmt.Errorf("application sandbox supplementary groups must be unique, sorted, and exclude the primary GID")
	}
	if plan.RuntimeUser.UID != 0 {
		if plan.RuntimeUser.GID == 0 || slices.Contains(plan.RuntimeUser.SupplementaryGIDs, 0) {
			return fmt.Errorf("non-root application sandbox identity must not include the root group")
		}
	}
	if !plan.ReadOnlyRoot {
		return fmt.Errorf("application sandbox requires a read-only container root")
	}
	if plan.TemporaryHome != environmentTemporaryHome || !path.IsAbs(plan.TemporaryHome) || path.Clean(plan.TemporaryHome) != plan.TemporaryHome {
		return fmt.Errorf("application sandbox temporary home must be %s", environmentTemporaryHome)
	}
	if !plan.Kernel.DropAllCapabilities {
		return fmt.Errorf("application sandbox must drop all Linux capabilities")
	}
	if !plan.Kernel.NoNewPrivileges {
		return fmt.Errorf("application sandbox must enable no-new-privileges")
	}
	if plan.Kernel.SeccompProfile != applicationSeccompProfileBuiltinV1 {
		return fmt.Errorf("application sandbox seccomp profile must be %q", applicationSeccompProfileBuiltinV1)
	}
	if plan.Kernel.Privileged {
		return fmt.Errorf("application sandbox must not use privileged mode")
	}
	if plan.Kernel.HostNamespaces == nil || len(plan.Kernel.HostNamespaces) != 0 {
		return fmt.Errorf("application sandbox must prohibit host namespaces")
	}
	if plan.Kernel.HostDevices == nil || len(plan.Kernel.HostDevices) != 0 {
		return fmt.Errorf("application sandbox must prohibit host devices")
	}
	return nil
}

func normalizeSupplementaryGIDsV1(primary int, values []int) ([]int, error) {
	result := append([]int(nil), values...)
	for _, gid := range result {
		if gid < 0 {
			return nil, fmt.Errorf("GID must be non-negative")
		}
	}
	slices.Sort(result)
	result = slices.Compact(result)
	result = slices.DeleteFunc(result, func(gid int) bool { return gid == primary })
	if result == nil {
		result = []int{}
	}
	return result, nil
}
