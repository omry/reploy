//go:build !linux || (!amd64 && !arm64)

package probe

import "fmt"

func installMappedEscapeFilterV1() error {
	return fmt.Errorf("mapped syscall filter is unavailable on this platform")
}

func validateMappedStartupV1(sandboxExecPlanV1) error {
	return fmt.Errorf("mapped startup is not qualified for this architecture")
}

func execMappedApplicationV1(sandboxExecPlanV1, []string) error {
	return fmt.Errorf("mapped startup is not qualified for this architecture")
}
