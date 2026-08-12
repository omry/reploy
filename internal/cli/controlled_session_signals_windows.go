//go:build windows

package cli

import "os"

func controlledSessionTerminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func controlledSessionSignalExitCode(os.Signal) int {
	return 130
}
