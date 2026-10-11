//go:build unix

package fsowner

import (
	"os"
	"syscall"
)

func FromInfo(info os.FileInfo) Owner {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Owner{}
	}
	return Owner{uid: int(stat.Uid), gid: int(stat.Gid), enabled: os.Geteuid() == 0 && stat.Uid != 0}
}

func sameOwner(info os.FileInfo, owner Owner) bool {
	stat := info.Sys().(*syscall.Stat_t)
	return int(stat.Uid) == owner.uid && int(stat.Gid) == owner.gid
}

func multipleLinks(info os.FileInfo) bool { return info.Sys().(*syscall.Stat_t).Nlink != 1 }

func removedDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Sys().(*syscall.Stat_t).Nlink == 0
}
