package dockerdeploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestPlanPathUpdatesMatrix(t *testing.T) {
	root := t.TempDir()
	staging := DockerExecutionPlan{Mounts: []MountExecutionPlan{
		{Name: "config", Mode: blueprint.MountManagedBind, Source: filepath.Join(root, "stage-conf"), Update: blueprint.UpdatePreserve},
		{Name: "data", Mode: blueprint.MountVolume, Source: "stage-data", Update: blueprint.UpdateReplace},
		{Name: "external", Mode: blueprint.MountBind, Source: filepath.Join(root, "external"), Update: blueprint.UpdateUnmanaged},
		{Name: "scratch", Mode: blueprint.MountTmpfs, Update: blueprint.UpdatePreserve},
	}}
	installRoot := filepath.Join(root, "installed")
	installed := DockerExecutionPlan{Mounts: []MountExecutionPlan{
		{Name: "config", Mode: blueprint.MountManagedBind, Source: filepath.Join(installRoot, "conf"), Update: blueprint.UpdatePreserve},
		{Name: "data", Mode: blueprint.MountVolume, Source: "installed-data", Update: blueprint.UpdateReplace},
		{Name: "external", Mode: blueprint.MountBind, Source: filepath.Join(root, "external"), Update: blueprint.UpdateUnmanaged},
		{Name: "scratch", Mode: blueprint.MountTmpfs, Update: blueprint.UpdatePreserve},
	}}
	actions, err := PlanPathUpdates(staging, installed, installRoot, PathUpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []PathUpdateActionKind{PathPreserveManagedBind, PathReplaceVolume, PathValidateUnmanaged, PathTmpfsNoop}
	for index, kind := range want {
		if actions[index].Kind != kind {
			t.Fatalf("actions[%d] = %#v", index, actions[index])
		}
	}
}

func TestPlanEnvironmentInstallPathUpdatesUsesEnvironmentPoliciesAndOverrides(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staging")
	target := filepath.Join(root, "installed")
	external := filepath.Join(root, "external")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{Environment: blueprint.Environment{ID: "demo"}, Docker: blueprint.Docker{Mounts: map[string]blueprint.DockerMount{
		"config": {
			Mode: blueprint.MountManagedBind, Source: "conf",
			Contract: blueprint.EnvironmentMount{Target: "/conf", UpdatePolicy: blueprint.UpdatePreserve},
		},
		"external": {
			Mode: blueprint.MountBind, Source: external,
			Contract: blueprint.EnvironmentMount{Target: "/external", UpdatePolicy: blueprint.UpdateUnmanaged},
		},
	}}}

	actions, preserve, err := planEnvironmentInstallPathUpdates(document, source, target, InstallScopeUser, []string{"conf"}, false, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(preserve) != 0 {
		t.Fatalf("preserve = %#v, want none", preserve)
	}
	if actions[0].Name != "config" || actions[0].Kind != PathReplaceManagedBind {
		t.Fatalf("config action = %#v", actions[0])
	}
	if actions[1].Name != "external" || actions[1].Kind != PathValidateUnmanaged {
		t.Fatalf("external action = %#v", actions[1])
	}

	actions, _, err = planEnvironmentInstallPathUpdates(document, source, target, InstallScopeUser, nil, true, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if actions[0].Kind != PathReplaceManagedBind || actions[1].Kind != PathValidateUnmanaged {
		t.Fatalf("clean actions = %#v", actions)
	}
}

func TestPlanPathUpdatesOverrideCannotReplaceUnmanaged(t *testing.T) {
	root := t.TempDir()
	staging := DockerExecutionPlan{Mounts: []MountExecutionPlan{{Name: "external", Mode: blueprint.MountBind, Source: root, Update: blueprint.UpdateUnmanaged}}}
	installed := staging
	actions, err := PlanPathUpdates(staging, installed, filepath.Join(root, "installed"), PathUpdateOptions{ReplaceAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if actions[0].Kind != PathValidateUnmanaged {
		t.Fatalf("action = %#v", actions[0])
	}
}

func TestPlanPathUpdatesRejectsManagedTargetEscape(t *testing.T) {
	root := t.TempDir()
	staging := DockerExecutionPlan{Mounts: []MountExecutionPlan{{Name: "config", Mode: blueprint.MountManagedBind, Source: filepath.Join(root, "stage"), Update: blueprint.UpdateReplace}}}
	installed := DockerExecutionPlan{Mounts: []MountExecutionPlan{{Name: "config", Mode: blueprint.MountManagedBind, Source: filepath.Join(root, "outside"), Update: blueprint.UpdateReplace}}}
	if _, err := PlanPathUpdates(staging, installed, filepath.Join(root, "installed"), PathUpdateOptions{}); err == nil {
		t.Fatal("expected target escape rejection")
	}
}
