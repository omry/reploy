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
	if service.User != plan.Sandbox.RuntimeUser.DockerUser || !service.ReadOnly {
		t.Fatalf("persistent sandbox identity/read-only = user %q, read-only %t", service.User, service.ReadOnly)
	}
	if !slices.Equal(service.GroupAdd, []string{"33", "44"}) || !slices.Equal(service.CapDrop, []string{"ALL"}) || !slices.Equal(service.SecurityOpt, []string{"no-new-privileges:true", "seccomp=builtin"}) {
		t.Fatalf("persistent kernel sandbox = groups %#v, caps %#v, security %#v", service.GroupAdd, service.CapDrop, service.SecurityOpt)
	}
	if service.Environment["HOME"] != plan.Sandbox.TemporaryHome || service.Environment["TMPDIR"] != plan.Sandbox.TemporaryHome {
		t.Fatalf("persistent sandbox environment = %#v", service.Environment)
	}
	if !containsString(service.Tmpfs, temporaryHomeMountForPlan(plan)) {
		t.Fatalf("persistent sandbox temporary home = %#v", service.Tmpfs)
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
	if !containsInOrder(transient.Args, []string{"--user", "501:20", "--cap-drop", "ALL"}) ||
		!containsInOrder(transient.Args, []string{"--group-add", "33", "--group-add", "44"}) ||
		!containsInOrder(transient.Args, []string{"--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin"}) ||
		!containsInOrder(transient.Args, []string{"--entrypoint", "/bin/true", plan.Image}) {
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
