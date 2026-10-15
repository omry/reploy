//go:build linux

package probe

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Podman compatibility workaround for rejected tmpfs uid/gid options:
// https://github.com/podman-container-tools/podman/issues/29979
// Remove only after the original mount options pass the ownership regressions.
// Only the fixed, private tmpfs mount root can receive setup ownership. Never
// walk the tree, follow a link, or accept an application-selected path.
func prepareApplicationTemporaryHomeV1(uid, gid uint32) error {
	parent, err := unix.Open("/mnt", unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, "reploy-home", unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	var fs unix.Statfs_t
	var mount unix.Statx_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return err
	}
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &mount); err != nil {
		return err
	}
	if mount.Mask&unix.STATX_MNT_ID == 0 {
		return fmt.Errorf("kernel did not identify the private home mount")
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	if err := validateApplicationHomeMountV1(string(data), mount.Mnt_id, stat, fs); err != nil {
		return err
	}
	if stat.Uid != uid || stat.Gid != gid {
		// AT_EMPTY_PATH changes the pinned directory itself, without reopening
		// a pathname. CAP_CHOWN is the only additional setup capability needed.
		if err := unix.Fchownat(fd, "", int(uid), int(gid), unix.AT_EMPTY_PATH); err != nil {
			return fmt.Errorf("set application home ownership: %w", err)
		}
	}
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Uid != uid || stat.Gid != gid || stat.Mode&0o7777 != 0o700 {
		return fmt.Errorf("private home ownership or mode verification failed")
	}
	return nil
}

func validateApplicationHomeMountV1(data string, mountID uint64, stat unix.Stat_t, fs unix.Statfs_t) error {
	flags := int64(unix.ST_NOEXEC | unix.ST_NOSUID | unix.ST_NODEV)
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o7777 != 0o700 ||
		fs.Type != unix.TMPFS_MAGIC || int64(fs.Flags)&flags != flags ||
		int64(fs.Flags)&unix.ST_RDONLY != 0 || fs.Bsize <= 0 ||
		fs.Blocks != (64*1024*1024)/uint64(fs.Bsize) {
		return fmt.Errorf("private home must be a writable 64 MiB tmpfs directory with mode 0700, noexec, nosuid and nodev")
	}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[0] != strconv.FormatUint(mountID, 10) {
			continue
		}
		separator := -1
		for index := 6; index < len(fields); index++ {
			if fields[index] == "-" {
				separator = index
				break
			}
		}
		if fields[3] == "/" && fields[4] == "/mnt/reploy-home" && separator >= 6 &&
			separator+3 < len(fields) && fields[separator+1] == "tmpfs" {
			return nil
		}
		return fmt.Errorf("private home is not the expected dedicated tmpfs mount root")
	}
	return fmt.Errorf("private home mount is absent from kernel mount information")
}
