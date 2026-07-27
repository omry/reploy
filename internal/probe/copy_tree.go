package probe

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	VolumeCopySourceRoot = "/.reploy-volume-copy/source"
	VolumeCopyTargetRoot = "/.reploy-volume-copy/target"
)

type copiedDirectory struct {
	path string
	info os.FileInfo
}

func copyFixedVolumeTree() error {
	return copyVolumeTree(VolumeCopySourceRoot, VolumeCopyTargetRoot)
}

func copyVolumeTree(sourceRoot string, targetRoot string) error {
	sourceRootInfo, err := requireVolumeCopyDirectory("source", sourceRoot)
	if err != nil {
		return err
	}
	if _, err := requireVolumeCopyDirectory("target", targetRoot); err != nil {
		return err
	}
	entries, err := os.ReadDir(targetRoot)
	if err != nil {
		return fmt.Errorf("read target root: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("target root is not empty")
	}

	directories := []copiedDirectory{}
	hardlinks := map[string]string{}
	err = filepath.Walk(sourceRoot, func(sourcePath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if relative == ".." || filepath.IsAbs(relative) {
			return fmt.Errorf("source entry escapes its root: %s", sourcePath)
		}
		targetPath := filepath.Join(targetRoot, relative)
		switch {
		case info.IsDir():
			if err := os.Mkdir(targetPath, 0o700); err != nil {
				return err
			}
			directories = append(directories, copiedDirectory{path: targetPath, info: info})
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(sourcePath)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, targetPath); err != nil {
				return err
			}
			return copyVolumeTreeOwnership(info, targetPath, true)
		case info.Mode().IsRegular():
			if identity, linked := volumeCopyHardlinkIdentity(info); linked {
				if first, exists := hardlinks[identity]; exists {
					return os.Link(first, targetPath)
				}
				hardlinks[identity] = targetPath
			}
			if err := copyVolumeRegularFile(sourcePath, targetPath, info); err != nil {
				return err
			}
			return nil
		default:
			return fmt.Errorf("unsupported source entry type at %s", sourcePath)
		}
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		directory := directories[index]
		if err := copyVolumeTreeOwnership(directory.info, directory.path, false); err != nil {
			return err
		}
		if err := os.Chmod(directory.path, volumeCopyMode(directory.info.Mode())); err != nil {
			return err
		}
		if err := os.Chtimes(directory.path, directory.info.ModTime(), directory.info.ModTime()); err != nil {
			return err
		}
	}
	if err := copyVolumeTreeOwnership(sourceRootInfo, targetRoot, false); err != nil {
		return err
	}
	if err := os.Chmod(targetRoot, volumeCopyMode(sourceRootInfo.Mode())); err != nil {
		return err
	}
	return os.Chtimes(targetRoot, sourceRootInfo.ModTime(), sourceRootInfo.ModTime())
}

func requireVolumeCopyDirectory(label string, root string) (os.FileInfo, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, fmt.Errorf("%s root must be an absolute clean path", label)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect %s root: %w", label, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s root must be a real directory", label)
	}
	return info, nil
}

func copyVolumeRegularFile(sourcePath string, targetPath string, info os.FileInfo) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	openedInfo, err := source.Stat()
	if err != nil {
		return err
	}
	if !openedInfo.Mode().IsRegular() {
		return fmt.Errorf("source file changed type while copying: %s", sourcePath)
	}
	if !os.SameFile(info, openedInfo) {
		return fmt.Errorf("source file changed while copying: %s", sourcePath)
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = target.Close()
		}
	}()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	closed = true
	if err := copyVolumeTreeOwnership(info, targetPath, false); err != nil {
		return err
	}
	if err := os.Chmod(targetPath, volumeCopyMode(info.Mode())); err != nil {
		return err
	}
	return os.Chtimes(targetPath, info.ModTime(), info.ModTime())
}

func volumeCopyMode(mode os.FileMode) os.FileMode {
	return mode.Perm() | mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky)
}
