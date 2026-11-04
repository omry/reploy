//go:build linux

package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"golang.org/x/sys/unix"
)

type managedMountSetupEvidenceV1 struct{}

func (managedMountSetupEvidenceV1) Plan(context.Context, deploy.StateV1, identitymapping.Geometry) ([]string, error) {
	return []string{"private-setup-fixture"}, nil
}
func (managedMountSetupEvidenceV1) Unused(context.Context, deploy.StateV1) error { return nil }
func (managedMountSetupEvidenceV1) Verify(context.Context, deploy.StateV1) error { return nil }

func managedMountSetupGeometryV1() identitymapping.Geometry {
	return identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{
		{ContainerStart: 0, HostStart: 100000, Count: 1},
		{ContainerStart: 200, HostStart: 100200, Count: 2},
	}, GIDs: []identitymapping.Interval{
		{ContainerStart: 0, HostStart: 100000, Count: 1},
		{ContainerStart: 300, HostStart: 100300, Count: 1},
	}}
}

func managedMountSetupOwnerV1(t *testing.T) (deploy.IdentityMappingOperationsV1, deploy.StateV1) {
	t.Helper()
	dir, _ := stageProviderBuildRunState(t, false)
	op, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.Unlock() })
	state, found, err := op.ReadStateV1()
	if err != nil || !found {
		t.Fatal("missing staged fixture", err)
	}
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "private helper fixture; no runtime admission"
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	state.Current = &deploy.EnvironmentGenerationState{Reference: "reploy/env/demo:g-setup-fixture", ImageDigest: digest,
		RootFSSubject: digest, BuildLockDigest: digest, Platform: state.Platform, RuntimePolicyDigest: digest}
	if err := op.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	ops := deploy.IdentityMappingOperationsV1{Operation: op, Evidence: managedMountSetupEvidenceV1{}}
	intent, err := ops.Configure(t.Context(), state, managedMountSetupGeometryV1(), "private-setup")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := ops.BeginEffects(t.Context(), intent, "private-setup")
	if err != nil {
		t.Fatal(err)
	}
	return ops, pending
}

func managedMountSetupHeldV1(t *testing.T, name string) *os.File {
	t.Helper()
	fd, err := unix.Open(name, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), name)
	t.Cleanup(func() { file.Close() })
	return file
}

func managedMountSetupDeclarationsV1() []blueprint.ManagedMountObject {
	root, directory, file := uint32(0o750), uint32(0o700), uint32(0o600)
	return []blueprint.ManagedMountObject{
		{Path: ".", Directory: true, Identity: blueprint.MountMetadataIdentity{UID: 0, GID: 300, Permissions: &root}},
		{Path: "private", Directory: true, Identity: blueprint.MountMetadataIdentity{UID: 200, GID: 300, Permissions: &directory}},
		{Path: "private/data", Identity: blueprint.MountMetadataIdentity{UID: 200, GID: 300, Permissions: &file}},
	}
}

// The guest harness supplies fresh setup-owned directories under a private
// operator ancestor. This models F2's handoff preconditions, not its authority.
// Host checks, subordinate map installation, child credentials and F1.2 effects
// are all real. Opt-in prevents a development host's identity setup being changed.
func TestManagedMountSetupNativeV1(t *testing.T) {
	dir := os.Getenv("REPLOY_TEST_MANAGED_SETUP_ROOT")
	if dir == "" {
		t.Skip("requires disposable files-only host with private100000-165535 delegation and fresh setup fixture")
	}
	if os.Geteuid() == 0 {
		t.Fatal("setup caller must be the unprivileged operator")
	}
	ops, state := managedMountSetupOwnerV1(t)
	root := managedMountSetupHeldV1(t, filepath.Join(dir, "root"))
	staging := managedMountSetupHeldV1(t, filepath.Join(dir, "staging"))
	before, err := os.ReadFile(filepath.Join(dir, "protected"))
	if err != nil {
		t.Fatal(err)
	}
	var protectedBefore unix.Stat_t
	if err := unix.Stat(filepath.Join(dir, "protected"), &protectedBefore); err != nil {
		t.Fatal(err)
	}
	rootBefore, err := managedMountObjectIdentityForFDV1(int(root.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	objects := managedMountSetupDeclarationsV1()
	prepared, err := prepareManagedMountSetupV1(t.Context(), ops, state, managedMountSetupGeometryV1(), "private-setup", root, staging, objects, map[string]*os.File{".": root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, object := range prepared {
			unix.Close(object.FD)
		}
	}()
	if len(prepared) != len(objects) {
		t.Fatal("incomplete descriptor handoff")
	}
	for _, declaration := range objects {
		object := prepared[declaration.Path]
		actual, err := managedMountObjectIdentityForFDV1(object.FD)
		wantUID := uint32(100200)
		if declaration.Path == "." {
			wantUID = 100000
		}
		if err != nil || actual.UID != wantUID || actual.GID != 100300 || actual.Mode&0o7777 != *declaration.Identity.Permissions ||
			object.Identity.UID != declaration.Identity.UID || object.Identity.GID != declaration.Identity.GID {
			t.Fatalf("%s: actual=%+v response=%+v err=%v", declaration.Path, actual, object.Identity, err)
		}
		flags, err := unix.FcntlInt(uintptr(object.FD), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatal("handoff leaked an inheritable descriptor", err)
		}
	}
	if _, err := unix.Write(prepared["private/data"].FD, []byte("private-recipient-data\n")); err != nil {
		t.Fatal(err)
	}
	retained := map[string]*os.File{}
	for name, object := range prepared {
		fd, err := unix.FcntlInt(uintptr(object.FD), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		retained[name] = os.NewFile(uintptr(fd), name)
		defer retained[name].Close()
	}
	again, err := prepareManagedMountSetupV1(t.Context(), ops, state, managedMountSetupGeometryV1(), "private-setup", root, staging, objects, retained)
	if err != nil {
		t.Fatal("exact recorded objects did not verify", err)
	}
	for name, object := range again {
		if object.Identity != prepared[name].Identity {
			t.Error("recorded object identity changed")
		}
		unix.Close(object.FD)
	}
	for name, change := range map[string]func(*deploy.StateV1, *identitymapping.Geometry){
		"revision": func(s *deploy.StateV1, _ *identitymapping.Geometry) {
			m := *s.IdentityMapping
			m.Revision = "999"
			s.IdentityMapping = &m
		},
		"geometry": func(_ *deploy.StateV1, g *identitymapping.Geometry) {
			g.UIDs = append([]identitymapping.Interval(nil), g.UIDs...)
			g.UIDs[1].HostStart++
		},
	} {
		t.Run(name, func(t *testing.T) {
			observation, active := state, managedMountSetupGeometryV1()
			change(&observation, &active)
			if got, err := prepareManagedMountSetupV1(t.Context(), ops, observation, active, "private-setup", root, staging, objects, retained); err == nil || got != nil {
				t.Fatal("stale authority admitted")
			}
		})
	}
	unmapped := append(append([]blueprint.ManagedMountObject(nil), objects...), blueprint.ManagedMountObject{Path: "unmapped", Identity: blueprint.MountMetadataIdentity{UID: 65534, GID: 300}})
	if got, err := prepareManagedMountSetupV1(t.Context(), ops, state, managedMountSetupGeometryV1(), "private-setup", root, staging, unmapped, retained); err == nil || got != nil {
		t.Fatal("unmapped declaration admitted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := prepareManagedMountSetupV1(canceled, ops, state, managedMountSetupGeometryV1(), "private-setup", root, staging, objects, retained); err == nil || got != nil {
		t.Fatal("canceled caller admitted")
	}
	// The helper must reject this symlink before any target mutation, even
	// though setup capabilities would otherwise make a chown possible.
	bad := append(append([]blueprint.ManagedMountObject(nil), objects...), blueprint.ManagedMountObject{Path: "foreign", Identity: blueprint.MountMetadataIdentity{UID: 200, GID: 300}})
	if got, err := prepareManagedMountSetupV1(t.Context(), ops, state, managedMountSetupGeometryV1(), "private-setup", root, staging, bad, retained); err == nil || got != nil {
		t.Fatal("foreign symlink accepted")
	}
	rootAfter, err := managedMountObjectIdentityForFDV1(int(root.Fd()))
	if err != nil || rootAfter != rootBefore {
		t.Fatal("recorded root metadata changed", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "protected"))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("foreign target changed", err)
	}
	var protectedAfter unix.Stat_t
	if err := unix.Stat(filepath.Join(dir, "protected"), &protectedAfter); err != nil || protectedBefore.Uid != protectedAfter.Uid ||
		protectedBefore.Gid != protectedAfter.Gid || protectedBefore.Mode != protectedAfter.Mode || protectedBefore.Ino != protectedAfter.Ino {
		t.Fatal("foreign target metadata changed", err)
	}
	assertManagedMountSetupRetiredV1(t)
	t.Log("real sparse maps, private subordinate metadata, exact reuse, stale/canceled refusal, foreign target preservation and helper retirement passed")
}

func assertManagedMountSetupRetiredV1(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		status, err := os.ReadFile("/proc/" + entry.Name() + "/status")
		if err != nil || !strings.Contains(string(status), "PPid:\t"+strconv.Itoa(os.Getpid())+"\n") {
			continue
		}
		argv, _ := os.ReadFile("/proc/" + entry.Name() + "/cmdline")
		if strings.Contains(string(argv), managedMountSetupArgumentV1) || strings.Contains(string(argv), "newuidmap") || strings.Contains(string(argv), "newgidmap") {
			t.Fatalf("setup process survived return: pid%s", entry.Name())
		}
	}
}

// Root is used only by the disposable fixture driver to establish a recipient
// map. The recipient itself runs with a nonzero UID and no capabilities. It
// opens by path relative to the held resource, not through a privileged file FD.
func TestManagedMountSetupRecipientV1(t *testing.T) {
	if role := os.Getenv("REPLOY_TEST_MANAGED_RECIPIENT"); role != "" {
		if os.Geteuid() != map[string]int{"allow": 200, "deny": 201}[role] || os.Getegid() != 300 {
			t.Fatal("wrong recipient credentials")
		}
		status, err := os.ReadFile("/proc/self/status")
		if err != nil || !strings.Contains(string(status), "CapEff:\t0000000000000000\n") {
			t.Fatal("recipient retained setup capability", err)
		}
		fd, err := unix.Openat(3, "private/data", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if role == "deny" {
			if err == nil {
				unix.Close(fd)
				t.Fatal("unrelated subordinate recipient accessed private data")
			}
			return
		}
		if err != nil {
			t.Fatal("selected recipient could not access private data", err)
		}
		file := os.NewFile(uintptr(fd), "recipient-data")
		defer file.Close()
		data := make([]byte, 64)
		n, err := file.Read(data)
		if err != nil || string(data[:n]) != "private-recipient-data\n" {
			t.Fatalf("recipient read %q: %v", data[:n], err)
		}
		return
	}
	dir := os.Getenv("REPLOY_TEST_MANAGED_RECIPIENT_ROOT")
	if dir == "" {
		t.Skip("requires prepared disposable guest fixture")
	}
	if os.Geteuid() != 0 {
		t.Fatal("only the disposable fixture driver may establish recipient maps")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := managedMountSetupHeldV1(t, filepath.Join(dir, "root"))
	for _, role := range []string{"allow", "deny"} {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		command := exec.CommandContext(ctx, executable, "-test.run=^TestManagedMountSetupRecipientV1$", "-test.v")
		command.ExtraFiles = []*os.File{root}
		command.Env = []string{"REPLOY_TEST_MANAGED_RECIPIENT=" + role}
		uid := uint32(200)
		if role == "deny" {
			uid++
		}
		command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWUSER,
			UidMappings: []syscall.SysProcIDMap{{ContainerID: 200, HostID: 100200, Size: 2}},
			GidMappings: []syscall.SysProcIDMap{{ContainerID: 300, HostID: 100300, Size: 1}},
			Credential:  &syscall.Credential{Uid: uid, Gid: 300}}
		data, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("recipient %s: %v\n%s", role, err, data)
		}
		t.Log(fmt.Sprintf("recipient %s: %s", role, data))
	}
}

func TestManagedMountSetupRefusesMissingAuthorityV1(t *testing.T) {
	file := managedMountSetupHeldV1(t, t.TempDir())
	if _, err := prepareManagedMountSetupV1(t.Context(), deploy.IdentityMappingOperationsV1{}, deploy.StateV1{}, identitymapping.Geometry{}, "foreign", file, file, managedMountSetupDeclarationsV1(), nil); err == nil {
		t.Fatal("missing owner/operation admitted")
	}
	if code, handled := RunManagedMountSetupChildV1([]string{managedMountSetupArgumentV1, "command"}, nil); handled || code != 0 {
		t.Fatal("private child accepted argv inputs")
	}
}

func TestManagedMountSetupChildRejectsInvalidHandoffV1(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	valid, err := json.Marshal(managedMountSetupManifestV1{Geometry: managedMountSetupGeometryV1(), Objects: managedMountSetupDeclarationsV1(), Parent: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		content []byte
		sealed  bool
		release bool
	}{
		{"unsealed", valid, false, false},
		{"malformed", []byte("{"), true, false},
		{"unknown-field", append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"command":"application"}`)...), true, false},
		{"missing-geometry", []byte(`{}`), true, false},
		{"over-bound", []byte(strings.Repeat(" ", managedMountSetupLimitV1+1)), true, false},
		{"wrong-kernel-map", valid, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootDir, stagingDir := t.TempDir(), t.TempDir()
			before, err := os.Stat(rootDir)
			if err != nil {
				t.Fatal(err)
			}
			root, staging := managedMountSetupHeldV1(t, rootDir), managedMountSetupHeldV1(t, stagingDir)
			fd, err := unix.MemfdCreate("invalid-setup-fixture", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
			if err != nil {
				t.Fatal(err)
			}
			manifest := os.NewFile(uintptr(fd), "manifest")
			defer manifest.Close()
			if _, err := manifest.Write(tc.content); err != nil {
				t.Fatal(err)
			}
			if _, err := manifest.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			if tc.sealed {
				if _, err := unix.FcntlInt(manifest.Fd(), unix.F_ADD_SEALS, managedMountSetupSealsV1); err != nil {
					t.Fatal(err)
				}
			}
			pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			parent, child := os.NewFile(uintptr(pair[0]), "parent"), os.NewFile(uintptr(pair[1]), "child")
			defer parent.Close()
			defer child.Close()
			channel, err := managedMountSetupSocketV1(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer channel.Close()
			if err := channel.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, managedMountSetupArgumentV1)
			command.ExtraFiles = []*os.File{manifest, child, root, staging}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			child.Close()
			if tc.release {
				var signal [1]byte
				if _, err := io.ReadFull(channel, signal[:]); err != nil || signal[0] != 1 {
					_ = command.Process.Kill()
					_ = command.Wait()
					t.Fatal("no readiness for kernel-map check", err)
				}
				if _, err := channel.Write([]byte{2}); err != nil {
					_ = command.Process.Kill()
					_ = command.Wait()
					t.Fatal(err)
				}
			}
			if err := command.Wait(); err == nil || ctx.Err() != nil {
				t.Fatal("invalid handoff did not refuse promptly", err, ctx.Err())
			}
			entries, err := os.ReadDir(rootDir)
			after, statErr := os.Stat(rootDir)
			if err != nil || statErr != nil || len(entries) != 0 || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("invalid handoff changed held storage", err, statErr)
			}
		})
	}
}

// The isolated guest driver pauses the real mapper after it installs its map.
// That barrier exercises cancellation and a fresh authority change before the
// production parent can release effects. No production hook controls this path.
func TestManagedMountSetupBarrierV1(t *testing.T) {
	mode := os.Getenv("REPLOY_TEST_MANAGED_SETUP_BARRIER")
	if mode == "" {
		t.Skip("requires disposable guest mapper barrier")
	}
	dir := os.Getenv("REPLOY_TEST_MANAGED_SETUP_ROOT")
	ops, state := managedMountSetupOwnerV1(t)
	root := managedMountSetupHeldV1(t, filepath.Join(dir, "root"))
	staging := managedMountSetupHeldV1(t, filepath.Join(dir, "staging"))
	ctx := t.Context()
	if strings.HasPrefix(mode, "cancel") {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	start := time.Now()
	objects := managedMountSetupDeclarationsV1()
	if strings.HasSuffix(mode, "effects") {
		objects = objects[:1]
		for i := 0; i < 4000; i++ {
			objects = append(objects, blueprint.ManagedMountObject{Path: fmt.Sprintf("item-%05d", i), Identity: blueprint.MountMetadataIdentity{UID: 200, GID: 300}})
		}
	}
	got, err := prepareManagedMountSetupV1(ctx, ops, state, managedMountSetupGeometryV1(), "private-setup", root, staging, objects, map[string]*os.File{".": root})
	if strings.HasPrefix(mode, "parent-death") {
		t.Fatalf("parent-death fixture unexpectedly returned: %v", err)
	}
	if err == nil || got != nil {
		t.Fatal("barrier failed to inhibit preparation")
	}
	if strings.HasPrefix(mode, "cancel") && time.Since(start) > 10*time.Second {
		t.Fatal("in-flight cancellation exceeded its bound")
	}
	assertManagedMountSetupRetiredV1(t)
	t.Log("barrier", mode, "refused and retired:", err)
}

func managedMountInspectFixtureV1(t *testing.T) (string, []blueprint.ManagedMountObject, map[string]int) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private/data"), []byte("retained data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	objects := managedMountSetupDeclarationsV1()
	recorded := map[string]int{}
	for i := range objects {
		objects[i].Identity.UID, objects[i].Identity.GID = uint32(os.Geteuid()), uint32(os.Getegid())
		fd, err := unix.Open(filepath.Join(root, objects[i].Path), unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			t.Fatal(err)
		}
		recorded[objects[i].Path] = fd
		t.Cleanup(func() { unix.Close(fd) })
	}
	return root, objects, recorded
}

func TestManagedMountInspectCurrentPathsV1(t *testing.T) {
	for _, mode := range []string{"exact", "replaced", "missing", "symlink", "type", "hardlink", "wrong-metadata", "missing-provenance", "invalid-descriptor", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			root, objects, recorded := managedMountInspectFixtureV1(t)
			data := filepath.Join(root, "private/data")
			protected := filepath.Join(t.TempDir(), "protected")
			if err := os.WriteFile(protected, []byte("foreign target\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if mode == "replaced" || mode == "missing" || mode == "symlink" || mode == "type" || mode == "hardlink" {
				if err := os.Rename(data, data+".retained"); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "replaced":
				if err := os.WriteFile(data, []byte("replacement data\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(protected, data); err != nil {
					t.Fatal(err)
				}
			case "type":
				if err := os.Mkdir(data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(protected, data); err != nil {
					t.Fatal(err)
				}
			case "wrong-metadata":
				if err := os.Chmod(data, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			before := map[string]managedMountObjectIdentityV1{}
			for name, fd := range recorded {
				var err error
				before[name], err = managedMountObjectIdentityForFDV1(fd)
				if err != nil {
					t.Fatal(err)
				}
			}
			proof := make(map[string]int, len(recorded))
			for name, fd := range recorded {
				proof[name] = fd
			}
			if mode == "missing-provenance" {
				delete(proof, "private/data")
			} else if mode == "invalid-descriptor" {
				proof["private/data"] = -1
			}
			ctx := t.Context()
			if mode == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			pathsBefore := map[string]unix.Stat_t{}
			pathsMissing := map[string]bool{}
			for _, name := range []string{root, filepath.Join(root, "private"), data, protected} {
				var stat unix.Stat_t
				if err := unix.Lstat(name, &stat); err == unix.ENOENT {
					pathsMissing[name] = true
				} else if err != nil {
					t.Fatal(err)
				} else {
					pathsBefore[name] = stat
				}
			}
			fdsBefore, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}
			got, err := inspectManagedMountObjectsV1(ctx, recorded["."], objects, proof)
			if mode == "exact" {
				if err != nil || len(got) != len(objects) {
					t.Fatal("exact retained paths refused", err)
				}
				for name, object := range got {
					if object.Identity != before[name] {
						t.Error("inspection changed object identity", name)
					}
					if _, err := unix.Write(object.FD, []byte("must not write")); err == nil {
						t.Error("inspection returned a writable descriptor")
					}
					unix.Close(object.FD)
				}
			} else if err == nil || got != nil {
				t.Fatal("unproved current path admitted", mode, err)
			}
			fdsAfter, err := os.ReadDir("/proc/self/fd")
			if err != nil || len(fdsBefore) != len(fdsAfter) {
				t.Fatal("inspection leaked a descriptor", len(fdsBefore), len(fdsAfter), err)
			}
			for name, fd := range recorded {
				after, err := managedMountObjectIdentityForFDV1(fd)
				if err != nil || after != before[name] {
					t.Fatal("inspection changed retained metadata", name, err)
				}
			}
			for name, before := range pathsBefore {
				var after unix.Stat_t
				if err := unix.Lstat(name, &after); err != nil || before.Dev != after.Dev || before.Ino != after.Ino ||
					before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Nlink != after.Nlink ||
					before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
					t.Fatal("inspection mutated a declared or foreign path", name, err)
				}
			}
			for name := range pathsMissing {
				var stat unix.Stat_t
				if err := unix.Lstat(name, &stat); err != unix.ENOENT {
					t.Fatal("inspection created an absent path", name, err)
				}
			}
			content, err := os.ReadFile(protected)
			if err != nil || string(content) != "foreign target\n" {
				t.Fatal("inspection changed foreign content", err)
			}
			if mode == "missing" {
				if _, err := os.Lstat(data); !os.IsNotExist(err) {
					t.Fatal("inspection created an absent object", err)
				}
			} else if mode == "replaced" || mode == "exact" || mode == "wrong-metadata" {
				content, err := os.ReadFile(data)
				want := "retained data\n"
				if mode == "replaced" {
					want = "replacement data\n"
				}
				if err != nil || string(content) != want {
					t.Fatal("inspection changed path content", err)
				}
			}
		})
	}
}

func TestManagedMountInspectRequiresAuthorizationV1(t *testing.T) {
	root := managedMountSetupHeldV1(t, t.TempDir())
	objects := managedMountSetupDeclarationsV1()
	for _, authorize := range []func(context.Context) error{nil, func(context.Context) error { return fmt.Errorf("owner changed") }} {
		if got, err := inspectManagedMountSetupV1(t.Context(), managedMountSetupGeometryV1(), root, objects, nil, authorize); err == nil || got != nil {
			t.Fatal("inspection accepted missing current authorization")
		}
	}
	calls := 0
	authorize := func(context.Context) error { calls++; return nil }
	if got, err := inspectManagedMountSetupV1(t.Context(), managedMountSetupGeometryV1(), root, objects, map[string]*os.File{".": root}, authorize); err == nil || got != nil || calls != 1 {
		t.Fatal("inspection accepted incomplete retained provenance", calls, err)
	}
}

// The disposable guest oracle changes protected paths only after the real
// preparation helper has retired. It independently checks the replacement and
// foreign target before/after observation. The operator cannot traverse this tree.
func TestManagedMountInspectNativeV1(t *testing.T) {
	dir, mode := os.Getenv("REPLOY_TEST_MANAGED_SETUP_ROOT"), os.Getenv("REPLOY_TEST_MANAGED_INSPECT_MODE")
	if dir == "" || mode == "" {
		t.Skip("requires disposable subordinate-owned private-tree inspection fixture")
	}
	ops, state := managedMountSetupOwnerV1(t)
	root := managedMountSetupHeldV1(t, filepath.Join(dir, "root"))
	staging := managedMountSetupHeldV1(t, filepath.Join(dir, "staging"))
	objects, geometry := managedMountSetupDeclarationsV1(), managedMountSetupGeometryV1()
	prepared, err := prepareManagedMountSetupV1(t.Context(), ops, state, geometry, "private-setup", root, staging, objects, map[string]*os.File{".": root})
	if err != nil {
		t.Fatal(err)
	}
	recorded := map[string]*os.File{}
	for name, object := range prepared {
		recorded[name] = os.NewFile(uintptr(object.FD), name)
		defer recorded[name].Close()
	}
	payload := []byte("native-inspection-preserved\n")
	if _, err := unix.Pwrite(prepared["private/data"].FD, payload, 0); err != nil {
		t.Fatal(err)
	}
	if fd, err := unix.Openat(int(root.Fd()), "private/data", unix.O_PATH|unix.O_CLOEXEC, 0); err == nil {
		unix.Close(fd)
		t.Fatal("fixture failed to deny parent traversal")
	} else if err != unix.EACCES {
		t.Fatal("fixture traversal failed for an unexpected reason", err)
	}
	if mode != "exact" && mode != "authorization" && mode != "canceled" {
		if err := os.WriteFile(filepath.Join(dir, "inspect-ready"), []byte(mode), 0o600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(dir, "inspect-done")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("disposable path oracle did not complete")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	before := map[string]managedMountObjectIdentityV1{}
	for name, file := range recorded {
		before[name], err = managedMountObjectIdentityForFDV1(int(file.Fd()))
		if err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	authorize := func(ctx context.Context) error {
		calls++
		if mode == "authorization" && calls == 2 {
			return fmt.Errorf("fresh owner authority was withdrawn")
		}
		_, err := ops.ObserveEffects(ctx, state, geometry, "private-setup")
		return err
	}
	ctx := t.Context()
	if mode == "canceled" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	observed, err := inspectManagedMountSetupV1(ctx, geometry, root, objects, recorded, authorize)
	if mode == "exact" {
		if err != nil || len(observed) != len(prepared) || calls != 2 {
			t.Fatal("protected current paths did not inspect", calls, err)
		}
		for name, object := range observed {
			if object.Identity != prepared[name].Identity {
				t.Error("inspection returned a different retained object", name)
			}
			flags, flagErr := unix.FcntlInt(uintptr(object.FD), unix.F_GETFD, 0)
			if flagErr != nil || flags&unix.FD_CLOEXEC == 0 {
				t.Error("inspection returned an inheritable descriptor", flagErr)
			}
			unix.Close(object.FD)
		}
	} else if err == nil || observed != nil {
		t.Fatal("changed or unauthorized protected path admitted", mode, err)
	}
	for name, file := range recorded {
		after, err := managedMountObjectIdentityForFDV1(int(file.Fd()))
		if err != nil || after != before[name] {
			t.Fatal("inspection changed retained metadata", name, err)
		}
	}
	content := make([]byte, len(payload))
	if n, err := unix.Pread(prepared["private/data"].FD, content, 0); err != nil || n != len(payload) || !reflect.DeepEqual(content, payload) {
		t.Fatal("inspection changed retained content", n, err)
	}
	assertManagedMountSetupRetiredV1(t)
	t.Log("native read-only inspection", mode, "retired; protected metadata/content unchanged; authorization checks", calls)
}

// Malformed path bytes must fail before JSON can rewrite them into a different
// existing path, and before a namespace or setup helper can be created.
func TestManagedMountSetupRejectsInvalidUTF8V1(t *testing.T) {
	for _, malformed := range []string{"private/\xff", "private/\xc3", "private/\xed\xa0\x80"} {
		for _, mode := range []string{"inspect-declaration", "prepare-declaration", "prepare-recorded"} {
			t.Run(fmt.Sprintf("%s/%x", mode, malformed), func(t *testing.T) {
				root := managedMountSetupHeldV1(t, t.TempDir())
				objects := managedMountSetupDeclarationsV1()
				recorded := map[string]*os.File{".": root, "private": root, "private/data": root}
				if mode == "prepare-recorded" {
					recorded[malformed] = root
				} else {
					objects[2].Path = malformed
					delete(recorded, "private/data")
					recorded[malformed] = root
				}
				calls := 0
				authorize := func(ctx context.Context) error {
					calls++
					return ctx.Err()
				}
				var got map[string]preparedManagedMountObjectV1
				var err error
				if mode == "inspect-declaration" {
					got, err = inspectManagedMountSetupV1(t.Context(), managedMountSetupGeometryV1(), root, objects, recorded, authorize)
				} else {
					// Exercise the shared preparation boundary without requiring native
					// host mapping admission for a pre-serialization rejection.
					got, err = runManagedMountSetupV1(t.Context(), managedMountSetupGeometryV1(), root, root, objects, recorded, false, authorize)
				}
				if calls != 1 {
					t.Fatalf("helper reached fresh-release authorization: %d", calls)
				}
				if err == nil || !strings.Contains(err.Error(), "invalid UTF-8") || got != nil {
					for _, object := range got {
						unix.Close(object.FD)
					}
					t.Fatalf("malformed path did not refuse before helper creation: %v", err)
				}
				assertManagedMountSetupRetiredV1(t)
			})
		}
	}
}
