//go:build windows

package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func copyProviderInstallManagedBindV1(ctx context.Context, sourceRoot string, targetRoot string, _ lockedProviderInstallV1) error {
	root, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	return filepath.WalkDir(sourceRoot, func(sourcePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink: %s", sourcePath)
		}
		targetPath := filepath.Join(targetRoot, relative)
		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to copy special file: %s", sourcePath)
		}
		source, err := root.Open(relative)
		if err != nil {
			return err
		}
		openedInfo, err := source.Stat()
		if err != nil || !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
			_ = source.Close()
			return fmt.Errorf("staging managed mount entry changed while copying: %s", sourcePath)
		}
		target, err := openInstallTargetNoFollow(targetPath, info.Mode().Perm())
		if err != nil {
			_ = source.Close()
			return err
		}
		if _, err := io.Copy(target, source); err != nil {
			_ = target.Close()
			_ = source.Close()
			return err
		}
		return errors.Join(target.Close(), source.Close())
	})
}
