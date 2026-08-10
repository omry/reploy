//go:build !windows

package dockerdeploy

import "os"

func oneShotOutputOwnershipBackend() oneShotOutputBackend {
	return oneShotOutputBackend{
		currentUID: func() uint32 { return uint32(os.Geteuid()) },
		currentGID: func() uint32 { return uint32(os.Getegid()) },
		chown: func(path string, uid uint32, gid uint32) error {
			return os.Lchown(path, int(uid), int(gid))
		},
	}
}
