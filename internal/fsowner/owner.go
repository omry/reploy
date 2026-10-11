// Package fsowner preserves the owner of private metadata during privileged
// preparation. It never changes modes or follows a symlink to change ownership.
package fsowner

import (
	"fmt"
	"io/fs"
	"os"
)

type Owner struct {
	uid, gid int
	enabled  bool
}

func (owner Owner) Enabled() bool { return owner.enabled }

func (owner Owner) IDs() (int, int) { return owner.uid, owner.gid }

// Directory derives authority from a real directory and its open descriptor,
// rather than from sudo or login environment variables.
func Directory(path string) (Owner, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return Owner{}, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return Owner{}, fmt.Errorf("metadata owner path must be a real directory: %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return Owner{}, err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return Owner{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return Owner{}, err
	}
	after, err := os.Lstat(path)
	if err != nil || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return Owner{}, fmt.Errorf("metadata owner directory changed: %s", path)
	}
	return FromInfo(opened), nil
}

// File acts only on a descriptor supplied by the metadata writer. Existing
// externally linked files are rejected, so ownership cannot escape the source.
func (owner Owner) File(file *os.File) error {
	if !owner.enabled {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("metadata ownership requires a regular file or directory")
	}
	if sameOwner(info, owner) {
		return nil
	}
	if info.Mode().IsRegular() && multipleLinks(info) {
		return fmt.Errorf("refuse ownership change of multiply linked metadata")
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return fmt.Errorf("refuse ownership change of set-ID metadata")
	}
	return file.Chown(owner.uid, owner.gid)
}

func (owner Owner) At(root *os.Root, path string) error {
	if !owner.enabled {
		return nil
	}
	before, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse symlink metadata ownership: %s", path)
	}
	if !before.IsDir() && !before.Mode().IsRegular() {
		return fmt.Errorf("refuse special metadata ownership: %s", path)
	}
	file, err := root.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	after, err := root.Lstat(path)
	if err != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return fmt.Errorf("metadata path changed before ownership transfer: %s", path)
	}
	return owner.File(file)
}

// Tree is restricted to newly created private scratch roots held by their
// creator. Descendants transfer first; symlink targets are never visited.
func (owner Owner) Tree(root *os.Root) error {
	if !owner.enabled {
		return nil
	}
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	// A removed scratch root has no surviving entries to transfer. Checking
	// the held inode avoids suppressing missing-child errors in live trees.
	if removedDirectory(info) {
		return nil
	}
	var paths []string
	if err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink == 0 {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return err
	}
	for index := len(paths) - 1; index >= 0; index-- {
		if err := owner.At(root, paths[index]); err != nil {
			return err
		}
	}
	return nil
}
