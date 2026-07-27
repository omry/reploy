//go:build !windows

package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenProviderInstallManagedChildRejectsSymlinkSwap(t *testing.T) {
	sourceRoot := t.TempDir()
	victim := filepath.Join(sourceRoot, "payload")
	if err := os.WriteFile(victim, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	entries, err := parent.Readdir(-1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %#v, error = %v", entries, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("must not be copied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, victim); err != nil {
		t.Fatal(err)
	}

	child, _, err := openProviderInstallManagedChildV1(parent, entries[0], victim)
	if child != nil {
		_ = child.Close()
		t.Fatal("symlink swap returned an open child")
	}
	if err == nil || (!strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "changed while copying")) {
		t.Fatalf("symlink swap error = %v", err)
	}
}
