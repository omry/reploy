//go:build windows

package dockerdeploy

func oneShotOutputOwnershipBackend() oneShotOutputBackend {
	return oneShotOutputBackend{
		currentUID: func() uint32 { return 0 },
		currentGID: func() uint32 { return 0 },
		chown:      func(string, uint32, uint32) error { return nil },
	}
}
