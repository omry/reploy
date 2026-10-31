package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"github.com/omry/reploy/internal/mappedlaunch"
	"github.com/omry/reploy/internal/providers/registry"
)

// Real locks, leases, queue and immutable build readers surround the existing
// waiter fixture. Host admission/F/kernel effects remain explicitly injected.
// These are private orchestration proofs, not production runtime qualification.
type mappedLiveFixtureV1 struct {
	waiter               *waiterFixtureV1
	plan                 mappedEntryPlanV1
	input                mappedEntryInputV1
	run                  deploy.LiveRunV1
	lease                *deploy.QueueEntryLeaseV1
	backend              mappedLiveRunBackendV1
	directory            string
	recoveries, starts   int
	retirementError      error
	incompleteRetirement bool
	attachErr            error
	exitStatus           string
	untilCancel          bool
	beforeRecovery       func(*deploy.OperationLock)
	beforeStart          func()
}

func writeMappedLiveStateFixtureV1(t *testing.T, operation *deploy.OperationLock, state deploy.StateV1) {
	t.Helper()
	encoded, err := deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(operation.Path()), "state.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

func newMappedLiveFixtureV1(t *testing.T, profile string, kind mappedEntryKindV1) *mappedLiveFixtureV1 {
	t.Helper()
	f := &mappedLiveFixtureV1{waiter: newWaiterFixtureV1(t)}
	f.exitStatus = "0\n"
	w := f.waiter
	operation := w.prepared.ops.Operation
	f.directory = filepath.Dir(filepath.Dir(operation.Path()))
	i := mappedEntryPlanFixtureV1(t, profile)
	i.Kind = kind
	i.Environment = []string{"HOME=/tmp/explicit", "TMPDIR=/tmp/explicit", "TOKEN=dummy"}
	i.Current.State.IdentityMapping.OwnerDir = f.directory
	i.Current.Generation.Reference = w.prepared.state.Current.Reference
	i.Current.State.Current = &i.Current.Generation
	i.Current.State.IdentityMapping.PendingOperation.ExpectedGeneration = i.Current.State.Current
	i.Runtime.Image = i.Current.Generation.Reference
	i.Current.State.IdentityMapping.PendingOperation.Native.Engine = w.prepared.endpoint
	i.Current.State.IdentityMapping.PendingOperation.Native.Process = "mapped-fixture-instance"
	plan, err := constructMappedEntryPlanV1(i)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.PublishBuildLock(i.Current.Lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	writeMappedLiveStateFixtureV1(t, operation, i.Current.State)
	f.input, f.plan = i, plan
	f.run = liveRunAdmissionFixtureV1("run-0000000000000041", false)
	f.run.Name, f.run.GenerationReference = i.Command, i.Current.Generation.Reference
	f.lease, err = operation.AcquireLiveRunLeaseV1(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.lease.Close() })
	if _, err := operation.AdmitLiveRunV1(f.run, false); err != nil {
		t.Fatal(err)
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	f.run = queue.Runs[0]
	f.backend = mappedLiveRunBackendV1{
		acquire: deploy.AcquireOperationLock,
		start: func(ctx context.Context, ops deploy.IdentityMappingOperationsV1, state deploy.StateV1, active identitymapping.Geometry, id string, spec CommandSpec, policy mappedSeccompPolicyV1, binding mappedlaunch.Binding, options RunOptions) (*ownedMappedWaiterV1, error) {
			f.starts++
			if f.beforeStart != nil {
				f.beforeStart()
			}
			if err := operation.RequireHeld(); err != nil {
				return nil, err
			}
			w.prepared.ops, w.prepared.state = ops, state
			w.prepared.active, w.prepared.operation = active, id
			w.prepared.spec, w.prepared.policy, w.binding = spec, policy, binding
			return startMappedWaiterWithBackendV1(ctx, w.prepared, binding, options, w.backend)
		},
		recover: func(ops deploy.IdentityMappingOperationsV1, ctx context.Context, state deploy.StateV1, id string) (deploy.StateV1, error) {
			f.recoveries++
			if f.beforeRecovery != nil {
				f.beforeRecovery(ops.Operation)
			}
			if err := ops.Operation.RequireHeld(); err != nil {
				return deploy.StateV1{}, err
			}
			if ctx.Err() != nil || w.removed != 1 || state.IdentityMapping.PendingOperation.Phase != "release-attempted" || id != bindingOperation(f.plan) {
				return deploy.StateV1{}, errors.New("retirement not bound to exact cleaned attempt")
			}
			if f.retirementError != nil {
				return deploy.StateV1{}, f.retirementError
			}
			if f.incompleteRetirement {
				return state, nil
			}
			state.IdentityMapping.PendingOperation = nil
			state.IdentityMapping.Revision = "3"
			writeMappedLiveStateFixtureV1(t, ops.Operation, state)
			return state, nil
		},
	}
	originalAdmit := w.backend.admit
	w.backend.admit = func(ops deploy.IdentityMappingOperationsV1, ctx context.Context, state deploy.StateV1, id string) (deploy.StateV1, error) {
		queue, _, err := operation.ReadLiveRunQueueV1()
		if err != nil || len(queue.Runs) == 0 || queue.Runs[0].Container != w.id {
			return deploy.StateV1{}, errors.New("release preceded durable exact live ownership")
		}
		if _, err := originalAdmit(ops, ctx, state, id); err != nil {
			return deploy.StateV1{}, err
		}
		// E1.2 tests the actual host-checked atomic writer. This injected
		// effect models only its validated release-attempted result.
		m := state.IdentityMapping
		active := m.PendingOperation.Native.Active
		m.UIDAssignments, m.GIDAssignments = active.UIDs, active.GIDs
		m.AcceptedUIDs, m.AcceptedGIDs = &active.UIDs, &active.GIDs
		m.AdmittedGeneration, m.Engine, m.NativeProcess = state.Current, &m.PendingOperation.Native.Engine, &m.PendingOperation.Native.Process
		m.Revision, m.PendingOperation.ExpectedRevision, m.PendingOperation.Phase = "2", "2", "release-attempted"
		writeMappedLiveStateFixtureV1(t, operation, state)
		return state, nil
	}
	originalRun := w.backend.run
	w.backend.run = func(spec CommandSpec, options RunOptions) error {
		if slices.Equal(spec.Args, []string{"wait", w.id}) {
			if options.Stdin != nil || options.Stdout == nil {
				return errors.New("wait must use engine status, not application streams")
			}
			_, err := fmt.Fprint(options.Stdout, f.exitStatus)
			return err
		}
		if spec.Args[0] != "start" {
			return originalRun(spec, options)
		}
		if err := originalRun(spec, options); err != nil {
			return err
		}
		// The frame/stream worker can finish before the caller unlocks.
		// No blocking helper reads the operation lock during release.
		if f.untilCancel {
			<-options.Context.Done()
			return options.Context.Err()
		}
		return f.attachErr
	}
	return f
}

func bindingOperation(p mappedEntryPlanV1) string { return p.binding.Operation }

func (f *mappedLiveFixtureV1) execute() error {
	return runMappedLiveInstanceWithBackendV1(f.waiter.ctx, f.directory, f.waiter.prepared.ops, f.run.ID, f.plan, f.waiter.options, f.backend)
}

func (f *mappedLiveFixtureV1) inspect(t *testing.T) (*deploy.OperationLock, deploy.LiveRunQueueV1, deploy.StateV1) {
	t.Helper()
	operation, err := deploy.AcquireOperationLock(t.Context(), f.directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	return operation, queue, state
}

func TestMappedLiveInstanceOwnsReleaseAndFiniteCompletionV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, kind := range []mappedEntryKindV1{mappedApplicationCommandV1, mappedRuntimeTestV1, mappedLifecycleHookV1, mappedWorkloadStartV1, mappedHostReplacementV1} {
			t.Run(fmt.Sprint(profile, kind), func(t *testing.T) {
				f := newMappedLiveFixtureV1(t, profile, kind)
				err := f.execute()
				master := kind == mappedWorkloadStartV1 || kind == mappedHostReplacementV1
				if master && !errors.Is(err, errMappedMasterExitedV1) || !master && err != nil {
					t.Fatalf("entry completion misclassified: %v", err)
				}
				_, queue, state := f.inspect(t)
				if len(queue.Runs) != 0 || state.IdentityMapping.PendingOperation != nil || f.starts != 1 || f.recoveries != 1 || f.waiter.created != 1 || f.waiter.removed != 1 || f.waiter.admissions != 1 {
					t.Fatal("instance did not complete exactly once")
				}
				if len(state.IdentityMapping.UIDAssignments) == 0 || state.IdentityMapping.AcceptedUIDs == nil || len(*state.IdentityMapping.AcceptedUIDs) == 0 {
					t.Fatal("retirement released retained mapping history")
				}
				select {
				case frame := <-f.waiter.frame:
					if frame.Binding != f.plan.binding || frame.Container != f.waiter.id {
						t.Fatal("wrong release frame")
					}
				default:
					t.Fatal("release was not delivered")
				}
			})
		}
	}
}

func TestMappedLiveInstanceCancellationReacquiresLockAndRetainsLeaseV1(t *testing.T) {
	f := newMappedLiveFixtureV1(t, "exact", mappedApplicationCommandV1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.waiter.options.Context, f.untilCancel = ctx, true
	f.beforeRecovery = func(operation *deploy.OperationLock) {
		if err := operation.RequireQueueEntryLeaseHeldV1(f.run.ID); err != nil {
			t.Error("live lease lost during attached run", err)
		}
	}
	result := make(chan error, 1)
	go func() { result <- f.execute() }()
	select {
	case <-f.waiter.frame:
	case <-time.After(2 * time.Second):
		t.Fatal("release blocked")
	}
	// An actual independent lock acquisition proves the attached path yielded
	// the operation lock, while its live lease and exact owner remain durable.
	operation, err := deploy.AcquireOperationLock(t.Context(), f.directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.RequireQueueEntryLeaseHeldV1(f.run.ID); err != nil {
		t.Fatal(err)
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].Container != f.waiter.id {
		t.Fatal("attached run lost exact ownership", err)
	}
	_ = operation.Unlock()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled instance did not finish")
	}
	_, queue, state := f.inspect(t)
	if len(queue.Runs) != 0 || state.IdentityMapping.PendingOperation != nil || f.waiter.removed != 1 {
		t.Fatal("cancelled instance not retired")
	}
}

func TestMappedLiveInstanceUncertaintyRetainsExactOwnershipV1(t *testing.T) {
	for _, mode := range []string{"create", "F", "admission", "partial frame", "cleanup", "retirement", "incomplete retirement", "reacquire", "changed journal", "changed queue"} {
		t.Run(mode, func(t *testing.T) {
			f := newMappedLiveFixtureV1(t, "exact", mappedApplicationCommandV1)
			w := f.waiter
			switch mode {
			case "create":
				w.createFailure = true
			case "F":
				w.effect.fail = true
			case "admission":
				w.backend.admit = func(deploy.IdentityMappingOperationsV1, context.Context, deploy.StateV1, string) (deploy.StateV1, error) {
					return deploy.StateV1{}, errors.New("uncertain admission persistence")
				}
			case "partial frame":
				original := w.backend.run
				w.backend.run = func(s CommandSpec, o RunOptions) error {
					if s.Args[0] != "start" {
						return original(s, o)
					}
					b := make([]byte, 7)
					_, err := o.Stdin.Read(b)
					return errors.Join(err, errors.New("partial frame disconnect"))
				}
			case "cleanup":
				w.cleanupFailure = true
			case "retirement":
				f.retirementError = errors.New("trusted retirement proof unavailable")
			case "incomplete retirement":
				f.incompleteRetirement = true
			case "reacquire":
				f.backend.acquire = func(context.Context, string) (*deploy.OperationLock, error) {
					return nil, errors.New("completion lock unavailable")
				}
			case "changed journal", "changed queue":
				f.backend.acquire = func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
					operation, err := deploy.AcquireOperationLock(ctx, dir)
					if err != nil {
						return nil, err
					}
					if mode == "changed journal" {
						state, _, _ := operation.ReadStateV1()
						state.IdentityMapping.PendingOperation.OperationID = "other"
						writeMappedLiveStateFixtureV1(t, operation, state)
					} else {
						_, _, _ = operation.RemoveLiveRunV1(f.run.ID)
					}
					return operation, nil
				}
			}
			if err := f.execute(); err == nil {
				t.Fatal("uncertain outcome claimed success")
			}
			_, queue, state := f.inspect(t)
			if state.IdentityMapping.PendingOperation == nil || mode != "changed queue" && len(queue.Runs) != 1 || f.recoveries > 1 || w.created > 1 || w.admissions > 1 {
				t.Fatal("uncertainty lost ownership or retried admission")
			}
			if mode != "create" && mode != "changed queue" && queue.Runs[0].Container != w.id {
				t.Fatal("uncertain exact ID not retained")
			}
			if mode == "create" || mode == "F" || mode == "admission" || mode == "partial frame" {
				if w.application.Load() != 0 {
					t.Fatal("failed release executed application")
				}
			}
		})
	}
}

func TestMappedLiveInstanceRejectsBeforeEffectsV1(t *testing.T) {
	for _, mode := range []string{"nil context", "unbounded admission", "cancelled admission", "nil lifetime", "cancelled lifetime", "wrong directory", "no lease", "missing run", "occupied run", "wrong command", "stale generation", "stale revision", "wrong operation", "wrong engine", "wrong instance", "wrong maps", "changed build", "unknown entry", "occupied entry", "corrupt plan", "backend"} {
		t.Run(mode, func(t *testing.T) {
			f := newMappedLiveFixtureV1(t, "exact", mappedApplicationCommandV1)
			ctx, dir, plan := f.waiter.ctx, f.directory, f.plan
			ops, options, backend := f.waiter.prepared.ops, f.waiter.options, f.backend
			state := f.input.Current.State
			switch mode {
			case "nil context":
				ctx = nil
			case "unbounded admission":
				ctx = t.Context()
			case "cancelled admission":
				f.waiter.cancelAdmission()
			case "nil lifetime":
				options.Context = nil
			case "cancelled lifetime":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				options.Context = ctx
			case "wrong directory":
				dir = t.TempDir()
			case "no lease":
				_ = f.lease.Close()
			case "missing run":
				_, _, _ = ops.Operation.RemoveLiveRunV1(f.run.ID)
			case "occupied run":
				_ = ops.Operation.RecordLiveRunContainerV1(f.run.ID, "foreign")
			case "wrong command":
				facts, _ := plan.facts()
				facts.Command = "foreign"
				plan = reboundMappedLivePlanFixtureV1(t, plan, facts)
			case "stale generation":
				facts, _ := plan.facts()
				facts.Generation = rendererDigest("f")
				plan = reboundMappedLivePlanFixtureV1(t, plan, facts)
			case "stale revision":
				state.IdentityMapping.Revision, state.IdentityMapping.PendingOperation.ExpectedRevision = "2", "2"
			case "wrong operation":
				state.IdentityMapping.PendingOperation.OperationID = "other"
			case "wrong engine":
				state.IdentityMapping.PendingOperation.Native.Engine = "other"
			case "wrong instance":
				state.IdentityMapping.PendingOperation.Native.Process = "other"
			case "wrong maps":
				facts, _ := plan.facts()
				facts.Active.Profile = "bounded"
				plan = reboundMappedLivePlanFixtureV1(t, plan, facts)
			case "changed build":
				ops.ValidateProfileOwner = nil
			case "unknown entry", "occupied entry":
				facts, _ := plan.facts()
				facts.Kind = "255"
				if mode == "occupied entry" {
					facts.Mode = strconv.Itoa(int(mappedEntryOccupiedExecV1))
				}
				plan = reboundMappedLivePlanFixtureV1(t, plan, facts)
			case "corrupt plan":
				plan.payload += "x"
			case "backend":
				backend.start = nil
			}
			writeMappedLiveStateFixtureV1(t, ops.Operation, state)
			if err := runMappedLiveInstanceWithBackendV1(ctx, dir, ops, f.run.ID, plan, options, backend); err == nil {
				t.Fatal("invalid owner reached execution")
			}
			if f.starts != 0 || f.waiter.created != 0 || f.waiter.application.Load() != 0 {
				t.Fatal("rejection had application or engine effects")
			}
		})
	}
}

func TestMappedLiveInstanceEnforcesDisabledStdinV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, supplied := range []bool{false, true} {
			t.Run(fmt.Sprint(profile, supplied), func(t *testing.T) {
				f := newMappedLiveFixtureV1(t, profile, mappedApplicationCommandV1)
				f.input.Stdin = false
				var err error
				f.plan, err = constructMappedEntryPlanV1(f.input)
				if err != nil {
					t.Fatal(err)
				}
				if !supplied {
					f.waiter.options.Stdin = nil
					f.waiter.expectedInput = ""
				}
				err = f.execute()
				if supplied {
					if err == nil || !strings.Contains(err.Error(), "stdin") {
						t.Fatalf("disabled stdin accepted a reader: %v", err)
					}
					if f.starts != 0 || f.waiter.created != 0 || f.waiter.application.Load() != 0 {
						t.Fatal("stdin refusal had engine or application effects")
					}
				} else if err != nil {
					t.Fatalf("disabled stdin without a reader failed: %v", err)
				}
			})
		}
	}
}

func reboundMappedLivePlanFixtureV1(t *testing.T, plan mappedEntryPlanV1, facts mappedEntryPayloadV1) mappedEntryPlanV1 {
	t.Helper()
	digest, err := canonical.Sum("mapped-entry", "v1", facts)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := canonical.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	plan.payload, plan.binding.Plan = string(encoded), digest
	plan.binding.Generation = facts.Generation
	return plan
}

func TestMappedLiveCreateUsesOnlyFrozenLaunchFactsV1(t *testing.T) {
	f := newMappedLiveFixtureV1(t, "exact", mappedApplicationCommandV1)
	facts, _ := f.plan.facts()
	spec, policy, err := mappedLiveCreateSpecV1(f.waiter.prepared.ops, f.input.Current.State, f.directory, facts)
	if err != nil {
		t.Fatal(err)
	}
	request, err := mappedPodmanApplicationCreateRequestV1(spec, "fixture", policy)
	if err != nil {
		t.Fatal(err)
	}
	body := request.body
	if request.name != facts.Run || !slices.Equal(body.Cmd, facts.Startup) || !slices.Equal(body.Env, facts.Environment) || body.Tty || body.HostConfig.AutoRemove || body.HostConfig.RestartPolicy.Name != "no" || policy.facts() != facts.Kernel || !body.HostConfig.ReadonlyRootfs {
		t.Fatalf("frozen launch facts changed: %s", mustMappedLiveJSONFixtureV1(t, body))
	}
	if !strings.Contains(strings.Join(body.Env, "\n"), "TOKEN=dummy") || !reflect.DeepEqual(facts.Mounts, f.input.Runtime.Mounts) {
		t.Fatal("expected input bytes changed")
	}
	if _, err := runMappedNilFixtureV1(); err == nil {
		t.Fatal("production entry accepted missing ownership")
	}
}

func mustMappedLiveJSONFixtureV1(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runMappedNilFixtureV1() (bool, error) {
	return false, runMappedLiveInstanceV1(nil, "", deploy.IdentityMappingOperationsV1{}, "", mappedEntryPlanV1{}, RunOptions{})
}

func TestMappedLiveCompletionRequiresEngineExitStatusV1(t *testing.T) {
	for _, value := range []string{"7\n", "", "00\n", "-1\n", "256\n", "0\n0\n", strings.Repeat("0", 40)} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			f := newMappedLiveFixtureV1(t, "exact", mappedApplicationCommandV1)
			f.exitStatus = value
			if err := f.execute(); err == nil {
				t.Fatal("bad or nonzero engine status became successful completion")
			}
			_, queue, state := f.inspect(t)
			if len(queue.Runs) != 0 || state.IdentityMapping.PendingOperation != nil || f.waiter.removed != 1 {
				t.Fatal("failed command status prevented exact retirement")
			}
		})
	}
	f := newMappedLiveFixtureV1(t, "exact", mappedApplicationCommandV1)
	cause := errors.New("attach transport lost")
	f.attachErr = cause
	if err := f.execute(); !errors.Is(err, cause) {
		t.Fatalf("attach failure lost: %v", err)
	}
	_, queue, state := f.inspect(t)
	if len(queue.Runs) != 0 || state.IdentityMapping.PendingOperation != nil {
		t.Fatal("attach failure did not retire owned instance")
	}
}
