//go:build !windows

package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// copyProviderInstallManagedBindV1 walks the staging tree through held
// directory descriptors. Every child is opened relative to its already-open
// parent with O_NOFOLLOW, so a user-writable staging tree cannot redirect a
// privileged system install outside the checked root while it is being copied.
func copyProviderInstallManagedBindV1(ctx context.Context, sourceRoot string, targetRoot string, locked lockedProviderInstallV1) error {
	fd, err := unix.Open(sourceRoot, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open staging managed mount root without following links: %w", err)
	}
	root := os.NewFile(uintptr(fd), sourceRoot)
	if root == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("open staging managed mount root returned an invalid handle")
	}
	defer root.Close()
	info, err := root.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("staging managed mount root is not a directory: %s", sourceRoot)
	}
	return copyProviderInstallManagedDirectoryV1(ctx, root, sourceRoot, targetRoot, info, locked)
}

func copyProviderInstallManagedDirectoryV1(
	ctx context.Context,
	source *os.File,
	sourcePath string,
	targetPath string,
	info os.FileInfo,
	locked lockedProviderInstallV1,
) error {
	entries, err := source.Readdir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		childSourcePath := filepath.Join(sourcePath, name)
		childTargetPath := filepath.Join(targetPath, name)
		child, openedInfo, err := openProviderInstallManagedChildV1(source, entry, childSourcePath)
		if err != nil {
			return err
		}
		switch {
		case openedInfo.IsDir():
			if err := os.Mkdir(childTargetPath, 0o700); err != nil {
				_ = child.Close()
				return err
			}
			err = copyProviderInstallManagedDirectoryV1(ctx, child, childSourcePath, childTargetPath, openedInfo, locked)
		case openedInfo.Mode().IsRegular():
			err = copyProviderInstallManagedFileHandleV1(child, childTargetPath, openedInfo.Mode().Perm(), locked)
		default:
			err = fmt.Errorf("refusing to copy special file: %s", childSourcePath)
		}
		closeErr := child.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
	}
	return applyProviderInstallManagedMetadataV1(targetPath, info.Mode().Perm(), locked)
}

func openProviderInstallManagedChildV1(parent *os.File, entry os.FileInfo, sourcePath string) (*os.File, os.FileInfo, error) {
	name := entry.Name()
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return nil, nil, fmt.Errorf("staging managed mount contains an invalid entry name %q", name)
	}
	if entry.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("refusing to copy symlink: %s", sourcePath)
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if entry.IsDir() {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, nil, fmt.Errorf("refusing to copy symlink: %s", sourcePath)
		}
		return nil, nil, fmt.Errorf("open staging managed mount entry %s: %w", sourcePath, err)
	}
	child := os.NewFile(uintptr(fd), sourcePath)
	if child == nil {
		_ = unix.Close(fd)
		return nil, nil, fmt.Errorf("open staging managed mount entry returned an invalid handle: %s", sourcePath)
	}
	openedInfo, err := child.Stat()
	if err != nil {
		_ = child.Close()
		return nil, nil, err
	}
	if !os.SameFile(entry, openedInfo) {
		_ = child.Close()
		return nil, nil, fmt.Errorf("staging managed mount entry changed while copying: %s", sourcePath)
	}
	return child, openedInfo, nil
}

func copyProviderInstallManagedFileHandleV1(source *os.File, targetPath string, mode os.FileMode, locked lockedProviderInstallV1) (err error) {
	target, err := openInstallTargetNoFollow(targetPath, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, target.Close()) }()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return applyProviderInstallManagedMetadataV1(targetPath, mode, locked)
}

func applyProviderInstallManagedMetadataV1(path string, mode os.FileMode, locked lockedProviderInstallV1) error {
	if locked.Input.Install.Scope == InstallScopeSystem {
		if err := os.Chown(path, locked.Input.Install.SystemUID, locked.Input.Install.SystemGID); err != nil {
			return fmt.Errorf("set installed managed mount ownership for %s: %w", path, err)
		}
	}
	return os.Chmod(path, mode)
}
