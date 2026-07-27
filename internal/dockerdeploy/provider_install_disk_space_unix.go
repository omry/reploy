//go:build linux || darwin

package dockerdeploy

import (
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

func providerInstallFilesystemSpace(path string) (providerInstallFilesystemSpaceV1, error) {
	var file unix.Stat_t
	if err := unix.Stat(path, &file); err != nil {
		return providerInstallFilesystemSpaceV1{}, err
	}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return providerInstallFilesystemSpaceV1{}, err
	}
	if filesystem.Bsize <= 0 || filesystem.Bavail < 0 {
		return providerInstallFilesystemSpaceV1{}, fmt.Errorf("filesystem reported invalid free-space values")
	}
	blockSize := uint64(filesystem.Bsize)
	availableBlocks := uint64(filesystem.Bavail)
	if availableBlocks != 0 && blockSize > math.MaxUint64/availableBlocks {
		return providerInstallFilesystemSpaceV1{}, fmt.Errorf("filesystem free-space byte count overflows uint64")
	}
	return providerInstallFilesystemSpaceV1{
		Key:       fmt.Sprintf("device:%d", uint64(file.Dev)),
		Available: availableBlocks * blockSize,
	}, nil
}
