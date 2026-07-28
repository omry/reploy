package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAtomicStateFileReturnsDirectorySyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	originalSync := syncAtomicStateFileDirectory
	syncAtomicStateFileDirectory = func(string) error {
		return errors.New("injected directory sync failure")
	}
	t.Cleanup(func() {
		syncAtomicStateFileDirectory = originalSync
	})

	err := writeAtomicStateFile(path, []byte("replacement"), 0o600)
	if err == nil || !strings.Contains(err.Error(), "injected directory sync failure") {
		t.Fatalf("writeAtomicStateFile() error = %v", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "replacement" {
		t.Fatalf("published content = %q", content)
	}
	temporary, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), ".state-*.tmp"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files remain: %#v", temporary)
	}
}
