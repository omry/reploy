//go:build linux

package dockerdeploy

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"golang.org/x/sys/unix"
)

type managedObjectCancelAtCompletion struct {
	context.Context
	checks int
}

func (ctx *managedObjectCancelAtCompletion) Err() error {
	ctx.checks++
	if ctx.checks >= 3 {
		return context.Canceled
	}
	return nil
}

func managedObjectTestFD(t *testing.T, name string) int {
	t.Helper()
	fd, err := unix.Open(name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	return fd
}

func managedObjectTestStagingFD(t *testing.T) int {
	t.Helper()
	name := t.TempDir()
	if err := os.Chmod(name, 0o700); err != nil {
		t.Fatal(err)
	}
	return managedObjectTestFD(t, name)
}

func managedObjectTestDeclaration(name string, directory bool, mode *uint32) blueprint.ManagedMountObject {
	return blueprint.ManagedMountObject{Path: name, Directory: directory, Identity: blueprint.MountMetadataIdentity{
		UID: uint32(os.Geteuid()), GID: uint32(os.Getegid()), Permissions: mode,
	}}
}

func managedObjectTestMode(mode uint32) *uint32 { return &mode }

func closePreparedManagedObjects(t *testing.T, objects map[string]preparedManagedMountObjectV1) {
	t.Helper()
	for name, object := range objects {
		if err := unix.Close(object.FD); err != nil {
			t.Errorf("close %s: %v", name, err)
		}
	}
}

func TestManagedMountObjectsCreateAndVerify(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	root := t.TempDir()
	fd := managedObjectTestFD(t, root)
	objects := []blueprint.ManagedMountObject{
		managedObjectTestDeclaration("queue/active/log", false, managedObjectTestMode(0o640)),
		managedObjectTestDeclaration("queue/active", true, managedObjectTestMode(0o750)),
		managedObjectTestDeclaration(".", true, nil),
		managedObjectTestDeclaration("queue", true, managedObjectTestMode(0o2750)),
		managedObjectTestDeclaration("zero", false, managedObjectTestMode(0)),
	}
	result, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd})
	if err != nil {
		t.Fatal(err)
	}
	defer closePreparedManagedObjects(t, result)
	if len(result) != len(objects) {
		t.Fatalf("prepared %d objects, want %d", len(result), len(objects))
	}
	for _, object := range objects {
		got := result[object.Path]
		if got.Identity.UID != object.Identity.UID || got.Identity.GID != object.Identity.GID {
			t.Errorf("%s: wrong ownership: %+v", object.Path, got.Identity)
		}
		if object.Identity.Permissions != nil && got.Identity.Mode&0o7777 != *object.Identity.Permissions {
			t.Errorf("%s: mode %#o", object.Path, got.Identity.Mode)
		}
		flags, err := unix.FcntlInt(uintptr(got.FD), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Errorf("%s: descriptor not close-on-exec: %d %v", object.Path, flags, err)
		}
	}
	log := filepath.Join(root, "queue/active/log")
	if err := os.WriteFile(log, []byte("application data survives verification"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorded := make(map[string]int)
	for name, object := range result {
		recorded[name] = object.FD
	}
	verified, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, recorded)
	if err != nil {
		t.Fatal(err)
	}
	closePreparedManagedObjects(t, verified)
	data, err := os.ReadFile(log)
	if err != nil || string(data) != "application data survives verification" {
		t.Fatalf("verification changed file contents: %q %v", data, err)
	}
}

func TestManagedMountObjectsRefuseMalformedManifestBeforeEffects(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	for _, name := range []string{"", "/escape", "..", "../escape", "a/../b", "a//b", "a/", "a\\b", "a\x00b", "a\nb", "a\tb"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			fd := managedObjectTestFD(t, root)
			objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("first", true, nil), managedObjectTestDeclaration(name, false, nil)}
			if result, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd}); err == nil || result != nil {
				t.Fatalf("accepted malformed manifest: %v", result)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid manifest had effects: %v %v", entries, err)
			}
		})
	}
	root := managedObjectTestDeclaration(".", true, nil)
	for _, objects := range [][]blueprint.ManagedMountObject{
		nil,
		{managedObjectTestDeclaration("file", false, nil)},
		{managedObjectTestDeclaration(".", false, nil)},
		{root, root},
		{root, managedObjectTestDeclaration("a", false, nil), managedObjectTestDeclaration("a/b", false, nil)},
		{root, managedObjectTestDeclaration("mode", false, managedObjectTestMode(0o10000))},
	} {
		if _, err := orderManagedMountObjectsV1(objects); err == nil {
			t.Fatalf("accepted invalid declarations: %+v", objects)
		}
	}
}

func TestManagedMountObjectsExistingObjectsRequireExactAuthority(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	for _, variant := range []string{"no root provenance", "no file provenance", "wrong root", "wrong file", "missing recorded file", "wrong owner", "wrong group", "wrong mode", "wrong type", "hardlink", "symlink", "fifo", "socket"} {
		t.Run(variant, func(t *testing.T) {
			root, foreign := t.TempDir(), t.TempDir()
			file, canary := filepath.Join(root, "file"), filepath.Join(foreign, "canary")
			for _, p := range []string{file, canary} {
				if err := os.WriteFile(p, []byte("retained data"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fd := managedObjectTestFD(t, root)
			fileFD := managedObjectTestFD(t, file)
			recorded := map[string]int{".": fd, "file": fileFD}
			objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("file", false, managedObjectTestMode(0o600))}
			switch variant {
			case "no root provenance":
				delete(recorded, ".")
			case "no file provenance":
				delete(recorded, "file")
			case "wrong root":
				recorded["."] = managedObjectTestFD(t, foreign)
			case "wrong file":
				recorded["file"] = managedObjectTestFD(t, canary)
			case "missing recorded file":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "wrong owner":
				objects[1].Identity.UID++
			case "wrong group":
				objects[1].Identity.GID++
			case "wrong mode":
				objects[1].Identity.Permissions = managedObjectTestMode(0o644)
			case "wrong type":
				objects[1].Directory = true
			case "hardlink":
				if err := os.Link(file, filepath.Join(foreign, "alias")); err != nil {
					t.Fatal(err)
				}
			case "symlink", "fifo", "socket":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				var err error
				switch variant {
				case "symlink":
					err = os.Symlink(canary, file)
				case "fifo":
					err = unix.Mkfifo(file, 0o600)
				case "socket":
					s, socketErr := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
					if socketErr != nil {
						t.Fatal(socketErr)
					}
					defer unix.Close(s)
					err = unix.Bind(s, &unix.SockaddrUnix{Name: file})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Stat(canary)
			if err != nil {
				t.Fatal(err)
			}
			result, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, recorded)
			if err == nil || result != nil {
				t.Fatalf("accepted %s: %+v", variant, result)
			}
			after, err := os.Stat(canary)
			data, readErr := os.ReadFile(canary)
			if err != nil || readErr != nil || string(data) != "retained data" || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatalf("foreign canary changed: %q %v %v", data, err, readErr)
			}
		})
	}
}

func TestManagedMountObjectsDoNotCreateOrAdoptImplicitParents(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	root := t.TempDir()
	fd := managedObjectTestFD(t, root)
	objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("parent/child", false, nil)}
	if _, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd}); err == nil {
		t.Fatal("created implicit parent")
	}
	if err := os.Mkdir(filepath.Join(root, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd}); err == nil {
		t.Fatal("adopted unrecorded parent")
	}
	parent := managedObjectTestFD(t, filepath.Join(root, "parent"))
	result, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd, "parent": parent})
	if err != nil {
		t.Fatal(err)
	}
	closePreparedManagedObjects(t, result)
}

func TestManagedMountObjectsRefuseNestedMountWithoutHostMutation(t *testing.T) {
	root := managedObjectTestFD(t, "/")
	proc := managedObjectTestFD(t, "/proc")
	rootStat, err := managedMountObjectIdentityForFDV1(root)
	if err != nil {
		t.Fatal(err)
	}
	procStat, err := managedMountObjectIdentityForFDV1(proc)
	if err != nil {
		t.Fatal(err)
	}
	if procStat.Mount == rootStat.Mount {
		t.Fatal("test requires Linux procfs to be a distinct mount")
	}
	// Isolate the confined lookup from preparation's private staging prerequisite.
	// The filesystem containing the test temporary directory is irrelevant here.
	fd, err := openManagedMountObjectV1(root, "proc", unix.O_PATH|unix.O_DIRECTORY)
	if fd >= 0 {
		unix.Close(fd)
	}
	if !errors.Is(err, unix.EXDEV) {
		t.Fatalf("nested mount lookup did not refuse crossing: %v", err)
	}
}

func TestManagedMountObjectsCancelAndCloseOwnedDescriptors(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	root := t.TempDir()
	fd := managedObjectTestFD(t, root)
	objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("a", true, nil), managedObjectTestDeclaration("missing-parent/failure", false, nil)}
	count := func() int {
		fds, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(fds)
	}
	before := count()
	for i := 0; i < 20; i++ {
		// First call creates a; later calls refuse its missing provenance.
		if result, err := prepareManagedMountObjectsV1(context.Background(), fd, stagingFD, objects, map[string]int{".": fd}); err == nil || result != nil {
			t.Fatalf("failure returned successful descriptors: %v %v", result, err)
		}
	}
	if after := count(); after != before {
		t.Fatalf("owned descriptors leaked: before %d after %d", before, after)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("borrowed root was closed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := prepareManagedMountObjectsV1(ctx, fd, stagingFD, objects, map[string]int{".": fd}); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("ignored cancellation: %v %v", result, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "a"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed preparation changed inert directory contents: %v %v", entries, err)
	}
}

func TestManagedMountObjectsOrderIsStableAndDoesNotMutateInput(t *testing.T) {
	objects := []blueprint.ManagedMountObject{
		managedObjectTestDeclaration("z/file", false, nil), managedObjectTestDeclaration("z/b", true, nil),
		managedObjectTestDeclaration("z", true, nil), managedObjectTestDeclaration(".", true, nil),
		managedObjectTestDeclaration("!first", true, nil), managedObjectTestDeclaration("a", true, nil), managedObjectTestDeclaration("a/file", false, nil),
	}
	before := append([]blueprint.ManagedMountObject(nil), objects...)
	ordered, err := orderManagedMountObjectsV1(objects)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(ordered))
	for i, object := range ordered {
		names[i] = object.Path
	}
	if !reflect.DeepEqual(names, []string{".", "!first", "a", "z", "z/b", "a/file", "z/file"}) || !reflect.DeepEqual(objects, before) {
		t.Fatalf("bad order %v or mutated input", names)
	}
}

func TestManagedMountObjectsInvalidDescriptorsNeverAcquireAuthority(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	root, foreign := t.TempDir(), t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootFD, fileFD := managedObjectTestFD(t, root), managedObjectTestFD(t, file)
	objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil)}
	for _, badRoot := range []int{-1, fileFD} {
		if result, err := prepareManagedMountObjectsV1(context.Background(), badRoot, stagingFD, objects, map[string]int{".": rootFD}); err == nil || result != nil {
			t.Fatalf("accepted invalid root %d: %v %v", badRoot, result, err)
		}
	}
	if result, err := prepareManagedMountObjectsV1(context.Background(), rootFD, stagingFD, objects, map[string]int{".": -1}); err == nil || result != nil {
		t.Fatalf("accepted invalid retained root: %v %v", result, err)
	}
	if err := os.Mkdir(filepath.Join(root, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	objects = append(objects, managedObjectTestDeclaration("parent/new", false, nil))
	for _, badParent := range []int{-1, managedObjectTestFD(t, foreign)} {
		if result, err := prepareManagedMountObjectsV1(context.Background(), rootFD, stagingFD, objects, map[string]int{".": rootFD, "parent": badParent}); err == nil || result != nil {
			t.Fatalf("accepted invalid retained parent: %v %v", result, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "parent"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid parent authority had effects: %v %v", entries, err)
	}
}

func TestManagedMountObjectsCancellationBeforeReceiptClosesPartialSuccess(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	root := t.TempDir()
	fd := managedObjectTestFD(t, root)
	ctx := &managedObjectCancelAtCompletion{Context: context.Background()}
	objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("new", false, nil)}
	result, err := prepareManagedMountObjectsV1(ctx, fd, stagingFD, objects, map[string]int{".": fd})
	if !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("completion cancellation returned success: %v %v", result, err)
	}
	info, err := os.Stat(filepath.Join(root, "new"))
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		t.Fatalf("partial inert object was removed or changed: %v %v", info, err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("borrowed root closed: %v", err)
	}
}

func TestManagedMountObjectsExclusiveCreationCannotAdoptExistingLeaf(t *testing.T) {
	stagingFD := managedObjectTestStagingFD(t)
	root := t.TempDir()
	fd := managedObjectTestFD(t, root)
	for _, directory := range []bool{false, true} {
		name := "file"
		if directory {
			name = "dir"
			if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(filepath.Join(root, name), []byte("retained"), 0o600); err != nil {
			t.Fatal(err)
		}
		identity, err := managedMountObjectIdentityForFDV1(fd)
		if err != nil {
			t.Fatal(err)
		}
		for _, parent := range []int{fd, -1} {
			_, err := createManagedMountObjectV1(parent, stagingFD, name, identity.Mount, managedObjectTestDeclaration(name, directory, managedObjectTestMode(0o777)))
			if parent == fd && !errors.Is(err, unix.EEXIST) || parent == -1 && !errors.Is(err, unix.EBADF) {
				t.Fatalf("exclusive creation %s parent %d: %v", name, parent, err)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "file"))
	if err != nil || string(data) != "retained" {
		t.Fatalf("exclusive creation changed existing file: %q %v", data, err)
	}
}

func TestManagedMountObjectsStagingAuthorityRequiredBeforeEffects(t *testing.T) {
	root, permissive := t.TempDir(), t.TempDir()
	if err := os.Chmod(permissive, 0o755); err != nil {
		t.Fatal(err)
	}
	rootFD := managedObjectTestFD(t, root)
	objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("new", true, nil)}
	for _, staging := range []int{-1, rootFD, managedObjectTestFD(t, permissive), managedObjectTestFD(t, "/proc")} {
		result, err := prepareManagedMountObjectsV1(context.Background(), rootFD, staging, objects, map[string]int{".": rootFD})
		if err == nil || result != nil {
			t.Fatalf("invalid staging %d accepted: %v %v", staging, result, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid staging had effects: %v %v", entries, err)
	}
}

func TestManagedMountObjectsDirectoryStagingPreservesInheritedPolicy(t *testing.T) {
	root, staging := t.TempDir(), t.TempDir()
	if err := os.Chmod(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	// A minimal default ACL; the kernel, not the preparer, interprets it.
	acl := make([]byte, 4+3*8)
	binary.LittleEndian.PutUint32(acl, 2)
	for i, entry := range [][2]uint16{{1, 7}, {4, 5}, {32, 1}} {
		binary.LittleEndian.PutUint16(acl[4+i*8:], entry[0])
		binary.LittleEndian.PutUint16(acl[6+i*8:], entry[1])
		binary.LittleEndian.PutUint32(acl[8+i*8:], ^uint32(0))
	}
	if err := unix.Setxattr(root, "system.posix_acl_default", acl, 0); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skip("test filesystem does not support POSIX ACLs")
		}
		t.Fatal(err)
	}
	if err := unix.Chmod(root, 0o2700); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(root, "control")
	if err := os.Mkdir(control, 0o755); err != nil {
		t.Fatal(err)
	}
	rootFD, stagingFD := managedObjectTestFD(t, root), managedObjectTestFD(t, staging)
	objects := []blueprint.ManagedMountObject{managedObjectTestDeclaration(".", true, nil), managedObjectTestDeclaration("staged", true, nil)}
	result, err := prepareManagedMountObjectsV1(context.Background(), rootFD, stagingFD, objects, map[string]int{".": rootFD})
	if err != nil {
		t.Fatal(err)
	}
	defer closePreparedManagedObjects(t, result)
	var expected unix.Stat_t
	if err := unix.Stat(control, &expected); err != nil {
		t.Fatal(err)
	}
	if got := result["staged"].Identity.Mode & 0o7777; got != expected.Mode&0o7777 {
		t.Fatalf("staging changed inherited permissions: got %#o, want %#o", got, expected.Mode&0o7777)
	}
	got := make([]byte, len(acl))
	n, err := unix.Getxattr(filepath.Join(root, "staged"), "system.posix_acl_default", got)
	if err != nil || n != len(acl) || !reflect.DeepEqual(got, acl) {
		t.Fatalf("staging lost the inherited default ACL: %v %v", got, err)
	}
	// Reusing staging for a parent without a default ACL must not retain it.
	plain := t.TempDir()
	plainFD := managedObjectTestFD(t, plain)
	result2, err := prepareManagedMountObjectsV1(context.Background(), plainFD, stagingFD, objects, map[string]int{".": plainFD})
	if err != nil {
		t.Fatal(err)
	}
	defer closePreparedManagedObjects(t, result2)
	if _, err := unix.Getxattr(filepath.Join(plain, "staged"), "system.posix_acl_default", got); !errors.Is(err, unix.ENODATA) {
		t.Fatalf("staging leaked the previous parent's default ACL: %v", err)
	}
}
