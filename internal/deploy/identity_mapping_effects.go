package deploy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/identitymapping"
)

// ObserveEffects checks the trusted caller's exact pending operation. It changes
// no state and grants no payload permission. BeginEffects must already be durable.
// Configure requires the full candidate; native-execute requires its freshly
// checked stored active projection. Neither uses the retained reservation union.
func (ops IdentityMappingOperationsV1) ObserveEffects(ctx context.Context, expected StateV1, active identitymapping.Geometry, operationID string) (StateV1, error) {
	if err := ops.Operation.RequireHeld(); err != nil {
		return StateV1{}, err
	}
	status, err := ops.Status()
	if err != nil || !reflect.DeepEqual(status.State, expected) {
		return StateV1{}, fmt.Errorf("mapped create owner, generation or revision changed: %v", err)
	}
	state := status.State
	m := state.IdentityMapping
	if state.Current == nil || m == nil || m.PendingOperation == nil || m.PendingOperation.Phase != "effects-pending" {
		return StateV1{}, fmt.Errorf("mapped create requires its recorded effects-pending operation")
	}
	canonical, err := identitymapping.ValidateGeometry(active)
	wanted := m.PendingOperation.Candidate.numeric()
	if m.PendingOperation.Kind == "native-execute" && m.PendingOperation.Native != nil {
		wanted = m.PendingOperation.Native.Active.numeric()
	}
	if err != nil || !reflect.DeepEqual(canonical, wanted) {
		return StateV1{}, fmt.Errorf("mapped create active geometry differs from its pending candidate: %v", err)
	}
	if err := ops.checkPending(ctx, state, operationID); err != nil {
		return StateV1{}, err
	}
	if err := ops.Operation.RequireHeld(); err != nil {
		return StateV1{}, err
	}
	return state, nil
}
