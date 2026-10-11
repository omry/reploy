package dockerdeploy

import (
	"fmt"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/fsowner"
	"github.com/omry/reploy/internal/providers/registry"
)

// Privileged installation builds the staged source for its operator, while
// installed runtime account adaptation remains a separate destination step.
func privilegedSourceRuntimeV1(operation *deploy.OperationLock, source string, runtime StagedProviderBuildRuntimeV1) (StagedProviderBuildRuntimeV1, error) {
	owner, err := fsowner.Directory(filepath.Join(source, ".reploy"))
	if err != nil || !owner.Enabled() {
		return runtime, err
	}
	uid, _ := owner.IDs()
	return sourceOperatorRuntimeV1(operation, uint32(uid), runtime, user.LookupId)
}

func sourceOperatorRuntimeV1(operation *deploy.OperationLock, uid uint32, runtime StagedProviderBuildRuntimeV1, lookup func(string) (*user.User, error)) (StagedProviderBuildRuntimeV1, error) {
	operator, err := lookup(strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return runtime, fmt.Errorf("resolve staging operator UID %d: %w", uid, err)
	}
	resolvedUID, uidOK := parseNumericInstallID(operator.Uid)
	gid, gidOK := parseNumericInstallID(operator.Gid)
	if !uidOK || resolvedUID != uid || !gidOK || gid == 0 || uid == 0 {
		return runtime, fmt.Errorf("staging operator must resolve to the same non-root UID and a non-root primary GID")
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return runtime, fmt.Errorf("read staged operator account: %w", err)
	}
	if found && state.Deployment != nil {
		return runtime, fmt.Errorf("provider install source must be staging, not an installed deployment")
	}
	if found && state.Current != nil {
		lock, exists, err := operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
		if err != nil || !exists {
			return runtime, fmt.Errorf("read staged operator build lock: found=%v: %v", exists, err)
		}
		if lock.PortableRuntimeLayer == nil {
			account := lock.RuntimeLayer.Account
			if account.UID != "0" {
				if account.UID != operator.Uid {
					return runtime, fmt.Errorf("staged build account UID %s differs from staging owner UID %d; rebuild as the staging operator before installation", account.UID, uid)
				}
				retainedGID, ok := parseNumericInstallID(account.GID)
				if !ok || retainedGID == 0 {
					return runtime, fmt.Errorf("staged operator build has an invalid primary GID")
				}
				gid = retainedGID
			}
		}
	}
	values, err := installLookupUserGroupIDs(operator)
	if err != nil {
		return runtime, fmt.Errorf("resolve staging operator groups: %w", err)
	}
	groups := make([]uint32, 0, len(values))
	for _, value := range values {
		group, ok := parseNumericInstallID(value)
		if !ok || group == 0 {
			return runtime, fmt.Errorf("staging operator has invalid or root supplementary group %q", value)
		}
		groups = append(groups, group)
	}
	groups, err = normalizeSupplementaryGIDsV1(gid, groups)
	if err != nil {
		return runtime, err
	}
	runtime.UID, runtime.GID, runtime.SupplementaryGIDs = uid, gid, groups
	return runtime, nil
}
