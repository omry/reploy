package deploy

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/identitymapping"
)

// CheckIdentityMappingHostV1 checks an existing selected owner under its held
// operation lock. It neither configures mappings nor proves engine identity.
func (lock *OperationLock) CheckIdentityMappingHostV1(ctx context.Context, uids, gids []identitymapping.Interval) (identitymapping.HostIdentity, error) {
	return lock.checkIdentityMappingHostV1(ctx, uids, gids, identitymapping.CheckHost)
}

func (lock *OperationLock) checkIdentityMappingHostV1(ctx context.Context, uids, gids []identitymapping.Interval,
	check func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error),
) (identitymapping.HostIdentity, error) {
	if ctx == nil {
		return identitymapping.HostIdentity{}, fmt.Errorf("host check requires a context")
	}
	state, found, err := lock.ReadStateV1()
	if err != nil {
		return identitymapping.HostIdentity{}, err
	}
	if !found || state.TerminalRemoval || state.Staging == nil && state.Deployment == nil {
		return identitymapping.HostIdentity{}, fmt.Errorf("identity mapping requires an existing nonterminal staged or installed deployment")
	}
	if state.Deployment != nil && state.Deployment.Installation.Scope != "user" {
		return identitymapping.HostIdentity{}, fmt.Errorf("identity mapping does not support system-scope installation")
	}
	if err := lock.RequireHeld(); err != nil {
		return identitymapping.HostIdentity{}, err
	}
	directory := filepath.Dir(filepath.Dir(lock.path))
	if state.Deployment != nil && state.Deployment.Installation.TargetDir != directory {
		return identitymapping.HostIdentity{}, fmt.Errorf("installation target differs from selected mapping owner")
	}
	identity, err := check(ctx, directory, uids, gids)
	if err != nil {
		return identitymapping.HostIdentity{}, err
	}
	if err := lock.RequireHeld(); err != nil {
		return identitymapping.HostIdentity{}, err
	}
	return identity, nil
}
