//go:build !linux

package dockerdeploy

import (
	"context"
	"fmt"
	"net"

	"github.com/omry/reploy/internal/identitymapping"
)

func dialMappedPodmanV1(context.Context, string) (net.Conn, error) {
	return nil, fmt.Errorf("mapped Podman creation requires local Linux")
}

func pinMappedProcessV1(int, identitymapping.Geometry, []string) (mappedWaiterProcessV1, error) {
	return nil, fmt.Errorf("mapped waiter observations require local Linux")
}
