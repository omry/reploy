package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

func TestStagePythonWorkspaceSourceSnapshotsCopiesOnlyManifestEntries(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	workspace := t.TempDir()
	sourceDir := filepath.Join(workspace, "demo")
	if err := os.MkdirAll(filepath.Join(sourceDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(sourceDir, "tool.py")
	if err := os.WriteFile(program, []byte("print('original')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ".git", "index"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{"Demo_Pkg": "demo"},
	}}}
	sources, err := ResolvePythonWorkspaceSources(document, workspace)
	if err != nil {
		t.Fatal(err)
	}

	snapshots, err := StagePythonWorkspaceSourceSnapshots(prepared, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].Distribution != "demo-pkg" ||
		snapshots[0].ContainerDir != prepared.InputContainerDir+"/sources/demo-pkg" ||
		snapshots[0].SourceManifestDigest != sources[0].SourceManifestDigest {
		t.Fatalf("snapshots = %#v", snapshots)
	}
	snapshotProgram := filepath.Join(snapshots[0].HostDir, "tool.py")
	content, err := os.ReadFile(snapshotProgram)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "print('original')\n" {
		t.Fatalf("snapshot content = %q", content)
	}
	info, err := os.Stat(snapshotProgram)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("snapshot mode = %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(snapshots[0].HostDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("ignored directory entered snapshot: %v", err)
	}
	if err := os.WriteFile(program, []byte("print('changed')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(snapshotProgram)
	if err != nil || string(content) != "print('original')\n" {
		t.Fatalf("snapshot changed with live checkout: %q, %v", content, err)
	}
	inputInfo, err := os.Stat(prepared.InputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if inputInfo.Mode().Perm()&0o222 != 0 {
		t.Fatalf("resolver input remained writable: %o", inputInfo.Mode().Perm())
	}
}

func TestStagePythonWorkspaceSourceSnapshotsRejectsChangedSourceAndCleansPartialSnapshot(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	workspace := t.TempDir()
	sourceDir := filepath.Join(workspace, "demo")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(sourceDir, "pyproject.toml")
	if err := os.WriteFile(filename, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{"demo": "demo"},
	}}}
	sources, err := ResolvePythonWorkspaceSources(document, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := StagePythonWorkspaceSourceSnapshots(prepared, sources); err == nil || !strings.Contains(err.Error(), "content changed") {
		t.Fatalf("changed-source error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.InputHostDir, pythonSourceSnapshotDirectory)); !os.IsNotExist(err) {
		t.Fatalf("failed staging left a snapshot directory: %v", err)
	}
	inputInfo, err := os.Stat(prepared.InputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if inputInfo.Mode().Perm()&0o222 != 0 {
		t.Fatalf("failed staging left resolver input writable: %o", inputInfo.Mode().Perm())
	}
}

func TestStagePythonWorkspaceSourceSnapshotsRejectsInvalidDistributionBeforeCreatingSnapshotRoot(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, err = StagePythonWorkspaceSourceSnapshots(prepared, []PythonWorkspaceSource{{Distribution: "demo/pkg"}})
	if err == nil || !strings.Contains(err.Error(), "valid Python distribution name") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.InputHostDir, pythonSourceSnapshotDirectory)); !os.IsNotExist(err) {
		t.Fatalf("invalid distribution created snapshot root: %v", err)
	}
}
