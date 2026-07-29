//go:build !linux && !darwin && !windows

package deploy

import (
	"fmt"
	"runtime"
)

func currentBootSessionIDV1() (string, error) {
	return "", fmt.Errorf("boot-session identity is not implemented on %s", runtime.GOOS)
}
