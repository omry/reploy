//go:build darwin

package dockerdeploy

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func createPortablePythonAliasEntryV1(parent *os.Root, target, name string) error {
	if parent == nil {
		return fmt.Errorf("Python alias destination parent handle is unavailable")
	}
	return parent.Symlink(target, name)
}

func readPortablePythonAliasEntryV1(parent *os.Root, name string) (string, error) {
	if parent == nil {
		return "", fmt.Errorf("Python alias parent handle is unavailable")
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", errPortablePythonAliasUnownedEntryV1
	}
	return parent.Readlink(name)
}

func createPortablePythonAliasStagedEntryV1(filename, target string) error {
	return os.Symlink(target, filename)
}

func readPortablePythonAliasStagedEntryV1(filename string) (string, error) {
	return os.Readlink(filename)
}

// publishPortablePythonAliasNoReplaceV1 uses renameatx_np(RENAME_EXCL), the
// Darwin no-replace rename, against the held destination directory handle.
func publishPortablePythonAliasNoReplaceV1(parent *os.Root, temporary, destination string) error {
	if parent == nil {
		return fmt.Errorf("Python alias destination parent handle is unavailable")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return fmt.Errorf("open Python alias destination directory handle: %w", err)
	}
	defer directory.Close()
	fd := int(directory.Fd())
	if err := unix.RenameatxNp(fd, temporary, fd, destination, unix.RENAME_EXCL); err != nil {
		return fmt.Errorf("rename temporary Python alias without replacement: %w", err)
	}
	return nil
}
