//go:build !linux

package controlledsession

import (
	"context"
	"fmt"
)

func DialSessionClientV1(context.Context, string) (*SessionClientV1, error) {
	return nil, fmt.Errorf("controlled-session client requires Linux")
}
