//go:build !linux

package identitymapping

import (
	"context"
	"fmt"
)

func CheckHost(_ context.Context, _ string, _, _ []Interval) (HostIdentity, error) {
	return HostIdentity{}, fmt.Errorf("identity mapping requires local Linux host authority")
}
