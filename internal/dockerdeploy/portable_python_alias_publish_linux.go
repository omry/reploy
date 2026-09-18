//go:build linux

package dockerdeploy

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// publishPortablePythonAliasNoReplaceV1 uses the directory descriptor held by
// os.Root and renameat2(RENAME_NOREPLACE), so a destination race cannot
// replace or overwrite an existing alias.
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
	if err := unix.Renameat2(fd, temporary, fd, destination, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("rename temporary Python alias without replacement: %w", err)
	}
	return nil
}
