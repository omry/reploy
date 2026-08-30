package providerstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveMaterializationDestinationStaysBoundAfterPathReplacement(t *testing.T) {
	parent := t.TempDir()
	destinationPath := filepath.Join(parent, "destination")
	movedDestinationPath := filepath.Join(parent, "moved-destination")
	replacementTarget := filepath.Join(parent, "replacement-target")
	if err := os.Mkdir(destinationPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(replacementTarget, 0o700); err != nil {
		t.Fatal(err)
	}

	destination, err := openArchiveMaterializationDestination(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.close()
	if err := os.Rename(destinationPath, movedDestinationPath); err != nil {
		t.Skipf("renaming an opened directory is unavailable: %v", err)
	}
	if err := os.Symlink(replacementTarget, destinationPath); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	workspace, err := destination.createWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.cleanup()
	if err := workspace.root.WriteFile("marker", []byte("bound"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := destination.publish(workspace, "install"); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(movedDestinationPath, "install", "marker"))
	if err != nil || string(content) != "bound" {
		t.Fatalf("original destination content = %q, err = %v", content, err)
	}
	if _, err := os.Lstat(filepath.Join(replacementTarget, "install")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("replacement target received publication, err = %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(replacementTarget, archiveMaterializationWorkspacePattern))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("replacement target received workspaces: %v", matches)
	}
}

func TestArchiveMaterializationWorkspaceCleanupStaysBoundAfterPathReplacement(t *testing.T) {
	parent := t.TempDir()
	destinationPath := filepath.Join(parent, "destination")
	movedDestinationPath := filepath.Join(parent, "moved-destination")
	replacementTarget := filepath.Join(parent, "replacement-target")
	if err := os.Mkdir(destinationPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(replacementTarget, 0o700); err != nil {
		t.Fatal(err)
	}

	destination, err := openArchiveMaterializationDestination(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.close()
	workspace, err := destination.createWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	workspaceName := workspace.name
	if err := workspace.root.WriteFile("marker", []byte("staged"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(destinationPath, movedDestinationPath); err != nil {
		workspace.cleanup()
		t.Skipf("renaming an opened directory is unavailable: %v", err)
	}
	if err := os.Symlink(replacementTarget, destinationPath); err != nil {
		workspace.cleanup()
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	workspace.cleanup()
	if _, err := os.Lstat(filepath.Join(movedDestinationPath, workspaceName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("original destination workspace remains, err = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(replacementTarget, workspaceName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("replacement target was modified, err = %v", err)
	}
}
