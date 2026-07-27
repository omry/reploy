package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestPublishCurrentRuntimeInputsV1WritesRepairsAndSkipsExactFiles(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	plan := currentRuntimeFilePlanV1()

	changed, err := PublishCurrentRuntimeInputsV1(operation, dir, plan)
	if err != nil || !changed {
		t.Fatalf("first publication = %v, %v", changed, err)
	}
	for _, relative := range []string{DockerEnvFileName, ComposeFileName} {
		info, err := os.Lstat(filepath.Join(dir, relative))
		if err != nil || !info.Mode().IsRegular() || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o644 {
			t.Fatalf("runtime input %q = %#v, %v", relative, info, err)
		}
	}
	changed, err = PublishCurrentRuntimeInputsV1(operation, dir, plan)
	if err != nil || changed {
		t.Fatalf("exact publication = %v, %v", changed, err)
	}

	environmentPath := filepath.Join(dir, DockerEnvFileName)
	if err := os.WriteFile(environmentPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(environmentPath, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err = PublishCurrentRuntimeInputsV1(operation, dir, plan)
	if err != nil || !changed {
		t.Fatalf("repair publication = %v, %v", changed, err)
	}
	info, err := os.Lstat(environmentPath)
	if err != nil || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o644 {
		t.Fatalf("repaired environment mode = %#v, %v", info, err)
	}
	content, err := os.ReadFile(environmentPath)
	if err != nil || string(content) == "stale\n" {
		t.Fatalf("repaired environment = %q, %v", content, err)
	}
}

func TestPublishCurrentRuntimeInputsV1RejectsForeignOrReleasedLock(t *testing.T) {
	dir := t.TempDir()
	foreign := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishCurrentRuntimeInputsV1(operation, dir, currentRuntimeFilePlanV1()); err == nil {
		t.Fatal("foreign operation lock was accepted")
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishCurrentRuntimeInputsV1(operation, foreign, currentRuntimeFilePlanV1()); err == nil {
		t.Fatal("released operation lock was accepted")
	}
}

func TestPublishCurrentRuntimeInputsV1RejectsSymlinkDestinationWithoutReplacement(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	target := filepath.Join(dir, "outside")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environmentPath := filepath.Join(dir, DockerEnvFileName)
	if err := os.Symlink(target, environmentPath); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishCurrentRuntimeInputsV1(operation, dir, currentRuntimeFilePlanV1()); err == nil {
		t.Fatal("runtime input symlink was replaced")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "keep\n" {
		t.Fatalf("symlink target = %q, %v", content, err)
	}
}

func TestRequireCurrentRuntimeInputsV1IsReadOnly(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	plan := currentRuntimeFilePlanV1()

	err = RequireCurrentRuntimeInputsV1(operation, dir, plan)
	if err == nil || !strings.Contains(err.Error(), "run `reploy up`") {
		t.Fatalf("missing runtime inputs error = %v", err)
	}
	for _, relative := range []string{DockerEnvFileName, ComposeFileName} {
		if _, err := os.Lstat(filepath.Join(dir, relative)); !os.IsNotExist(err) {
			t.Fatalf("missing runtime input %q was created: %v", relative, err)
		}
	}

	if changed, err := PublishCurrentRuntimeInputsV1(operation, dir, plan); err != nil || !changed {
		t.Fatalf("publish runtime inputs = %v, %v", changed, err)
	}
	if err := RequireCurrentRuntimeInputsV1(operation, dir, plan); err != nil {
		t.Fatalf("require exact runtime inputs: %v", err)
	}

	environmentPath := filepath.Join(dir, DockerEnvFileName)
	if err := os.WriteFile(environmentPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(environmentPath, 0o600); err != nil {
		t.Fatal(err)
	}
	err = RequireCurrentRuntimeInputsV1(operation, dir, plan)
	if err == nil || !strings.Contains(err.Error(), "run `reploy up`") {
		t.Fatalf("stale runtime inputs error = %v", err)
	}
	content, err := os.ReadFile(environmentPath)
	if err != nil || string(content) != "stale\n" {
		t.Fatalf("stale runtime input was replaced: %q, %v", content, err)
	}
	info, err := os.Lstat(environmentPath)
	if err != nil || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o600 {
		t.Fatalf("stale runtime input mode was repaired: %#v, %v", info, err)
	}
}

func currentRuntimeFilePlanV1() CurrentRuntimePlanV1 {
	return CurrentRuntimePlanV1{
		Document: blueprint.Document{Environment: blueprint.Environment{ControlScript: "demo"}},
		Docker: DockerExecutionPlan{
			EnvironmentID: "demo", Phase: blueprint.PhaseStaged,
			Image: "reploy/env/demo-deadbeef:g-current", ContainerName: "demo-staging", NetworkName: "demo-staging",
			RuntimeUser: RuntimeUserPlan{DockerUser: "1000:1000"}, TemporaryHome: environmentTemporaryHome,
		},
	}
}
