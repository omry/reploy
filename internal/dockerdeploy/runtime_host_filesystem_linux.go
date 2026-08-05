//go:build linux

package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   uint64(unix.O_PATH | unix.O_CLOEXEC),
		Resolve: unix.RESOLVE_NO_MAGICLINKS,
	})
	if err == nil {
		if err := unix.Close(fd); err != nil {
			return "", fmt.Errorf("close validated host path: %w", err)
		}
		return "", nil
	}
	if errors.Is(err, unix.ELOOP) {
		return "procfs magic link", nil
	}
	if errors.Is(err, unix.ENOSYS) {
		return "", fmt.Errorf("kernel does not support no-magic-link host path validation")
	}
	return "", fmt.Errorf("resolve without procfs magic links: %w", err)
}

func protectedRuntimeHostFilesystemV1(path string) (string, error) {
	rootFilesystem, err := runtimeHostSharesRootFilesystemV1(path)
	if err != nil {
		return "", err
	}
	if rootFilesystem {
		return "host filesystem root", nil
	}

	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return "", err
	}
	filesystemMagic := uint64(filesystem.Type)
	if kind := protectedRuntimeHostFilesystemKindV1(filesystemMagic); kind != "" {
		return kind, nil
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
	devSubmount, err := runtimeHostSharesProtectedTmpfsV1(path, "/dev")
	if err != nil {
		return "", err
	}
	if devSubmount {
		return "host /dev tmpfs", nil
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

func runtimeHostSharesProtectedTmpfsV1(path string, protectedTree string) (bool, error) {
	candidateID, found, err := runtimeHostMountIDV1(path)
	if err != nil || !found {
		return false, err
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	return runtimeHostMountSharesProtectedTreeV1(data, candidateID, protectedTree)
}

func runtimeHostMountFilesystemV1(path string) (string, error) {
	mountID, found, err := runtimeHostMountIDV1(path)
	if err != nil || !found {
		return "", err
	}

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	kind, found, err := runtimeHostMountFilesystemByIDV1(data, mountID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("mount ID %d is absent from /proc/self/mountinfo", mountID)
	}
	return kind, nil
}

func runtimeHostSharesRootFilesystemV1(path string) (bool, error) {
	candidateID, found, err := runtimeHostMountIDV1(path)
	if err != nil || !found {
		return false, err
	}
	rootID, found, err := runtimeHostMountIDV1("/")
	if err != nil || !found || candidateID == rootID {
		return false, err
	}

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	return runtimeHostMountsExposeSameRootV1(data, candidateID, rootID, path)
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
	device     string
	root       string
	mountPoint string
	filesystem string
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
	return filepath.Clean(path) == filepath.Clean(candidate.mountPoint) &&
		candidate.device == root.device && candidate.root == root.root, nil
}

func runtimeHostMountSharesProtectedTreeV1(data []byte, candidateID uint64, protectedTree string) (bool, error) {
	candidate, found, err := runtimeHostMountIdentityByIDV1(data, candidateID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("mount ID %d is absent from /proc/self/mountinfo", candidateID)
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
		if pathWithinV1(candidate.root, identity.root) || pathWithinV1(identity.root, candidate.root) {
			return true, nil
		}
	}
	return false, nil
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
	for index, field := range fields {
		if field == "-" {
			if index+1 >= len(fields) {
				return runtimeHostMountIdentityV1{}, fmt.Errorf("mountinfo record is missing filesystem type")
			}
			return runtimeHostMountIdentityV1{
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
