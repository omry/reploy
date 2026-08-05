//go:build !windows

package dockerdeploy

import (
	"fmt"
	"os"
	"runtime"
)

func currentHostRuntimeIdentityV1() (string, int, int, []int, error) {
	groups, err := os.Getgroups()
	if err != nil {
		return "", 0, 0, nil, fmt.Errorf("resolve current supplementary groups: %w", err)
	}
	return runtime.GOOS, os.Getuid(), os.Getgid(), groups, nil
}
