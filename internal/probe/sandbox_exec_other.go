//go:build !linux

package probe

import (
	"fmt"
	"os"
)

func sandboxAndExecApplicationV1(sandboxExecPlanV1) error {
	return fmt.Errorf("application sandbox setup is supported only in Linux containers")
}

func mappedWaitV1([]string, *os.File) error {
	return fmt.Errorf("mapped waiter requires a qualified local Linux runtime")
}
