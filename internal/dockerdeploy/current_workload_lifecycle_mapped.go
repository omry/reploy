package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/mappedlaunch"
)

// F/G's trusted effect owner supplies these observations, never an operator,
// application marker or engine-running assertion. Retire stops the exact owned
// instance and proves its retirement, including an already absent instance.
// A detached observation must follow qualified stream detachment and freshly
// observe the same live instance. E supplies no production provider.
// Calls retain the operation lock. Detach retains the waiter's ownership fence:
// it detaches streams, not the lifetime owner, and must not recursively close it.
type mappedWorkloadEvidenceV1 interface {
	RetireMappedWorkload(context.Context, deploy.StateV1, mappedWorkloadRequestV1) (mappedWorkloadProofV1, error)
	DetachMappedWorkload(context.Context, deploy.StateV1, mappedWorkloadRequestV1) error
	MappedWorkload(context.Context, deploy.StateV1, mappedWorkloadRequestV1) (mappedWorkloadProofV1, error)
}

type mappedWorkloadRequestV1 struct {
	Phase, Installation, Revision, Engine, Run string
	Generation                                 canonical.Digest
	Record                                     deploy.WorkloadLifecycleV1
	Binding                                    mappedlaunch.Binding
	Container, PID                             string
}

type mappedWorkloadProofV1 struct {
	Request                                     canonical.Digest
	Container, StartedAt                        string
	Running, Retired, StartupComplete, Detached bool
	AutoRemove                                  bool
	RestartPolicy                               string
	RestartCount                                uint64
}

func mappedWorkloadEndpointV1(engine string) (string, error) {
	digest, err := canonical.Sum("mapped-workload-endpoint", "v1", engine)
	return strings.TrimPrefix(string(digest), "sha256:"), err
}

func requireMappedWorkloadOwnerV1(ctx context.Context, ops deploy.IdentityMappingOperationsV1) (mappedWorkloadEvidenceV1, error) {
	if ctx == nil || ops.Operation == nil {
		return nil, fmt.Errorf("mapped workload requires bounded context and operation lock")
	}
	if _, bounded := ctx.Deadline(); !bounded || ctx.Err() != nil {
		return nil, fmt.Errorf("mapped workload requires live bounded context")
	}
	if err := ops.Operation.RequireWritable(); err != nil {
		return nil, err
	}
	owner, ok := ops.Evidence.(mappedWorkloadEvidenceV1)
	if !ok {
		return nil, fmt.Errorf("mapped workload trusted observation provider is unavailable")
	}
	return owner, nil
}

func verifyMappedWorkloadProofV1(ctx context.Context, request mappedWorkloadRequestV1, proof mappedWorkloadProofV1) error {
	digest, err := canonical.Sum("mapped-workload-observation", "v1", request)
	if err != nil || ctx.Err() != nil || proof.Request != digest || proof.Container != request.Container ||
		proof.RestartPolicy != "no" || proof.RestartCount != 0 || proof.AutoRemove {
		return fmt.Errorf("mapped workload observation is missing, stale or substituted")
	}
	identity := request.Record
	identity.ContainerID, identity.StartedAt = proof.Container, proof.StartedAt
	if identity.ContainerID == "" || identity.Validate() != nil {
		return fmt.Errorf("mapped workload observation lacks exact process/start identity")
	}
	if request.Record.StartedAt != "" && request.Record.StartedAt != proof.StartedAt {
		return fmt.Errorf("mapped workload start identity changed")
	}
	if request.Phase == "retirement" {
		if proof.Running || !proof.Retired {
			return fmt.Errorf("mapped workload exact retirement is unverified")
		}
	} else if !proof.Running || proof.Retired || !proof.StartupComplete || proof.Detached != (request.Phase == "detached") {
		return fmt.Errorf("mapped workload startup or stream handoff is unverified")
	}
	return nil
}

// reload is the existing trusted held-lock admission reader/writer: it reloads
// current generation, host principal/delegation and policy, then prepares a new
// inhibited native operation. It runs only after exact old retirement. The
// returned plan still requires E3.1.1's owned-waiter gate; it is no admission token.
func prepareMappedWorkloadReplacementV1(ctx context.Context, ops deploy.IdentityMappingOperationsV1, reload func(context.Context, deploy.IdentityMappingOperationsV1) (mappedEntryInputV1, error)) (mappedEntryPlanV1, error) {
	var empty mappedEntryPlanV1
	owner, err := requireMappedWorkloadOwnerV1(ctx, ops)
	if err != nil || reload == nil {
		return empty, errors.Join(err, fmt.Errorf("mapped replacement requires a fresh trusted admission reader"))
	}
	old, found, err := ops.Operation.ReadWorkloadLifecycleV1()
	if err != nil {
		return empty, err
	}
	status, err := ops.Status()
	if err != nil || status.State.IdentityMapping == nil {
		return empty, errors.Join(err, fmt.Errorf("mapped replacement requires selected native ownership"))
	}
	state := status.State
	m := state.IdentityMapping
	oldRun, oldOperation := "", ""
	if m.NativeProcess != nil {
		oldRun = *m.NativeProcess
	}
	if p := m.PendingOperation; p != nil {
		if p.Kind != "native-execute" || p.Phase != "release-attempted" || p.Native == nil || !found || old.ContainerID == "" {
			return empty, fmt.Errorf("mapped replacement has an unresolved native attempt")
		}
		oldOperation = p.OperationID
	}
	if found && old.ContainerID != "" {
		if m.Engine == nil || oldRun == "" || m.AdmittedGeneration == nil {
			return empty, fmt.Errorf("mapped replacement lacks old native ownership")
		}
		endpoint, err := mappedWorkloadEndpointV1(*m.Engine)
		if err != nil || endpoint != old.Endpoint {
			return empty, fmt.Errorf("mapped replacement old endpoint differs")
		}
		old.State = "stopping"
		if err := ops.Operation.WriteWorkloadLifecycleV1(old); err != nil {
			return empty, err
		}
		request := mappedWorkloadRequestV1{Phase: "retirement", Installation: m.InstallationID, Revision: m.Revision,
			Engine: *m.Engine, Run: oldRun, Generation: m.AdmittedGeneration.BuildLockDigest, Record: old, Container: old.ContainerID}
		proof, err := owner.RetireMappedWorkload(ctx, state, request)
		if err != nil {
			return empty, fmt.Errorf("mapped replacement retirement requires recovery: %w", err)
		}
		if err := verifyMappedWorkloadProofV1(ctx, request, proof); err != nil {
			return empty, err
		}
		if oldOperation != "" {
			state, err = ops.Recover(ctx, state, oldOperation, false)
			if err != nil || state.IdentityMapping == nil || state.IdentityMapping.PendingOperation != nil {
				return empty, errors.Join(err, fmt.Errorf("mapped replacement native retirement remains uncertain"))
			}
		}
		old.State = "down"
		if err := ops.Operation.WriteWorkloadLifecycleV1(old); err != nil {
			return empty, err
		}
	} else if found && old.State != "down" {
		return empty, fmt.Errorf("mapped replacement has unbound startup uncertainty")
	}
	input, err := reload(ctx, ops)
	if err != nil || ctx.Err() != nil {
		return empty, errors.Join(err, ctx.Err())
	}
	current, err := ops.Status()
	if err != nil || !reflect.DeepEqual(current.State, input.Current.State) || input.Kind != mappedHostReplacementV1 {
		return empty, fmt.Errorf("mapped replacement admission is not the current locked snapshot: %v", err)
	}
	plan, err := constructMappedEntryPlanV1(input)
	if err != nil {
		return empty, err
	}
	facts, err := plan.facts()
	if err != nil || facts.Run == oldRun || facts.Operation == oldOperation || found && strings.TrimPrefix(string(plan.binding.Plan), "sha256:") == old.Runtime {
		return empty, fmt.Errorf("mapped replacement cannot reuse an old attempt or plan: %v", err)
	}
	endpoint, err := mappedWorkloadEndpointV1(facts.Engine)
	if err != nil {
		return empty, err
	}
	record := deploy.WorkloadLifecycleV1{Schema: "workload-lifecycle-v1", State: "starting", Endpoint: endpoint, Runtime: strings.TrimPrefix(string(plan.binding.Plan), "sha256:")}
	if err := ops.Operation.WriteWorkloadLifecycleV1(record); err != nil {
		return empty, err
	}
	return plan, nil
}

// This is called by the trusted lifetime owner after its one-shot release,
// while it still holds the operation lock. A failed confirmation keeps starting
// ownership; the lifetime owner must perform its normal bounded stop/retirement.
// A detached proof includes actual qualified detachment followed by a fresh
// exact live observation. No production detach transport is introduced here.
func confirmMappedWorkloadStartupV1(ctx context.Context, ops deploy.IdentityMappingOperationsV1, plan mappedEntryPlanV1, waiter *ownedMappedWaiterV1, detached bool) error {
	owner, err := requireMappedWorkloadOwnerV1(ctx, ops)
	if err != nil {
		return err
	}
	facts, err := plan.facts()
	if err != nil || facts.Kind != strconv.Itoa(int(mappedHostReplacementV1)) || waiter == nil || waiter.prepared == nil ||
		waiter.prepared.ops.Operation != ops.Operation || waiter.binding != plan.binding || waiter.container == "" || waiter.pid <= 0 {
		return fmt.Errorf("mapped startup requires its exact owned replacement waiter: %v", err)
	}
	waiter.mu.Lock()
	released := waiter.consumed && !waiter.closed
	waiter.mu.Unlock()
	if !released {
		return fmt.Errorf("mapped startup requires an attempted owned release")
	}
	status, err := ops.Status()
	if err != nil || status.State.Current == nil || status.State.IdentityMapping == nil {
		return fmt.Errorf("mapped startup current ownership is unavailable: %v", err)
	}
	state, m := status.State, status.State.IdentityMapping
	p := m.PendingOperation
	revision, err := strconv.ParseUint(facts.Revision, 10, 64)
	if err != nil || revision == ^uint64(0) || m.Revision != strconv.FormatUint(revision+1, 10) ||
		m.InstallationID != facts.Installation || state.Current.BuildLockDigest != facts.Generation ||
		p == nil || p.Kind != "native-execute" || p.Phase != "release-attempted" || p.OperationID != facts.Operation ||
		p.Native == nil || p.Native.Engine != facts.Engine || p.Native.Process != facts.Run {
		return fmt.Errorf("mapped startup native release owner changed")
	}
	record, found, err := ops.Operation.ReadWorkloadLifecycleV1()
	endpoint, endpointErr := mappedWorkloadEndpointV1(facts.Engine)
	if err != nil || endpointErr != nil || !found || record.State != "starting" || record.Endpoint != endpoint ||
		record.Runtime != strings.TrimPrefix(string(plan.binding.Plan), "sha256:") || record.ContainerID != "" || record.TransferClaimed || record.StartupComplete || record.HandoffComplete {
		return fmt.Errorf("mapped startup lifecycle owner changed")
	}
	request := mappedWorkloadRequestV1{Phase: "attached", Installation: facts.Installation, Revision: m.Revision, Engine: facts.Engine,
		Run: facts.Run, Generation: facts.Generation, Record: record, Binding: plan.binding, Container: waiter.container, PID: strconv.Itoa(waiter.pid)}
	proof, err := owner.MappedWorkload(ctx, state, request)
	if err != nil {
		return fmt.Errorf("mapped startup completion requires recovery: %w", err)
	}
	if err := verifyMappedWorkloadProofV1(ctx, request, proof); err != nil {
		return err
	}
	record.ContainerID, record.StartedAt, record.StartupComplete = proof.Container, proof.StartedAt, true
	if detached {
		// Claim this exact transfer before detaching any stream. A failed or
		// ambiguous detach cannot be retried as another successful handoff.
		if err := func() error {
			waiter.mu.Lock()
			defer waiter.mu.Unlock()
			if waiter.closed || !waiter.consumed || ctx.Err() != nil {
				return fmt.Errorf("mapped lifetime owner ended before stream detachment")
			}
			record.TransferClaimed = true
			if err := ops.Operation.WriteWorkloadLifecycleV1(record); err != nil {
				return err
			}
			request.Phase, request.Record = "detached", record
			return owner.DetachMappedWorkload(ctx, state, request)
		}(); err != nil {
			return fmt.Errorf("mapped stream detachment requires recovery: %w", err)
		}
		proof, err = owner.MappedWorkload(ctx, state, request)
		if err != nil {
			return fmt.Errorf("mapped detached instance requires recovery: %w", err)
		}
		if err := verifyMappedWorkloadProofV1(ctx, request, proof); err != nil {
			return err
		}
	}
	// Never translate a frame write, engine-running result or application marker
	// into startup success. Only the exact trusted observation reaches this write.
	// Serialize the commit with close: an owner that closed during observation
	// cannot publish up, and cleanup cannot start inside the commit interval.
	waiter.mu.Lock()
	defer waiter.mu.Unlock()
	if waiter.closed || !waiter.consumed || ctx.Err() != nil {
		return fmt.Errorf("mapped lifetime owner ended before startup commit")
	}
	record.State, record.ContainerID, record.StartedAt = "up", proof.Container, proof.StartedAt
	record.StartupComplete = true
	record.TransferClaimed, record.HandoffComplete = detached, detached
	return ops.Operation.WriteWorkloadLifecycleV1(record)
}
