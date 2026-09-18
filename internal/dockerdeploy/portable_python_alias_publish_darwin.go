//go:build darwin

package dockerdeploy

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

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
