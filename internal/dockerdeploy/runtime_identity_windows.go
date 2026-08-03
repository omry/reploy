//go:build windows

package dockerdeploy

import (
	"fmt"
	"os/user"
)

func currentHostRuntimeIdentityV1() (string, int, int, []int, error) {
	current, err := user.Current()
	if err != nil {
		return "", 0, 0, nil, fmt.Errorf("resolve current Windows user: %w", err)
	}
	uid, gid, err := windowsSIDRuntimeIdentityV1(current.Uid)
	if err != nil {
		return "", 0, 0, nil, fmt.Errorf("map current Windows user SID: %w", err)
	}
	return "windows", uid, gid, []int{}, nil
}
