package dockerdeploy

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestPlanLiveRunConcurrencyV1AppliesBlueprintPolicy(t *testing.T) {
	readOnly := DockerExecutionPlan{Mounts: []MountExecutionPlan{{Name: "config", Target: "/conf", ReadOnly: true}}}
	writable := DockerExecutionPlan{Mounts: []MountExecutionPlan{
		{Name: "data", Target: "/data", ReadOnly: false},
		{Name: "config", Target: "/conf", ReadOnly: false},
	}}
	tests := []struct {
		name     string
		policy   blueprint.ConcurrentRunPolicy
		plan     DockerExecutionPlan
		overlap  bool
		conflict string
	}{
		{name: "yes with writable mounts", policy: blueprint.ConcurrentRunYes, plan: writable, overlap: true},
		{name: "no with read-only mounts", policy: blueprint.ConcurrentRunNo, plan: readOnly},
		{name: "auto with read-only mounts", policy: blueprint.ConcurrentRunAuto, plan: readOnly, overlap: true},
		{name: "auto with writable mounts", policy: blueprint.ConcurrentRunAuto, plan: writable, conflict: "config"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := blueprint.Document{Environment: blueprint.Environment{AllowConcurrent: test.policy}}
			decision, err := PlanLiveRunConcurrencyV1(document, test.plan, nil)
			if err != nil {
				t.Fatal(err)
			}
			if decision.AllowsOverlap != test.overlap || decision.WritableMount != test.conflict {
				t.Fatalf("decision = %#v", decision)
			}
			if test.name == "auto with writable mounts" && !reflect.DeepEqual(decision.WritablePaths, []string{"/conf", "/data"}) {
				t.Fatalf("writable paths = %#v", decision.WritablePaths)
			}
		})
	}
}

func TestLiveRunConflictErrorV1NamesPolicyAndWaitWithoutAssumingPublicBlocker(t *testing.T) {
	err := liveRunConflictErrorV1(blueprint.ConcurrentRunAuto, "data")
	if !errors.Is(err, deploy.ErrLiveRunConflict) {
		t.Fatalf("conflict cause = %v", err)
	}
	for _, want := range []string{"allow_concurrent: auto", `writable mount: "data"`, "--wait"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("conflict error missing %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "reploy runs list") {
		t.Fatalf("conflict error assumes a public run is the blocker: %v", err)
	}
	if strings.Contains(err.Error(), deploy.ErrLiveRunConflict.Error()) {
		t.Fatalf("conflict error leaked internal sentinel text: %v", err)
	}
}

func TestPlanLiveRunConcurrencyV1TreatsOutputDirectoryAsSharedAndOutputFileAsPrivate(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{AllowConcurrent: blueprint.ConcurrentRunAuto}}
	for _, test := range []struct {
		name     string
		output   *transientOutputMount
		overlap  bool
		conflict string
	}{
		{name: "none", overlap: true},
		{name: "directory", output: &transientOutputMount{Variable: runtimeOutputDirectoryVariable, ContainerPath: runtimeOutputRoot}, conflict: "--output-dir"},
		{name: "file", output: &transientOutputMount{Variable: runtimeOutputFileVariable}, overlap: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := PlanLiveRunConcurrencyV1(document, DockerExecutionPlan{}, test.output)
			if err != nil {
				t.Fatal(err)
			}
			if decision.AllowsOverlap != test.overlap || decision.WritableMount != test.conflict {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestPlanLiveRunConcurrencyV1RejectsInvalidPolicyAndOutput(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{AllowConcurrent: "sometimes"}}
	if _, err := PlanLiveRunConcurrencyV1(document, DockerExecutionPlan{}, nil); err == nil || !strings.Contains(err.Error(), "allow_concurrent") {
		t.Fatalf("policy error = %v", err)
	}
	document.Environment.AllowConcurrent = blueprint.ConcurrentRunAuto
	if _, err := PlanLiveRunConcurrencyV1(document, DockerExecutionPlan{}, &transientOutputMount{Variable: "UNKNOWN"}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("output error = %v", err)
	}
}
