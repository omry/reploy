//go:build linux

package fsowner

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPrivilegedOwnerRefusesExternalLinksAndSpecialFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root in disposable mapped namespace")
	}
	base, err := os.MkdirTemp("", "reploy-hf-links-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	operator := filepath.Join(base, "operator")
	if err := os.Mkdir(operator, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(operator, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	owner, err := Directory(operator)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(base, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(base, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"symlink": "symlink", "hardlink": "multiply linked", "fifo": "special"} {
		if err := owner.At(root, path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", path, err)
		}
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != 0 || stat.Gid != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("external file ownership or mode changed: %v", info)
	}
	if _, err := Directory(filepath.Join(base, "symlink")); err == nil {
		t.Fatal("accepted a link as ownership authority")
	}
}

func TestPrivilegedOwnerTreeSkipsSymlinkTargets(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires mapped root")
	}
	base, err := os.MkdirTemp("", "reploy-hf-tree-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	operator := filepath.Join(base, "operator")
	scratch := filepath.Join(base, "scratch")
	for _, path := range []string{operator, scratch} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(operator, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "data")
	if err := os.WriteFile(outside, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(scratch, "link")); err != nil {
		t.Fatal(err)
	}
	owner, err := Directory(operator)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := owner.Tree(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Fatal("external application data changed owner")
	}
	info, err = os.Stat(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if info.Sys().(*syscall.Stat_t).Uid != 1000 || info.Mode().Perm() != 0o700 {
		t.Fatal("private scratch was not transferred intact")
	}
}

func TestPrivilegedOwnerTreeRestoresSafeEntriesAfterRefusal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires mapped root")
	}
	base := t.TempDir()
	operator := filepath.Join(base, "operator")
	scratch := filepath.Join(base, "scratch")
	for _, path := range []string{operator, scratch} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(operator, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	diagnostic := filepath.Join(scratch, "a-diagnostic")
	outside := filepath.Join(base, "outside")
	for _, path := range []string{diagnostic, outside} {
		if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Reverse traversal encounters both refused entries before the safe file.
	if err := os.Link(outside, filepath.Join(scratch, "z-shared")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(scratch, "y-fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner, err := Directory(operator)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = owner.Tree(root)
	if err == nil || !strings.Contains(err.Error(), "multiply linked") || !strings.Contains(err.Error(), "special") {
		t.Fatalf("expected both ownership refusals, got %v", err)
	}
	for _, path := range []string{scratch, diagnostic, outside} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		wantUID, wantMode := uint32(1000), os.FileMode(0o600)
		if path == scratch {
			wantMode = 0o700
		} else if path == outside {
			wantUID = 0
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != wantUID || stat.Gid != wantUID || info.Mode().Perm() != wantMode {
			t.Errorf("%s: uid/gid=%d/%d mode=%o; want %d/%d %o", path, stat.Uid, stat.Gid, info.Mode().Perm(), wantUID, wantUID, wantMode)
		}
	}
}
