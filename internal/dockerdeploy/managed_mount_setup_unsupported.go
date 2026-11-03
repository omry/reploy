//go:build !linux

package dockerdeploy

import "fmt"

func runManagedMountSetupChildV1() error {
	return fmt.Errorf("managed mount setup requires Linux")
}
