package dockerdeploy

import (
	"fmt"

	"github.com/omry/reploy/internal/deploy"
)

// Producing intermediate heads must reject consumers whose portable ownership
// transition is not delivered yet, before those consumers mutate resources.
func requirePublicationConsumerBoundaryV1(operation *deploy.OperationLock, boundary string) error {
	if err := operation.RequireOwnerWritable(); err != nil {
		return err
	}
	if err := requireValidatedConsumerBoundaryV1(operation, boundary); err != nil {
		return err
	}
	pending, found, err := operation.ReadPendingBuild()
	if err != nil {
		return err
	}
	if found && (pending.Candidate.Companion != nil || len(pending.OldReferences) == 2) {
		return fmt.Errorf("portable ownership requires %s; publication intent was preserved", boundary)
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if !found || state.Current == nil {
		return nil
	}
	// Detect ownership without requiring an old provider profile to pass the
	// new execution schema; no-cache cutover retains ordinary old owners.
	lock, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("current generation build lock is missing before %s", boundary)
	}
	if err := validateGenerationBuildLock(*state.Current, lock, acceptProviderProfileOwnerForCutoverV1); err != nil {
		return err
	}
	if lock.PortableRuntimeLayer != nil {
		return fmt.Errorf("portable ownership requires %s; current owner was preserved", boundary)
	}
	return nil
}
