//go:build linux

package dockerdeploy

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestProtectedRuntimeHostPathV1RejectsProcMagicLinkAliases(t *testing.T) {
	root := t.TempDir()
	directAlias := filepath.Join(root, "cwd")
	if err := os.Symlink("/proc/self/cwd", directAlias); err != nil {
		t.Fatal(err)
	}
	procAlias := filepath.Join(root, "proc")
	if err := os.Symlink("/proc", procAlias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directAlias, filepath.Join(procAlias, "self", "cwd")} {
		got, err := protectedRuntimeHostPathV1(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != "procfs magic link" {
			t.Fatalf("protected path %q = %q, want procfs magic link", path, got)
		}
	}
}

func TestProtectedRuntimeHostPathV1AllowsOrdinarySymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	got, err := protectedRuntimeHostPathV1(alias)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("ordinary symlink classified as %q", got)
	}
}

func TestProtectedRuntimeHostFilesystemV1RecognizesKernelFilesystems(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/proc", want: "procfs"},
		{path: "/sys", want: "sysfs"},
		{path: "/dev/pts", want: "devpts"},
	} {
		t.Run(test.want, func(t *testing.T) {
			if _, err := os.Stat(test.path); os.IsNotExist(err) {
				t.Skipf("host path %q is absent", test.path)
			} else if err != nil {
				t.Fatal(err)
			}
			got, err := protectedRuntimeHostFilesystemV1(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("filesystem for %q = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestProtectedRuntimeHostFilesystemV1DoesNotRejectOrdinaryTmpfs(t *testing.T) {
	for _, candidate := range []string{"/run", "/tmp"} {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		kind, err := runtimeHostMountFilesystemV1(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if kind != "tmpfs" {
			continue
		}
		protected, err := protectedRuntimeHostFilesystemV1(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if protected != "" {
			t.Fatalf("ordinary tmpfs %q classified as %q", candidate, protected)
		}
		return
	}
	t.Skip("host has no ordinary tmpfs candidate")
}

func TestProtectedRuntimeHostFilesystemKindV1RecognizesKernelControlFilesystems(t *testing.T) {
	for _, test := range []struct {
		magic uint64
		want  string
	}{
		{magic: unix.CGROUP_SUPER_MAGIC, want: "cgroup"},
		{magic: unix.CGROUP2_SUPER_MAGIC, want: "cgroup2"},
		{magic: unix.DEBUGFS_MAGIC, want: "debugfs"},
		{magic: unix.TRACEFS_MAGIC, want: "tracefs"},
		{magic: unix.SECURITYFS_MAGIC, want: "securityfs"},
		{magic: unix.BPF_FS_MAGIC, want: "bpf"},
		{magic: unix.BINFMTFS_MAGIC, want: "binfmt_misc"},
		{magic: unix.EFIVARFS_MAGIC, want: "efivarfs"},
		{magic: unix.NSFS_MAGIC, want: "nsfs"},
		{magic: unix.PSTOREFS_MAGIC, want: "pstore"},
		{magic: unix.SELINUX_MAGIC, want: "selinuxfs"},
	} {
		if got := protectedRuntimeHostFilesystemKindV1(test.magic); got != test.want {
			t.Fatalf("filesystem magic %#x = %q, want %q", test.magic, got, test.want)
		}
	}
}

func TestRuntimeHostSharesDedicatedDevFilesystemV1(t *testing.T) {
	dev, err := runtimeHostSharesDedicatedDevFilesystemV1("/dev")
	if err != nil {
		t.Fatal(err)
	}
	var devInfo, rootInfo syscall.Stat_t
	if err := syscall.Stat("/dev", &devInfo); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat("/", &rootInfo); err != nil {
		t.Fatal(err)
	}
	if devInfo.Dev == rootInfo.Dev {
		if dev {
			t.Fatal("non-dedicated /dev filesystem classified as dedicated")
		}
		return
	}
	if !dev {
		t.Fatal("dedicated /dev filesystem was not recognized")
	}
}

func TestRuntimeHostMountFilesystemV1ParsesIdentity(t *testing.T) {
	data := []byte("41 1 0:1 / / rw - ext4 /dev/root rw\n" +
		"42 41 0:2 / /safe\\040tree rw - devtmpfs udev rw\n" +
		"43 42 0:3 / /safe\\040tree/nested rw - proc proc rw\n")

	kind, found, err := runtimeHostMountFilesystemByIDV1(data, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !found || kind != "devtmpfs" {
		t.Fatalf("mount ID lookup = %q, %t", kind, found)
	}
	identity, found, err := runtimeHostMountIdentityByIDV1(data, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !found || identity.mountPoint != "/safe tree" {
		t.Fatalf("mount point lookup = %q, %t", identity.mountPoint, found)
	}
}

func TestRuntimeHostMountsExposeSameRootV1(t *testing.T) {
	data := []byte("41 1 0:1 / / rw - ext4 /dev/root rw\n" +
		"42 41 0:1 / /safe/root rw - ext4 /dev/root rw\n" +
		"43 41 0:1 /var /safe/var rw - ext4 /dev/root rw\n" +
		"44 41 0:2 / /other rw - ext4 /dev/other rw\n")

	for _, test := range []struct {
		name      string
		candidate uint64
		path      string
		want      bool
	}{
		{name: "root alias", candidate: 42, path: "/safe/root", want: true},
		{name: "path below root alias", candidate: 42, path: "/safe/root/home/me/app", want: false},
		{name: "subdirectory bind", candidate: 43, path: "/safe/var", want: false},
		{name: "different filesystem", candidate: 44, path: "/other", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := runtimeHostMountsExposeSameRootV1(data, test.candidate, 41, test.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("same root = %t, want %t", got, test.want)
			}
		})
	}
}
