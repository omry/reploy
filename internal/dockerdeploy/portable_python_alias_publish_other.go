//go:build !linux && !darwin && !windows

package dockerdeploy

import (
	"fmt"
	"os"
)

// Other hosts do not expose a common no-replace rename syscall through the
// standard library. Refuse publication rather than weakening the alias
// publication guarantee.
func publishPortablePythonAliasNoReplaceV1(parent *os.Root, temporary, destination string) error {
	return fmt.Errorf("Python alias no-replace rename is unsupported on this host")
}
