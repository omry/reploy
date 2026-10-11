//go:build linux || darwin

package dockerdeploy

import (
	"os"
	"syscall"
)

func openProviderPreflightStateV1(root *os.Root) (*os.File, error) {
	// A regular file swapped for a FIFO must fail validation without blocking.
	return root.OpenFile("state.json", os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
