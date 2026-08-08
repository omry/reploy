//go:build !windows

package dockerdeploy

import (
	"context"
	"fmt"
	"net"
	"net/url"
)

func dialLocalDockerEndpointV1(ctx context.Context, endpoint string) (net.Conn, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "unix" || parsed.Path == "" {
		return nil, fmt.Errorf("local Docker endpoint scheme %q is unsupported on this host", parsed.Scheme)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", parsed.Path)
}
