//go:build !windows

package dockerdeploy

import "os"

func oneShotOutputOwnershipBackend() oneShotOutputBackend {
	return oneShotOutputBackend{
		currentUID: os.Geteuid,
		currentGID: os.Getegid,
		chown:      os.Lchown,
	}
}
