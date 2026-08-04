//go:build !linux

package probe

import "fmt"

func installApplicationLocalAccount(string, string, string, string) error {
	return fmt.Errorf("local application accounts are unsupported on this target OS")
}
