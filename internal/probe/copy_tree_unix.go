//go:build linux

package probe

import (
	"fmt"
	"os"
	"syscall"
)

func copyVolumeTreeOwnership(info os.FileInfo, targetPath string, symlink bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("source entry has no Unix ownership metadata")
	}
	if symlink {
		return os.Lchown(targetPath, int(stat.Uid), int(stat.Gid))
	}
	return os.Chown(targetPath, int(stat.Uid), int(stat.Gid))
}

func volumeCopyHardlinkIdentity(info os.FileInfo) (string, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink < 2 {
		return "", false
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), true
}
