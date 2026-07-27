package probe

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCopyVolumeTreeRejectsSpecialFiles(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	fifo := filepath.Join(source, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyVolumeTree(source, target); err == nil || !strings.Contains(err.Error(), "unsupported source entry type") {
		t.Fatalf("special-file error = %v", err)
	}
}
