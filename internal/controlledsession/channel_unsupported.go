//go:build !linux

package controlledsession

import (
	"fmt"
)

func preparePrivateChannelTransportV1(string, uint32, uint32) (privateChannelTransportV1, error) {
	return nil, fmt.Errorf("controlled-session private channels currently require Linux")
}

func isPlatformControllerDisconnectV1(error) bool { return false }
