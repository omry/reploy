package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

// The existing fixture uses real locks, queue leases, state and waiter protocol.
// Native admission, engine and F/G observations are injected; these positive
// cases do not qualify a live engine or remove public activation gates.
func TestMappedControlledEntryV1UsesSharedLifetime(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, kind := range []mappedEntryKindV1{mappedWorkloadStartV1, mappedApplicationCommandV1, mappedRuntimeTestV1, mappedLifecycleHookV1} {
			t.Run(fmt.Sprint(profile, kind), func(t *testing.T) {
				f := newMappedLiveFixtureV1(t, profile, kind)
				err := runMappedControlledEntryWithBackendV1(f.waiter.ctx, f.directory, f.waiter.prepared.ops, f.run.ID, f.input, f.waiter.options, f.backend)
				if kind == mappedWorkloadStartV1 {
					if !errors.Is(err, errMappedMasterExitedV1) {
						t.Fatalf("required master completion = %v", err)
					}
				} else if err != nil {
					t.Fatalf("finite entry became master failure: %v", err)
				}
				_, queue, state := f.inspect(t)
				if len(queue.Runs) != 0 || state.IdentityMapping.PendingOperation != nil || f.starts != 1 || f.recoveries != 1 || f.waiter.created != 1 || f.waiter.removed != 1 || f.waiter.admissions != 1 {
					t.Fatal("adapter did not use one owned release and retirement")
				}
				if state.IdentityMapping.AcceptedUIDs == nil || len(*state.IdentityMapping.AcceptedUIDs) == 0 || len(state.IdentityMapping.UIDAssignments) == 0 {
					t.Fatal("adapter discarded retained identity history")
				}
				select {
				case frame := <-f.waiter.frame:
					if frame.Binding != f.plan.binding || frame.Container != f.waiter.id {
						t.Fatal("adapter substituted the shared final entry binding")
					}
				default:
					t.Fatal("missing single release frame")
				}
			})
		}
	}
}

func TestMappedControlledEntryV1RefusesBeforeEffects(t *testing.T) {
	cases := []struct {
		name   string
		change func(*mappedEntryInputV1)
	}{
		{"shell", func(i *mappedEntryInputV1) { i.Kind = mappedShellV1 }},
		{"session", func(i *mappedEntryInputV1) { i.Kind = mappedControlledSessionV1 }},
		{"deployed command", func(i *mappedEntryInputV1) { i.Kind = mappedDeployedCommandV1 }},
		{"host native", func(i *mappedEntryInputV1) { i.Kind = mappedHostNativeCommandV1 }},
		{"replacement cannot skip retirement", func(i *mappedEntryInputV1) { i.Kind = mappedHostReplacementV1 }},
		{"unknown", func(i *mappedEntryInputV1) { i.Kind = 255 }},
		{"tty", func(i *mappedEntryInputV1) { i.TTY = true }},
		{"private environment", func(i *mappedEntryInputV1) { i.PrivateEnvironment = true }},
		{"observed exit", func(i *mappedEntryInputV1) { i.ObservedExit = true }},
		{"stale image", func(i *mappedEntryInputV1) { i.Runtime.Image += "-stale" }},
		{"missing role", func(i *mappedEntryInputV1) { i.Command = "undeclared" }},
	}
	for _, profile := range []string{"exact", "bounded"} {
		for _, tc := range cases {
			t.Run(profile+"/"+tc.name, func(t *testing.T) {
				f := newMappedLiveFixtureV1(t, profile, mappedApplicationCommandV1)
				op := f.waiter.prepared.ops.Operation
				before, _, err := op.ReadStateV1()
				if err != nil {
					t.Fatal(err)
				}
				tc.change(&f.input)
				err = runMappedControlledEntryWithBackendV1(f.waiter.ctx, f.directory, f.waiter.prepared.ops, f.run.ID, f.input, f.waiter.options, f.backend)
				if err == nil || f.starts != 0 || f.recoveries != 0 || f.waiter.created != 0 || f.waiter.admissions != 0 || f.waiter.application.Load() != 0 {
					t.Fatalf("unsupported entry reached effects: %v", err)
				}
				if err := op.RequireHeld(); err != nil {
					t.Fatal("preparation failure lost caller lock", err)
				}
				after, _, err := op.ReadStateV1()
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("refused adapter changed native state", err)
				}
			})
		}
	}
}

func TestMappedControlledEntryV1RevalidatesCurrentNativeOwner(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		t.Run(profile, func(t *testing.T) {
			f := newMappedLiveFixtureV1(t, profile, mappedApplicationCommandV1)
			state := cloneMappedWorkloadStateFixtureV1(t, f.input.Current.State)
			state.IdentityMapping.Revision = "99"
			state.IdentityMapping.PendingOperation.ExpectedRevision = "99"
			writeMappedLiveStateFixtureV1(t, f.waiter.prepared.ops.Operation, state)
			err := runMappedControlledEntryWithBackendV1(f.waiter.ctx, f.directory, f.waiter.prepared.ops, f.run.ID, f.input, f.waiter.options, f.backend)
			if err == nil || f.starts != 0 || f.waiter.created != 0 {
				t.Fatalf("old prepared snapshots authorized changed state: %v", err)
			}
		})
	}
}

func TestMappedControlledEntryV1ProductionPathRequiresProof(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		t.Run(profile, func(t *testing.T) {
			f := newMappedLiveFixtureV1(t, profile, mappedApplicationCommandV1)
			ops := f.waiter.prepared.ops
			ops.Evidence = nil
			if err := runMappedControlledEntryV1(f.waiter.ctx, f.directory, ops, f.run.ID, f.input, f.waiter.options); err == nil {
				t.Fatal("production adapter accepted missing effect evidence")
			}
			if f.waiter.created != 0 || f.waiter.admissions != 0 || f.waiter.application.Load() != 0 {
				t.Fatal("unqualified production adapter released a payload")
			}
		})
	}
}

func TestMappedReplacementEntryV1RefusesMissingOwnershipOrProvider(t *testing.T) {
	called := false
	reload := func(context.Context, deploy.IdentityMappingOperationsV1) (mappedEntryInputV1, error) {
		called = true
		return mappedEntryInputV1{}, errors.New("unexpected admission")
	}
	if err := runMappedReplacementEntryV1(t.Context(), "", deploy.IdentityMappingOperationsV1{}, "", reload, RunOptions{}); err == nil || called {
		t.Fatal("replacement reached admission without ownership", err)
	}
	for _, profile := range []string{"exact", "bounded"} {
		t.Run(profile, func(t *testing.T) {
			f := newMappedLiveFixtureV1(t, profile, mappedApplicationCommandV1)
			err := runMappedReplacementEntryV1(f.waiter.ctx, f.directory, f.waiter.prepared.ops, f.run.ID, reload, f.waiter.options)
			if err == nil || !strings.Contains(err.Error(), "observation provider") || called || f.waiter.created != 0 {
				t.Fatalf("replacement escaped unqualified F/G boundary: %v", err)
			}
			if err := f.waiter.prepared.ops.Operation.RequireHeld(); err != nil {
				t.Fatal("refusal lost caller lock", err)
			}
		})
	}
}

func TestMappedReplacementEntryV1RefusesInvalidCallerBeforeRetirement(t *testing.T) {
	for _, name := range []string{"foreign directory", "no lifetime", "cancelled lifetime", "no lease"} {
		t.Run(name, func(t *testing.T) {
			f := newMappedLiveFixtureV1(t, "exact", mappedApplicationCommandV1)
			ops, directory, options := f.waiter.prepared.ops, f.directory, f.waiter.options
			switch name {
			case "foreign directory":
				directory = t.TempDir()
			case "no lifetime":
				options.Context = nil
			case "cancelled lifetime":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				options.Context = ctx
			case "no lease":
				if err := f.lease.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, _, err := ops.Operation.ReadStateV1()
			if err != nil {
				t.Fatal(err)
			}
			reloaded := false
			err = runMappedReplacementEntryV1(f.waiter.ctx, directory, ops, f.run.ID, func(context.Context, deploy.IdentityMappingOperationsV1) (mappedEntryInputV1, error) {
				reloaded = true
				return f.input, nil
			}, options)
			if err == nil || reloaded || f.waiter.created != 0 {
				t.Fatalf("invalid replacement caller reached admission: %v", err)
			}
			after, _, err := ops.Operation.ReadStateV1()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("invalid replacement caller changed native state", err)
			}
		})
	}
}

// Even a qualified observation owner cannot make this incomplete replacement
// composition safe. Refusal must preserve the old live owner on every attempt.
func TestMappedReplacementEntryV1QualifiedOwnerRefusesBeforeEffects(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, stream := range []string{"none", "borrowed", "owned"} {
			t.Run(profile+"/"+stream, func(t *testing.T) {
				f := newMappedWorkloadFixtureV1(t, profile)
				op := f.ops.Operation
				before, _, err := op.ReadStateV1()
				if err != nil {
					t.Fatal(err)
				}
				queue, _, err := op.ReadLiveRunQueueV1()
				if err != nil {
					t.Fatal(err)
				}
				old, found, err := op.ReadWorkloadLifecycleV1()
				if err != nil || !found {
					t.Fatal("missing existing owner", err)
				}
				options := f.live.waiter.options
				if stream == "borrowed" {
					options.Stdin = strings.NewReader("borrowed")
				}
				if stream == "owned" {
					options.Stdin = io.NopCloser(strings.NewReader("owned"))
				}
				for attempt := 0; attempt < 2; attempt++ {
					err := runMappedReplacementEntryV1(f.live.waiter.ctx, f.live.directory, f.ops, f.live.run.ID, f.reload, options)
					if err == nil || !strings.Contains(err.Error(), "replacement lifecycle composition is not yet available") {
						t.Fatalf("unsupported replacement was not refused: %v", err)
					}
					after, _, err := op.ReadStateV1()
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("refusal changed native owner", err)
					}
					afterQueue, _, err := op.ReadLiveRunQueueV1()
					if err != nil || !reflect.DeepEqual(queue, afterQueue) {
						t.Fatal("refusal changed queue", err)
					}
					record, found, err := op.ReadWorkloadLifecycleV1()
					if err != nil || !found || record != old {
						t.Fatal("refusal changed lifecycle owner", err)
					}
					if len(f.trace) != 0 || f.live.waiter.created != 0 || f.live.waiter.admissions != 0 || f.live.waiter.application.Load() != 0 {
						t.Fatal("refusal reached retirement, admission or application effects", f.trace)
					}
					if err := op.RequireHeld(); err != nil {
						t.Fatal("refusal lost caller lock", err)
					}
				}
			})
		}
	}
}

func TestMappedEntryAdaptersV1PublicKindsPreserveGuardsAndOrdinaryDocument(t *testing.T) {
	kinds := []mappedEntryKindV1{mappedWorkloadStartV1, mappedApplicationCommandV1, mappedRuntimeTestV1, mappedShellV1, mappedControlledSessionV1, mappedLifecycleHookV1, mappedHostReplacementV1, mappedDeployedCommandV1, mappedHostNativeCommandV1}
	for _, owner := range []string{"ordinary", "declaration", "unresolved", "policy", "retained"} {
		state := mappedEntryStateFixtureV1(t, owner)
		before, _ := json.Marshal(state)
		want, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range kinds {
			document, err := unmappedRuntimeDocumentForEntryV1(state, kind)
			if owner == "ordinary" {
				if err != nil || !reflect.DeepEqual(document, want) {
					t.Fatalf("ordinary entry %d changed: %v", kind, err)
				}
			} else {
				if err == nil || !reflect.DeepEqual(document, blueprint.Document{}) || !strings.Contains(err.Error(), "not yet available") {
					t.Fatalf("public mapped entry %s/%d escaped inhibition: %v", owner, kind, err)
				}
				if kind == mappedShellV1 || kind == mappedControlledSessionV1 || kind == mappedDeployedCommandV1 {
					if !errors.Is(err, errMappedOccupiedEntryV1) {
						t.Fatalf("occupied semantics lost: %v", err)
					}
				}
				if kind == mappedHostNativeCommandV1 && !errors.Is(err, errMappedHostNativeEntryV1) {
					t.Fatalf("host-native refusal lost: %v", err)
				}
			}
		}
		after, _ := json.Marshal(state)
		if string(before) != string(after) {
			t.Fatal("public guard changed owner state")
		}
		for _, kind := range []mappedEntryKindV1{0, 255} {
			if _, err := unmappedRuntimeDocumentForEntryV1(state, kind); err == nil {
				t.Fatal("unknown adapter kind accepted")
			}
		}
	}
	for _, action := range []string{"up", "down", "restart"} {
		kind := mappedWorkloadEntryKindV1(action)
		if (kind == mappedHostReplacementV1) != (action == "restart") {
			t.Fatalf("restart lost replacement semantics: %s/%d", action, kind)
		}
	}
}

func TestMappedEntryAdaptersV1CheckBothSessionOwners(t *testing.T) {
	for _, controller := range []string{"ordinary", "declaration", "policy", "retained"} {
		for _, workload := range []string{"ordinary", "declaration", "policy", "retained"} {
			err := requireUnmappedSessionOwnersV1(mappedEntryStateFixtureV1(t, controller), mappedEntryStateFixtureV1(t, workload))
			if controller == "ordinary" && workload == "ordinary" {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err == nil || !errors.Is(err, errMappedOccupiedEntryV1) {
				t.Fatalf("session accepted mapped pair %s/%s: %v", controller, workload, err)
			}
			if controller != "ordinary" && !strings.Contains(err.Error(), "controller owner") || workload != "ordinary" && !strings.Contains(err.Error(), "workload owner") {
				t.Fatalf("session did not inspect both owners: %v", err)
			}
		}
	}
}

func TestMappedEntryAdaptersV1PublicLifecycleRejectsBeforeHooks(t *testing.T) {
	for _, owner := range []string{"declaration", "unresolved", "policy", "retained"} {
		for _, action := range []string{"up", "down", "restart"} {
			err := RunCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
				Current: CurrentBuild{State: mappedEntryStateFixtureV1(t, owner)}, Action: action,
			})
			if err == nil || !strings.Contains(err.Error(), "not yet available") {
				t.Fatalf("public lifecycle reached hooks or ordinary engine path: %s/%s: %v", owner, action, err)
			}
		}
	}
}

func TestMappedEntryAdaptersV1SessionPairRefusesBeforePreparation(t *testing.T) {
	for _, role := range []string{"controller", "workload"} {
		for _, owner := range []string{"declaration", "unresolved", "policy", "retained"} {
			t.Run(role+"/"+owner, func(t *testing.T) {
				root := t.TempDir()
				controllerDir, workloadDir := filepath.Join(root, "controller"), filepath.Join(root, "workload")
				for _, dir := range []string{controllerDir, workloadDir} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				ordinary := mappedEntryStateFixtureV1(t, "ordinary")
				mapped := mappedEntryStateFixtureV1(t, owner)
				loaded, effects := 0, 0
				backend := currentControlledSessionRunBackendV1{
					acquire: deploy.AcquireOperationLock,
					loadRuntime: func(_ context.Context, op *deploy.OperationLock, dir string, _ StagedProviderBuildRuntimeV1) (currentControlledSessionRuntimeV1, error) {
						if err := op.RequireHeld(); err != nil {
							t.Fatal(err)
						}
						loaded++
						state := ordinary
						if role == "controller" && dir == controllerDir || role == "workload" && dir == workloadDir {
							state = mapped
						}
						return currentControlledSessionRuntimeV1{current: CurrentBuild{State: state}}, nil
					},
					privateEnv: func(string) (privateWorkloadEnvironmentV1, error) {
						effects++
						return privateWorkloadEnvironmentV1{}, errors.New("unexpected preparation")
					},
					prepareController: preparedControlledSessionControllerForTestV1,
					cleanupController: func(context.Context, *preparedControlledSessionControllerV1) error { return nil },
					prepareOutput:     prepareOneShotOutput,
					abortOutput:       func(*oneShotOutputSession) error { return nil },
					publishOutput:     func(*oneShotOutputSession) error { return nil },
					concurrency:       PlanLiveRunConcurrencyV1,
					newRunID:          deploy.NewLiveRunIDV1,
					newHandle:         func() (string, error) { return "", errors.New("unexpected handle") },
					acquireLease: func(*deploy.OperationLock, string) (*deploy.QueueEntryLeaseV1, error) {
						return nil, errors.New("unexpected lease")
					},
					await: func(context.Context, string, *deploy.OperationLock, deploy.LiveRunV1, bool, io.Writer) (*deploy.OperationLock, error) {
						return nil, errors.New("unexpected admission")
					},
					plan: PlanControlledSessionV1,
					run: func(context.Context, *deploy.OperationLock, *deploy.OperationLock, ControlledSessionExecutionPlanV1, ControlledSessionRunOptionsV1) (ControlledSessionRunResultV1, error) {
						return ControlledSessionRunResultV1{}, errors.New("unexpected supervisor")
					},
				}
				_, err := runCurrentControlledSessionV1(t.Context(), CurrentControlledSessionRunInputV1{ControllerDeploymentDir: controllerDir, WorkloadDeploymentDir: workloadDir}, backend)
				if err == nil || !errors.Is(err, errMappedOccupiedEntryV1) || !strings.Contains(err.Error(), role+" owner") || loaded != 2 || effects != 0 {
					t.Fatalf("session reached preparation: loaded=%d effects=%d err=%v", loaded, effects, err)
				}
				for _, dir := range []string{controllerDir, workloadDir} {
					op, err := deploy.AcquireOperationLock(t.Context(), dir)
					if err != nil {
						t.Fatal("session refusal leaked owner lock", err)
					}
					queue, _, err := op.ReadLiveRunQueueV1()
					if err != nil || len(queue.Runs) != 0 {
						t.Fatal("session refusal admitted a live entry", err)
					}
					if err := op.Unlock(); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
