//go:build linux

package deploy

import (
	"fmt"
	"os"
)

func currentBootSessionIDV1() (string, error) {
	content, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("read Linux boot ID: %w", err)
	}
	return string(content), nil
}
