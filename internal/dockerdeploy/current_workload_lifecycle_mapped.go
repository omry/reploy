package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/mappedlaunch"
)

// F/G's trusted effect owner supplies these observations, never an operator,
// application marker or engine-running assertion. Retire stops the exact owned
// instance, including an already absent instance.
// A detached observation must follow qualified stream detachment and freshly
// observe the same live instance. E supplies no production provider.
// MappedRecoveryOwners enumerates every registered engine/controller owner from
// trusted host state; unknown or incomplete discovery is an error. Retirement
// fences that entire inventory and stops/removes only the exact owned runtime,
// preserving retained data. It runs WITHOUT the operation lock so manager
// cleanup can acquire it. Command completion is not effect authority.
// MappedWorkload observes the complete current inventory and durable inhibition
// afresh under the reacquired lock. Retirement FencedOwners contains only actual
// fences that remain effective until a later explicitly authorized new start.
// Detach retains the waiter's ownership fence and must not recursively close it.
// Concrete controller transport/registration belongs to later G adapters.
type mappedWorkloadEvidenceV1 interface {
	MappedRecoveryOwners(context.Context, deploy.StateV1, mappedWorkloadRequestV1) ([]mappedRecoveryOwnerV1, error)
	RetireMappedWorkload(context.Context, deploy.StateV1, mappedWorkloadRequestV1) error
	DetachMappedWorkload(context.Context, deploy.StateV1, mappedWorkloadRequestV1) error
	MappedWorkload(context.Context, deploy.StateV1, mappedWorkloadRequestV1) (mappedWorkloadProofV1, error)
}

type mappedRecoveryOwnerV1 struct {
	Kind, ID, Revision string
}

type mappedWorkloadRequestV1 struct {
	RecoveryOwners                             []mappedRecoveryOwnerV1
	Phase, Installation, Revision, Engine, Run string
	Generation                                 canonical.Digest
	Record                                     deploy.WorkloadLifecycleV1
	Binding                                    mappedlaunch.Binding
	Container, PID                             string
}

type mappedWorkloadProofV1 struct {
	FencedOwners                                []mappedRecoveryOwnerV1
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
		if proof.Running || !proof.Retired || len(request.RecoveryOwners) == 0 || !slices.Equal(proof.FencedOwners, request.RecoveryOwners) {
			return fmt.Errorf("mapped workload exact retirement is unverified")
		}
	} else if !proof.Running || proof.Retired || !proof.StartupComplete || proof.Detached != (request.Phase == "detached") {
		return fmt.Errorf("mapped workload startup or stream handoff is unverified")
	}
	return nil
}

// Capture real file identities while the original lock is held. A new lock at
// the same spelling is not continuation of an installation that was replaced.
type mappedOperationLockIdentityV1 struct {
	path  string
	files [3]os.FileInfo
}

func captureMappedOperationLockV1(lock *deploy.OperationLock) (mappedOperationLockIdentityV1, error) {
	var identity mappedOperationLockIdentityV1
	if err := lock.RequireWritable(); err != nil {
		return identity, err
	}
	identity.path = lock.Path()
	paths := []string{filepath.Dir(filepath.Dir(identity.path)), filepath.Dir(identity.path), identity.path}
	for index, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return identity, err
		}
		info, statErr := file.Stat()
		err = errors.Join(statErr, file.Close())
		if err != nil {
			return identity, err
		}
		identity.files[index] = info
	}
	return identity, nil
}

func validateMappedRecoveryOwnersV1(request mappedWorkloadRequestV1, owners []mappedRecoveryOwnerV1) error {
	previous, engine := "", false
	for _, owner := range owners {
		key := owner.Kind + "/" + owner.ID
		if owner.ID == "" || owner.Revision == "" || key <= previous {
			return fmt.Errorf("restart-owner inventory is incomplete, duplicated or unordered")
		}
		previous = key
		switch owner.Kind {
		case "engine":
			if engine || owner.ID != request.Container || owner.Revision != request.Record.StartedAt {
				return fmt.Errorf("restart-owner engine identity differs")
			}
			engine = true
		case "controller":
			// The trusted adapter owns the exact system/user manager identity and its
			// revision. Names alone or an empty engine listing cannot supply inventory.
		default:
			return fmt.Errorf("unknown restart-owner kind")
		}
	}
	if !engine {
		return fmt.Errorf("restart-owner inventory lacks the exact engine object")
	}
	return nil
}

func retireMappedWorkloadOwnersV1(ctx context.Context, ops *deploy.IdentityMappingOperationsV1, owner mappedWorkloadEvidenceV1, state deploy.StateV1, request *mappedWorkloadRequestV1) error {
	identity, err := captureMappedOperationLockV1(ops.Operation)
	if err != nil {
		return err
	}
	owners, err := owner.MappedRecoveryOwners(ctx, state, *request)
	if err != nil {
		return fmt.Errorf("discover complete restart owners: %w", err)
	}
	if err := validateMappedRecoveryOwnersV1(*request, owners); err != nil {
		return err
	}
	request.RecoveryOwners = slices.Clone(owners)
	inventory, err := canonical.Sum("mapped-recovery-owners", "v1", owners)
	if err != nil {
		return err
	}
	old := request.Record
	retainedOwner, retainedInventory := old.RetirementOwner, old.RecoveryOwners
	old.State, old.RetirementOwner, old.RecoveryOwners = "stopping", "", inventory
	request.Record = old
	transaction, err := canonical.Sum("mapped-workload-retirement", "v1", *request)
	if err != nil {
		return err
	}
	controlID := "control-" + strings.TrimPrefix(string(transaction), "sha256:")[:16]
	if retainedOwner != "" && (retainedOwner != controlID || retainedInventory != inventory) {
		return fmt.Errorf("retained restart-owner inventory changed")
	}
	old.RetirementOwner = controlID
	request.Record = old
	lease, err := ops.Operation.AcquireControlLeaseV1(controlID)
	if err != nil {
		return err
	}
	// Ambiguous retirement retains the lease file and durable inhibition. A
	// later explicit retry must reacquire ownership and reobserve every fence.
	defer lease.Close()
	if err := ops.Operation.WriteWorkloadLifecycleV1(old); err != nil {
		return err
	}
	held := ops.Operation
	ops.Operation = nil // the caller must never reuse the released lock
	if err := held.Unlock(); err != nil {
		return err
	}
	stopErr := owner.RetireMappedWorkload(ctx, state, *request)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), privateWorkloadEnvironmentTimeoutV1)
	defer cancel()
	currentLock, reacquireErr := deploy.AcquireExistingOperationLock(cleanupCtx, filepath.Dir(filepath.Dir(identity.path)))
	if reacquireErr != nil {
		return errors.Join(stopErr, fmt.Errorf("reacquire after mapped retirement: %w", reacquireErr))
	}
	ops.Operation = currentLock // transfer even on refusal, for caller cleanup
	currentIdentity, err := captureMappedOperationLockV1(currentLock)
	if err != nil {
		return err
	}
	for index, original := range identity.files {
		if !os.SameFile(original, currentIdentity.files[index]) {
			return fmt.Errorf("mapped retirement operation-lock identity changed")
		}
	}
	if err := lease.RequireHeld(); err != nil {
		return err
	}
	if err := errors.Join(stopErr, ctx.Err()); err != nil {
		return fmt.Errorf("mapped retirement remains uncertain: %w", err)
	}
	actual, err := ops.Status()
	if err != nil || !reflect.DeepEqual(actual.State, state) {
		return fmt.Errorf("mapped retirement state changed: %v", err)
	}
	record, found, err := currentLock.ReadWorkloadLifecycleV1()
	if err != nil || !found || record != old {
		return fmt.Errorf("mapped retirement lost durable inhibition: %v", err)
	}
	currentOwners, err := owner.MappedRecoveryOwners(ctx, actual.State, *request)
	if err != nil || !slices.Equal(currentOwners, request.RecoveryOwners) {
		return fmt.Errorf("mapped retirement owner inventory changed: %v", err)
	}
	proof, err := owner.MappedWorkload(ctx, actual.State, *request)
	if err != nil {
		return fmt.Errorf("fresh mapped retirement observation: %w", err)
	}
	if err := verifyMappedWorkloadProofV1(ctx, *request, proof); err != nil {
		return err
	}
	// The observer is trusted to read facts, but a lost lock/context or changed
	// local state during that call still cannot authorize preparation.
	after, err := ops.Status()
	record, found, readErr := currentLock.ReadWorkloadLifecycleV1()
	if err != nil || readErr != nil || !found || record != old || !reflect.DeepEqual(after.State, state) || ctx.Err() != nil {
		return fmt.Errorf("mapped retirement observation lost its exact owner")
	}
	if err := lease.RequireHeld(); err != nil {
		return err
	}
	return lease.Release()
}

// reload is the existing trusted held-lock admission reader/writer: it reloads
// current generation, host principal/delegation and policy, then prepares a new
// inhibited native operation. It runs only after exact old retirement. The
// returned plan still requires E3.1.1's owned-waiter gate; it is no admission token.
func prepareMappedWorkloadReplacementV1(ctx context.Context, ops *deploy.IdentityMappingOperationsV1, reload func(context.Context, deploy.IdentityMappingOperationsV1) (mappedEntryInputV1, error)) (mappedEntryPlanV1, error) {
	var empty mappedEntryPlanV1
	if ops == nil {
		return empty, fmt.Errorf("mapped replacement requires caller-owned operations")
	}
	owner, err := requireMappedWorkloadOwnerV1(ctx, *ops)
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
		request := mappedWorkloadRequestV1{Phase: "retirement", Installation: m.InstallationID, Revision: m.Revision,
			Engine: *m.Engine, Run: oldRun, Generation: m.AdmittedGeneration.BuildLockDigest, Record: old, Container: old.ContainerID}
		if err := retireMappedWorkloadOwnersV1(ctx, ops, owner, state, &request); err != nil {
			return empty, err
		}
		old = request.Record

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
	} else if oldRun != "" || found && old.State != "down" {
		return empty, fmt.Errorf("mapped replacement has unbound startup uncertainty")
	}
	input, err := reload(ctx, *ops)
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
