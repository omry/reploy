//go:build linux

package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const legacyDevfsSuperMagicV1 = 0x1373

func protectedRuntimeHostFilesystemV1(path string) (string, error) {
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return "", err
	}
	switch uint64(filesystem.Type) {
	case unix.PROC_SUPER_MAGIC:
		return "procfs", nil
	case unix.SYSFS_MAGIC:
		return "sysfs", nil
	case unix.DEVPTS_SUPER_MAGIC:
		return "devpts", nil
	case legacyDevfsSuperMagicV1:
		return "devfs", nil
	case unix.TMPFS_MAGIC:
		// devtmpfs deliberately shares tmpfs's superblock implementation. Use
		// the exact mount identity to distinguish it without rejecting ordinary
		// tmpfs sources.
	default:
		return "", nil
	}

	kind, err := runtimeHostMountFilesystemV1(path)
	if err != nil {
		return "", err
	}
	switch kind {
	case "devtmpfs":
		return kind, nil
	default:
		return "", nil
	}
}

func runtimeHostMountFilesystemV1(path string) (string, error) {
	var status unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_SYNC_AS_STAT, unix.STATX_MNT_ID, &status)
	if err != nil && !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP) {
		return "", fmt.Errorf("statx mount identity: %w", err)
	}
	if err != nil || status.Mask&unix.STATX_MNT_ID == 0 {
		return "", nil
	}

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	kind, found, err := runtimeHostMountFilesystemByIDV1(data, status.Mnt_id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("mount ID %d is absent from /proc/self/mountinfo", status.Mnt_id)
	}
	return kind, nil
}

func runtimeHostMountFilesystemByIDV1(data []byte, mountID uint64) (string, bool, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		candidate, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return "", false, fmt.Errorf("parse mount ID %q: %w", fields[0], err)
		}
		if candidate != mountID {
			continue
		}
		kind, err := runtimeHostMountFilesystemFromFieldsV1(fields)
		return kind, true, err
	}
	return "", false, nil
}

func runtimeHostMountFilesystemFromFieldsV1(fields []string) (string, error) {
	for index, field := range fields {
		if field == "-" {
			if index+1 >= len(fields) {
				return "", fmt.Errorf("mountinfo record is missing filesystem type")
			}
			return fields[index+1], nil
		}
	}
	return "", fmt.Errorf("mountinfo record is missing field separator")
}
