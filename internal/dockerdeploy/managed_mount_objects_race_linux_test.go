//go:build linux

package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"golang.org/x/sys/unix"
)

func TestManagedMountObjectsPinnedRootSurvivesNameSubstitution(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	base := t.TempDir()
	root, held, foreign := filepath.Join(base, "root"), filepath.Join(base, "held"), filepath.Join(base, "foreign")
	for _, name := range []string{root, foreign} {
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fd := managedObjectTestFD(t, root)
	if err := os.Rename(root, held); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, root); err != nil {
		t.Fatal(err)
	}
	objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("new", false, managedObjectTestMode(0o640))}
	result, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd})
	if err != nil {
		t.Fatal(err)
	}
	closePreparedManagedObjects(t, result)
	if _, err := os.Stat(filepath.Join(held, "new")); err != nil {
		t.Fatalf("held root not used: %v", err)
	}
	entries, err := os.ReadDir(foreign)
	if err != nil || len(entries) != 0 {
		t.Fatalf("foreign replacement was changed: %v %v", entries, err)
	}
}

func TestManagedMountObjectsPinnedLeafSurvivesNameSubstitution(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	root, foreign := t.TempDir(), t.TempDir()
	leaf, canary := filepath.Join(root, "file"), filepath.Join(foreign, "canary")
	if err := os.WriteFile(canary, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	fd := managedObjectTestFD(t, root)
	objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("file", false, nil)}
	result, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd})
	if err != nil {
		t.Fatal(err)
	}
	defer closePreparedManagedObjects(t, result)
	if err := os.Rename(leaf, filepath.Join(root, "retained")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canary, leaf); err != nil {
		t.Fatal(err)
	}
	// The returned FD is the created inode, not a later lookup of its name.
	if _, err := unix.Write(result["file"].FD, []byte("held inode")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(canary)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("foreign target changed: %q %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(root, "retained"))
	if err != nil || string(data) != "held inode" {
		t.Fatalf("returned descriptor lost its inode: %q %v", data, err)
	}
	recorded := map[string]int{".": fd, "file": result["file"].FD}
	if retry, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, recorded); err == nil || retry != nil {
		t.Fatalf("later preparation accepted substituted leaf: %v %v", retry, err)
	}
}

func TestManagedMountObjectsParentReplacementRaceCannotWriteForeignTree(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	root, foreign := t.TempDir(), t.TempDir()
	parent, held := filepath.Join(root, "parent"), filepath.Join(root, "held")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(foreign, "canary")
	if err := os.WriteFile(canary, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootFD, parentFD := managedObjectTestFD(t, root), managedObjectTestFD(t, parent)
	recorded := map[string]int{".": rootFD, "parent": parentFD}
	ready, stop, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(finished)
		defer once.Do(func() { close(ready) })
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := os.Rename(parent, held); err != nil {
				t.Error(err)
				return
			}
			if err := os.Symlink(foreign, parent); err != nil {
				t.Error(err)
				return
			}
			once.Do(func() { close(ready) })
			if err := os.Remove(parent); err != nil {
				t.Error(err)
				return
			}
			if err := os.Rename(held, parent); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	<-ready
	for i := 0; i < 100; i++ {
		objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration(fmt.Sprintf("parent/file-%03d", i), false, managedObjectTestMode(0o600))}
		result, err := prepareManagedMountObjectsV1(context.Background(), rootFD, stagingFD, objects, recorded)
		if err == nil {
			closePreparedManagedObjects(t, result)
		} else if result != nil {
			t.Error("failure returned partial success")
		}
	}
	close(stop)
	<-finished
	entries, err := os.ReadDir(foreign)
	if err != nil || len(entries) != 1 || entries[0].Name() != "canary" {
		t.Fatalf("preparation escaped to foreign tree: %v %v", entries, err)
	}
	data, err := os.ReadFile(canary)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("foreign canary changed: %q %v", data, err)
	}
}

func TestManagedMountObjectsCreationHonorsUmask(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	if os.Getenv("REPLOY_TEST_MANAGED_OBJECT_UMASK") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestManagedMountObjectsCreationHonorsUmask$")
		command.Env = append(os.Environ(), "REPLOY_TEST_MANAGED_OBJECT_UMASK=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated umask proof: %v\n%s", err, output)
		}
		return
	}
	// Isolate process-wide umask from all other tests and the invoking agent.
	unix.Umask(0o077)
	root := t.TempDir()
	fd := managedObjectTestFD(t, root)
	objects := []blueprint.ManagedMountObject{
		managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("dir", true, nil),
		managedObjectTestDeclaration("dir/file", false, nil), managedObjectTestDeclaration("explicit", false, managedObjectTestMode(0o640)),
	}
	result, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd})
	if err != nil {
		t.Fatal(err)
	}
	defer closePreparedManagedObjects(t, result)
	for name, mode := range map[string]uint32{"dir": 0o700, "dir/file": 0o600, "explicit": 0o640} {
		if got := result[name].Identity.Mode & 0o7777; got != mode {
			t.Errorf("%s: mode %#o, want %#o", name, got, mode)
		}
	}
}

func TestManagedMountObjectsDirectoryReplacementCannotChangeForeignMetadata(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	root, outside, staging := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Chmod(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(outside, "foreign")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "canary"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	rfd, ofd := managedObjectTestFD(t, root), managedObjectTestFD(t, outside)
	ffd, sfd := managedObjectTestFD(t, foreign), managedObjectTestFD(t, staging)
	deadline := time.Now().Add(5 * time.Second)
	exchanges := 0
	for i := 0; i < 2000 && time.Now().Before(deadline); i++ {
		name := fmt.Sprintf("new-%d", i)
		var stop atomic.Bool
		ready, done := make(chan struct{}), make(chan error, 1)
		go func() {
			close(ready)
			for !stop.Load() {
				err := unix.Renameat2(rfd, name, ofd, "foreign", unix.RENAME_EXCHANGE)
				if err == nil || err != unix.ENOENT {
					done <- err
					return
				}
				runtime.Gosched()
			}
			done <- unix.ENOENT
		}()
		<-ready
		objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration(name, true, managedObjectTestMode(0o711))}
		result, prepareErr := prepareManagedMountObjectsV1(context.Background(), rfd, sfd, objects, map[string]int{".": rfd})
		stop.Store(true)
		exchangeErr := <-done
		closePreparedManagedObjects(t, result)
		var actual unix.Stat_t
		if err := unix.Fstat(ffd, &actual); err != nil {
			t.Fatal(err)
		}
		if exchangeErr == nil {
			exchanges++
			if err := unix.Renameat2(rfd, name, ofd, "foreign", unix.RENAME_EXCHANGE); err != nil {
				t.Fatal(err)
			}
		} else if exchangeErr != unix.ENOENT {
			t.Fatal(exchangeErr)
		}
		if actual.Mode&0o7777 != 0o700 {
			t.Fatalf("foreign metadata changed at iteration %d: mode %#o, preparation error %v", i, actual.Mode&0o7777, prepareErr)
		}
		if err := unix.Unlinkat(rfd, name, unix.AT_REMOVEDIR); err != nil && err != unix.ENOENT {
			t.Fatal(err)
		}
	}
	if exchanges == 0 {
		t.Fatal("replacement control never exchanged a published directory")
	}
	data, err := os.ReadFile(filepath.Join(foreign, "canary"))
	if err != nil || string(data) != "retained" {
		t.Fatalf("foreign contents changed: %q %v", data, err)
	}
}
