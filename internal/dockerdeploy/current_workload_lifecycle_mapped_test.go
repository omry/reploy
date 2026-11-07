package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

// These are private orchestration fixtures with real lock/state/lifecycle and
// retained acknowledged native history. Engine, admission and F/G observations are injected;
// no live-engine startup or detachment qualification is claimed.
type mappedWorkloadFixtureV1 struct {
	owners       []mappedRecoveryOwnerV1
	inventories  int
	inventoryErr error
	changeOwners func(int, []mappedRecoveryOwnerV1) []mappedRecoveryOwnerV1
	duringRetire func(*deploy.OperationLock, mappedWorkloadRequestV1)
	*waiterEffectFixtureV1
	t              *testing.T
	live           *mappedLiveFixtureV1
	ops            deploy.IdentityMappingOperationsV1
	pending        *deploy.IdentityMappingOperationV1
	old            deploy.WorkloadLifecycleV1
	trace          []string
	retireErr      error
	observeErr     error
	detachErr      error
	changeProof    func(mappedWorkloadRequestV1, *mappedWorkloadProofV1)
	changeInput    func(*mappedEntryInputV1)
	afterObserve   func(mappedWorkloadRequestV1)
	cancelOnRetire context.CancelFunc
}

func cloneMappedWorkloadStateFixtureV1(t *testing.T, state deploy.StateV1) deploy.StateV1 {
	t.Helper()
	data, err := deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := deploy.DecodeStateV1(data)
	if err != nil {
		t.Fatal(err)
	}
	return copy
}

func newMappedWorkloadFixtureV1(t *testing.T, profile string) *mappedWorkloadFixtureV1 {
	t.Helper()
	live := newMappedLiveFixtureV1(t, profile, mappedHostReplacementV1)
	f := &mappedWorkloadFixtureV1{waiterEffectFixtureV1: live.waiter.effect, t: t, live: live, ops: live.waiter.prepared.ops}
	f.ops.Evidence = f
	t.Cleanup(func() {
		if f.ops.Operation != nil {
			_ = f.ops.Operation.Unlock()
		}
	})
	live.waiter.prepared.ops = f.ops
	state := cloneMappedWorkloadStateFixtureV1(t, live.input.Current.State)
	m := state.IdentityMapping
	p := m.PendingOperation
	p.OperationID, p.Native.Process = "old-operation", "old-process"
	p.Phase, p.ExpectedRevision, m.Revision = "release-attempted", "2", "2"
	a := p.Native.Active
	m.UIDAssignments, m.GIDAssignments = a.UIDs, a.GIDs
	m.AcceptedUIDs, m.AcceptedGIDs = &a.UIDs, &a.GIDs
	m.AdmittedGeneration, m.Engine, m.NativeProcess = state.Current, &p.Native.Engine, &p.Native.Process
	f.pending = cloneMappedWorkloadStateFixtureV1(t, state).IdentityMapping.PendingOperation
	// The old release was acknowledged. Pending-release recovery also invokes
	// the host preflight; that refusal is tested separately on this fixture.
	m.PendingOperation = nil
	writeMappedLiveStateFixtureV1(t, f.ops.Operation, state)
	endpoint, err := mappedWorkloadEndpointV1(*m.Engine)
	if err != nil {
		t.Fatal(err)
	}
	f.old = deploy.WorkloadLifecycleV1{Schema: "workload-lifecycle-v1", State: "up", Endpoint: endpoint,
		Runtime: strings.Repeat("1", 64), ContainerID: strings.Repeat("a", 64), StartedAt: "2026-01-01T00:00:00Z", StartupComplete: true}
	f.owners = []mappedRecoveryOwnerV1{{Kind: "controller", ID: "user/reploy-fixture", Revision: "manager-generation-1"}, {Kind: "engine", ID: f.old.ContainerID, Revision: f.old.StartedAt}}
	if err := f.ops.Operation.WriteWorkloadLifecycleV1(f.old); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *mappedWorkloadFixtureV1) proof(request mappedWorkloadRequestV1) mappedWorkloadProofV1 {
	f.t.Helper()
	digest, err := canonical.Sum("mapped-workload-observation", "v1", request)
	if err != nil {
		f.t.Fatal(err)
	}
	started := request.Record.StartedAt
	if started == "" {
		started = "2026-01-02T00:00:00Z"
	}
	proof := mappedWorkloadProofV1{FencedOwners: slices.Clone(request.RecoveryOwners), Request: digest, Container: request.Container, StartedAt: started, RestartPolicy: "no",
		Running: request.Phase != "retirement", Retired: request.Phase == "retirement", StartupComplete: request.Phase != "retirement", Detached: request.Phase == "detached"}
	if f.changeProof != nil {
		f.changeProof(request, &proof)
	}
	return proof
}

func (f *mappedWorkloadFixtureV1) MappedRecoveryOwners(ctx context.Context, _ deploy.StateV1, _ mappedWorkloadRequestV1) ([]mappedRecoveryOwnerV1, error) {
	f.t.Helper()
	if f.ops.Operation.RequireHeld() != nil || ctx.Err() != nil {
		f.t.Fatal("owner inventory lost its held lock/context")
	}
	f.inventories++
	owners := slices.Clone(f.owners)
	if f.changeOwners != nil {
		owners = f.changeOwners(f.inventories, owners)
	}
	return owners, f.inventoryErr
}

func (f *mappedWorkloadFixtureV1) RetireMappedWorkload(ctx context.Context, state deploy.StateV1, request mappedWorkloadRequestV1) error {
	f.t.Helper()
	if f.ops.Operation != nil || ctx.Err() != nil {
		f.t.Fatal("manager stop retained the operation lock or lost context")
	}
	// This is a real cleanup worker acquiring the deployment lock during the
	// external manager stop. Holding the original lock deadlocks this boundary.
	workerCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	worker, err := deploy.AcquireExistingOperationLock(workerCtx, f.live.directory)
	if err != nil {
		f.t.Fatalf("manager cleanup could not acquire operation lock: %v", err)
	}
	defer worker.Unlock()
	record, found, err := worker.ReadWorkloadLifecycleV1()
	if err != nil || !found || record.State != "stopping" || record.ContainerID != f.old.ContainerID || request.Container != f.old.ContainerID ||
		request.Record != record || record.RetirementOwner == "" || record.RecoveryOwners == "" || request.Engine != *state.IdentityMapping.Engine || request.Run != *state.IdentityMapping.NativeProcess {
		f.t.Fatal("retirement did not select the persisted exact inhibited owner")
	}
	if err := worker.RequireQueueEntryLeaseHeldV1(record.RetirementOwner); err != nil {
		f.t.Fatal("retirement lost exclusive lease", err)
	}
	f.trace = append(f.trace, "retire")
	if f.duringRetire != nil {
		f.duringRetire(worker, request)
	}
	if f.cancelOnRetire != nil {
		f.cancelOnRetire()
	}
	return f.retireErr
}

func (f *mappedWorkloadFixtureV1) MappedWorkload(_ context.Context, _ deploy.StateV1, request mappedWorkloadRequestV1) (mappedWorkloadProofV1, error) {
	f.trace = append(f.trace, request.Phase)
	proof := f.proof(request)
	if f.afterObserve != nil {
		f.afterObserve(request)
	}
	return proof, f.observeErr
}

func (f *mappedWorkloadFixtureV1) DetachMappedWorkload(_ context.Context, _ deploy.StateV1, request mappedWorkloadRequestV1) error {
	f.t.Helper()
	record, found, err := f.ops.Operation.ReadWorkloadLifecycleV1()
	if err != nil || !found || record != request.Record || record.State != "starting" || record.ContainerID != f.live.waiter.id ||
		!record.TransferClaimed || record.HandoffComplete || !record.StartupComplete {
		f.t.Fatal("stream detachment preceded its durable exact-instance claim")
	}
	f.trace = append(f.trace, "detach")
	return f.detachErr
}

func (f *mappedWorkloadFixtureV1) reload(ctx context.Context, ops deploy.IdentityMappingOperationsV1) (mappedEntryInputV1, error) {
	f.t.Helper()
	f.trace = append(f.trace, "reload")
	if ctx.Err() != nil || ops.Operation.RequireHeld() != nil {
		f.t.Fatal("fresh reader lost its held lock/context")
	}
	s, err := ops.Status()
	if err != nil || s.State.IdentityMapping.PendingOperation != nil {
		f.t.Fatal("fresh reader preceded exact native retirement")
	}
	record, found, err := ops.Operation.ReadWorkloadLifecycleV1()
	if err != nil || !found || record.State != "down" || record.ContainerID != f.old.ContainerID {
		f.t.Fatal("fresh reader preceded persisted old completion")
	}
	input := f.live.input
	state := cloneMappedWorkloadStateFixtureV1(f.t, s.State)
	template := cloneMappedWorkloadStateFixtureV1(f.t, f.live.input.Current.State).IdentityMapping.PendingOperation
	template.ExpectedRevision, template.ExpectedGeneration = state.IdentityMapping.Revision, state.Current
	state.IdentityMapping.PendingOperation = template
	input.Current.State = state
	input.Current.Generation = *state.Current
	if f.changeInput != nil {
		f.changeInput(&input)
	}
	writeMappedLiveStateFixtureV1(f.t, ops.Operation, state)
	return input, nil
}

func (f *mappedWorkloadFixtureV1) prepare() (mappedEntryPlanV1, error) {
	return prepareMappedWorkloadReplacementV1(f.live.waiter.ctx, &f.ops, f.reload)
}

func (f *mappedWorkloadFixtureV1) released(plan mappedEntryPlanV1) *ownedMappedWaiterV1 {
	f.t.Helper()
	w := f.live.waiter
	w.prepared.ops = f.ops
	facts, err := plan.facts()
	if err != nil {
		f.t.Fatal(err)
	}
	status, err := f.ops.Status()
	if err != nil {
		f.t.Fatal(err)
	}
	// Reuse the existing owned-waiter transport fixture. This models the
	// atomic writer result only; actual host-checked admission is tested in E1.2.
	w.backend.admit = func(ops deploy.IdentityMappingOperationsV1, ctx context.Context, state deploy.StateV1, id string) (deploy.StateV1, error) {
		if ctx.Err() != nil || ops.Operation.RequireHeld() != nil || id != facts.Operation {
			return deploy.StateV1{}, errors.New("wrong admission owner")
		}
		m := state.IdentityMapping
		p := m.PendingOperation
		a := p.Native.Active
		m.UIDAssignments, m.GIDAssignments = a.UIDs, a.GIDs
		m.AcceptedUIDs, m.AcceptedGIDs = &a.UIDs, &a.GIDs
		m.AdmittedGeneration, m.Engine, m.NativeProcess = state.Current, &p.Native.Engine, &p.Native.Process
		r, _ := strconv.ParseUint(m.Revision, 10, 64)
		m.Revision = strconv.FormatUint(r+1, 10)
		p.ExpectedRevision, p.Phase = m.Revision, "release-attempted"
		writeMappedLiveStateFixtureV1(f.t, ops.Operation, state)
		return state, nil
	}
	f.live.untilCancel = true
	f.live.plan = plan
	spec, policy, err := mappedLiveCreateSpecV1(f.ops, status.State, f.live.directory, facts)
	if err != nil {
		f.t.Fatal(err)
	}
	waiter, err := f.live.backend.start(w.ctx, f.ops, status.State, mappedStateGeometryV1(facts.Active), facts.Operation, spec, policy, plan.binding, w.options)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = waiter.close() })
	if err := f.ops.Operation.RecordLiveRunContainerV1(f.live.run.ID, waiter.container); err != nil {
		f.t.Fatal(err)
	}
	if _, err := waiter.release(); err != nil {
		f.t.Fatal(err)
	}
	return waiter
}

func TestMappedWorkloadReplacementOrdersRetirementAndFreshAdmissionV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		t.Run(profile, func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, profile)
			before, _ := f.ops.Status()
			plan, err := f.prepare()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.trace, []string{"retire", "retirement", "reload"}) || f.live.waiter.created != 0 {
				t.Fatal("replacement bypassed ordered trusted retirement or created early")
			}
			record, _, err := f.ops.Operation.ReadWorkloadLifecycleV1()
			if err != nil || record.State != "starting" || record.ContainerID != "" || record.StartupComplete || record.Runtime != strings.TrimPrefix(string(plan.binding.Plan), "sha256:") {
				t.Fatal("replacement persisted stale startup evidence")
			}
			current, _ := f.ops.Status()
			if !reflect.DeepEqual(before.State.IdentityMapping.UIDAssignments, current.State.IdentityMapping.UIDAssignments) ||
				!reflect.DeepEqual(before.State.IdentityMapping.AcceptedUIDs, current.State.IdentityMapping.AcceptedUIDs) ||
				current.State.IdentityMapping.PendingOperation.OperationID == "old-operation" || plan.binding == f.live.plan.binding {
				t.Fatal("replacement lost history or reused original final plan")
			}
		})
	}
}

func TestMappedWorkloadRetirementUncertaintyBlocksReloadV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, name := range []string{"stop error", "still running", "not retired", "wrong digest", "wrong instance", "wrong start", "restart", "autoremove", "native recovery"} {
			t.Run(profile+"/"+name, func(t *testing.T) {
				f := newMappedWorkloadFixtureV1(t, profile)
				switch name {
				case "stop error":
					f.retireErr = errors.New("owned stop unproved")
				case "native recovery":
					f.fail = true
					state, _ := f.ops.Status()
					state.State.IdentityMapping.PendingOperation = f.pending
					writeMappedLiveStateFixtureV1(t, f.ops.Operation, state.State)
				default:
					f.changeProof = func(_ mappedWorkloadRequestV1, p *mappedWorkloadProofV1) {
						switch name {
						case "still running":
							p.Running = true
						case "not retired":
							p.Retired = false
						case "wrong digest":
							p.Request = canonical.Digest(strings.Repeat("f", 64))
						case "wrong instance":
							p.Container = strings.Repeat("b", 64)
						case "wrong start":
							p.StartedAt = "2026-01-03T00:00:00Z"
						case "restart":
							p.RestartPolicy = "always"
						case "autoremove":
							p.AutoRemove = true
						}
					}
				}
				want := []string{"retire", "retirement"}
				if name == "stop error" {
					want = []string{"retire"}
				}
				if _, err := f.prepare(); err == nil || !slices.Equal(f.trace, want) {
					t.Fatal("uncertain retirement reached fresh admission")
				}
				r, _, _ := f.ops.Operation.ReadWorkloadLifecycleV1()
				s, _ := f.ops.Status()
				if r.State != "stopping" || r.ContainerID != f.old.ContainerID || name == "native recovery" && s.State.IdentityMapping.PendingOperation == nil || len(s.State.IdentityMapping.UIDAssignments) == 0 {
					t.Fatal("uncertainty erased old ownership or reservations")
				}
			})
		}
	}
}

func TestMappedWorkloadReplacementRejectsCachedOrWrongEntryV1(t *testing.T) {
	for _, name := range []string{"old process", "application entry", "unlocked snapshot", "wrong generation"} {
		t.Run(name, func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, "exact")
			f.changeInput = func(input *mappedEntryInputV1) {
				switch name {
				case "old process":
					input.Current.State.IdentityMapping.PendingOperation.Native.Process = "old-process"
				case "application entry":
					input.Kind = mappedApplicationCommandV1
				case "unlocked snapshot":
					input.Current.State = cloneMappedWorkloadStateFixtureV1(t, input.Current.State)
					input.Current.State.BlueprintSource = "not the locked owner"
				case "wrong generation":
					input.Current.Generation.Reference = "changed"
				}
			}
			if _, err := f.prepare(); err == nil || f.live.waiter.created != 0 {
				t.Fatal("cached or unrelated replacement accepted")
			}
			record, _, _ := f.ops.Operation.ReadWorkloadLifecycleV1()
			if record.State != "down" || record.StartedAt != f.old.StartedAt {
				t.Fatal("rejected fresh admission replaced old completed record")
			}
		})
	}
}

func TestMappedWorkloadRequiresTrustedBoundedOwnerBeforeEffectsV1(t *testing.T) {
	for _, name := range []string{"missing provider", "no context", "unbounded", "cancelled", "no reader", "old endpoint", "old identity missing"} {
		t.Run(name, func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, "exact")
			ctx, reload := f.live.waiter.ctx, f.reload
			switch name {
			case "missing provider":
				f.ops.Evidence = f.waiterEffectFixtureV1
			case "no context":
				ctx = nil
			case "unbounded":
				ctx = context.Background()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				cancel()
			case "no reader":
				reload = nil
			case "old endpoint":
				r := f.old
				r.Endpoint = strings.Repeat("e", 64)
				if err := f.ops.Operation.WriteWorkloadLifecycleV1(r); err != nil {
					t.Fatal(err)
				}
			case "old identity missing":
				r := f.old
				r.ContainerID, r.StartedAt, r.State, r.StartupComplete = "", "", "starting", false
				if err := f.ops.Operation.WriteWorkloadLifecycleV1(r); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := prepareMappedWorkloadReplacementV1(ctx, &f.ops, reload); err == nil || len(f.trace) != 0 {
				t.Fatal("unqualified owner reached lifecycle effects")
			}
		})
	}
}

func TestMappedWorkloadStartupAndQualifiedHandoffV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, detached := range []bool{false, true} {
			t.Run(profile+"/"+strconv.FormatBool(detached), func(t *testing.T) {
				f := newMappedWorkloadFixtureV1(t, profile)
				plan, err := f.prepare()
				if err != nil {
					t.Fatal(err)
				}
				waiter := f.released(plan)
				if err := confirmMappedWorkloadStartupV1(f.live.waiter.ctx, f.ops, plan, waiter, detached); err != nil {
					t.Fatal(err)
				}
				record, _, _ := f.ops.Operation.ReadWorkloadLifecycleV1()
				if record.State != "up" || !record.StartupComplete || record.ContainerID != waiter.container || record.StartedAt == f.old.StartedAt || record.HandoffComplete != detached || record.TransferClaimed != detached {
					t.Fatal("lifecycle did not preserve exact startup/handoff identity")
				}
				want := []string{"retire", "retirement", "reload", "attached"}
				if detached {
					want = append(want, "detach", "detached")
				}
				if !slices.Equal(f.trace, want) {
					t.Fatalf("startup/detachment observation order: %v", f.trace)
				}
				if err := confirmMappedWorkloadStartupV1(f.live.waiter.ctx, f.ops, plan, waiter, detached); err == nil || !slices.Equal(f.trace, want) {
					t.Fatal("completed instance resent startup/detachment")
				}
			})
		}
	}
}

func TestMappedWorkloadMissingStartupOrDetachProofRetainsUncertaintyV1(t *testing.T) {
	for _, name := range []string{"provider error", "running only", "exited", "wrong instance", "wrong digest", "invalid start", "restart policy", "restart count", "autoremove", "early detach", "detach error", "detach unproved", "post-detach identity", "post-detach exited"} {
		t.Run(name, func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, "exact")
			plan, err := f.prepare()
			if err != nil {
				t.Fatal(err)
			}
			waiter := f.released(plan)
			detach := strings.HasPrefix(name, "detach") || strings.HasPrefix(name, "post-detach")
			if name == "provider error" {
				f.observeErr = errors.New("F unavailable")
			}
			if name == "detach error" {
				f.detachErr = errors.New("transport uncertain")
			}
			f.changeProof = func(r mappedWorkloadRequestV1, p *mappedWorkloadProofV1) {
				switch name {
				case "running only":
					p.StartupComplete = false
				case "exited":
					p.Running = false
				case "wrong instance":
					p.Container = f.old.ContainerID
				case "wrong digest":
					p.Request = canonical.Digest(strings.Repeat("f", 64))
				case "invalid start":
					p.StartedAt = "not-a-time"
				case "restart policy":
					p.RestartPolicy = "always"
				case "restart count":
					p.RestartCount = 1
				case "autoremove":
					p.AutoRemove = true
				case "early detach":
					p.Detached = true
				case "detach unproved":
					p.Detached = false
				case "post-detach identity":
					if r.Phase == "detached" {
						p.StartedAt = "2026-01-03T00:00:00Z"
					}
				case "post-detach exited":
					if r.Phase == "detached" {
						p.Running = false
					}
				}
			}
			if err := confirmMappedWorkloadStartupV1(f.live.waiter.ctx, f.ops, plan, waiter, detach); err == nil {
				t.Fatal("unqualified startup/handoff was accepted")
			}
			record, _, _ := f.ops.Operation.ReadWorkloadLifecycleV1()
			s, _ := f.ops.Status()
			if record.State != "starting" || record.HandoffComplete || s.State.IdentityMapping.PendingOperation == nil || len(s.State.IdentityMapping.UIDAssignments) == 0 {
				t.Fatal("startup uncertainty lost recovery owner or reserves")
			}
			before := slices.Clone(f.trace)
			if detach && record.TransferClaimed {
				if err := confirmMappedWorkloadStartupV1(f.live.waiter.ctx, f.ops, plan, waiter, true); err == nil || !slices.Equal(before, f.trace) {
					t.Fatal("uncertain detachment repeated its transfer")
				}
			}
		})
	}
}

func TestMappedWorkloadStartupRefusesChangedOwnerBeforeObservationV1(t *testing.T) {
	for _, name := range []string{"no release", "closed waiter", "wrong binding", "wrong operation lock", "missing provider", "changed native process", "changed revision", "changed lifecycle"} {
		t.Run(name, func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, "bounded")
			plan, err := f.prepare()
			if err != nil {
				t.Fatal(err)
			}
			waiter := f.released(plan)
			switch name {
			case "no release":
				waiter.mu.Lock()
				waiter.consumed = false
				waiter.mu.Unlock()
			case "closed waiter":
				if err := waiter.close(); err != nil {
					t.Fatal(err)
				}
			case "wrong binding":
				plan.binding.Operation = "foreign"
			case "wrong operation lock":
				f.ops.Operation = nil
			case "missing provider":
				f.ops.Evidence = f.waiterEffectFixtureV1
			case "changed native process", "changed revision":
				status, _ := f.ops.Status()
				if name == "changed native process" {
					status.State.IdentityMapping.PendingOperation.Native.Process = "foreign"
					*status.State.IdentityMapping.NativeProcess = "foreign"
				} else {
					status.State.IdentityMapping.Revision = "42"
					status.State.IdentityMapping.PendingOperation.ExpectedRevision = "42"
				}
				writeMappedLiveStateFixtureV1(t, f.ops.Operation, status.State)
			case "changed lifecycle":
				record, _, _ := f.ops.Operation.ReadWorkloadLifecycleV1()
				record.Runtime = strings.Repeat("e", 64)
				if err := f.ops.Operation.WriteWorkloadLifecycleV1(record); err != nil {
					t.Fatal(err)
				}
			}
			trace := slices.Clone(f.trace)
			if err := confirmMappedWorkloadStartupV1(f.live.waiter.ctx, f.ops, plan, waiter, true); err == nil || !slices.Equal(trace, f.trace) {
				t.Fatal("changed owner reached startup or detachment observation")
			}
		})
	}
}

func TestMappedWorkloadCancellationCannotRetireOrReloadV1(t *testing.T) {
	f := newMappedWorkloadFixtureV1(t, "exact")
	ctx, cancel := context.WithTimeout(f.live.waiter.ctx, time.Second)
	defer cancel()
	f.cancelOnRetire = cancel
	if _, err := prepareMappedWorkloadReplacementV1(ctx, &f.ops, f.reload); err == nil || !slices.Equal(f.trace, []string{"retire"}) {
		t.Fatal("cancelled retirement reached fresh admission")
	}
	record, _, err := f.ops.Operation.ReadWorkloadLifecycleV1()
	if err != nil || record.State != "stopping" || record.ContainerID != f.old.ContainerID {
		t.Fatal("cancellation cleared exact old ownership")
	}
}

func TestMappedWorkloadOwnerCloseDuringObservationCannotCommitUpV1(t *testing.T) {
	for _, detached := range []bool{false, true} {
		t.Run(strconv.FormatBool(detached), func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, "exact")
			plan, err := f.prepare()
			if err != nil {
				t.Fatal(err)
			}
			waiter := f.released(plan)
			f.afterObserve = func(request mappedWorkloadRequestV1) {
				if request.Phase == "detached" || !detached {
					if err := waiter.close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := confirmMappedWorkloadStartupV1(f.live.waiter.ctx, f.ops, plan, waiter, detached); err == nil {
				t.Fatal("closed lifetime owner was recorded up")
			}
			record, _, err := f.ops.Operation.ReadWorkloadLifecycleV1()
			if err != nil || record.State != "starting" || record.HandoffComplete {
				t.Fatal("owner loss completed lifecycle handoff")
			}
		})
	}
}

func TestMappedWorkloadOwnerLossBeforeDetachHasNoTransferEffectsV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		t.Run(profile, func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, profile)
			plan, err := f.prepare()
			if err != nil {
				t.Fatal(err)
			}
			waiter := f.released(plan)
			f.afterObserve = func(request mappedWorkloadRequestV1) {
				if request.Phase == "attached" {
					if err := waiter.close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := confirmMappedWorkloadStartupV1(f.live.waiter.ctx, f.ops, plan, waiter, true); err == nil {
				t.Fatal("lost foreground owner completed detached startup")
			}
			if !slices.Equal(f.trace, []string{"retire", "retirement", "reload", "attached"}) {
				t.Fatalf("lost foreground owner reached detach effects: %v", f.trace)
			}
			record, _, err := f.ops.Operation.ReadWorkloadLifecycleV1()
			if err != nil || record.State != "starting" || record.TransferClaimed || record.HandoffComplete || record.ContainerID != "" {
				t.Fatal("lost foreground owner persisted a transfer claim")
			}
		})
	}
}

func TestMappedWorkloadOwnerInventoryMustBeCompleteBeforeInhibitionV1(t *testing.T) {
	for _, name := range []string{"unknown inventory", "empty", "missing engine", "duplicate", "wrong engine", "wrong engine revision", "unknown owner", "unordered", "unversioned controller"} {
		t.Run(name, func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, "exact")
			before, _ := f.ops.Status()
			switch name {
			case "unknown inventory":
				f.inventoryErr = errors.New("manager enumeration unavailable")
			case "empty":
				f.owners = nil
			case "missing engine":
				f.owners = f.owners[:1]
			case "duplicate":
				f.owners = append(f.owners, f.owners[1])
			case "wrong engine":
				f.owners[1].ID = strings.Repeat("b", 64)
			case "wrong engine revision":
				f.owners[1].Revision = "2026-01-03T00:00:00Z"
			case "unknown owner":
				f.owners[0].Kind = "daemon"
			case "unordered":
				slices.Reverse(f.owners)
			case "unversioned controller":
				f.owners[0].Revision = ""
			}
			if _, err := f.prepare(); err == nil || len(f.trace) != 0 {
				t.Fatal("incomplete inventory reached retirement or admission")
			}
			record, found, err := f.ops.Operation.ReadWorkloadLifecycleV1()
			after, stateErr := f.ops.Status()
			if err != nil || stateErr != nil || !found || record != f.old || !reflect.DeepEqual(before.State, after.State) {
				t.Fatal("inventory refusal changed retained state")
			}
		})
	}
}

func TestMappedWorkloadFreshRetirementRequiresEveryOwnerFenceV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, name := range []string{"missing controller fence", "changed controller fence", "inventory changed", "inventory lost", "fresh observation lost", "observer changed lifecycle"} {
			t.Run(profile+"/"+name, func(t *testing.T) {
				f := newMappedWorkloadFixtureV1(t, profile)
				before, _ := f.ops.Status()
				data := filepath.Join(f.live.directory, "retained-data")
				if err := os.WriteFile(data, []byte("application sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
				switch name {
				case "missing controller fence", "changed controller fence":
					f.changeProof = func(r mappedWorkloadRequestV1, p *mappedWorkloadProofV1) {
						if r.Phase != "retirement" {
							return
						}
						if name == "missing controller fence" {
							p.FencedOwners = p.FencedOwners[1:]
						} else {
							p.FencedOwners[0].Revision = "stale-manager"
						}
					}
				case "inventory changed":
					f.changeOwners = func(n int, o []mappedRecoveryOwnerV1) []mappedRecoveryOwnerV1 {
						if n > 1 {
							o[0].Revision = "replacement-manager"
						}
						return o
					}
				case "inventory lost":
					f.duringRetire = func(*deploy.OperationLock, mappedWorkloadRequestV1) {
						f.inventoryErr = errors.New("manager inventory lost")
					}
				case "fresh observation lost":
					f.observeErr = errors.New("engine observation lost after successful stop")
				case "observer changed lifecycle":
					f.afterObserve = func(r mappedWorkloadRequestV1) {
						if r.Phase == "retirement" {
							old := r.Record
							old.Runtime = strings.Repeat("f", 64)
							if err := f.ops.Operation.WriteWorkloadLifecycleV1(old); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				if _, err := f.prepare(); err == nil || slices.Contains(f.trace, "reload") {
					t.Fatal("unproved complete fence reached preparation")
				}
				after, err := f.ops.Status()
				got, readErr := os.ReadFile(data)
				if err != nil || readErr != nil || string(got) != "application sentinel" || !reflect.DeepEqual(before.State, after.State) {
					t.Fatal("fence refusal lost data or reservations")
				}
				r, found, err := f.ops.Operation.ReadWorkloadLifecycleV1()
				if err != nil || !found || r.State != "stopping" || r.RetirementOwner == "" || r.RecoveryOwners == "" {
					t.Fatal("fence refusal lost durable inhibition")
				}
				if _, err := os.Stat(filepath.Join(f.live.directory, ".reploy", r.RetirementOwner+".lease")); err != nil {
					t.Fatal("uncertain retirement lost retained lease file", err)
				}
			})
		}
	}
}

func TestMappedWorkloadStopSuccessIsNotFreshRetirementProofV1(t *testing.T) {
	f := newMappedWorkloadFixtureV1(t, "bounded")
	f.observeErr = errors.New("fresh observation unavailable")
	if _, err := f.prepare(); err == nil || !slices.Equal(f.trace, []string{"retire", "retirement"}) {
		t.Fatal("successful stop substituted for fresh observation")
	}
}

func TestMappedWorkloadCompetingRetirementCannotPassHeldLeaseV1(t *testing.T) {
	f := newMappedWorkloadFixtureV1(t, "exact")
	original := f.ops.Operation
	f.duringRetire = func(worker *deploy.OperationLock, request mappedWorkloadRequestV1) {
		competing := *f
		competing.trace = nil
		competing.ops.Operation = worker
		competing.ops.Evidence = &competing
		competing.duringRetire = nil
		if _, err := competing.prepare(); err == nil || len(competing.trace) != 0 {
			t.Fatal("concurrent retirement bypassed exact control lease")
		}
		retained, _, err := worker.ReadWorkloadLifecycleV1()
		if err != nil || retained != request.Record {
			t.Fatal("competing owner changed inhibition")
		}
	}
	if _, err := f.prepare(); err != nil {
		t.Fatal(err)
	}
	if f.ops.Operation == original || original.RequireHeld() == nil || f.ops.Operation.RequireHeld() != nil {
		t.Fatal("caller did not receive exclusively reacquired lock")
	}
}

func TestMappedWorkloadUncertainRetirementRetriesSameDurableOwnerV1(t *testing.T) {
	f := newMappedWorkloadFixtureV1(t, "bounded")
	f.retireErr = errors.New("stop response lost")
	if _, err := f.prepare(); err == nil {
		t.Fatal("lost response accepted")
	}
	first, _, err := f.ops.Operation.ReadWorkloadLifecycleV1()
	if err != nil {
		t.Fatal(err)
	}
	f.retireErr = nil
	f.trace = nil
	f.duringRetire = func(_ *deploy.OperationLock, r mappedWorkloadRequestV1) {
		if r.Record != first {
			t.Fatal("retry replaced retained retirement owner")
		}
	}
	if _, err := f.prepare(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.live.directory, ".reploy", first.RetirementOwner+".lease")); !os.IsNotExist(err) {
		t.Fatal("completed retirement retained an active transaction lease")
	}
}

func TestMappedWorkloadRejectsStateChangedDuringUnlockedStopV1(t *testing.T) {
	for _, name := range []string{"mapping revision", "native owner", "lifecycle owner", "lock file", "lease file"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && (name == "lock file" || name == "lease file") {
				t.Skip("Windows denies renaming an open locked file; covered by kernel sharing refusal")
			}
			f := newMappedWorkloadFixtureV1(t, "exact")
			f.duringRetire = func(worker *deploy.OperationLock, r mappedWorkloadRequestV1) {
				switch name {
				case "mapping revision", "native owner":
					ops := f.ops
					ops.Operation = worker
					actual, err := ops.Status()
					if err != nil {
						t.Fatal(err)
					}
					if name == "mapping revision" {
						actual.State.IdentityMapping.Revision = "3"
					} else {
						*actual.State.IdentityMapping.NativeProcess = "replacement-process"
					}
					writeMappedLiveStateFixtureV1(t, worker, actual.State)
				case "lifecycle owner":
					record := r.Record
					record.Runtime = strings.Repeat("e", 64)
					if err := worker.WriteWorkloadLifecycleV1(record); err != nil {
						t.Fatal(err)
					}
				case "lock file", "lease file":
					path := worker.Path()
					if name == "lease file" {
						path = filepath.Join(filepath.Dir(path), r.Record.RetirementOwner+".lease")
					}
					if err := os.Rename(path, path+".retained"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("foreign replacement"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := f.prepare(); err == nil || !slices.Equal(f.trace, []string{"retire"}) {
				t.Fatal("changed owner reached fresh proof or admission")
			}
			if name == "lease file" {
				entries, err := filepath.Glob(filepath.Join(f.live.directory, ".reploy", "control-*.lease"))
				if err != nil || len(entries) != 1 {
					t.Fatal(entries, err)
				}
				content, err := os.ReadFile(entries[0])
				if err != nil || string(content) != "foreign replacement" {
					t.Fatal("refusal removed a foreign replacement lease")
				}
			}
		})
	}
}

func TestMappedWorkloadStopRequiresSeparateFreshObservationV1(t *testing.T) {
	f := newMappedWorkloadFixtureV1(t, "exact")
	stopped, observations := false, 0
	f.duringRetire = func(*deploy.OperationLock, mappedWorkloadRequestV1) { stopped = true }
	f.changeProof = func(r mappedWorkloadRequestV1, _ *mappedWorkloadProofV1) {
		if r.Phase == "retirement" {
			observations++
			if !stopped || f.ops.Operation.RequireHeld() != nil {
				t.Fatal("fresh observation preceded unlocked stop and lock reacquisition")
			}
		}
	}
	if _, err := f.prepare(); err != nil || observations != 1 || !slices.Equal(f.trace, []string{"retire", "retirement", "reload"}) {
		t.Fatal("retirement command substituted for separate fresh observation", err, observations)
	}
}

func TestMappedWorkloadReplacedDirectoriesCannotAcquireContinuationV1(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows sharing refuses replacement of open lock directories")
	}
	for _, kind := range []string{"deployment", "state directory"} {
		t.Run(kind, func(t *testing.T) {
			f := newMappedWorkloadFixtureV1(t, "bounded")
			f.duringRetire = func(worker *deploy.OperationLock, r mappedWorkloadRequestV1) {
				original := f.live.directory
				if kind == "state directory" {
					original = filepath.Join(original, ".reploy")
				}
				retained := original + ".retained"
				if err := os.Rename(original, retained); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = os.RemoveAll(original)
					if err := os.Rename(retained, original); err != nil {
						t.Error(err)
					}
				})
				destination := original
				source := retained
				if kind == "deployment" {
					source = filepath.Join(retained, ".reploy")
					destination = filepath.Join(original, ".reploy")
				}
				if err := os.MkdirAll(destination, 0700); err != nil {
					t.Fatal(err)
				}
				// The foreign deployment deliberately has identical logical state. Only
				// original directory identity can distinguish it from this transaction.
				for _, name := range []string{"operation.lock", "state.json", "workload-lifecycle.json"} {
					data, err := os.ReadFile(filepath.Join(source, name))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(destination, name), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := f.prepare(); err == nil || !slices.Equal(f.trace, []string{"retire"}) {
				t.Fatal("foreign directory accepted as retirement continuation")
			}
		})
	}
}

func TestMappedWorkloadRetirementObservationLossKeepsDataAndReservationsV1(t *testing.T) {
	f := newMappedWorkloadFixtureV1(t, "bounded")
	before, _ := f.ops.Status()
	data := filepath.Join(f.live.directory, "application-data")
	if err := os.WriteFile(data, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.live.waiter.ctx, time.Second)
	defer cancel()
	original := f.ops.Operation
	f.cancelOnRetire = cancel
	if _, err := prepareMappedWorkloadReplacementV1(ctx, &f.ops, f.reload); err == nil {
		t.Fatal("cancelled manager stop authorized preparation")
	}
	if f.ops.Operation == nil || f.ops.Operation == original || f.ops.Operation.RequireHeld() != nil {
		t.Fatal("bounded cancellation cleanup did not return the current lock")
	}
	after, err := f.ops.Status()
	contents, readErr := os.ReadFile(data)
	if err != nil || readErr != nil || string(contents) != "retained" || !reflect.DeepEqual(before.State, after.State) {
		t.Fatal("cancelled retirement lost retained data or mapping reservations")
	}
	record, found, err := f.ops.Operation.ReadWorkloadLifecycleV1()
	if err != nil || !found || record.State != "stopping" || record.RetirementOwner == "" {
		t.Fatal("cancelled retirement lost durable inhibition")
	}
}

func TestMappedWorkloadRetainedInventoryCannotBeSilentlyReboundV1(t *testing.T) {
	f := newMappedWorkloadFixtureV1(t, "exact")
	f.retireErr = errors.New("uncertain manager stop")
	if _, err := f.prepare(); err == nil {
		t.Fatal("fixture should retain uncertain retirement")
	}
	retained, _, err := f.ops.Operation.ReadWorkloadLifecycleV1()
	if err != nil {
		t.Fatal(err)
	}
	f.trace = nil
	f.retireErr = nil
	f.owners[0].Revision = "new-registration"
	if _, err := f.prepare(); err == nil || len(f.trace) != 0 {
		t.Fatal("new owner inventory rebound a retained retirement")
	}
	current, _, err := f.ops.Operation.ReadWorkloadLifecycleV1()
	if err != nil || current != retained {
		t.Fatal("rejected inventory changed original durable owner")
	}
}

func TestMappedWorkloadMissingOriginalAuthorityCannotPrepareV1(t *testing.T) {
	f := newMappedWorkloadFixtureV1(t, "exact")
	if _, err := prepareMappedWorkloadReplacementV1(f.live.waiter.ctx, nil, f.reload); err == nil {
		t.Fatal("nil caller authority accepted")
	}
	path := filepath.Join(f.live.directory, ".reploy", "workload-lifecycle.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.prepare(); err == nil || len(f.trace) != 0 {
		t.Fatal("unbound old native process bypassed retirement")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing old lifecycle was fabricated")
	}
}

func TestMappedWorkloadFailedReacquireRetainsInhibitionV1(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows sharing prevents removing the cleanup worker's open lock file")
	}
	f := newMappedWorkloadFixtureV1(t, "bounded")
	var owner string
	f.duringRetire = func(worker *deploy.OperationLock, r mappedWorkloadRequestV1) {
		owner = r.Record.RetirementOwner
		if err := os.Remove(worker.Path()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.prepare(); err == nil || f.ops.Operation != nil || !slices.Equal(f.trace, []string{"retire"}) {
		t.Fatal("failed reacquisition reached admission or returned a stale lock")
	}
	contents, err := os.ReadFile(filepath.Join(f.live.directory, ".reploy", "workload-lifecycle.json"))
	if err != nil || !strings.Contains(string(contents), owner) || !strings.Contains(string(contents), "stopping") {
		t.Fatal("failed reacquisition lost original inhibition")
	}
	if _, err := os.Stat(filepath.Join(f.live.directory, ".reploy", owner+".lease")); err != nil {
		t.Fatal("failed reacquisition deleted retained lease evidence", err)
	}
}
