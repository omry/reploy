//go:build !linux && !darwin && !windows

package dockerdeploy

import (
	"fmt"
	"os"
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

// Other hosts do not expose a common no-replace rename syscall through the
// standard library. Refuse publication rather than weakening the alias
// publication guarantee.
func publishPortablePythonAliasNoReplaceV1(parent *os.Root, temporary, destination string) error {
	return fmt.Errorf("Python alias no-replace rename is unsupported on this host")
}
