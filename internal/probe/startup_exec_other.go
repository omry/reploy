//go:build !linux

package probe

import "fmt"

func execApplication([]string) error {
	return fmt.Errorf("application startup verification is supported only on Linux")
}
