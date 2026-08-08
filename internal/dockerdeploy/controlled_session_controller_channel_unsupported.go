//go:build !linux

package dockerdeploy

import "fmt"

func requirePreparedControlledSessionControllerChannelV1(ControlledSessionContainerPlanV1) error {
	return fmt.Errorf("controlled-session private channels are currently supported only on Linux hosts")
}
