package dockerdeploy

import (
	"fmt"
	"path"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

const applicationSeccompProfileBuiltinV1 = "builtin"
const applicationGooglePublicDNSPrimaryV1 = "8.8.8.8"
const applicationGooglePublicDNSSecondaryV1 = "8.8.4.4"

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
	RuntimeUser     RuntimeUserPlan
	Network         ApplicationNetworkPolicyV1
	ReadOnlyRoot    bool
	TemporaryHome   string
	StartupVerifier deploy.ApplicationStartupVerifierV1
	Kernel          ApplicationKernelPolicyV1
}

type ApplicationNetworkPolicyV1 struct {
	Public    blueprint.NetworkAccess
	Local     blueprint.NetworkAccess
	Ambiguous blueprint.AmbiguousNetworkAccess
}

func newApplicationSandboxPlanV1(runtimeUser RuntimeUserPlan) ApplicationSandboxPlanV1 {
	return newApplicationSandboxPlanWithNetworkV1(runtimeUser, blueprint.RuntimeNetwork{})
}

func newApplicationSandboxPlanWithNetworkV1(runtimeUser RuntimeUserPlan, network blueprint.RuntimeNetwork) ApplicationSandboxPlanV1 {
	if runtimeUser.LocalUser == "" {
		runtimeUser.LocalUser = runtimeLocalUserNameV1("", runtimeUser.UID)
	}
	network = normalizeRuntimeNetworkV1(network)
	return ApplicationSandboxPlanV1{
		RuntimeUser:     runtimeUser,
		Network:         ApplicationNetworkPolicyV1{Public: network.Public, Local: network.Local, Ambiguous: network.Ambiguous},
		ReadOnlyRoot:    true,
		TemporaryHome:   environmentTemporaryHome,
		StartupVerifier: deploy.ApplicationStartupVerifierContractV1(),
		Kernel: ApplicationKernelPolicyV1{
			DropAllCapabilities: true,
			NoNewPrivileges:     true,
			SeccompProfile:      applicationSeccompProfileBuiltinV1,
			HostNamespaces:      []string{},
			HostDevices:         []string{},
		},
	}
}

func normalizeRuntimeNetworkV1(network blueprint.RuntimeNetwork) blueprint.RuntimeNetwork {
	if network.Public == "" {
		network.Public = blueprint.NetworkAccessDeny
	}
	if network.Local == "" {
		network.Local = blueprint.NetworkAccessDeny
	}
	if network.Ambiguous == "" {
		network.Ambiguous = blueprint.AmbiguousNetworkAccessRequireBoth
	}
	return network
}

func applicationDockerDNSResolversV1(network ApplicationNetworkPolicyV1) []string {
	if network.Public == blueprint.NetworkAccessAllow && network.Local == blueprint.NetworkAccessDeny {
		return []string{applicationGooglePublicDNSPrimaryV1, applicationGooglePublicDNSSecondaryV1}
	}
	return nil
}

func ValidateApplicationSandboxPlanV1(plan ApplicationSandboxPlanV1) error {
	if plan.RuntimeUser.UID < 0 || plan.RuntimeUser.GID < 0 {
		return fmt.Errorf("application sandbox requires a non-negative numeric UID and GID")
	}
	if err := validateApplicationNetworkAccessV1("public", plan.Network.Public); err != nil {
		return err
	}
	if err := validateApplicationNetworkAccessV1("local", plan.Network.Local); err != nil {
		return err
	}
	if err := validateApplicationAmbiguousNetworkAccessV1(plan.Network.Ambiguous); err != nil {
		return err
	}
	wantUser := strconv.Itoa(plan.RuntimeUser.UID) + ":" + strconv.Itoa(plan.RuntimeUser.GID)
	if plan.RuntimeUser.DockerUser != wantUser {
		return fmt.Errorf("application sandbox Docker user must match its numeric UID and GID")
	}
	if plan.RuntimeUser.LocalUser == "" {
		return fmt.Errorf("application sandbox requires a container-local user name")
	}
	if plan.RuntimeUser.UID == 0 && plan.RuntimeUser.LocalUser != "root" {
		return fmt.Errorf("root application sandbox identity must use the local user name root")
	}
	if plan.RuntimeUser.UID != 0 && plan.RuntimeUser.LocalUser == "root" {
		return fmt.Errorf("non-root application sandbox identity must not use the local user name root")
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
	if err := deploy.ValidateApplicationStartupVerifierV1(plan.StartupVerifier, false); err != nil {
		return fmt.Errorf("application sandbox startup verifier: %w", err)
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

func validateApplicationNetworkAccessV1(name string, access blueprint.NetworkAccess) error {
	switch access {
	case blueprint.NetworkAccessDeny, blueprint.NetworkAccessAllow:
		return nil
	default:
		return fmt.Errorf("application sandbox %s network access must be allow or deny", name)
	}
}

func validateApplicationAmbiguousNetworkAccessV1(access blueprint.AmbiguousNetworkAccess) error {
	switch access {
	case blueprint.AmbiguousNetworkAccessRequireBoth, blueprint.AmbiguousNetworkAccessAllow:
		return nil
	default:
		return fmt.Errorf("application sandbox ambiguous network access must be require-both or allow")
	}
}

func applicationLocalAccountV1(plan ApplicationSandboxPlanV1) (deploy.ApplicationLocalAccountV1, error) {
	if err := ValidateApplicationSandboxPlanV1(plan); err != nil {
		return deploy.ApplicationLocalAccountV1{}, err
	}
	account := deploy.ApplicationLocalAccountV1{
		Schema: deploy.ApplicationLocalAccountSchemaV1,
		Name:   plan.RuntimeUser.LocalUser,
		UID:    strconv.Itoa(plan.RuntimeUser.UID),
		GID:    strconv.Itoa(plan.RuntimeUser.GID),
		Home:   plan.TemporaryHome,
	}
	if err := deploy.ValidateApplicationLocalAccountV1(account); err != nil {
		return deploy.ApplicationLocalAccountV1{}, err
	}
	return account, nil
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
