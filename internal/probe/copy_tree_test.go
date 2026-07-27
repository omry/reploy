//go:build linux

package probe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyVolumeTreeCopiesRegularDirectoriesLinksAndHardlinks(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	rootTime := time.Unix(1_700_000_000, 0)
	if err := os.Chmod(source, 0o750); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(source, "nested")
	if err := os.Mkdir(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(nested, "first")
	if err := os.WriteFile(first, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, filepath.Join(nested, "second")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("first", filepath.Join(nested, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, rootTime, rootTime); err != nil {
		t.Fatal(err)
	}
	if err := copyVolumeTree(source, target); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(target, "nested", "current"))
	if err != nil || string(content) != "payload" {
		t.Fatalf("copied content = %q, error = %v", content, err)
	}
	firstInfo, err := os.Stat(filepath.Join(target, "nested", "first"))
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(filepath.Join(target, "nested", "second"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("hard-linked source files were copied as independent files")
	}
	link, err := os.Readlink(filepath.Join(target, "nested", "current"))
	if err != nil || link != "first" {
		t.Fatalf("copied link = %q, error = %v", link, err)
	}
	if firstInfo.Mode().Perm() != 0o640 {
		t.Fatalf("copied mode = %04o", firstInfo.Mode().Perm())
	}
	targetRootInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if targetRootInfo.Mode().Perm() != 0o750 || !targetRootInfo.ModTime().Equal(rootTime) {
		t.Fatalf("copied root metadata = mode %04o, mtime %s", targetRootInfo.Mode().Perm(), targetRootInfo.ModTime())
	}
}

func TestCopyVolumeTreeRequiresEmptyRealTarget(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "existing"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyVolumeTree(source, target); err == nil {
		t.Fatal("nonempty target accepted")
	}
	if err := os.Remove(filepath.Join(target, "existing")); err != nil {
		t.Fatal(err)
	}
	if err := copyVolumeTree(source, target); err != nil {
		t.Fatal(err)
	}
}
