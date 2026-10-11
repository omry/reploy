//go:build linux

package deploy

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivilegedOperationLockRejectsSwappedSymlink(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires disposable mapped root")
	}
	deployment := t.TempDir()
	state := filepath.Join(deployment, ".reploy")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{deployment, state} {
		if err := os.Chown(path, 1000, 1000); err != nil {
			t.Fatal(err)
		}
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(state, "operation.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	swap := filepath.Join(state, "swap")
	if err := os.Symlink(victim, swap); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if err := unix.Renameat2(unix.AT_FDCWD, lockPath, unix.AT_FDCWD, swap, unix.RENAME_EXCHANGE); err != nil {
				done <- err
				return
			}
		}
	}()
	for i := 0; i < 2000; i++ {
		lock, err := AcquireOperationLock(context.Background(), deployment)
		if err == nil {
			_ = lock.Unlock()
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != 0 || stat.Gid != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("external victim was changed: %+v", stat)
	}
	content, err := os.ReadFile(victim)
	if err != nil || string(content) != "private" {
		t.Fatalf("victim content = %q: %v", content, err)
	}
}

func TestPrivilegedOperationLockOpeningRemainsAtHeldDirectory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires disposable mapped root")
	}
	parent := t.TempDir()
	state := filepath.Join(parent, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(state, moved); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	victim := filepath.Join(outside, "operation.lock")
	if err := os.WriteFile(victim, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, state); err != nil {
		t.Fatal(err)
	}
	file, err := openOperationLockFile(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Chown(1000, 1000); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]uint32{victim: 0, filepath.Join(moved, "operation.lock"): 1000} {
		info, err := os.Stat(path)
		if err != nil || info.Sys().(*syscall.Stat_t).Uid != want {
			t.Fatalf("%s changed unexpectedly: %v, %v", path, info, err)
		}
	}
}
