package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

// Public adapters name their actual operation before any runtime effect. Keep
// the public inhibition as well as the narrower occupied/host-native refusal.
// This is not an admission flag, and ordinary entries retain their old path.
func unmappedRuntimeDocumentForEntryV1(state deploy.StateV1, kind mappedEntryKindV1) (blueprint.Document, error) {
	mode, err := mappedEntryModeForKindV1(kind)
	if err != nil {
		return blueprint.Document{}, err
	}
	document, err := unmappedRuntimeDocumentV1(state)
	if err != nil {
		_, modeErr := runtimeDocumentForEntryModeV1(state, mode)
		return blueprint.Document{}, errors.Join(err, modeErr)
	}
	return document, nil
}

func requireUnmappedSessionOwnersV1(controller, workload deploy.StateV1) error {
	var failures []error
	for _, owner := range []struct {
		name  string
		state deploy.StateV1
	}{{"controller", controller}, {"workload", workload}} {
		if _, err := unmappedRuntimeDocumentForEntryV1(owner.state, mappedControlledSessionV1); err != nil {
			failures = append(failures, fmt.Errorf("controlled-session %s owner: %w", owner.name, err))
		}
	}
	return errors.Join(failures...)
}

func mappedWorkloadEntryKindV1(action string) mappedEntryKindV1 {
	if action == "restart" {
		return mappedHostReplacementV1
	}
	return mappedWorkloadStartV1
}

// The existing trusted caller supplies held-lock current snapshots and owns the
// queue lease. Construction grants no authority; the shared runner revalidates
// native admission and owns release, completion and cleanup. No public caller
// reaches this private composition while the public guards remain inhibited.
func runMappedControlledEntryV1(admission context.Context, directory string, ops deploy.IdentityMappingOperationsV1, runID string, input mappedEntryInputV1, options RunOptions) error {
	return runMappedControlledEntryWithBackendV1(admission, directory, ops, runID, input, options, mappedLiveHostBackendV1())
}

func runMappedControlledEntryWithBackendV1(admission context.Context, directory string, ops deploy.IdentityMappingOperationsV1, runID string, input mappedEntryInputV1, options RunOptions, backend mappedLiveRunBackendV1) error {
	if input.Kind == mappedHostReplacementV1 {
		return fmt.Errorf("mapped replacement requires exact retirement and fresh admission")
	}
	plan, err := constructMappedEntryPlanV1(input)
	if err != nil {
		return err
	}
	return runMappedLiveInstanceWithBackendV1(admission, directory, ops, runID, plan, options, backend)
}

// Replacement requires complete startup/retirement lifecycle composition. Until
// that integration is qualified, refuse before retiring the old instance. The
// caller retains its lock and all native, lifecycle and queue ownership.
func runMappedReplacementEntryV1(admission context.Context, directory string, ops deploy.IdentityMappingOperationsV1, runID string, reload func(context.Context, deploy.IdentityMappingOperationsV1) (mappedEntryInputV1, error), options RunOptions) error {
	if ops.Operation == nil {
		return fmt.Errorf("mapped replacement requires its operation lock")
	}
	if admission == nil || options.Context == nil || admission.Err() != nil || options.Context.Err() != nil {
		return fmt.Errorf("mapped replacement requires live admission and lifetime contexts")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil || directory == "" || filepath.Dir(filepath.Dir(ops.Operation.Path())) != absolute {
		return fmt.Errorf("mapped replacement lock belongs to a different deployment")
	}
	if err := ops.Operation.RequireQueueEntryLeaseHeldV1(runID); err != nil {
		return err
	}
	queue, _, err := ops.Operation.ReadLiveRunQueueV1()
	if err != nil {
		return err
	}
	run, found := findLiveRunV1(queue, runID)
	if !found || run.Status != deploy.LiveRunStatusActiveV1 || run.Container != "" {
		return fmt.Errorf("mapped replacement requires an unstarted active live entry")
	}
	if _, err := requireMappedWorkloadOwnerV1(admission, ops); err != nil {
		return err
	}
	return fmt.Errorf("mapped replacement lifecycle composition is not yet available")
}
