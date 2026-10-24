//go:build !windows

package deploy

import (
	"os"

	"golang.org/x/sys/unix"
)

func openPackageOverridesFile(path string) (*os.File, error) {
	// Do not follow a replaced symlink or wait for a FIFO writer before Stat.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
