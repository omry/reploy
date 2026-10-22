//go:build !linux

package probe

import (
	"fmt"
	"io"
)

func launchPrivateWorkloadEnvironmentV1([]string, []string) error {
	return fmt.Errorf("private workload launch requires Linux")
}

func relayPrivateWorkloadEnvironmentV1(io.Reader) error {
	return fmt.Errorf("private workload relay requires Linux")
}

func waitForApplicationLaunchV1() error {
	return fmt.Errorf("application launch proof requires Linux")
}
