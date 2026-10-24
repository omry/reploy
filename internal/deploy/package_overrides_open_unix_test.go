//go:build !windows

package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadPackageOverridesFileRejectsSpecialFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "dangling-symlink", "fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			selected := filepath.Join(dir, "selected.yaml")
			var err error
			switch kind {
			case "symlink", "dangling-symlink":
				target := filepath.Join(dir, "target.yaml")
				if kind == "symlink" {
					err = os.WriteFile(target, overrideReadFixture(t, "target"), 0o600)
					if err != nil {
						t.Fatal(err)
					}
				}
				err = os.Symlink(target, selected)
			case "fifo":
				err = unix.Mkfifo(selected, 0o600)
			case "directory":
				err = os.Mkdir(selected, 0o700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := ReadPackageOverridesFileV1(selected); err == nil || found {
				t.Fatalf("special file accepted: found=%v, err=%v", found, err)
			}
		})
	}
}

func TestReadPackageOverridesFileNeverFollowsReplacement(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	dir := t.TempDir()
	selected := filepath.Join(dir, "selected.yaml")
	other := filepath.Join(dir, "other")
	regular := filepath.Join(dir, "regular.yaml")
	target := filepath.Join(dir, "target.yaml")
	if err := os.WriteFile(selected, overrideReadFixture(t, "selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, overrideReadFixture(t, "forbidden-target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(selected, regular); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, other); err != nil {
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
			// Each replacement is atomic. Neither regular file's contents change.
			for _, replace := range []func() error{
				func() error { return os.Rename(other, selected) },
				func() error { return os.Link(regular, other) },
				func() error { return os.Rename(other, selected) },
				func() error { return os.Symlink(target, other) },
			} {
				if err := replace(); err != nil {
					done <- err
					return
				}
			}
		}
	}()
	defer func() {
		close(stop)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}()
	reads := 0
	for range 16384 {
		overrides, found, err := ReadPackageOverridesFileV1(selected)
		if err != nil || !found {
			continue // Rejected symlink entries are valid race outcomes.
		}
		reads++
		if overrides.Environment.ID != "selected" {
			t.Fatalf("followed a replacement symlink: environment=%q", overrides.Environment.ID)
		}
	}
	if reads == 0 {
		t.Fatal("race test did not exercise a successful regular-file read")
	}
}

func overrideReadFixture(t *testing.T, id string) []byte {
	t.Helper()
	content, err := EncodePackageOverridesV1(EmptyPackageOverridesV1(id))
	if err != nil {
		t.Fatal(err)
	}
	return content
}
