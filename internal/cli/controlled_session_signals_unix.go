//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cli

import (
	"os"
	"syscall"
)

func controlledSessionTerminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func controlledSessionSignalExitCode(value os.Signal) int {
	if value == syscall.SIGTERM {
		return 143
	}
	return 130
}
