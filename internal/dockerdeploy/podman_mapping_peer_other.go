//go:build !linux

package dockerdeploy

import (
	"context"
	"fmt"
	"net"
)

func dialMappedPodmanV1(context.Context, string) (net.Conn, error) {
	return nil, fmt.Errorf("mapped Podman creation requires local Linux")
}
