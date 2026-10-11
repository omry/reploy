// Package fsowner preserves the owner of private metadata during privileged
// preparation. It never changes modes or follows a symlink to change ownership.
package fsowner

import (
	"errors"
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
	root, owner, err := OpenDirectory(path)
	if root != nil {
		_ = root.Close()
	}
	return owner, err
}

// OpenDirectory keeps the validated directory open for subsequent mutations.
// Callers must not reopen its pathname when transferring ownership.
func OpenDirectory(path string) (*os.Root, Owner, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, Owner{}, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, Owner{}, fmt.Errorf("metadata owner path must be a real directory: %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, Owner{}, err
	}
	file, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, Owner{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		_ = root.Close()
		return nil, Owner{}, err
	}
	after, err := os.Lstat(path)
	if err != nil || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		_ = root.Close()
		return nil, Owner{}, fmt.Errorf("metadata owner directory changed: %s", path)
	}
	return root, FromInfo(opened), nil
}

// EnsureDirectory creates and opens one directory beneath the held parent.
// Existing ownership is preserved; new ownership is applied to the validated
// child descriptor, without resolving the parent's pathname again.
func (owner Owner) EnsureDirectory(parent *os.Root, name string, mode os.FileMode) (*os.Root, error) {
	return owner.directoryAt(parent, name, mode, true)
}

// OpenChildDirectory validates and retains an existing directory without mutation.
func OpenChildDirectory(parent *os.Root, name string) (*os.Root, error) {
	return Owner{}.directoryAt(parent, name, 0, false)
}

func (owner Owner) directoryAt(parent *os.Root, name string, mode os.FileMode, create bool) (*os.Root, error) {
	if owner.enabled {
		info, err := parent.Stat(".")
		if err != nil {
			return nil, err
		}
		if !sameOwner(info, owner) {
			return nil, fmt.Errorf("metadata parent owner differs from staging authority")
		}
	}
	created := false
	info, err := parent.Lstat(name)
	if os.IsNotExist(err) && create {
		err = parent.Mkdir(name, mode)
		created = err == nil
		if err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is not a real directory", name)
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	file, err := child.Open(".")
	if err != nil {
		_ = child.Close()
		return nil, err
	}
	defer file.Close()
	opened, statErr := file.Stat()
	after, afterErr := parent.Lstat(name)
	if statErr != nil || afterErr != nil || !after.IsDir() || !os.SameFile(info, opened) || !os.SameFile(opened, after) {
		_ = child.Close()
		return nil, fmt.Errorf("metadata directory changed before ownership transfer: %s", name)
	}
	if created {
		if err := owner.File(file); err != nil {
			_ = child.Close()
			return nil, err
		}
	}
	return child, nil
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
	var result error
	for index := len(paths) - 1; index >= 0; index-- {
		result = errors.Join(result, owner.At(root, paths[index]))
	}
	return result
}
