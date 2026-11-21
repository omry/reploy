//go:build !linux

package probe

import (
	"context"
	"fmt"
)

func verifyMappedNetworkPolicyV1(context.Context, MappedNetworkObservationV1) error {
	return fmt.Errorf("mapped network observation requires Linux")
}
