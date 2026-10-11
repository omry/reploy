package dockerdeploy

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDestinationPreflightRejectsSpecialStateWithoutBlocking(t *testing.T) {
	destination := t.TempDir()
	if err := os.Mkdir(filepath.Join(destination, ".reploy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(destination, StateFileName), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preflightProviderInstallDestinationV1(destination, ""); err == nil {
		t.Fatal("FIFO state accepted")
	}
	root, err := os.OpenRoot(filepath.Join(destination, ".reploy"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// The descriptor open is also nonblocking if replacement happens after Lstat.
	file, err := openProviderPreflightStateV1(root)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().IsRegular() {
		t.Fatal("FIFO unexpectedly regular")
	}
}
