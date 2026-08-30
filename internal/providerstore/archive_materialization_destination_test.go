package providerstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveMaterializationDestinationRejectsReplacedResultPath(t *testing.T) {
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
		t.Fatalf("handle-bound publication content = %q, err = %v", content, err)
	}
	if err := destination.validatePublished(workspace, "install"); err == nil {
		t.Fatal("replaced destination-root path retained a successful result")
	}
	if _, err := os.Lstat(filepath.Join(movedDestinationPath, "install")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("original destination retained rolled-back publication, err = %v", err)
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

func TestArchiveMaterializationPublishErrorRollsBackPublishedDirectory(t *testing.T) {
	destinationPath := t.TempDir()
	destination, err := openArchiveMaterializationDestination(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.close()

	workspace, err := destination.createWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.cleanup()
	if err := workspace.root.WriteFile("marker", []byte("published"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := destination.root.Rename(workspace.name, "install"); err != nil {
		t.Fatal(err)
	}

	publishErr := errors.New("sync destination root after publication")
	if err := destination.handlePublicationResult(workspace, "install", true, publishErr); !errors.Is(err, publishErr) {
		t.Fatalf("publication error = %v, want %v", err, publishErr)
	}
	if _, err := destination.root.Lstat("install"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("published directory remains after rollback, err = %v", err)
	}
	if workspace.root != nil {
		t.Fatal("published workspace root remains open after rollback")
	}
}

func TestArchiveMaterializationDestinationRejectsReplacedPublishedName(t *testing.T) {
	destinationPath := t.TempDir()
	destination, err := openArchiveMaterializationDestination(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.close()

	workspace, err := destination.createWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.cleanup()
	if err := workspace.root.WriteFile("marker", []byte("published"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := destination.publish(workspace, "install"); err != nil {
		t.Fatal(err)
	}
	if err := destination.root.Rename("install", "moved-install"); err != nil {
		t.Fatal(err)
	}
	if err := destination.root.Mkdir("install", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := destination.root.WriteFile("install/replacement", []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := destination.validatePublished(workspace, "install"); err == nil {
		t.Fatal("replaced published destination unexpectedly validated")
	}
	if _, err := destination.root.Lstat("install/replacement"); err != nil {
		t.Fatalf("replacement destination was removed: %v", err)
	}
	if _, err := destination.root.Lstat("moved-install/marker"); err != nil {
		t.Fatalf("published directory was removed: %v", err)
	}
}

func TestArchiveMaterializationPublishErrorPreservesReplacedPublishedName(t *testing.T) {
	destinationPath := t.TempDir()
	destination, err := openArchiveMaterializationDestination(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.close()

	workspace, err := destination.createWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.cleanup()
	if err := workspace.root.WriteFile("marker", []byte("published"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := destination.root.Rename(workspace.name, "install"); err != nil {
		t.Fatal(err)
	}
	if err := destination.root.Rename("install", "moved-install"); err != nil {
		t.Fatal(err)
	}
	if err := destination.root.Mkdir("install", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := destination.root.WriteFile("install/replacement", []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	publishErr := errors.New("sync destination root after publication")
	if err := destination.handlePublicationResult(workspace, "install", true, publishErr); !errors.Is(err, publishErr) {
		t.Fatalf("publication error = %v, want %v", err, publishErr)
	}
	if _, err := destination.root.Lstat("install/replacement"); err != nil {
		t.Fatalf("replacement destination was removed: %v", err)
	}
	if _, err := destination.root.Lstat("moved-install/marker"); err != nil {
		t.Fatalf("published directory was removed: %v", err)
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
