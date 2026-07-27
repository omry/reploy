//go:build !linux

package probe

import "fmt"

func runTransientProcess(string, int, int, []string) error {
	return fmt.Errorf("transient command execution requires Linux")
}
