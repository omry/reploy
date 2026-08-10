package dockerdeploy

import (
	"os"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestRenderDockerInputsFromResolvedPlan(t *testing.T) {
	plan := DockerExecutionPlan{
		EnvironmentID: "demo", DeploymentDir: "/deployment", Phase: blueprint.PhaseStaged, Image: "reploy/demo:staging",
		ContainerName: "demo-staging-abcd", NetworkName: "demo-staging-abcd", Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 501, GID: 20, DockerUser: "501:20"}),
		Mounts: []MountExecutionPlan{{Name: "config", Mode: blueprint.MountManagedBind, Source: "/tmp/demo/conf", Target: "/config", ReadOnly: true}},
		Workload: &WorkloadExecutionPlan{Command: "server", Argv: []string{"/opt/reploy/python/bin/demo", "serve"}, Endpoints: map[string]EndpointExecutionPlan{
			"http": {Scheme: "http", PublishAddress: "127.0.0.1", PublishedPort: 18080, ContainerPort: 8080},
		}},
	}
	inputs, err := RenderDockerInputs(plan, "demo")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(inputs.Compose)
	wantGolden, err := os.ReadFile("testdata/resolved_compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	normalizedGolden := strings.ReplaceAll(string(wantGolden), "\r\n", "\n")
	if compose != normalizedGolden {
		t.Fatalf("compose golden mismatch\nactual:\n%s\nwant:\n%s", compose, wantGolden)
	}
	for _, want := range []string{"image: reploy/demo:staging", "pull_policy: never", `user: "0:0"`, "cap_drop:", "- ALL", "cap_add:", "- NET_ADMIN", "- SETGID", "- SETPCAP", "- SETUID", "sandbox-exec", "--public", "deny", "--local", "deny", "--inbound-tcp", "no-new-privileges:true", "seccomp=builtin", "read_only: true", "HOME: /mnt/reploy-home", "TMPDIR: /mnt/reploy-home", "- /mnt/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=0700,uid=501,gid=20", "type: bind", "127.0.0.1:18080:8080", "/opt/reploy/python/bin/demo", "name: demo-staging-abcd"} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose missing %q:\n%s", want, compose)
		}
	}
	if inputs.Environment["REPLOY_PHASE"] != "staged" || inputs.Control.Script != "demo" || !inputs.Control.HasWorkload {
		t.Fatalf("inputs = %#v", inputs)
	}
}
