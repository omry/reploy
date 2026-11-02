package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestIntrinsicRootPlansDoNotCreateRootBinds(t *testing.T) {
	for _, host := range []blueprint.HostOS{blueprint.HostLinux, blueprint.HostMacOS, blueprint.HostWindows} {
		for _, explicit := range []bool{false, true} {
			document := blueprint.Document{Environment: blueprint.Environment{ID: "root-policy", Mounts: map[string]blueprint.EnvironmentMount{}}}
			if explicit {
				document.Environment.Mounts["root"] = blueprint.EnvironmentMount{Target: "/"}
			}
			plan, err := PlanDockerExecution(document, DockerPlanContext{
				DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged, GeneratedImage: "pinned-image", Host: host, UID: 1000, GID: 1000,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Sandbox.ReadOnlyRoot || len(plan.Mounts) != 0 {
				t.Fatal("intrinsic read-only root became a bind or changed the sandbox")
			}
			rendered, err := RenderDockerInputs(plan, "appctl")
			if err != nil || !strings.Contains(string(rendered.Compose), "read_only: true") {
				t.Fatalf("read-only rendering: %s, %v", rendered.Compose, err)
			}
		}
	}
}

func TestIntrinsicWritableRootCannotBypassCurrentSandboxQualification(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{ID: "root-policy", Mounts: map[string]blueprint.EnvironmentMount{
		"root": {Target: "/", Writable: true},
	}}}
	_, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir: t.TempDir(), Phase: blueprint.PhaseStaged, GeneratedImage: "pinned-image", UID: 1000, GID: 1000,
	})
	if err == nil || !strings.Contains(err.Error(), "read-only container root") {
		t.Fatalf("unqualified root-writable execution was accepted or silently changed to read-only: %v", err)
	}
}

func TestManagedMetadataExecutionRefusesBeforePreparationExists(t *testing.T) {
	for _, mount := range []blueprint.EnvironmentMount{
		{MountMetadata: blueprint.MountMetadata{Owner: blueprint.DefaultRuntimeUser}},
		{MountMetadata: blueprint.MountMetadata{Permissions: "0750"}},
		{Directories: map[string]blueprint.MountMetadata{"queue": {}}},
		{Files: map[string]blueprint.MountMetadata{"file": {Permissions: "0000"}}},
	} {
		mount.Target, mount.UpdatePolicy, mount.Writable = "/data", blueprint.UpdatePreserve, true
		document := blueprint.Document{
			Environment: blueprint.Environment{ID: "metadata", Mounts: map[string]blueprint.EnvironmentMount{"data": mount}},
			Docker: blueprint.Docker{Mounts: map[string]blueprint.DockerMount{"data": {
				Extends: "environment.mounts.data", Mode: blueprint.MountManagedBind, Source: "data", Contract: mount,
			}}},
		}
		root := t.TempDir()
		_, err := PlanDockerExecution(document, DockerPlanContext{
			DeploymentDir: root, Phase: blueprint.PhaseStaged, GeneratedImage: "pinned-image", UID: 1000, GID: 1000,
		})
		if err == nil || !strings.Contains(err.Error(), "requires qualified managed metadata preparation") {
			t.Fatalf("metadata was ignored instead of refusing execution: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
			t.Fatalf("refused metadata execution changed the managed root: %v", err)
		}
	}
}

func TestManagedMetadataContractWithoutExactReferenceRefusesBeforeEffects(t *testing.T) {
	mount := blueprint.EnvironmentMount{Target: "/data", UpdatePolicy: blueprint.UpdatePreserve, Writable: true}
	contract := mount
	contract.Permissions = "0700"
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "metadata", Mounts: map[string]blueprint.EnvironmentMount{"data": mount}},
		Docker: blueprint.Docker{Mounts: map[string]blueprint.DockerMount{"data": {
			Extends: "data", Mode: blueprint.MountManagedBind, Source: "data", Contract: contract,
		}}},
	}
	root := t.TempDir()
	_, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir: root, Phase: blueprint.PhaseStaged, GeneratedImage: "pinned-image", UID: 1000, GID: 1000,
	})
	if err == nil || !strings.Contains(err.Error(), "exact environment mount contract") {
		t.Fatalf("unmatched metadata was ignored instead of refusing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Fatalf("refused unmatched metadata changed the managed root: %v", err)
	}
}
