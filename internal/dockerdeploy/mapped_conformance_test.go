package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

// These journeys use production planners, locks, leases, stream framing and
// lifetime code. Engine, process pinning, native admission and F observations
// remain injected. The separate probe suite exercises the real exec mechanism.
func TestMappedConformanceFreshJourneyV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, entry := range []struct {
			name string
			kind mappedEntryKindV1
		}{
			{"workload", mappedWorkloadStartV1},
			{"application", mappedApplicationCommandV1},
			{"runtime-test", mappedRuntimeTestV1},
			{"hook", mappedLifecycleHookV1},
		} {
			for repetition := range 3 {
				t.Run(fmt.Sprintf("%s/%s/%d", profile, entry.name, repetition), func(t *testing.T) {
					f := newMappedLiveFixtureV1(t, profile, entry.kind)
					var stdout, stderr bytes.Buffer
					f.waiter.options.Stdout, f.waiter.options.Stderr = &stdout, &stderr
					original := f.waiter.backend.run
					f.waiter.backend.run = func(s CommandSpec, o RunOptions) error {
						err := original(s, o)
						if err == nil && s.Args[0] == "start" {
							if _, err = io.WriteString(o.Stdout, "application output\n"); err == nil {
								_, err = io.WriteString(o.Stderr, "application diagnostic\n")
							}
						}
						return err
					}
					f.beforeRecovery = func(op *deploy.OperationLock) {
						if err := op.RequireQueueEntryLeaseHeldV1(f.run.ID); err != nil {
							t.Error("owned lease lost before recovery", err)
						}
					}
					err := runMappedControlledEntryWithBackendV1(f.waiter.ctx, f.directory, f.waiter.prepared.ops, f.run.ID, f.input, f.waiter.options, f.backend)
					if entry.kind == mappedWorkloadStartV1 {
						if !errors.Is(err, errMappedMasterExitedV1) {
							t.Fatalf("required master exit lost: %v", err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					_, queue, state := f.inspect(t)
					if len(queue.Runs) != 0 || state.IdentityMapping.PendingOperation != nil || f.starts != 1 || f.recoveries != 1 || f.waiter.created != 1 || f.waiter.admissions != 1 || f.waiter.removed != 1 || f.waiter.process.closed != 1 {
						t.Fatal("journey did not release and retire exactly one owned instance")
					}
					if f.waiter.application.Load() == 0 || stdout.String() != "application output\n" || stderr.String() != "application diagnostic\n" {
						t.Fatal("application streams lost, mixed, or contain release metadata")
					}
					if state.IdentityMapping.AcceptedUIDs == nil || len(*state.IdentityMapping.AcceptedUIDs) == 0 || !reflect.DeepEqual(state.IdentityMapping.UIDAssignments, f.input.Current.State.IdentityMapping.PendingOperation.Native.Active.UIDs) {
						t.Fatal("retirement discarded admitted identity history")
					}
					select {
					case frame := <-f.waiter.frame:
						if frame.Binding != f.plan.binding || frame.Container != f.waiter.id {
							t.Fatal("release substituted generation, role, image or plan")
						}
					default:
						t.Fatal("missing release frame")
					}
					select {
					case <-f.waiter.frame:
						t.Fatal("duplicate release frame")
					default:
					}
					facts, err := f.plan.facts()
					if err != nil {
						t.Fatal(err)
					}
					evidence, err := json.Marshal(struct {
						Profile, Entry, Compiler, OS, Architecture string
						Facts                                      mappedEntryPayloadV1
						Qualification                              string
					}{profile, entry.name, runtime.Version(), runtime.GOOS, runtime.GOARCH, facts, "private-orchestration; injected engine/native/F"})
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("E conformance receipt: %s", evidence)
				})
			}
		}
	}
}

func TestMappedConformanceRefusalJourneyV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, mode := range []string{"role", "image", "revision", "runtime-account", "maps", "owner", "occupied", "host-native"} {
			t.Run(profile+"/"+mode, func(t *testing.T) {
				f := newMappedLiveFixtureV1(t, profile, mappedApplicationCommandV1)
				op := f.waiter.prepared.ops.Operation
				before, _, err := op.ReadStateV1()
				if err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "role":
					f.input.Command = "undeclared-role"
				case "image":
					f.input.Runtime.Image += "-stale"
				case "revision":
					f.input.Current.State.IdentityMapping.Revision = "99"
				case "runtime-account":
					f.input.Current.State.IdentityMapping.PendingOperation.Native.RuntimeUsers = []string{"999"}
				case "maps":
					f.input.Current.State.IdentityMapping.PendingOperation.Native.Active.UIDs[0].HostStart = "999999"
				case "owner":
					f.input.Current.State.IdentityMapping.PendingOperation.Native.Engine = "foreign"
				case "occupied":
					f.input.Kind = mappedControlledSessionV1
				case "host-native":
					f.input.Kind = mappedHostNativeCommandV1
				}
				if err := runMappedControlledEntryWithBackendV1(f.waiter.ctx, f.directory, f.waiter.prepared.ops, f.run.ID, f.input, f.waiter.options, f.backend); err == nil {
					t.Fatal("invalid authority accepted")
				}
				if f.starts != 0 || f.waiter.created != 0 || f.waiter.admissions != 0 || f.waiter.application.Load() != 0 || f.recoveries != 0 {
					t.Fatal("refusal reached an engine or application effect")
				}
				// Pure construction keeps the caller lock. Once construction
				// succeeds, the lifetime runner owns and releases that lock even
				// when its durable owner check refuses before create.
				if op.RequireHeld() != nil {
					op, err = deploy.AcquireOperationLock(t.Context(), f.directory)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = op.Unlock() })
				}
				after, _, err := op.ReadStateV1()
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("refusal changed durable state", err)
				}
			})
		}
	}
}

func TestMappedConformanceInterruptedJourneyV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, mode := range []string{"engine", "create", "pin", "pid", "F", "sandbox", "admission", "partial-frame", "cleanup", "retirement"} {
			t.Run(profile+"/"+mode, func(t *testing.T) {
				f := newMappedLiveFixtureV1(t, profile, mappedApplicationCommandV1)
				w := f.waiter
				switch mode {
				case "engine":
					w.observeFailure = true
				case "create":
					w.createFailure = true
				case "pin":
					w.pinFailure = true
				case "pid":
					w.process.fail = true
				case "F":
					w.effect.fail = true
				case "sandbox":
					w.effect.sandboxFailure = errors.New("missing trusted kernel/code observation")
				case "admission":
					w.backend.admit = func(deploy.IdentityMappingOperationsV1, context.Context, deploy.StateV1, string) (deploy.StateV1, error) {
						return deploy.StateV1{}, errors.New("uncertain native admission")
					}
				case "partial-frame":
					original := w.backend.run
					w.backend.run = func(s CommandSpec, o RunOptions) error {
						if s.Args[0] != "start" {
							return original(s, o)
						}
						_, err := io.ReadFull(o.Stdin, make([]byte, 7))
						return errors.Join(err, errors.New("release interrupted"))
					}
				case "cleanup":
					w.cleanupFailure = true
				case "retirement":
					f.retirementError = errors.New("retirement proof unavailable")
				}
				if err := runMappedControlledEntryWithBackendV1(w.ctx, f.directory, w.prepared.ops, f.run.ID, f.input, w.options, f.backend); err == nil {
					t.Fatal("interrupted journey claimed success")
				}
				_, queue, state := f.inspect(t)
				if state.IdentityMapping.PendingOperation == nil || len(queue.Runs) != 1 || w.created > 1 || w.admissions > 1 || f.recoveries > 1 {
					t.Fatal("uncertainty discarded ownership or retried effects")
				}
				if w.created == 1 && mode != "create" && queue.Runs[0].Container != w.id && w.removed != 1 {
					t.Fatal("uncertainty neither retained nor cleaned the exact instance")
				}
				if mode != "cleanup" && mode != "retirement" {
					if w.application.Load() != 0 {
						t.Fatal("refused or incomplete release reached application input")
					}
					select {
					case <-w.frame:
						t.Fatal("refused journey delivered a complete release frame")
					default:
					}
				}
			})
		}
	}
}

func TestMappedConformanceCancellationJourneyV1(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		t.Run(profile, func(t *testing.T) {
			f := newMappedLiveFixtureV1(t, profile, mappedApplicationCommandV1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.waiter.options.Context, f.untilCancel = ctx, true
			result := make(chan error, 1)
			go func() {
				result <- runMappedControlledEntryWithBackendV1(f.waiter.ctx, f.directory, f.waiter.prepared.ops, f.run.ID, f.input, f.waiter.options, f.backend)
			}()
			select {
			case <-f.waiter.frame:
			case <-f.waiter.ctx.Done():
				t.Fatal("release did not complete within admission deadline")
			}
			op, err := deploy.AcquireOperationLock(f.waiter.ctx, f.directory)
			if err != nil {
				t.Fatal(err)
			}
			queue, _, err := op.ReadLiveRunQueueV1()
			owned := err == nil && len(queue.Runs) == 1 && queue.Runs[0].Container == f.waiter.id && op.RequireQueueEntryLeaseHeldV1(f.run.ID) == nil
			_ = op.Unlock()
			if !owned {
				t.Fatal("attached run did not retain exact ownership and lease", err)
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			case <-f.waiter.ctx.Done():
				t.Fatal("cancellation did not finish within fixture deadline")
			}
			_, queue, state := f.inspect(t)
			if len(queue.Runs) != 0 || state.IdentityMapping.PendingOperation != nil || f.waiter.removed != 1 || f.recoveries != 1 || state.IdentityMapping.AcceptedUIDs == nil || len(*state.IdentityMapping.AcceptedUIDs) == 0 {
				t.Fatal("cancelled journey lost cleanup or admitted mapping history")
			}
		})
	}
}
