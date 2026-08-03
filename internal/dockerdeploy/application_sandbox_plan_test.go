package dockerdeploy

import (
	"slices"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"gopkg.in/yaml.v3"
)

func TestApplicationRenderersConsumeCanonicalSandboxPlan(t *testing.T) {
	plan := DockerExecutionPlan{
		EnvironmentID: "demo",
		DeploymentDir: t.TempDir(),
		Phase:         blueprint.PhaseStaged,
		Image:         "reploy/demo:staging",
		ContainerName: "demo-staging-abcd",
		NetworkName:   "demo-staging-abcd",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{
			UID: 501, GID: 20, SupplementaryGIDs: []int{33, 44}, DockerUser: "501:20",
		}),
		Workload: &WorkloadExecutionPlan{Argv: []string{"/bin/true"}, Endpoints: map[string]EndpointExecutionPlan{}},
	}

	persistent, err := RenderDockerInputs(plan, "demo")
	if err != nil {
		t.Fatal(err)
	}
	var compose composePlanDocument
	if err := yaml.Unmarshal(persistent.Compose, &compose); err != nil {
		t.Fatal(err)
	}
	service := compose.Services["environment"]
	if service.User != "0:0" || !service.ReadOnly {
		t.Fatalf("persistent sandbox identity/read-only = user %q, read-only %t", service.User, service.ReadOnly)
	}
	if len(service.GroupAdd) != 0 || !slices.Equal(service.CapDrop, []string{"ALL"}) ||
		!slices.Equal(service.CapAdd, []string{"NET_ADMIN", "SETGID", "SETPCAP", "SETUID"}) ||
		!slices.Equal(service.SecurityOpt, []string{"no-new-privileges:true", "seccomp=builtin"}) {
		t.Fatalf("persistent kernel sandbox = groups %#v, drop %#v, add %#v, security %#v", service.GroupAdd, service.CapDrop, service.CapAdd, service.SecurityOpt)
	}
	if service.Environment["HOME"] != plan.Sandbox.TemporaryHome || service.Environment["TMPDIR"] != plan.Sandbox.TemporaryHome {
		t.Fatalf("persistent sandbox environment = %#v", service.Environment)
	}
	if !containsString(service.Tmpfs, temporaryHomeMountForPlan(plan)) {
		t.Fatalf("persistent sandbox temporary home = %#v", service.Tmpfs)
	}
	baseOnly := plan
	baseOnly.Workload = nil
	baseRendered, err := RenderDockerInputs(baseOnly, "demo")
	if err != nil {
		t.Fatal(err)
	}
	var baseCompose composePlanDocument
	if err := yaml.Unmarshal(baseRendered.Compose, &baseCompose); err != nil {
		t.Fatal(err)
	}
	baseService := baseCompose.Services["environment"]
	if baseService.User != plan.Sandbox.RuntimeUser.DockerUser || len(baseService.CapAdd) != 0 || !slices.Equal(baseService.GroupAdd, []string{"33", "44"}) {
		t.Fatalf("base-only dormant service authority = user %q, groups %#v, caps %#v", baseService.User, baseService.GroupAdd, baseService.CapAdd)
	}

	transient, err := TransientCommandSpec(
		plan,
		ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}},
		nil,
		false,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !containsInOrder(transient.Args, []string{"--read-only", "--tmpfs", transientHomeMountForPlan(plan)}) {
		t.Fatalf("transient sandbox read-only root/home = %#v", transient.Args)
	}
	if !containsInOrder(transient.Args, []string{
		"--env", "HOME=" + plan.Sandbox.TemporaryHome,
		"--env", "TMPDIR=" + plan.Sandbox.TemporaryHome,
	}) {
		t.Fatalf("transient sandbox environment = %#v", transient.Args)
	}
	if !containsInOrder(transient.Args, []string{"--user", "0:0", "--cap-drop", "ALL"}) ||
		!containsInOrder(transient.Args, []string{"--cap-add", "NET_ADMIN", "--cap-add", "SETGID", "--cap-add", "SETPCAP", "--cap-add", "SETUID"}) ||
		!containsInOrder(transient.Args, []string{"--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin"}) ||
		!containsInOrder(transient.Args, []string{"--entrypoint", plan.Sandbox.StartupVerifier.Path, plan.Image, "sandbox-exec", "--uid", "501", "--gid", "20", "--groups", "33,44", "--public", "deny", "--local", "deny", "--", "/bin/true"}) {
		t.Fatalf("transient sandbox runtime identity = %#v", transient.Args)
	}

	invalid := plan
	invalid.Sandbox.ReadOnlyRoot = false
	if _, err := RenderDockerInputs(invalid, "demo"); err == nil || !strings.Contains(err.Error(), "read-only container root") {
		t.Fatalf("persistent invalid sandbox error = %v", err)
	}
	if _, err := TransientCommandSpec(invalid, ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}}, nil, false, false); err == nil || !strings.Contains(err.Error(), "read-only container root") {
		t.Fatalf("transient invalid sandbox error = %v", err)
	}
}

func TestApplicationSandboxPlanRejectsIdentityAndKernelEscapes(t *testing.T) {
	base := newApplicationSandboxPlanV1(RuntimeUserPlan{
		UID: 501, GID: 20, SupplementaryGIDs: []int{33, 44}, DockerUser: "501:20",
	})
	tests := []struct {
		name   string
		mutate func(*ApplicationSandboxPlanV1)
		want   string
	}{
		{name: "root primary group", mutate: func(plan *ApplicationSandboxPlanV1) { plan.RuntimeUser.GID, plan.RuntimeUser.DockerUser = 0, "501:0" }, want: "root group"},
		{name: "root supplementary group", mutate: func(plan *ApplicationSandboxPlanV1) { plan.RuntimeUser.SupplementaryGIDs = []int{0, 33} }, want: "root group"},
		{name: "noncanonical groups", mutate: func(plan *ApplicationSandboxPlanV1) { plan.RuntimeUser.SupplementaryGIDs = []int{44, 33} }, want: "unique, sorted"},
		{name: "capabilities", mutate: func(plan *ApplicationSandboxPlanV1) { plan.Kernel.DropAllCapabilities = false }, want: "drop all"},
		{name: "privilege escalation", mutate: func(plan *ApplicationSandboxPlanV1) { plan.Kernel.NoNewPrivileges = false }, want: "no-new-privileges"},
		{name: "seccomp", mutate: func(plan *ApplicationSandboxPlanV1) { plan.Kernel.SeccompProfile = "" }, want: "seccomp"},
		{name: "privileged", mutate: func(plan *ApplicationSandboxPlanV1) { plan.Kernel.Privileged = true }, want: "privileged"},
		{name: "host namespace", mutate: func(plan *ApplicationSandboxPlanV1) { plan.Kernel.HostNamespaces = []string{"pid"} }, want: "host namespaces"},
		{name: "host device", mutate: func(plan *ApplicationSandboxPlanV1) { plan.Kernel.HostDevices = []string{"/dev/kvm"} }, want: "host devices"},
		{name: "verifier path", mutate: func(plan *ApplicationSandboxPlanV1) { plan.StartupVerifier.Path = "/bin/true" }, want: "startup verifier"},
		{name: "verifier recipe", mutate: func(plan *ApplicationSandboxPlanV1) { plan.StartupVerifier.RecipeVersion = "unchecked-exec-v1" }, want: "startup verifier"},
		{name: "verifier artifact", mutate: func(plan *ApplicationSandboxPlanV1) {
			plan.StartupVerifier.Artifact = rendererDigest("1")
		}, want: "must not contain an artifact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			plan.RuntimeUser.SupplementaryGIDs = append([]int(nil), base.RuntimeUser.SupplementaryGIDs...)
			plan.Kernel.HostNamespaces = []string{}
			plan.Kernel.HostDevices = []string{}
			test.mutate(&plan)
			if err := ValidateApplicationSandboxPlanV1(plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestApplicationNetworkPolicyControlsOnlySetupCapability(t *testing.T) {
	for _, test := range []struct {
		name     string
		public   blueprint.NetworkAccess
		local    blueprint.NetworkAccess
		netAdmin bool
	}{
		{name: "deny both", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessDeny, netAdmin: true},
		{name: "public only", public: blueprint.NetworkAccessAllow, local: blueprint.NetworkAccessDeny, netAdmin: true},
		{name: "local only", public: blueprint.NetworkAccessDeny, local: blueprint.NetworkAccessAllow, netAdmin: true},
		{name: "allow both", public: blueprint.NetworkAccessAllow, local: blueprint.NetworkAccessAllow},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := newApplicationSandboxPlanWithNetworkV1(
				RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"},
				blueprint.RuntimeNetwork{Public: test.public, Local: test.local},
			)
			capabilities := applicationSetupCapabilitiesV1(plan)
			if containsString(capabilities, "NET_ADMIN") != test.netAdmin {
				t.Fatalf("setup capabilities = %#v", capabilities)
			}
			argv := sandboxApplicationArgvV1(DockerExecutionPlan{
				Sandbox: plan,
				Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
					"http": {ContainerPort: 8080},
				}},
			}, []string{"/bin/true"}, true, []int{8080})
			if !containsInOrder(argv, []string{"--public", string(test.public), "--local", string(test.local), "--inbound-tcp", "8080", "--", "/bin/true"}) {
				t.Fatalf("sandbox argv = %#v", argv)
			}
		})
	}
}

func TestRootApplicationSetupCanClearSupplementaryGroups(t *testing.T) {
	plan := newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 0, GID: 0, DockerUser: "0:0"})
	capabilities := applicationSetupCapabilitiesV1(plan)
	if !containsString(capabilities, "SETGID") || !containsString(capabilities, "SETPCAP") || !containsString(capabilities, "NET_ADMIN") || containsString(capabilities, "SETUID") {
		t.Fatalf("root setup capabilities = %#v", capabilities)
	}
}

func TestTransientApplicationDoesNotInheritWorkloadEndpointGrants(t *testing.T) {
	plan := DockerExecutionPlan{
		DeploymentDir: t.TempDir(), Image: "reploy/demo:staging", ContainerName: "demo",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"}),
		Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
			"http": {ContainerPort: 8080},
		}},
	}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}}, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(spec.Args, "--inbound-tcp") || containsString(spec.Args, "8080") {
		t.Fatalf("transient command inherited workload endpoint grant: %#v", spec.Args)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
