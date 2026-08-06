//go:build linux

package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	legacyDevfsSuperMagicV1 = 0x1373
	fuseCtlSuperMagicV1     = 0x65735543
	mqueueMagicV1           = 0x19800202
)

func protectedRuntimeHostPathV1(path string) (string, error) {
	return protectedRuntimeHostPathWithResolverV1(path, runtimeHostResolveNoMagicLinksV1)
}

func runtimeHostResolveNoMagicLinksV1(path string) error {
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   uint64(unix.O_PATH | unix.O_CLOEXEC),
		Resolve: unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

func protectedRuntimeHostPathWithResolverV1(path string, resolve func(string) error) (string, error) {
	err := resolve(path)
	if err == nil {
		return "", nil
	}
	if errors.Is(err, unix.ELOOP) {
		return "procfs magic link", nil
	}
	if errors.Is(err, unix.ENOSYS) {
		symlink, err := runtimeHostPathContainsSymlinkV1(path)
		if err != nil {
			return "", fmt.Errorf("inspect host path without openat2: %w", err)
		}
		if symlink {
			return "", fmt.Errorf("kernel does not support safe validation of symlinked host paths")
		}
		return "", nil
	}
	return "", fmt.Errorf("resolve without procfs magic links: %w", err)
}

func runtimeHostPathContainsSymlinkV1(path string) (bool, error) {
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == "." || component == ".." {
			return false, fmt.Errorf("host path contains unsupported %q traversal without openat2", component)
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	current := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(filepath.Clean(absolute), current), current) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}

func protectedRuntimeHostFilesystemV1(path string) (string, error) {
	rootFilesystem, err := runtimeHostSharesRootFilesystemV1(path)
	if err != nil {
		return "", err
	}
	if rootFilesystem {
		return "host filesystem root", nil
	}
	mountFilesystem, err := runtimeHostMountFilesystemV1(path)
	if err != nil {
		return "", err
	}
	if protectedRuntimeHostFilesystemNameV1(mountFilesystem) {
		if mountFilesystem == "proc" {
			return "procfs", nil
		}
		return mountFilesystem, nil
	}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return "", err
	}
	filesystemMagic := uint64(filesystem.Type)
	if kind := protectedRuntimeHostFilesystemKindV1(filesystemMagic); kind != "" {
		return kind, nil
	}
	protectedMount, err := runtimeHostSharesProtectedMountV1(path)
	if err != nil {
		return "", err
	}
	if protectedMount {
		return "protected host submount", nil
	}
	protectedSubmount, err := runtimeHostContainsProtectedSubmountV1(path)
	if err != nil {
		return "", err
	}
	if protectedSubmount {
		return "protected nested host submount", nil
	}
	if filesystemMagic != unix.TMPFS_MAGIC {
		return "", nil
	}
	// devtmpfs deliberately shares tmpfs's superblock implementation. Use
	// the exact mount identity to distinguish it without rejecting ordinary
	// tmpfs sources.

	kind, err := runtimeHostMountFilesystemV1(path)
	if err != nil {
		return "", err
	}
	switch kind {
	case "devtmpfs":
		return kind, nil
	}
	devFilesystem, err := runtimeHostSharesDedicatedDevFilesystemV1(path)
	if err != nil {
		return "", err
	}
	if devFilesystem {
		return "host /dev filesystem", nil
	}
	return "", nil
}

func protectedRuntimeHostFilesystemKindV1(filesystemMagic uint64) string {
	switch filesystemMagic {
	case unix.PROC_SUPER_MAGIC:
		return "procfs"
	case unix.SYSFS_MAGIC:
		return "sysfs"
	case unix.DEVPTS_SUPER_MAGIC:
		return "devpts"
	case legacyDevfsSuperMagicV1:
		return "devfs"
	case unix.CGROUP_SUPER_MAGIC:
		return "cgroup"
	case unix.CGROUP2_SUPER_MAGIC:
		return "cgroup2"
	case unix.DEBUGFS_MAGIC:
		return "debugfs"
	case unix.TRACEFS_MAGIC:
		return "tracefs"
	case unix.SECURITYFS_MAGIC:
		return "securityfs"
	case unix.BPF_FS_MAGIC:
		return "bpf"
	case unix.BINFMTFS_MAGIC:
		return "binfmt_misc"
	case unix.EFIVARFS_MAGIC:
		return "efivarfs"
	case unix.NSFS_MAGIC:
		return "nsfs"
	case unix.PSTOREFS_MAGIC:
		return "pstore"
	case unix.SELINUX_MAGIC:
		return "selinuxfs"
	case fuseCtlSuperMagicV1:
		return "fusectl"
	case mqueueMagicV1:
		return "mqueue"
	}
	return ""
}

func runtimeHostSharesDedicatedDevFilesystemV1(path string) (bool, error) {
	var candidate unix.Stat_t
	if err := unix.Stat(path, &candidate); err != nil {
		return false, fmt.Errorf("stat candidate filesystem: %w", err)
	}
	var dev unix.Stat_t
	if err := unix.Stat("/dev", &dev); err != nil {
		return false, fmt.Errorf("stat /dev filesystem: %w", err)
	}
	var root unix.Stat_t
	if err := unix.Stat("/", &root); err != nil {
		return false, fmt.Errorf("stat root filesystem: %w", err)
	}
	if dev.Dev == root.Dev {
		return false, nil
	}
	return candidate.Dev == dev.Dev, nil
}

func runtimeHostSharesProtectedMountV1(path string) (bool, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	candidate, err := runtimeHostMountIdentityForPathV1(data, path)
	if err != nil {
		return false, err
	}
	for _, protectedTree := range []string{"/proc", "/dev", "/sys"} {
		matched, err := runtimeHostMountIdentitySharesProtectedTreeV1(data, candidate, path, protectedTree)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

func runtimeHostContainsProtectedSubmountV1(path string) (bool, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	return runtimeHostMountContainsProtectedSubmountV1(data, path)
}

func runtimeHostMountContainsProtectedSubmountV1(data []byte, path string) (bool, error) {
	cleanPath := filepath.Clean(path)
	root, err := runtimeHostMountIdentityByPathV1(data, string(filepath.Separator))
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		identity, err := runtimeHostMountIdentityFromFieldsV1(fields)
		if err != nil {
			return false, err
		}
		mountPoint := filepath.Clean(identity.mountPoint)
		if mountPoint == cleanPath || !pathWithinV1(mountPoint, cleanPath) {
			continue
		}
		visible, err := runtimeHostMountIdentityByPathV1(data, mountPoint)
		if err != nil {
			return false, fmt.Errorf("resolve visible mount at %q: %w", mountPoint, err)
		}
		if visible.mountID != identity.mountID {
			continue
		}
		if protectedRuntimeHostFilesystemNameV1(identity.filesystem) {
			return true, nil
		}
		exposesRoot, err := runtimeHostMountIdentitiesExposeSameRootV1(identity, root, mountPoint)
		if err != nil {
			return false, err
		}
		if exposesRoot {
			return true, nil
		}
		for _, protectedTree := range []string{"/proc", "/dev", "/sys"} {
			exposesProtectedTree, err := runtimeHostMountIdentitySharesProtectedTreeV1(data, identity, mountPoint, protectedTree)
			if err != nil {
				return false, err
			}
			if exposesProtectedTree {
				return true, nil
			}
		}
	}
	return false, nil
}

func protectedRuntimeHostFilesystemNameV1(filesystem string) bool {
	switch filesystem {
	case "anon_inodefs", "bdev", "binder", "binderfs", "binfmt_misc", "bpf",
		"cgroup", "cgroup2", "configfs", "cpuset", "debugfs", "devfs",
		"devmem", "devpts", "devtmpfs", "dma_buf", "efivarfs", "fusectl",
		"futexfs", "hugetlbfs", "mqueue", "nfsd", "nsfs", "pipefs", "proc",
		"pstore", "resctrl", "rpc_pipefs", "secretmem", "securityfs",
		"selinuxfs", "smackfs", "sockfs", "sysfs", "tracefs", "usbfs", "xenfs":
		return true
	default:
		return false
	}
}

func runtimeHostMountFilesystemV1(path string) (string, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	identity, err := runtimeHostMountIdentityForPathV1(data, path)
	if err != nil {
		return "", err
	}
	return identity.filesystem, nil
}

func runtimeHostSharesRootFilesystemV1(path string) (bool, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	candidate, err := runtimeHostMountIdentityForPathV1(data, path)
	if err != nil {
		return false, err
	}
	root, err := runtimeHostMountIdentityForPathV1(data, "/")
	if err != nil {
		return false, err
	}
	return runtimeHostMountIdentitiesExposeSameRootV1(candidate, root, path)
}

func runtimeHostMountIDV1(path string) (uint64, bool, error) {
	var status unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_SYNC_AS_STAT, unix.STATX_MNT_ID, &status)
	if err != nil && !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP) {
		return 0, false, fmt.Errorf("statx mount identity: %w", err)
	}
	if err != nil || status.Mask&unix.STATX_MNT_ID == 0 {
		return 0, false, nil
	}
	return status.Mnt_id, true, nil
}

func runtimeHostMountFilesystemByIDV1(data []byte, mountID uint64) (string, bool, error) {
	identity, found, err := runtimeHostMountIdentityByIDV1(data, mountID)
	return identity.filesystem, found, err
}

type runtimeHostMountIdentityV1 struct {
	mountID    uint64
	parentID   uint64
	device     string
	root       string
	mountPoint string
	filesystem string
}

func runtimeHostMountIdentityForPathV1(data []byte, path string) (runtimeHostMountIdentityV1, error) {
	return runtimeHostMountIdentityForPathWithResolverV1(data, path, runtimeHostMountIDV1)
}

func runtimeHostMountIdentityForPathWithResolverV1(
	data []byte,
	path string,
	resolveMountID func(string) (uint64, bool, error),
) (runtimeHostMountIdentityV1, error) {
	mountID, found, err := resolveMountID(path)
	if err != nil {
		return runtimeHostMountIdentityV1{}, err
	}
	if found {
		identity, found, err := runtimeHostMountIdentityByIDV1(data, mountID)
		if err != nil {
			return runtimeHostMountIdentityV1{}, err
		}
		if !found {
			return runtimeHostMountIdentityV1{}, fmt.Errorf("mount ID %d is absent from /proc/self/mountinfo", mountID)
		}
		return identity, nil
	}
	return runtimeHostMountIdentityByPathV1(data, path)
}

func runtimeHostMountIdentityByPathV1(data []byte, path string) (runtimeHostMountIdentityV1, error) {
	cleanPath := filepath.Clean(path)
	byMountPoint := make(map[string][]runtimeHostMountIdentityV1)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		identity, err := runtimeHostMountIdentityFromFieldsV1(fields)
		if err != nil {
			return runtimeHostMountIdentityV1{}, err
		}
		if !pathWithinV1(cleanPath, identity.mountPoint) {
			continue
		}
		mountPoint := filepath.Clean(identity.mountPoint)
		byMountPoint[mountPoint] = append(byMountPoint[mountPoint], identity)
	}
	rootMounts := byMountPoint[string(filepath.Separator)]
	visible, err := runtimeHostTopmostMountAtLocationV1(rootMounts)
	if err != nil {
		return runtimeHostMountIdentityV1{}, fmt.Errorf("resolve visible root mount: %w", err)
	}
	if visible.mountPoint == "" {
		return runtimeHostMountIdentityV1{}, fmt.Errorf("host path %q is absent from /proc/self/mountinfo", path)
	}

	mountPoints := make([]string, 0, len(byMountPoint))
	for mountPoint := range byMountPoint {
		if mountPoint != string(filepath.Separator) {
			mountPoints = append(mountPoints, mountPoint)
		}
	}
	sort.Slice(mountPoints, func(left int, right int) bool {
		return len(mountPoints[left]) < len(mountPoints[right])
	})
	for _, mountPoint := range mountPoints {
		candidate, found, err := runtimeHostVisibleMountAtLocationV1(byMountPoint[mountPoint], visible.mountID)
		if err != nil {
			return runtimeHostMountIdentityV1{}, fmt.Errorf("resolve visible mount at %q: %w", mountPoint, err)
		}
		if found {
			visible = candidate
		}
	}
	return visible, nil
}

func runtimeHostTopmostMountAtLocationV1(mounts []runtimeHostMountIdentityV1) (runtimeHostMountIdentityV1, error) {
	parents := make(map[uint64]struct{}, len(mounts))
	for _, mount := range mounts {
		if mount.parentID != mount.mountID {
			parents[mount.parentID] = struct{}{}
		}
	}
	var top runtimeHostMountIdentityV1
	found := false
	for _, mount := range mounts {
		if _, hidden := parents[mount.mountID]; hidden {
			continue
		}
		if found {
			return runtimeHostMountIdentityV1{}, fmt.Errorf("mount topology has multiple topmost entries")
		}
		top = mount
		found = true
	}
	return top, nil
}

func runtimeHostVisibleMountAtLocationV1(
	mounts []runtimeHostMountIdentityV1,
	visibleParentID uint64,
) (runtimeHostMountIdentityV1, bool, error) {
	children := make(map[uint64][]runtimeHostMountIdentityV1, len(mounts))
	for _, mount := range mounts {
		children[mount.parentID] = append(children[mount.parentID], mount)
	}
	currentID := visibleParentID
	var visible runtimeHostMountIdentityV1
	found := false
	seen := make(map[uint64]struct{}, len(mounts))
	for {
		candidates := children[currentID]
		if len(candidates) == 0 {
			return visible, found, nil
		}
		if len(candidates) != 1 {
			return runtimeHostMountIdentityV1{}, false, fmt.Errorf("mount topology has multiple visible children")
		}
		next := candidates[0]
		if _, duplicate := seen[next.mountID]; duplicate {
			return runtimeHostMountIdentityV1{}, false, fmt.Errorf("mount topology contains a cycle")
		}
		seen[next.mountID] = struct{}{}
		visible = next
		found = true
		currentID = next.mountID
	}
}

func runtimeHostMountsExposeSameRootV1(data []byte, candidateID uint64, rootID uint64, path string) (bool, error) {
	if candidateID == rootID {
		return false, nil
	}
	candidate, found, err := runtimeHostMountIdentityByIDV1(data, candidateID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("mount ID %d is absent from /proc/self/mountinfo", candidateID)
	}
	root, found, err := runtimeHostMountIdentityByIDV1(data, rootID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("root mount ID %d is absent from /proc/self/mountinfo", rootID)
	}
	return runtimeHostMountIdentitiesExposeSameRootV1(candidate, root, path)
}

func runtimeHostMountIdentitiesExposeSameRootV1(candidate runtimeHostMountIdentityV1, root runtimeHostMountIdentityV1, path string) (bool, error) {
	if candidate.device != root.device {
		return false, nil
	}
	effective, err := runtimeHostEffectiveBackingPathV1(candidate, path)
	if err != nil {
		return false, err
	}
	return pathWithinV1(root.root, effective), nil
}

func runtimeHostMountSharesProtectedTreeV1(data []byte, candidateID uint64, path string, protectedTree string) (bool, error) {
	candidate, found, err := runtimeHostMountIdentityByIDV1(data, candidateID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("mount ID %d is absent from /proc/self/mountinfo", candidateID)
	}
	return runtimeHostMountIdentitySharesProtectedTreeV1(data, candidate, path, protectedTree)
}

func runtimeHostMountIdentitySharesProtectedTreeV1(data []byte, candidate runtimeHostMountIdentityV1, path string, protectedTree string) (bool, error) {
	effective, err := runtimeHostEffectiveBackingPathV1(candidate, path)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		identity, err := runtimeHostMountIdentityFromFieldsV1(fields)
		if err != nil {
			return false, err
		}
		if !pathWithinV1(identity.mountPoint, protectedTree) || identity.device != candidate.device {
			continue
		}
		visible, err := runtimeHostMountIdentityByPathV1(data, identity.mountPoint)
		if err != nil {
			return false, fmt.Errorf("resolve visible mount at %q: %w", identity.mountPoint, err)
		}
		if visible.mountID != identity.mountID {
			continue
		}
		if pathWithinV1(effective, identity.root) || pathWithinV1(identity.root, effective) {
			return true, nil
		}
	}
	return false, nil
}

func runtimeHostEffectiveBackingPathV1(identity runtimeHostMountIdentityV1, path string) (string, error) {
	if !pathWithinV1(path, identity.mountPoint) {
		return "", fmt.Errorf("host path %q is outside mount point %q", path, identity.mountPoint)
	}
	relative, err := filepath.Rel(filepath.Clean(identity.mountPoint), filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve path within host mount: %w", err)
	}
	return filepath.Clean(filepath.Join(identity.root, relative)), nil
}

func runtimeHostMountIdentityByIDV1(data []byte, mountID uint64) (runtimeHostMountIdentityV1, bool, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		candidate, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return runtimeHostMountIdentityV1{}, false, fmt.Errorf("parse mount ID %q: %w", fields[0], err)
		}
		if candidate != mountID {
			continue
		}
		identity, err := runtimeHostMountIdentityFromFieldsV1(fields)
		return identity, true, err
	}
	return runtimeHostMountIdentityV1{}, false, nil
}

func runtimeHostMountFilesystemFromFieldsV1(fields []string) (string, error) {
	identity, err := runtimeHostMountIdentityFromFieldsV1(fields)
	return identity.filesystem, err
}

func runtimeHostMountIdentityFromFieldsV1(fields []string) (runtimeHostMountIdentityV1, error) {
	if len(fields) < 5 {
		return runtimeHostMountIdentityV1{}, fmt.Errorf("mountinfo record is missing identity fields")
	}
	mountID, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return runtimeHostMountIdentityV1{}, fmt.Errorf("parse mount ID %q: %w", fields[0], err)
	}
	parentID, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return runtimeHostMountIdentityV1{}, fmt.Errorf("parse parent mount ID %q: %w", fields[1], err)
	}
	for index, field := range fields {
		if field == "-" {
			if index+1 >= len(fields) {
				return runtimeHostMountIdentityV1{}, fmt.Errorf("mountinfo record is missing filesystem type")
			}
			return runtimeHostMountIdentityV1{
				mountID:    mountID,
				parentID:   parentID,
				device:     fields[2],
				root:       runtimeHostMountPathV1(fields[3]),
				mountPoint: runtimeHostMountPathV1(fields[4]),
				filesystem: fields[index+1],
			}, nil
		}
	}
	return runtimeHostMountIdentityV1{}, fmt.Errorf("mountinfo record is missing field separator")
}

func runtimeHostMountPathV1(path string) string {
	return strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	).Replace(path)
}
