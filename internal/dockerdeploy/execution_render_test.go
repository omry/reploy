package dockerdeploy

import (
	"os"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestRenderDockerInputsFromResolvedPlan(t *testing.T) {
	plan := DockerExecutionPlan{
		EnvironmentID: "demo", Phase: blueprint.PhaseStaged, Image: "reploy/demo:staging",
		ContainerName: "demo-staging-abcd", NetworkName: "demo-staging-abcd", RuntimeUser: RuntimeUserPlan{DockerUser: "501:20"},
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
	if compose != string(wantGolden) {
		t.Fatalf("compose golden mismatch\nactual:\n%s\nwant:\n%s", compose, wantGolden)
	}
	for _, want := range []string{"image: reploy/demo:staging", `user: "501:20"`, "read_only: true", "HOME: /tmp/reploy-home", "TMPDIR: /tmp/reploy-home", "- /tmp/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=1777", "type: bind", "127.0.0.1:18080:8080", "/opt/reploy/python/bin/demo"} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose missing %q:\n%s", want, compose)
		}
	}
	if inputs.Environment["REPLOY_PHASE"] != "staged" || inputs.Control.Script != "demo" || !inputs.Control.HasWorkload {
		t.Fatalf("inputs = %#v", inputs)
	}
}
