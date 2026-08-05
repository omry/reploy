package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestValidateLifecycleRuntimeHostSourcesV1EnforcesRootBindPolicy(t *testing.T) {
	root := t.TempDir()
	plan := DockerExecutionPlan{
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 0, GID: 0, DockerUser: "0:0"}),
		Mounts: []MountExecutionPlan{{
			Name: "config", Mode: blueprint.MountBind, Source: root,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/mnt/config", ReadOnly: true,
		}},
	}
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
		Destination: "/mnt/config", SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true,
	}})
	policy.Plans[0].ID = "command/check"
	if err := validateLifecycleRuntimeHostSourcesV1(policy, plan, "check"); err == nil || !strings.Contains(err.Error(), "root application runtime") {
		t.Fatalf("root lifecycle bind error = %v", err)
	}

	plan.Sandbox = newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 1000, GID: 1000, DockerUser: "1000:1000"})
	if err := validateLifecycleRuntimeHostSourcesV1(policy, plan, "check"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLifecycleRuntimeHostSourcesV1ChecksHostKind(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "config")
	if err := os.WriteFile(file, []byte("value=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := DockerExecutionPlan{
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 1000, GID: 1000, DockerUser: "1000:1000"}),
		Mounts: []MountExecutionPlan{{
			Name: "config", Mode: blueprint.MountBind, Source: file,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/mnt/config", ReadOnly: true,
		}},
	}
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
		Destination: "/mnt/config", SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true,
	}})
	policy.Plans[0].ID = "command/check"
	if err := validateLifecycleRuntimeHostSourcesV1(policy, plan, "check"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("lifecycle host kind error = %v", err)
	}
}
