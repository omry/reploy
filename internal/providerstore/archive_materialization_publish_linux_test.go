//go:build linux

package providerstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishArchiveMaterializedDirectoryPublishesAcrossDistinctParents(t *testing.T) {
	root := t.TempDir()
	stageParent := filepath.Join(root, "stages")
	destinationParent := filepath.Join(root, "destinations")
	if err := os.Mkdir(stageParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destinationParent, 0o700); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(stageParent, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "marker"), []byte("published"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(destinationParent, "destination")

	if err := publishArchiveMaterializedDirectory(stage, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage still exists after publication: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "marker"))
	if err != nil || string(content) != "published" {
		t.Fatalf("published marker = %q, %v", content, err)
	}
}
