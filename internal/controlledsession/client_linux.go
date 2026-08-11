//go:build linux

package controlledsession

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
)

// DialSessionClientV1 claims the Linux lease-private socket exposed only to
// the controller and consumes its mandatory opened event.
func DialSessionClientV1(ctx context.Context, socketPath string) (*SessionClientV1, error) {
	if ctx == nil || ctx.Done() == nil {
		return nil, fmt.Errorf("dial controlled-session client: cancelable context is required")
	}
	if !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath {
		return nil, fmt.Errorf("dial controlled-session client requires an absolute clean socket path")
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial controlled-session socket: %w", err)
	}
	return newSessionClientV1(ctx, connection)
}
