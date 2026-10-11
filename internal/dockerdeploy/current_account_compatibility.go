package dockerdeploy

import (
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/deploy"
)

func requireCurrentAccountCompatibilityV1(lock deploy.BuildLockV1, plan DockerExecutionPlan) error {
	account, err := applicationLocalAccountV1(plan.Sandbox)
	if err != nil {
		return fmt.Errorf("runtime local account: %w", err)
	}
	if !reflect.DeepEqual(account, lock.RuntimeLayer.Account) {
		return fmt.Errorf("runtime application account differs from the recorded build (recorded UID/GID %s/%s, expected %s/%s)", lock.RuntimeLayer.Account.UID, lock.RuntimeLayer.Account.GID, account.UID, account.GID)
	}
	return nil
}
