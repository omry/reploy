package dockerdeploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestStagedControlSurfaceSurvivesDirectoryMove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows staging wrapper is a PowerShell script")
	}
	runtimePath := writeFakeEmbeddedReploy(t, t.TempDir())
	originalExecutable := embeddedRuntimeExecutable
	embeddedRuntimeExecutable = func() (string, error) { return runtimePath, nil }
	t.Cleanup(func() { embeddedRuntimeExecutable = originalExecutable })

	root := t.TempDir()
	stagingDir := filepath.Join(root, "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", ControlScript: "democtl"},
	}
	changed, err := syncStagedControlSurfaceV1(stagingDir, document)
	if err != nil || !changed {
		t.Fatalf("initial sync: changed=%v err=%v", changed, err)
	}

	movedDir := filepath.Join(root, "moved")
	if err := os.Rename(stagingDir, movedDir); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(root, "args.txt")
	command := exec.Command(filepath.Join(movedDir, "democtl"), "help")
	command.Env = append(os.Environ(), "REPLOY_ARGS_FILE="+argsPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("moved control command: %v\n%s", err, output)
	}
	content, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"_control", "--dir", movedDir, "--script-name", "democtl", "help"}
	if got := strings.Fields(string(content)); !reflect.DeepEqual(got, want) {
		t.Fatalf("moved control arguments = %q, want %q", got, want)
	}
}

func TestStagedControlSurfaceRefreshesRuntimeAndRemovesRenamedWrapper(t *testing.T) {
	runtimePath := writeFakeEmbeddedReploy(t, t.TempDir())
	originalExecutable := embeddedRuntimeExecutable
	embeddedRuntimeExecutable = func() (string, error) { return runtimePath, nil }
	t.Cleanup(func() { embeddedRuntimeExecutable = originalExecutable })

	stagingDir := t.TempDir()
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", ControlScript: "democtl"},
	}
	if _, err := syncStagedControlSurfaceV1(stagingDir, document); err != nil {
		t.Fatal(err)
	}
	replacementRuntime := []byte("#!/bin/sh\nexit 19\n")
	if err := os.WriteFile(runtimePath, replacementRuntime, 0o755); err != nil {
		t.Fatal(err)
	}
	document.Environment.ControlScript = "newctl"
	changed, err := syncStagedControlSurfaceV1(stagingDir, document)
	if err != nil || !changed {
		t.Fatalf("refresh sync: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(filepath.Join(stagingDir, "democtl")); !os.IsNotExist(err) {
		t.Fatalf("old control wrapper remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stagingDir, "newctl")); err != nil {
		t.Fatalf("new control wrapper is missing: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(stagingDir, filepath.FromSlash(embeddedRuntimeFileName())))
	if err != nil || !reflect.DeepEqual(content, replacementRuntime) {
		t.Fatalf("staged runtime = %q, err=%v", content, err)
	}
}

func TestStagedControlSurfaceRefusesUnmanagedWrapperCollision(t *testing.T) {
	runtimePath := writeFakeEmbeddedReploy(t, t.TempDir())
	originalExecutable := embeddedRuntimeExecutable
	embeddedRuntimeExecutable = func() (string, error) { return runtimePath, nil }
	t.Cleanup(func() { embeddedRuntimeExecutable = originalExecutable })

	stagingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stagingDir, "democtl"), []byte("user content"), 0o755); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", ControlScript: "democtl"},
	}
	if _, err := planStagedControlSurfaceV1(stagingDir, document); err == nil ||
		!strings.Contains(err.Error(), "refusing to replace unmanaged") {
		t.Fatalf("collision error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(stagingDir, "democtl"))
	if err != nil || string(content) != "user content" {
		t.Fatalf("unmanaged wrapper changed: content=%q err=%v", content, err)
	}
}

func TestStagedControlSurfaceRejectsSymlinkedRuntimeDirectory(t *testing.T) {
	runtimePath := writeFakeEmbeddedReploy(t, t.TempDir())
	originalExecutable := embeddedRuntimeExecutable
	embeddedRuntimeExecutable = func() (string, error) { return runtimePath, nil }
	t.Cleanup(func() { embeddedRuntimeExecutable = originalExecutable })

	stagingDir := t.TempDir()
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", ControlScript: "democtl"},
	}
	if _, err := syncStagedControlSurfaceV1(stagingDir, document); err != nil {
		t.Fatal(err)
	}
	runtimeContent, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "reploy"), runtimeContent, 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(stagingDir, ".reploy", "bin")
	if err := os.Remove(filepath.Join(binDir, "reploy")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(binDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, binDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := syncStagedControlSurfaceV1(stagingDir, document); err == nil ||
		!strings.Contains(err.Error(), "ancestor must be a real directory") {
		t.Fatalf("symlinked runtime directory error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(outsideDir, "reploy"))
	if err != nil || !reflect.DeepEqual(content, runtimeContent) {
		t.Fatalf("external runtime changed: content=%q err=%v", content, err)
	}
}

func TestStagedControlSurfaceRefusesToRemoveModifiedManagedWrapper(t *testing.T) {
	runtimePath := writeFakeEmbeddedReploy(t, t.TempDir())
	originalExecutable := embeddedRuntimeExecutable
	embeddedRuntimeExecutable = func() (string, error) { return runtimePath, nil }
	t.Cleanup(func() { embeddedRuntimeExecutable = originalExecutable })

	stagingDir := t.TempDir()
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", ControlScript: "democtl"},
	}
	if _, err := syncStagedControlSurfaceV1(stagingDir, document); err != nil {
		t.Fatal(err)
	}
	modified := []byte("#!/bin/sh\nprintf 'user replacement\\n'\n")
	oldPath := filepath.Join(stagingDir, "democtl")
	if err := os.WriteFile(oldPath, modified, 0o755); err != nil {
		t.Fatal(err)
	}
	document.Environment.ControlScript = "newctl"
	if _, err := planStagedControlSurfaceV1(stagingDir, document); err == nil ||
		!strings.Contains(err.Error(), "modified; refusing to remove") {
		t.Fatalf("modified managed wrapper error = %v", err)
	}
	content, err := os.ReadFile(oldPath)
	if err != nil || !reflect.DeepEqual(content, modified) {
		t.Fatalf("modified managed wrapper changed: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(stagingDir, "newctl")); !os.IsNotExist(err) {
		t.Fatalf("replacement wrapper was created after rejected plan: %v", err)
	}
}

func TestStagedControlSurfaceAdoptsInstalledControlFiles(t *testing.T) {
	runtimePath := writeFakeEmbeddedReploy(t, t.TempDir())
	originalExecutable := embeddedRuntimeExecutable
	embeddedRuntimeExecutable = func() (string, error) { return runtimePath, nil }
	t.Cleanup(func() { embeddedRuntimeExecutable = originalExecutable })

	stagingDir := t.TempDir()
	document := blueprint.Document{
		Environment: blueprint.Environment{ID: "demo", ControlScript: "democtl"},
	}
	spec := controlScriptSpec{
		Mode: controlScriptModeDeployed, TargetDir: stagingDir, ControlScript: "democtl",
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "democtl"), []byte(renderControlScript(spec)), 0o755); err != nil {
		t.Fatal(err)
	}
	installedRuntime := filepath.Join(stagingDir, filepath.FromSlash(embeddedRuntimeFileName()))
	if err := os.MkdirAll(filepath.Dir(installedRuntime), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedRuntime, []byte("older installed runtime"), 0o755); err != nil {
		t.Fatal(err)
	}

	changed, err := syncStagedControlSurfaceV1(stagingDir, document)
	if err != nil || !changed {
		t.Fatalf("adopt installed control surface: changed=%v err=%v", changed, err)
	}
	wrapper, err := os.ReadFile(filepath.Join(stagingDir, "democtl"))
	if err != nil {
		t.Fatal(err)
	}
	spec.Mode = controlScriptModeStaged
	if want := renderControlScript(spec); string(wrapper) != want {
		t.Fatalf("adopted wrapper is not staged:\n%s", wrapper)
	}
	if _, found, err := readStagedControlManifestV1(stagingDir); err != nil || !found {
		t.Fatalf("adopted manifest: found=%v err=%v", found, err)
	}
}

func TestStagedPowerShellControlScriptUsesScriptDirectory(t *testing.T) {
	content := renderPowerShellControlScript(controlScriptSpec{
		Mode: controlScriptModeStaged, ControlScript: "democtl.ps1",
	})
	if !strings.Contains(content, "$TargetDir = $PSScriptRoot") {
		t.Fatalf("staged PowerShell wrapper is not relocatable:\n%s", content)
	}
}
