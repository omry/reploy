package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/probearchive"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPrepareProbeWorkspaceUsesDeploymentStoreAndExactMount(t *testing.T) {
	deploymentRoot := t.TempDir()
	store, err := providerstore.NewStore(deploymentRoot)
	if err != nil {
		t.Fatal(err)
	}
	executable := packedProbeExecutable(t)
	previous := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return executable, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previous })
	platform, err := blueprint.ParsePlatform("linux/arm/v7")
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareProbeWorkspace(context.Background(), store, platform)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ContainerDir != ProbeContainerRoot || prepared.ContainerExecutable != ProbeContainerExecutable || !prepared.ReadOnly || prepared.Platform != platform || prepared.SHA256 == "" {
		t.Fatalf("prepared workspace = %#v", prepared)
	}
	wantParent := filepath.Join(store.Root(), "tmp") + string(filepath.Separator)
	if !strings.HasPrefix(prepared.HostDir, wantParent) || filepath.Dir(prepared.HostExecutable) != prepared.HostDir || filepath.Base(prepared.HostExecutable) != probearchive.ExtractedFileName {
		t.Fatalf("host paths = %q, %q; want beneath %q", prepared.HostDir, prepared.HostExecutable, wantParent)
	}
	entries, err := os.ReadDir(prepared.HostDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != probearchive.ExtractedFileName {
		t.Fatalf("probe workspace entries = %#v", entries)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("repeat cleanup: %v", err)
	}
	if _, err := os.Lstat(prepared.HostDir); !os.IsNotExist(err) {
		t.Fatalf("probe workspace remains after cleanup: %v", err)
	}
	if _, err := os.Stat(deploymentRoot); err != nil {
		t.Fatalf("cleanup removed deployment root: %v", err)
	}
}

func TestPrepareProbeWorkspaceRejectsUnsupportedPlatformBeforeCreatingWorkspace(t *testing.T) {
	deploymentRoot := t.TempDir()
	store, err := providerstore.NewStore(deploymentRoot)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := blueprint.ParsePlatform("linux/riscv64")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareProbeWorkspace(context.Background(), store, platform); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("unsupported platform error = %v", err)
	}
	if _, err := os.Lstat(store.Root()); !os.IsNotExist(err) {
		t.Fatalf("unsupported platform created provider storage: %v", err)
	}
}

func TestPrepareProbeWorkspaceRemovesFailedExtraction(t *testing.T) {
	deploymentRoot := t.TempDir()
	store, err := providerstore.NewStore(deploymentRoot)
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "plain-reploy")
	if err := os.WriteFile(plain, []byte("no archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return plain, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previous })
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareProbeWorkspace(context.Background(), store, platform); err == nil {
		t.Fatal("missing probe archive was accepted")
	}
	workspaces, err := filepath.Glob(filepath.Join(store.Root(), "tmp", "probe-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("failed extraction retained workspaces: %q", workspaces)
	}
}

func packedProbeExecutable(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	executable := writeProbeTestFile(t, dir, "reploy", "prefix")
	inputs := []probearchive.HelperInput{
		{Platform: "linux/amd64", Path: writeProbeTestFile(t, dir, "amd64", "amd64")},
		{Platform: "linux/arm/v7", Path: writeProbeTestFile(t, dir, "arm-v7", "arm-v7")},
		{Platform: "linux/arm64", Path: writeProbeTestFile(t, dir, "arm64", "arm64")},
	}
	controllers := []probearchive.ControllerInput{
		{Platform: "linux/amd64", Path: inputs[0].Path},
		{Platform: "linux/arm64", Path: inputs[2].Path},
	}
	if err := probearchive.Append(executable, probearchive.ReleaseV1{Version: "test"}, inputs, controllers); err != nil {
		t.Fatal(err)
	}
	return executable
}

func writeProbeTestFile(t *testing.T, dir string, name string, content string) string {
	t.Helper()
	filePath := filepath.Join(dir, name)
	if err := os.WriteFile(filePath, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return filePath
}
