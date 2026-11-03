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
