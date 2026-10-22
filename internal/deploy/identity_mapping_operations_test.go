package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/identitymapping"
)

type mappingOperationEvidenceV1 struct {
	handles                       []string
	planErr, unusedErr, verifyErr error
	plans, unused, verified       int
	effects                       bool
	observed                      StateV1
	mutate                        bool
}

func (e *mappingOperationEvidenceV1) Plan(_ context.Context, state StateV1, candidate identitymapping.Geometry) ([]string, error) {
	e.plans++
	e.observed = state
	if e.mutate {
		state.Blueprint = "foreign"
		candidate.UIDs[0].HostStart++
		if state.IdentityMapping != nil {
			state.IdentityMapping.OperatorUID = "999"
		}
	}
	return e.handles, e.planErr
}
func (e *mappingOperationEvidenceV1) Unused(_ context.Context, state StateV1) error {
	e.unused++
	e.observed = state
	if e.effects {
		return errors.New("candidate has owned storage or preparation history")
	}
	return e.unusedErr
}
func (e *mappingOperationEvidenceV1) Verify(_ context.Context, state StateV1) error {
	e.verified++
	e.observed = state
	if !e.effects || !reflect.DeepEqual(state.IdentityMapping.PendingOperation.OwnedObjects, e.handles) {
		return errors.New("exact effects are missing or foreign")
	}
	return e.verifyErr
}

func operationGeometryV1(host uint32) identitymapping.Geometry {
	return identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{{ContainerStart: 0, HostStart: host, Count: 16}}, GIDs: []identitymapping.Interval{{ContainerStart: 0, HostStart: host + 100000, Count: 16}}}
}

func operationFixtureV1(t *testing.T, installed, first bool) (string, IdentityMappingOperationsV1, StateV1, *mappingOperationEvidenceV1) {
	t.Helper()
	path, lock, state := mappingStateFixtureV1(t, installed)
	if installed {
		state.Deployment.Installation.Scope = "user"
		writeMappingFixtureV1(t, path, state)
	}
	if first {
		state.IdentityMapping = nil
		writeMappingFixtureV1(t, path, state)
	}
	evidence := &mappingOperationEvidenceV1{}
	ops := IdentityMappingOperationsV1{Operation: lock, Evidence: evidence,
		hostCheck: func(ctx context.Context, dir string, uids, gids []identitymapping.Interval) (identitymapping.HostIdentity, error) {
			if dir != filepath.Dir(filepath.Dir(path)) || len(uids) == 0 || len(gids) == 0 {
				t.Fatal("host check lost the selected owner or reservations")
			}
			for _, values := range [][]identitymapping.Interval{uids, gids} {
				for _, value := range values {
					if err := identitymapping.ValidateInterval(value); err != nil {
						t.Fatal(err)
					}
				}
			}
			return identitymapping.HostIdentity{Username: "operator", UID: 1000, GID: 1000}, ctx.Err()
		}}
	return path, ops, state, evidence
}

func readOperationStateV1(t *testing.T, ops IdentityMappingOperationsV1) StateV1 {
	t.Helper()
	state, found, err := ops.Operation.ReadStateV1()
	if err != nil || !found {
		t.Fatalf("read operation state: found=%v err=%v", found, err)
	}
	return state
}

func TestMappingPendingGenerationRejectsStateChangeAndRemainsRecoverableV1(t *testing.T) {
	for _, installed := range []bool{false, true} {
		for _, phase := range []string{"intent", "effects-pending", "verified"} {
			t.Run(fmt.Sprintf("installed=%v/%s", installed, phase), func(t *testing.T) {
				path, ops, original, evidence := operationFixtureV1(t, installed, !installed)
				pending, err := ops.Configure(t.Context(), original, operationGeometryV1(100000), "generation-bound")
				if err != nil {
					t.Fatal(err)
				}
				if phase != "intent" {
					pending, err = ops.BeginEffects(t.Context(), pending, "generation-bound")
					if err != nil {
						t.Fatal(err)
					}
					evidence.effects = true
				}
				if phase == "verified" {
					pending.IdentityMapping.PendingOperation.Phase = phase
					writeMappingFixtureV1(t, path, pending) // Crash after durable verification.
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				replacement := cloneMappingStateV1(pending)
				replacement.Current = nil
				if err := ops.Operation.CommitStateV1(pending.Current, replacement); err == nil {
					t.Fatal("generation change orphaned a pending mapping operation")
				}
				if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
					t.Fatalf("rejected generation change mutated state: %v", err)
				}
				metadata := cloneMappingStateV1(pending)
				metadata.BlueprintSource = "same-generation metadata update"
				if err := ops.Operation.CommitStateV1(pending.Current, metadata); err != nil {
					t.Fatalf("same-generation update rejected: %v", err)
				}
				recovered, err := ops.Recover(t.Context(), readOperationStateV1(t, ops), "generation-bound", phase == "intent")
				if err != nil || recovered.IdentityMapping.PendingOperation != nil || !reflect.DeepEqual(recovered.Current, original.Current) {
					t.Fatalf("preserved operation cannot recover: %v", err)
				}
			})
		}
	}
}

func TestMappingOperationsConfigureRetryAndExactEffectRecoveryV1(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "staged", true: "installed retained"}[installed], func(t *testing.T) {
			path, ops, original, evidence := operationFixtureV1(t, installed, !installed)
			evidence.handles = []string{"data-volume"}
			input := operationGeometryV1(100000)
			before, _ := os.ReadFile(path)
			checked, err := ops.Check(t.Context(), input)
			if err != nil || !reflect.DeepEqual(checked, original) {
				t.Fatalf("check: %v", err)
			}
			if got, _ := os.ReadFile(path); string(got) != string(before) {
				t.Fatal("check wrote state")
			}
			candidate, err := ops.Configure(t.Context(), original, input, "operation-one")
			if err != nil {
				t.Fatal(err)
			}
			m := candidate.IdentityMapping
			if m.InstallationID == "" || m.PendingOperation.Phase != "intent" || !m.PendingOperation.Inhibited || len(m.UIDAssignments) != 0 || m.AdmittedGeneration != nil || m.Engine != nil || m.Supervisor != nil || !reflect.DeepEqual(candidate.Current, original.Current) {
				t.Fatalf("configure accepted or admitted a candidate: %#v", m)
			}
			status, err := ops.Status()
			if err != nil || status.Status != "candidate" || !reflect.DeepEqual(*status.Reservations, input) {
				t.Fatalf("candidate status: %#v %v", status, err)
			}
			intentBytes, _ := os.ReadFile(path)
			retry, err := ops.Configure(t.Context(), candidate, input, "operation-one")
			if err != nil || !reflect.DeepEqual(retry, candidate) {
				t.Fatalf("exact retry: %v", err)
			}
			if after, _ := os.ReadFile(path); string(after) != string(intentBytes) {
				t.Fatal("exact retry rewrote state")
			}
			if _, err := ops.Configure(t.Context(), original, input, "operation-one"); err == nil {
				t.Fatal("stale observation accepted")
			}
			if _, err := ops.Recover(t.Context(), candidate, "operation-one", false); err == nil {
				t.Fatal("intent completed without effects")
			}
			pending, err := ops.BeginEffects(t.Context(), candidate, "operation-one")
			if err != nil || pending.IdentityMapping.PendingOperation.Phase != "effects-pending" {
				t.Fatalf("durable inhibition: %v", err)
			}
			if _, err := ops.BeginEffects(t.Context(), pending, "operation-one"); err == nil {
				t.Fatal("advanced phase silently retried effects")
			}
			if _, err := ops.Recover(t.Context(), pending, "operation-one", true); err == nil {
				t.Fatal("effects-pending candidate cancelled")
			}
			if _, err := ops.Recover(t.Context(), pending, "operation-one", false); err == nil {
				t.Fatal("missing effects accepted")
			}
			evidence.effects = true // injected effect occurs only after durable phase
			complete, err := ops.Recover(t.Context(), pending, "operation-one", false)
			if err != nil {
				t.Fatal(err)
			}
			m = complete.IdentityMapping
			if m.PendingOperation != nil || m.AdmittedGeneration != nil || m.Engine != nil || m.Supervisor != nil || !reflect.DeepEqual(m.geometry().numeric(), input) || !reflect.DeepEqual(m.RetainedStorage, evidence.handles) {
				t.Fatalf("completion lost reservations or granted admission: %#v", m)
			}
			if installed && !reflect.DeepEqual(complete.Deployment, original.Deployment) {
				t.Fatal("installed binding changed")
			}
			status, err = ops.Status()
			if err != nil || status.Status != "retained" || !reflect.DeepEqual(*status.Reservations, input) {
				t.Fatalf("retained status: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o600 {
				t.Fatalf("state mode: %v %v", info, err)
			}
		})
	}
}

func TestMappingOperationsUnusedCorrectionAndCancellationV1(t *testing.T) {
	for _, protect := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "protected"}[protect], func(t *testing.T) {
			path, ops, state, evidence := operationFixtureV1(t, false, false)
			input := operationGeometryV1(100000)
			if protect {
				geometry := mappingGeometryStateV1(input)
				state.IdentityMapping.UIDAssignments, state.IdentityMapping.GIDAssignments = geometry.UIDs, geometry.GIDs
				input.UIDs = append(input.UIDs, identitymapping.Interval{ContainerStart: 30, HostStart: 300000, Count: 16})
				input.GIDs = append(input.GIDs, identitymapping.Interval{ContainerStart: 30, HostStart: 400000, Count: 16})
				writeMappingFixtureV1(t, path, state)
			}
			intent, err := ops.Configure(t.Context(), state, input, "surplus")
			if err != nil {
				t.Fatal(err)
			}
			corrected := operationGeometryV1(500000)
			if protect {
				corrected = operationGeometryV1(100000) // remove only unused surplus
			}
			before, _ := os.ReadFile(path)
			evidence.unusedErr = errors.New("incomplete ownership history")
			if _, err := ops.Configure(t.Context(), intent, corrected, "corrected"); err == nil {
				t.Fatal("unknown history discarded candidate")
			}
			if _, err := ops.Recover(t.Context(), intent, "surplus", true); err == nil {
				t.Fatal("unknown history cancelled candidate")
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Fatal("rejected candidate changes wrote state")
			}
			evidence.unusedErr = nil
			evidence.effects = true
			if _, err := ops.Configure(t.Context(), intent, corrected, "corrected"); err == nil {
				t.Fatal("used candidate replaced")
			}
			evidence.effects = false
			replacement, err := ops.Configure(t.Context(), intent, corrected, "corrected")
			if err != nil || !reflect.DeepEqual(replacement.IdentityMapping.PendingOperation.Candidate.numeric(), corrected) {
				t.Fatalf("unused correction: %v", err)
			}
			if !reflect.DeepEqual(replacement.IdentityMapping.UIDAssignments, state.IdentityMapping.UIDAssignments) {
				t.Fatal("correction replaced protected authority")
			}
			if _, err := ops.Recover(t.Context(), replacement, "surplus", true); err == nil {
				t.Fatal("old operation cancelled successor")
			}
			cancelled, err := ops.Recover(t.Context(), replacement, "corrected", true)
			if err != nil || cancelled.IdentityMapping.PendingOperation != nil || !reflect.DeepEqual(cancelled.IdentityMapping.UIDAssignments, state.IdentityMapping.UIDAssignments) || cancelled.IdentityMapping.InstallationID != state.IdentityMapping.InstallationID {
				t.Fatalf("unused cancellation lost protected owner: %v", err)
			}
		})
	}
}

func TestMappingOperationsCrashBoundariesV1(t *testing.T) {
	for _, boundary := range []string{"intent", "effects-pending", "verified", "complete"} {
		for _, durable := range []bool{false, true} {
			t.Run(boundary+map[bool]string{false: "/before", true: "/after"}[durable], func(t *testing.T) {
				path, ops, state, evidence := operationFixtureV1(t, false, true)
				evidence.handles = []string{"data-volume"}
				input := operationGeometryV1(100000)
				crashed := false
				ops.write = func(path string, content []byte) error {
					next, err := DecodeStateV1(content)
					if err != nil {
						t.Fatal(err)
					}
					phase := "complete"
					if next.IdentityMapping.PendingOperation != nil {
						phase = next.IdentityMapping.PendingOperation.Phase
					}
					if !crashed && phase == boundary {
						crashed = true
						if durable {
							if err := writeAtomicStateFile(path, content, 0o600); err != nil {
								t.Fatal(err)
							}
						}
						return errors.New("injected process loss")
					}
					return writeAtomicStateFile(path, content, 0o600)
				}
				state, err := ops.Configure(t.Context(), state, input, "crash-operation")
				if err == nil {
					state, err = ops.BeginEffects(t.Context(), state, "crash-operation")
				}
				if err == nil {
					evidence.effects = true
					_, err = ops.Recover(t.Context(), state, "crash-operation", false)
				}
				if err == nil || !crashed {
					t.Fatal("crash boundary did not execute")
				}
				stored := readOperationStateV1(t, ops)
				m := stored.IdentityMapping
				if boundary == "intent" && !durable {
					if m != nil {
						t.Fatal("pre-intent failure changed authority")
					}
					return
				}
				if m == nil || m.InstallationID == "" || m.AdmittedGeneration != nil {
					t.Fatal("crash lost owner or granted admission")
				}
				status, statusErr := ops.Status()
				if statusErr != nil || status.Reservations == nil || !reflect.DeepEqual(*status.Reservations, input) {
					t.Fatalf("crash released reservations: %v", statusErr)
				}
				if p := m.PendingOperation; p != nil {
					if !p.Inhibited || !reflect.DeepEqual(p.OwnedObjects, evidence.handles) {
						t.Fatal("crash forgot pending authority")
					}
					if p.Phase == "intent" {
						if _, err := ops.Recover(t.Context(), stored, "crash-operation", true); err != nil {
							t.Fatalf("unused intent recovery: %v", err)
						}
					} else {
						if _, err := ops.Recover(t.Context(), stored, "crash-operation", true); err == nil {
							t.Fatal("crash after phase advance allowed cancellation")
						}
						if boundary == "effects-pending" {
							if _, err := ops.Recover(t.Context(), stored, "crash-operation", false); err == nil {
								t.Fatal("call never issued but recovery assumed success")
							}
							evidence.effects = true // exact owner later completes effect
						}
						if _, err := ops.Recover(t.Context(), stored, "crash-operation", false); err != nil {
							t.Fatalf("exact recovery: %v", err)
						}
					}
				} else if boundary != "complete" || !durable || !reflect.DeepEqual(m.RetainedStorage, evidence.handles) {
					t.Fatal("unexpected completed crash state")
				}
				if info, err := os.Stat(path); err != nil || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o600 {
					t.Fatal("recovery changed private state mode")
				}
			})
		}
	}
}

func TestMappingOperationsRejectUnknownStaleForeignAndRevokedV1(t *testing.T) {
	for _, scenario := range []string{"no evidence", "active inventory", "bad handles", "host failure", "operator changed", "generation changed", "revision changed", "revoked", "overflow", "wrong profile", "first installed", "cancelled", "bad operation", "protected remap", "invalid geometry"} {
		t.Run(scenario, func(t *testing.T) {
			path, ops, state, evidence := operationFixtureV1(t, scenario == "first installed", scenario == "first installed")
			input := operationGeometryV1(100000)
			ctx := t.Context()
			opID := "one"
			switch scenario {
			case "no evidence":
				ops.Evidence = nil
			case "invalid geometry":
				input.UIDs = nil
			case "active inventory":
				evidence.planErr = errors.New("live or unknown inventory")
			case "bad handles":
				evidence.handles = []string{"z", "a"}
			case "host failure":
				ops.hostCheck = func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
					return identitymapping.HostIdentity{}, errors.New("incomplete delegation or principal inventory")
				}
			case "operator changed":
				state.IdentityMapping.OperatorUID = "1001"
				writeMappingFixtureV1(t, path, state)
			case "generation changed":
				stale := cloneMappingStateV1(state)
				state.Current = nil
				writeMappingFixtureV1(t, path, state)
				state = stale
			case "revision changed":
				stale := cloneMappingStateV1(state)
				state.IdentityMapping.Revision = "2"
				writeMappingFixtureV1(t, path, state)
				state = stale
			case "revoked":
				state.IdentityMapping.Revoked = true
				writeMappingFixtureV1(t, path, state)
			case "overflow":
				state.IdentityMapping.Revision = "18446744073709551615"
				writeMappingFixtureV1(t, path, state)
			case "wrong profile":
				input.Profile = "bounded"
				state.IdentityMapping.Profile = "exact"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "bad operation":
				opID = "-foreign"
			case "protected remap":
				g := mappingGeometryStateV1(operationGeometryV1(500000))
				state.IdentityMapping.UIDAssignments, state.IdentityMapping.GIDAssignments = g.UIDs, g.GIDs
				writeMappingFixtureV1(t, path, state)
			}
			before, _ := os.ReadFile(path)
			if _, err := ops.Configure(ctx, state, input, opID); err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Fatal("rejection changed state")
			}
		})
	}
}

func TestMappingOperationsReadOnlyAndObserverIsolationV1(t *testing.T) {
	path, ops, state, evidence := operationFixtureV1(t, false, false)
	before, _ := os.ReadFile(path)
	status, err := ops.Status()
	if err != nil || status.Status != "retained" || status.Reservations != nil {
		t.Fatalf("empty retained status: %v", err)
	}
	evidence.mutate = true
	configured, err := ops.Configure(t.Context(), state, operationGeometryV1(100000), "isolated")
	if err != nil || configured.Blueprint != state.Blueprint || configured.IdentityMapping.OperatorUID != "1000" || configured.IdentityMapping.PendingOperation.Candidate.UIDs[0].HostStart != "100000" {
		t.Fatalf("observer mutated authority: %v", err)
	}
	stale := cloneMappingStateV1(configured)
	stale.IdentityMapping.PendingOperation.Kind = "uninstall"
	if _, err := ops.BeginEffects(t.Context(), stale, "isolated"); err == nil {
		t.Fatal("foreign operation kind accepted")
	}
	for _, phase := range []string{"effects-pending", "verified"} {
		stale = cloneMappingStateV1(configured)
		stale.IdentityMapping.PendingOperation.Phase = phase
		writeMappingFixtureV1(t, path, stale)
		if _, err := ops.Configure(t.Context(), stale, operationGeometryV1(100000), "other"); err == nil {
			t.Fatal("advanced candidate replaced")
		}
	}
	writeMappingFixtureV1(t, path, configured)
	configured.IdentityMapping.PendingOperation.ExpectedRevision = "1"
	if configured.IdentityMapping.Revision != "1" {
		writeMappingFixtureV1(t, path, configured)
		if _, err := ops.BeginEffects(t.Context(), configured, "isolated"); err == nil {
			t.Fatal("stale intent revision accepted")
		}
	}
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ops.Operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Status(); err == nil {
		t.Fatal("unheld status accepted")
	}
	if _, err := (IdentityMappingOperationsV1{}).Status(); err == nil {
		t.Fatal("nil lock status accepted")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := AcquireExistingOperationLock(t.Context(), missing); err == nil {
		t.Fatal("missing read-only owner created")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("inspection created owner")
	}
	if _, err := ops.Configure(nil, state, operationGeometryV1(100000), "one"); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatal("nil context accepted")
	}
}

func TestMappingOperationsCompetingWritersV1(t *testing.T) {
	_, ops, state, _ := operationFixtureV1(t, false, true)
	start := make(chan struct{})
	results := make(chan error, 2)
	var writers sync.WaitGroup
	for _, id := range []string{"writer-one", "writer-two"} {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			_, err := ops.Configure(t.Context(), state, operationGeometryV1(100000), id)
			results <- err
		}()
	}
	close(start)
	writers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("competing configurations succeeded=%d", succeeded)
	}
	stored := readOperationStateV1(t, ops)
	if stored.IdentityMapping.Revision != "1" || stored.IdentityMapping.PendingOperation == nil {
		t.Fatal("competing writer changed the winner")
	}
	if err := ops.Operation.RequireNoRetainedIdentityV1(); err == nil {
		t.Fatal("actual configured candidate lost destructive protection")
	}
	omitted := cloneMappingStateV1(stored)
	omitted.IdentityMapping = nil
	if err := ops.Operation.CommitStateV1(stored.Current, omitted); err == nil {
		t.Fatal("ordinary writer stripped actual candidate")
	}
}

func TestMappingOperationsPhaseAndVerificationGuardsV1(t *testing.T) {
	for _, scenario := range []string{"no evidence", "changed handles", "active before effects", "wrong operation", "changed candidate same ID", "stale intent revision", "stale intent generation", "foreign kind", "unknown verification", "foreign verification", "nil context"} {
		t.Run(scenario, func(t *testing.T) {
			path, ops, state, evidence := operationFixtureV1(t, false, false)
			evidence.handles = []string{} // empty and nil inventory are equivalent
			input := operationGeometryV1(100000)
			intent, err := ops.Configure(t.Context(), state, input, "guarded")
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			operationID := "guarded"
			switch scenario {
			case "no evidence":
				ops.Evidence = nil
			case "changed handles":
				evidence.handles = []string{"foreign"}
			case "active before effects":
				evidence.planErr = errors.New("owner became active")
			case "wrong operation":
				operationID = "foreign"
			case "stale intent revision":
				intent.IdentityMapping.PendingOperation.ExpectedRevision = "1"
				writeMappingFixtureV1(t, path, intent)
			case "stale intent generation":
				intent.IdentityMapping.PendingOperation.ExpectedGeneration = nil
				writeMappingFixtureV1(t, path, intent)
			case "foreign kind":
				intent.IdentityMapping.PendingOperation.Kind = "uninstall"
				writeMappingFixtureV1(t, path, intent)
			case "nil context":
				ctx = nil
			}
			before, _ := os.ReadFile(path)
			if scenario == "changed candidate same ID" {
				_, err = ops.Configure(ctx, intent, operationGeometryV1(500000), operationID)
			} else if strings.HasSuffix(scenario, "verification") {
				intent, err = ops.BeginEffects(ctx, intent, operationID)
				if err != nil {
					t.Fatal(err)
				}
				before, _ = os.ReadFile(path)
				evidence.effects = true
				if scenario == "unknown verification" {
					evidence.verifyErr = errors.New("incomplete exact inventory")
				} else {
					evidence.handles = []string{"foreign"}
				}
				_, err = ops.Recover(ctx, intent, operationID, false)
			} else {
				_, err = ops.BeginEffects(ctx, intent, operationID)
			}
			if err == nil {
				t.Fatal("invalid phase/effect authority accepted")
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Fatal("phase rejection changed state")
			}
			if scenario == "no evidence" {
				if _, err := ops.Recover(ctx, intent, operationID, true); err == nil {
					t.Fatal("recovery without evidence accepted")
				}
			}
		})
	}
}

func TestMappingOperationsStatusInhibitsUnconfiguredDeclarationV1(t *testing.T) {
	for _, installed := range []bool{false, true} {
		for _, hostStart := range []string{"100000", `"???"`} {
			t.Run(fmt.Sprintf("installed=%v/host=%s", installed, hostStart), func(t *testing.T) {
				path, ops, state, evidence := operationFixtureV1(t, installed, true)
				document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
				if err != nil {
					t.Fatal(err)
				}
				source, err := blueprint.Decode([]byte(fmt.Sprintf("blueprint: {schema: 1}\nenvironment:\n  runtime:\n    identity_mapping: {schema: 1, profile: exact, uids: [{container_start: 0, host_start: %s, count: 16}], gids: [{container_start: 0, host_start: 200000, count: 16}]}\n", hostStart)))
				if err != nil {
					t.Fatal(err)
				}
				document.Environment.Runtime.IdentityMapping = source.Environment.Runtime.IdentityMapping
				state.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
				if err != nil {
					t.Fatal(err)
				}
				writeMappingFixtureV1(t, path, state)
				before, _ := os.ReadFile(path)
				status, err := ops.Status()
				if err != nil || status.Status != "inhibited" || status.Reservations != nil || status.State.IdentityMapping != nil {
					t.Fatalf("declared status=%s reservations=%v retained=%v: %v", status.Status, status.Reservations, status.State.IdentityMapping != nil, err)
				}
				if after, _ := os.ReadFile(path); string(after) != string(before) || evidence.plans != 0 || evidence.unused != 0 || evidence.verified != 0 {
					t.Fatal("status changed state or consulted effect evidence")
				}
			})
		}
	}
}

func TestMappingOperationsStatusDoesNotNeedDesiredInputV1(t *testing.T) {
	path, ops, state, _ := operationFixtureV1(t, false, false)
	input := mappingGeometryStateV1(operationGeometryV1(100000))
	for _, disposition := range []string{"ordinary", "admitted", "revoked"} {
		next := cloneMappingStateV1(state)
		if disposition == "ordinary" {
			next.IdentityMapping = nil
		} else {
			m := next.IdentityMapping
			m.UIDAssignments, m.GIDAssignments = input.UIDs, input.GIDs
			m.AdmittedGeneration = next.Current
			engine, supervisor := "engine", "supervisor"
			m.Engine, m.Supervisor = &engine, &supervisor
			m.Revoked = disposition == "revoked"
		}
		writeMappingFixtureV1(t, path, next)
		before, _ := os.ReadFile(path)
		status, err := ops.Status()
		if err != nil || status.Status != disposition {
			t.Fatalf("status=%s: %v", status.Status, err)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Fatal("status wrote state")
		}
	}
	writeMappingFixtureV1(t, path, state)
	if _, err := ops.Check(t.Context(), identitymapping.Geometry{}); err == nil {
		t.Fatal("check accepted incomplete input")
	}
	if _, err := ops.Check(nil, operationGeometryV1(100000)); err == nil {
		t.Fatal("check accepted nil context")
	}
	if err := os.WriteFile(path, []byte(`{"identity_mapping":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Check(t.Context(), operationGeometryV1(100000)); err == nil {
		t.Fatal("malformed authority accepted")
	}
}

func TestMappingOperationsFreshPendingHostAndNativeCheckV1(t *testing.T) {
	path, ops, state, _ := operationFixtureV1(t, false, false)
	intent, err := ops.Configure(t.Context(), state, operationGeometryV1(100000), "fresh")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	calls := 0
	ops.hostCheck = func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
		calls++
		name := "operator"
		if calls > 1 {
			name = "different-principal"
		}
		return identitymapping.HostIdentity{Username: name, UID: 1000, GID: 1000}, nil
	}
	if _, err := ops.BeginEffects(t.Context(), intent, "fresh"); err == nil {
		t.Fatal("host changed while pending reservations were checked")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("host conflict wrote metadata")
	}
	// Exercise the actual B1 probe as well. Unsupported test hosts may reject;
	// either result must leave this read-only operation's authority unchanged.
	ops.hostCheck = nil
	_, _ = ops.Check(t.Context(), operationGeometryV1(100000))
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("native host check changed state")
	}
}

func TestMappingOperationsRejectForeignConfigureKindsV1(t *testing.T) {
	for _, kind := range []string{"uninstall", "future-operation"} {
		for _, requestedID := range []string{"original", "replacement"} {
			t.Run(kind+"/"+requestedID, func(t *testing.T) {
				path, ops, state, evidence := operationFixtureV1(t, false, false)
				input := operationGeometryV1(100000)
				intent, err := ops.Configure(t.Context(), state, input, "original")
				if err != nil {
					t.Fatal(err)
				}
				intent.IdentityMapping.PendingOperation.Kind = kind
				writeMappingFixtureV1(t, path, intent)
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ops.Configure(t.Context(), intent, input, requestedID); err == nil {
					t.Errorf("configure adopted/replaced foreign pending kind %q", kind)
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(after) != string(before) {
					t.Error("foreign-operation rejection changed authority")
				}
				if evidence.unused != 0 {
					t.Error("mapping unused proof was applied to a foreign operation")
				}
			})
		}
	}
}
