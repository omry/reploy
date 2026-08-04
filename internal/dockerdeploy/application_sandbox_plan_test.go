package dockerdeploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"gopkg.in/yaml.v3"
)

func TestApplicationRenderersConsumeCanonicalSandboxPlan(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	workspace := testPreparedProbeWorkspace(t, platform, t.TempDir())
	plan := DockerExecutionPlan{
		EnvironmentID: "demo",
		DeploymentDir: t.TempDir(),
		Phase:         blueprint.PhaseStaged,
		Image:         "reploy/demo:staging",
		ContainerName: "demo-staging-abcd",
		NetworkName:   "demo-staging-abcd",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{
			UID: 501, GID: 20, DockerUser: "501:20",
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
	if service.Environment["HOME"] != plan.Sandbox.TemporaryHome || service.Environment["TMPDIR"] != plan.Sandbox.TemporaryHome {
		t.Fatalf("persistent sandbox environment = %#v", service.Environment)
	}
	if !containsString(service.Tmpfs, temporaryHomeMountForPlan(plan)) {
		t.Fatalf("persistent sandbox temporary home = %#v", service.Tmpfs)
	}

	transient, err := TransientCommandSpec(
		plan,
		ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}},
		workspace,
		nil,
		false,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !containsInOrder(transient.Args, []string{"--read-only", "--mount", transientHomeMountForPlan(plan)}) {
		t.Fatalf("transient sandbox read-only root/home = %#v", transient.Args)
	}
	if !containsInOrder(transient.Args, []string{
		"--env", "HOME=" + plan.Sandbox.TemporaryHome,
		"--env", "TMPDIR=" + plan.Sandbox.TemporaryHome,
	}) {
		t.Fatalf("transient sandbox environment = %#v", transient.Args)
	}
	if !containsInOrder(transient.Args, []string{
		"--entrypoint", ProbeContainerExecutable,
		plan.Image, "run-transient", "501", "20", "/bin/true",
	}) {
		t.Fatalf("transient sandbox runtime identity = %#v", transient.Args)
	}

	invalid := plan
	invalid.Sandbox.ReadOnlyRoot = false
	if _, err := RenderDockerInputs(invalid, "demo"); err == nil || !strings.Contains(err.Error(), "read-only container root") {
		t.Fatalf("persistent invalid sandbox error = %v", err)
	}
	if _, err := TransientCommandSpec(invalid, ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}}, workspace, nil, false, false); err == nil || !strings.Contains(err.Error(), "read-only container root") {
		t.Fatalf("transient invalid sandbox error = %v", err)
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
