//go:build darwin

package deploy

import (
	"fmt"
	"syscall"
)

func currentBootSessionIDV1() (string, error) {
	value, err := syscall.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", fmt.Errorf("read macOS boot-session UUID: %w", err)
	}
	return value, nil
}
