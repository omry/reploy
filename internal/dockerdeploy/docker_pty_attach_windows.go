//go:build windows

package dockerdeploy

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	winio "github.com/Microsoft/go-winio"
)

func dialLocalDockerEndpointV1(ctx context.Context, endpoint string) (net.Conn, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "npipe" || parsed.Path == "" {
		return nil, fmt.Errorf("local Docker endpoint scheme %q is unsupported on this host", parsed.Scheme)
	}
	pipe := strings.ReplaceAll(parsed.Path, "/", `\`)
	return winio.DialPipeContext(ctx, pipe)
}
