package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
			Path: blueprint.Path{Container: "/conf", Update: blueprint.UpdatePreserve},
		},
		"external": {
			Mode: blueprint.MountBind, Source: external,
			Path: blueprint.Path{Container: "/external", Update: blueprint.UpdateUnmanaged},
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

func TestPrepareEnvironmentPathUpdatesRemovesOnlyReplaceManagedBind(t *testing.T) {
	root := t.TempDir()
	replace := filepath.Join(root, "replace")
	preserve := filepath.Join(root, "preserve")
	for _, dir := range []string{replace, preserve} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "user-edited"), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareEnvironmentPathUpdates(installPlan{PathUpdates: []PathUpdateAction{
		{Name: "replace", Kind: PathReplaceManagedBind, Target: replace},
		{Name: "preserve", Kind: PathPreserveManagedBind, Target: preserve},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(replace); !os.IsNotExist(err) {
		t.Fatalf("replace target still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(preserve, "user-edited")); err != nil {
		t.Fatalf("preserved content missing: %v", err)
	}
}

func TestPrepareEnvironmentPathUpdatesCopiesNamedVolumes(t *testing.T) {
	previousCommand := runInstallPathUpdateCommand
	previousOutput := runInstallPathUpdateOutput
	t.Cleanup(func() {
		runInstallPathUpdateCommand = previousCommand
		runInstallPathUpdateOutput = previousOutput
	})
	commands := []CommandSpec{}
	runInstallPathUpdateCommand = func(spec CommandSpec, _ RunOptions) error {
		commands = append(commands, spec)
		return nil
	}
	runInstallPathUpdateOutput = func(_ context.Context, args ...string) (string, error) {
		name := args[len(args)-1]
		if name == "installed-preserved" || strings.HasPrefix(name, "stage-") {
			return "present", nil
		}
		return "", fmt.Errorf("No such volume")
	}
	plan := installPlan{PathUpdateImage: "reploy/demo:staging", PathUpdates: []PathUpdateAction{
		{Name: "preserved", Kind: PathPreserveVolume, Source: "stage-preserved", Target: "installed-preserved"},
		{Name: "new", Kind: PathPreserveVolume, Source: "stage-new", Target: "installed-new"},
		{Name: "replace", Kind: PathReplaceVolume, Source: "stage-replace", Target: "installed-replace"},
	}}
	if err := prepareEnvironmentPathUpdates(plan); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 5 {
		t.Fatalf("commands = %#v, want create+copy and remove+create+copy", commands)
	}
	if got := commands[0]; got.Name != "docker" || !containsAdjacent(got.Args, "volume", "create") || got.Args[len(got.Args)-1] != "installed-new" {
		t.Fatalf("new volume create = %#v", got)
	}
	copy := commands[1]
	if !containsAdjacent(copy.Args, "--entrypoint", "/bin/sh") || copy.Args[len(copy.Args)-3] != "reploy/demo:staging" {
		t.Fatalf("copy command = %#v", copy)
	}
	if got := commands[2]; !containsInOrder(got.Args, []string{"volume", "rm", "-f", "installed-replace"}) {
		t.Fatalf("replace remove = %#v", got)
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
