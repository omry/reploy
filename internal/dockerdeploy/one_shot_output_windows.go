//go:build windows

package dockerdeploy

func oneShotOutputOwnershipBackend() oneShotOutputBackend {
	return oneShotOutputBackend{
		currentUID: func() int { return 0 },
		currentGID: func() int { return 0 },
		chown:      func(string, int, int) error { return nil },
	}
}
