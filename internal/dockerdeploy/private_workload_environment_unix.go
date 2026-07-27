//go:build !windows

package dockerdeploy

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func readPrivateWorkloadEnvironmentFileV1(deploymentDir string) (content []byte, found bool, err error) {
	directory, err := os.Open(deploymentDir)
	if err != nil {
		return nil, false, fmt.Errorf("open deployment directory for %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	defer func() {
		if closeErr := directory.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	var directoryStat unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &directoryStat); err != nil {
		return nil, false, fmt.Errorf("inspect deployment directory for %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	descriptor, err := unix.Openat(
		int(directory.Fd()),
		PrivateWorkloadEnvironmentFileName,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open %s without following links: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	file := os.NewFile(uintptr(descriptor), PrivateWorkloadEnvironmentFileName)
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, false, fmt.Errorf("open %s: invalid file descriptor", PrivateWorkloadEnvironmentFileName)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	var stat unix.Stat_t
	if err := unix.Fstat(descriptor, &stat); err != nil {
		return nil, false, fmt.Errorf("inspect opened %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, false, fmt.Errorf("%s must be a real regular file", PrivateWorkloadEnvironmentFileName)
	}
	if stat.Nlink != 1 {
		return nil, false, fmt.Errorf("%s must not have hard links", PrivateWorkloadEnvironmentFileName)
	}
	if stat.Uid != directoryStat.Uid {
		return nil, false, fmt.Errorf("%s must be owned by the deployment directory owner", PrivateWorkloadEnvironmentFileName)
	}
	permissions := stat.Mode & 0o7777
	if permissions&0o400 == 0 || permissions&0o177 != 0 || permissions&0o7000 != 0 {
		return nil, false, fmt.Errorf("%s permissions must be 0400 or 0600 (owner-readable with no execute, group, other, or special access); found %04o", PrivateWorkloadEnvironmentFileName, permissions)
	}
	limited := io.LimitReader(file, privateWorkloadEnvironmentMaxBytes+1)
	content, err = io.ReadAll(limited)
	if err != nil {
		return nil, false, fmt.Errorf("read opened %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	if len(content) > privateWorkloadEnvironmentMaxBytes {
		return nil, false, fmt.Errorf("%s exceeds the %d-byte limit", PrivateWorkloadEnvironmentFileName, privateWorkloadEnvironmentMaxBytes)
	}
	return content, true, nil
}

func publishPrivateWorkloadEnvironmentFileV1(target string, content []byte, replace bool) (created bool, err error) {
	if replace {
		prepared, err := prepareProviderInstallFileCandidatesV1([]providerInstallFileCandidateV1{{
			Path: target, Content: content, Mode: 0o600,
		}})
		if err != nil {
			return false, err
		}
		defer func() {
			if cleanupErr := prepared.Cleanup(); err == nil && cleanupErr != nil {
				err = cleanupErr
			}
		}()
		if err := prepared.Publish(); err != nil {
			return false, err
		}
		return true, nil
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := file.Write(content); err != nil {
		return false, errors.Join(err, file.Close(), os.Remove(target))
	}
	if err := file.Sync(); err != nil {
		return false, errors.Join(err, file.Close(), os.Remove(target))
	}
	if err := file.Close(); err != nil {
		return false, errors.Join(err, os.Remove(target))
	}
	return true, nil
}
