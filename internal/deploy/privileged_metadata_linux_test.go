//go:build linux

package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// Run this test in a mapped user namespace: it exercises real root writes and
// a separate non-root operator without granting the test host root privileges.
func TestPrivilegedWritesPreserveStagingOperator(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root in a disposable user namespace")
	}
	base, err := os.MkdirTemp("", "reploy-hf-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "staging")
	stateDir := filepath.Join(dir, ".reploy")
	for _, path := range []string{dir, stateDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, 1000, 1000); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	state := StateV1{Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t),
		Platform: stateV1TestPlatform(t), Overlay: EmptyRequestOverlayV1()}
	if err := lock.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	digest, err := lock.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	paths := map[string]os.FileMode{
		filepath.Join(stateDir, "state.json"):                       0o600,
		filepath.Join(stateDir, "operation.lock"):                   0o600,
		filepath.Join(stateDir, "locks"):                            0o700,
		filepath.Join(stateDir, "locks", buildLockFilename(digest)): 0o600,
	}
	for path, mode := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != 1000 || stat.Gid != 1000 || info.Mode().Perm() != mode {
			t.Errorf("%s: owner %d:%d mode %o; want operator 1000:1000 mode %o", path, stat.Uid, stat.Gid, info.Mode().Perm(), mode)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(base, "operator.test")
	if err := os.WriteFile(binary, content, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestStagingOperatorChild$", "-test.v")
	cmd.Env = append(os.Environ(), "REPLOY_OWNER_TEST_DIR="+dir, "REPLOY_OWNER_TEST_LOCK="+string(digest))
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{1000}}}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("ordinary staging operator failed: %v\n%s", err, output)
	}
}

func TestStagingOperatorChild(t *testing.T) {
	dir := os.Getenv("REPLOY_OWNER_TEST_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	if os.Geteuid() != 1000 || os.Getegid() != 1000 {
		t.Fatal("helper must run as the non-root staging operator")
	}
	lock, err := AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	if _, found, err := lock.ReadStateV1(); err != nil || !found {
		t.Fatalf("ordinary state inspection: found=%v error=%v", found, err)
	}
	path := filepath.Join(dir, ".reploy", "locks", fmt.Sprintf("sha256-%s.json", os.Getenv("REPLOY_OWNER_TEST_LOCK")[7:]))
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("ordinary build lock inspection: %v", err)
	}
}
