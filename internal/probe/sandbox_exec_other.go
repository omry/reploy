//go:build !linux

package probe

import "fmt"

func sandboxAndExecApplicationV1(sandboxExecPlanV1) error {
	return fmt.Errorf("application sandbox setup is supported only in Linux containers")
}
