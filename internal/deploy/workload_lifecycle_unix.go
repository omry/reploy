//go:build !windows

package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func readWorkloadLifecycleFileV1(path string) ([]byte, bool, error) {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, false, err
	}
	defer directory.Close()
	var parent unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &parent); err != nil {
		return nil, false, err
	}
	fd, err := unix.Openat(int(directory.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open workload lifecycle: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, false, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Uid != parent.Uid || stat.Mode&0o7777 != 0o600 {
		return nil, false, fmt.Errorf("workload lifecycle must be an owner-only regular file owned by the deployment owner")
	}
	content, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return nil, false, err
	}
	if len(content) > 8192 {
		return nil, false, fmt.Errorf("workload lifecycle exceeds its size limit")
	}
	return content, true, nil
}
