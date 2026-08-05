//go:build linux

package probe

import (
	"os"
	"syscall"
)

func execApplication(argv []string) error {
	return syscall.Exec(argv[0], argv, os.Environ())
}
