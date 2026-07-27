package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestPrepareStagedManagedBindPathsV1CreatesOnlyMissingManagedDirectories(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := DockerExecutionPlan{Phase: blueprint.PhaseStaged, Mounts: []MountExecutionPlan{
		{Name: "config", Mode: blueprint.MountManagedBind, Source: filepath.Join(root, "state", "conf")},
		{Name: "data", Mode: blueprint.MountManagedBind, Source: existing},
		{Name: "external", Mode: blueprint.MountBind, Source: filepath.Join(root, "ignored")},
	}}
	if err := PrepareStagedManagedBindPathsV1(root, plan); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "state"), filepath.Join(root, "state", "conf")} {
		if info, err := os.Lstat(path); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("managed directory %q = %#v, error = %v", path, info, err)
		}
	}
	if info, err := os.Stat(existing); err != nil || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o700 {
		t.Fatalf("existing directory changed: %#v, error = %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "ignored")); !os.IsNotExist(err) {
		t.Fatalf("unmanaged bind source was created: %v", err)
	}
}

func TestPrepareStagedManagedBindPathsV1RejectsUnsafeExistingComponent(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := DockerExecutionPlan{Phase: blueprint.PhaseStaged, Mounts: []MountExecutionPlan{{
		Name: "config", Mode: blueprint.MountManagedBind, Source: filepath.Join(blocked, "conf"),
	}}}
	err := PrepareStagedManagedBindPathsV1(root, plan)
	if err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("unsafe managed path error = %v", err)
	}
}
