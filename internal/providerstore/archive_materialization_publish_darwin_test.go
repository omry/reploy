//go:build darwin

package providerstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishArchiveMaterializedDirectoryDarwinRestoresAndPublishesRelativeToOpenedParent(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "marker"), []byte("published"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stage, 0o555); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination")
	t.Cleanup(func() { _ = os.Chmod(destination, 0o700) })

	if err := publishArchiveMaterializedDirectoryForTest(stage, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage still exists after publication: %v", err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o555 {
		t.Fatalf("published directory mode = %o, want 555", mode)
	}
	content, err := os.ReadFile(filepath.Join(destination, "marker"))
	if err != nil || string(content) != "published" {
		t.Fatalf("published marker = %q, %v", content, err)
	}
}
