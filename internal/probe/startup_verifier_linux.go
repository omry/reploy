//go:build linux

package probe

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func applicationKernelStatusFallbackPathV1() (string, error) {
	return fmt.Sprintf("/proc/self/task/%d/status", unix.Gettid()), nil
}
