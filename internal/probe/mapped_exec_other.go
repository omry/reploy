//go:build !linux || (!amd64 && !arm64)

package probe

import "fmt"

func validateMappedStartupV1(sandboxExecPlanV1) error {
	return fmt.Errorf("mapped startup is not qualified for this architecture")
}

func execMappedApplicationV1(sandboxExecPlanV1, []string) error {
	return fmt.Errorf("mapped startup is not qualified for this architecture")
}
