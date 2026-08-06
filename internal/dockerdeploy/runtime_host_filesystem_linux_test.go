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

func TestProtectedRuntimeHostPathWithResolverV1FallsBackWithoutOpenat2(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	unsupported := func(string) error { return unix.ENOSYS }

	got, err := protectedRuntimeHostPathWithResolverV1(target, unsupported)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("ordinary path classified as %q", got)
	}
	if _, err := protectedRuntimeHostPathWithResolverV1(alias, unsupported); err == nil {
		t.Fatal("symlinked path accepted without openat2")
	}
}

func TestProtectedRuntimeHostPathWithResolverV1ClassifiesMagicLinks(t *testing.T) {
	magicLink := func(string) error { return unix.ELOOP }
	got, err := protectedRuntimeHostPathWithResolverV1("/ordinary/path", magicLink)
	if err != nil {
		t.Fatal(err)
	}
	if got != "procfs magic link" {
		t.Fatalf("magic link classified as %q", got)
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
		if protected == "protected nested host submount" {
			continue
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
		{magic: fuseCtlSuperMagicV1, want: "fusectl"},
		{magic: mqueueMagicV1, want: "mqueue"},
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

func TestRuntimeHostMountIdentityForPathV1FallsBackWithoutMountIDs(t *testing.T) {
	data := []byte("41 1 0:1 / / rw - ext4 /dev/root rw\n" +
		"42 41 0:1 / /safe/root rw - ext4 /dev/root rw\n" +
		"43 41 0:3 / /dev/shm rw - tmpfs shm rw\n" +
		"44 41 0:3 / /safe/shm rw - tmpfs shm rw\n")
	unsupported := func(string) (uint64, bool, error) { return 0, false, nil }

	rootAlias, err := runtimeHostMountIdentityForPathWithResolverV1(data, "/safe/root", unsupported)
	if err != nil {
		t.Fatal(err)
	}
	root, err := runtimeHostMountIdentityForPathWithResolverV1(data, "/", unsupported)
	if err != nil {
		t.Fatal(err)
	}
	exposesRoot, err := runtimeHostMountIdentitiesExposeSameRootV1(rootAlias, root, "/safe/root")
	if err != nil {
		t.Fatal(err)
	}
	if !exposesRoot {
		t.Fatal("root bind alias was not recognized without statx mount IDs")
	}

	devAlias, err := runtimeHostMountIdentityForPathWithResolverV1(data, "/safe/shm", unsupported)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := runtimeHostMountIdentitySharesProtectedTreeV1(data, devAlias, "/safe/shm", "/dev")
	if err != nil {
		t.Fatal(err)
	}
	if !protected {
		t.Fatal("protected submount alias was not recognized without statx mount IDs")
	}
}

func TestRuntimeHostMountIdentityByPathV1ResolvesVisibleMountTopology(t *testing.T) {
	data := []byte("41 41 0:1 / / rw - ext4 /dev/root rw\n" +
		"42 41 0:2 / /stack rw - tmpfs lower rw\n" +
		"43 42 0:3 / /stack/hidden rw - tmpfs hidden rw\n" +
		"44 42 0:4 / /stack rw - tmpfs upper rw\n" +
		"45 44 0:5 / /stack/visible rw - tmpfs visible rw\n")

	for _, test := range []struct {
		path        string
		wantMountID uint64
	}{
		{path: "/stack", wantMountID: 44},
		{path: "/stack/file", wantMountID: 44},
		{path: "/stack/hidden", wantMountID: 44},
		{path: "/stack/visible", wantMountID: 45},
		{path: "/stack/visible/file", wantMountID: 45},
	} {
		identity, err := runtimeHostMountIdentityByPathV1(data, test.path)
		if err != nil {
			t.Fatal(err)
		}
		if identity.mountID != test.wantMountID {
			t.Fatalf("visible mount for %q = %d, want %d", test.path, identity.mountID, test.wantMountID)
		}
	}
}

func TestRuntimeHostMountContainsProtectedSubmountV1(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want bool
	}{
		{
			name: "root alias",
			data: "41 41 0:1 / / rw - ext4 /dev/root rw\n" +
				"42 41 0:1 / /safe/root rw - ext4 /dev/root rw\n",
			want: true,
		},
		{
			name: "proc alias",
			data: "41 41 0:1 / / rw - ext4 /dev/root rw\n" +
				"42 41 0:2 / /proc rw - proc proc rw\n" +
				"43 41 0:2 / /safe/proc rw - proc proc rw\n",
			want: true,
		},
		{
			name: "hidden proc alias",
			data: "41 41 0:1 / / rw - ext4 /dev/root rw\n" +
				"42 41 0:2 / /proc rw - proc proc rw\n" +
				"43 41 0:2 / /safe/proc rw - proc proc rw\n" +
				"44 43 0:3 / /safe/proc rw - tmpfs visible rw\n",
			want: false,
		},
		{
			name: "ordinary nested filesystem",
			data: "41 41 0:1 / / rw - ext4 /dev/root rw\n" +
				"42 41 0:4 / /safe/data rw - ext4 /dev/data rw\n",
			want: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := runtimeHostMountContainsProtectedSubmountV1([]byte(test.data), "/safe")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("protected nested mount = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRuntimeHostMountIdentitiesExposeSameRootV1UsesEffectiveBackingPath(t *testing.T) {
	root := runtimeHostMountIdentityV1{device: "0:1", root: "/@", mountPoint: "/", filesystem: "btrfs"}
	topLevel := runtimeHostMountIdentityV1{device: "0:1", root: "/", mountPoint: "/mnt/btrfs", filesystem: "btrfs"}

	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/mnt/btrfs", want: true},
		{path: "/mnt/btrfs/@", want: true},
		{path: "/mnt/btrfs/@/home", want: false},
		{path: "/mnt/btrfs/other", want: false},
	} {
		got, err := runtimeHostMountIdentitiesExposeSameRootV1(topLevel, root, test.path)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("root exposure for %q = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestRuntimeHostMountSharesProtectedTreeV1(t *testing.T) {
	data := []byte("41 1 0:1 / / rw - ext4 /dev/root rw\n" +
		"42 41 0:2 / /dev rw - tmpfs tmpfs rw\n" +
		"43 42 0:3 / /dev/shm rw - tmpfs shm rw\n" +
		"44 41 0:3 / /safe/shm rw - tmpfs shm rw\n" +
		"45 41 0:4 / /run rw - tmpfs tmpfs rw\n" +
		"46 41 0:3 /session /safe/session rw - tmpfs shm rw\n" +
		"47 41 0:5 / /sys/fs/resctrl rw - resctrl resctrl rw\n" +
		"48 41 0:5 / /safe/resctrl rw - resctrl resctrl rw\n")

	for _, test := range []struct {
		name      string
		candidate uint64
		path      string
		tree      string
		want      bool
	}{
		{name: "dev shm alias", candidate: 44, path: "/safe/shm", tree: "/dev", want: true},
		{name: "dev shm subdirectory alias", candidate: 46, path: "/safe/session", tree: "/dev", want: true},
		{name: "sys resctrl alias", candidate: 48, path: "/safe/resctrl", tree: "/sys", want: true},
		{name: "unrelated tmpfs", candidate: 45, path: "/run", tree: "/dev", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := runtimeHostMountSharesProtectedTreeV1(data, test.candidate, test.path, test.tree)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("protected tree match = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRuntimeHostMountSharesProtectedTreeV1UsesEffectiveBackingPath(t *testing.T) {
	data := []byte("41 1 0:1 / / rw - ext4 /dev/root rw\n" +
		"42 41 0:3 / /run rw - tmpfs tmpfs rw\n" +
		"43 41 0:3 /protected /dev/protected rw - tmpfs tmpfs rw\n")
	candidate, found, err := runtimeHostMountIdentityByIDV1(data, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("candidate mount not found")
	}

	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/run", want: true},
		{path: "/run/protected", want: true},
		{path: "/run/protected/child", want: true},
		{path: "/run/ordinary", want: false},
	} {
		got, err := runtimeHostMountIdentitySharesProtectedTreeV1(data, candidate, test.path, "/dev")
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("protected mount exposure for %q = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestRuntimeHostMountSharesProtectedTreeV1IgnoresHiddenMount(t *testing.T) {
	data := []byte("41 41 0:1 / / rw - ext4 /dev/root rw\n" +
		"42 41 0:2 / /dev rw - tmpfs tmpfs rw\n" +
		"43 42 0:3 /secret /dev/x rw - tmpfs hidden rw\n" +
		"44 43 0:4 / /dev/x rw - tmpfs visible rw\n" +
		"45 41 0:3 / /safe rw - tmpfs candidate rw\n")
	candidate, found, err := runtimeHostMountIdentityByIDV1(data, 45)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("candidate mount not found")
	}

	got, err := runtimeHostMountIdentitySharesProtectedTreeV1(data, candidate, "/safe/secret", "/dev")
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("hidden protected-tree mount was treated as visible")
	}
}
