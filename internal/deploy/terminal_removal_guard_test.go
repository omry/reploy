package deploy

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func terminalGuardFixtureV1(t *testing.T) (string, *OperationLock, StateV1) {
	t.Helper()
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Unlock(); err != nil {
			t.Error(err)
		}
	})
	state := StateV1{Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t), Overlay: EmptyRequestOverlayV1(), BlueprintSource: "fixture", Staging: &StagingStateV1{Schema: StagingStateSchemaV1}}
	terminalGuardMustV1(t, lock.CommitStateV1(nil, state))
	return dir, lock, state
}

func TestTerminalGuardAuthorityV1(t *testing.T) {
	for _, mode := range []string{"bound", "diagnostic", "changed", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			dir, lock, state := terminalGuardFixtureV1(t)
			record := pendingValidatedFixtureV1(t).Candidate
			terminalGuardMustV1(t, lock.CommitValidatedBuildV1(record))
			terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
			guarded, found, err := lock.ReadStateV1()
			if err != nil || !found || !guarded.TerminalRemoval {
				t.Fatalf("guard=%#v found=%v err=%v", guarded, found, err)
			}
			statePath := filepath.Join(dir, ".reploy", stateFilenameV1)
			if mode == "diagnostic" {
				terminalGuardMustV1(t, lock.Unlock())
				if ordinary, err := AcquireOperationLock(t.Context(), dir); err == nil || ordinary != nil {
					t.Fatal("ordinary admission accepted guard")
				}
				lock, err = AcquireExistingOperationLock(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Unlock()
				if _, found, err := lock.ReadStateV1(); err != nil || !found {
					t.Fatalf("diagnostic read: %v", err)
				}
				if err := lock.RemoveValidatedBuildV1(); err == nil {
					t.Fatal("diagnostic lock retired ownership")
				}
				terminalGuardMustV1(t, lock.ResumeTerminalRemovalV1(guarded))
			}
			if mode == "changed" {
				changed := guarded
				changed.BlueprintSource = "replacement"
				content, err := EncodeStateV1(changed)
				if err != nil {
					t.Fatal(err)
				}
				terminalGuardMustV1(t, os.WriteFile(statePath, content, 0o600))
			} else if mode == "deleted" {
				terminalGuardMustV1(t, os.Remove(statePath))
			}
			for _, mutate := range []func() error{
				lock.RequireWritable,
				func() error { return lock.CommitStateV1(nil, state) },
				func() error { return lock.WritePendingValidatedBuildV1(pendingValidatedFixtureV1(t)) },
				func() error { return lock.CommitLiveRunQueueV1(NewLiveRunQueueV1()) },
				func() error { _, err := lock.AcquireLiveRunLeaseV1("run-0000000000000001"); return err },
				func() error { return lock.CommitValidatedBuildV1(record) },
			} {
				if err := mutate(); err == nil {
					t.Fatal("terminal authority admitted an owner writer")
				}
			}
			if mode == "changed" || mode == "deleted" {
				if err := lock.RequireRetirement(); err == nil {
					t.Fatal("changed authority allowed retirement")
				}
				if err := lock.ResumeTerminalRemovalV1(guarded); err == nil {
					t.Fatal("retry accepted changed state")
				}
				return
			}
			terminalGuardMustV1(t, lock.RequireRetirement())
			discarded := record
			discarded.Discarded = true
			discarded.PendingCleanup = []ValidatedBuildReferenceV1{{Image: record.Image, ImageReference: record.ImageReference}, {Image: record.Companion.Image, ImageReference: record.Companion.Reference, CompanionOwner: record.Owner}}
			sort.Slice(discarded.PendingCleanup, func(i, j int) bool {
				return discarded.PendingCleanup[i].ImageReference < discarded.PendingCleanup[j].ImageReference
			})
			incomplete := discarded
			incomplete.PendingCleanup = discarded.PendingCleanup[:1]
			if err := lock.CommitValidatedBuildV1(incomplete); err == nil {
				t.Fatal("discard lost owner inventory")
			}
			terminalGuardMustV1(t, lock.CommitValidatedBuildV1(discarded))
			replacement := discarded
			replacement.ImageReference = "reploy/env/demo:replacement"
			if err := lock.CommitValidatedBuildV1(replacement); err == nil {
				t.Fatal("retirement replaced owner")
			}
			discarded.PendingCleanup = nil
			terminalGuardMustV1(t, lock.CommitValidatedBuildV1(discarded))
			terminalGuardMustV1(t, lock.RemoveValidatedBuildV1())
			after, _, err := lock.ReadStateV1()
			if err != nil || !reflect.DeepEqual(after, guarded) {
				t.Fatalf("retirement replaced state: %v", err)
			}
		})
	}
}

func TestTerminalGuardRejectsUnconfirmedPublicationV1(t *testing.T) {
	for _, phase := range []string{"pending-current", "pending-validated", "changed-state", "replace", "replace-applied", "sync"} {
		t.Run(phase, func(t *testing.T) {
			dir, lock, state := terminalGuardFixtureV1(t)
			replace, sync := replaceAtomicStateFile, syncAtomicStateFileDirectory
			defer func() { replaceAtomicStateFile, syncAtomicStateFileDirectory = replace, sync }()
			failure := errors.New("injected guard fault")
			switch phase {
			case "pending-current", "pending-validated":
				name := pendingBuildFilename
				if phase == "pending-validated" {
					name = pendingValidatedBuildFilenameV1
				}
				terminalGuardMustV1(t, os.WriteFile(filepath.Join(dir, ".reploy", name), []byte("intent"), 0o600))
			case "changed-state":
				state.BlueprintSource = "unobserved"
			case "replace":
				replaceAtomicStateFile = func(string, string) error { return failure }
			case "replace-applied":
				replaceAtomicStateFile = func(source, destination string) error {
					if err := replace(source, destination); err != nil {
						return err
					}
					return failure
				}
			case "sync":
				syncAtomicStateFileDirectory = func(string) error { return failure }
			}
			if err := lock.BeginTerminalRemovalV1(state); err == nil {
				t.Fatal("unconfirmed guard accepted")
			}
			if len(lock.retirementState) != 0 {
				t.Fatal("failed write authorized retirement")
			}
			actual, _, err := lock.ReadStateV1()
			if err != nil || actual.TerminalRemoval != (phase == "sync" || phase == "replace-applied") {
				t.Fatalf("state=%#v err=%v", actual, err)
			}
			if phase == "sync" || phase == "replace-applied" {
				if err := lock.RequireRetirement(); err == nil {
					t.Fatal("applied-then-error write authorized retirement")
				}
				if err := lock.ResumeTerminalRemovalV1(actual); phase == "sync" && err == nil {
					t.Fatal("unconfirmed retry authorized retirement")
				}
				syncAtomicStateFileDirectory = sync
				terminalGuardMustV1(t, lock.ResumeTerminalRemovalV1(actual))
			}
		})
	}
}

func TestTerminalGuardStrictAuthorityAndLegacyV1(t *testing.T) {
	dir, lock, state := terminalGuardFixtureV1(t)
	state.TerminalRemoval = true
	state.Current = pendingValidatedFixtureV1(t).Candidate.Owner
	content, err := EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", stateFilenameV1)
	for name, invalid := range map[string][]byte{
		"duplicate guard": bytes.Replace(content, []byte(`"terminal_removal":true`), []byte(`"terminal_removal":false,"terminal_removal":true`), 1),
		"escaped guard":   bytes.Replace(content, []byte(`"terminal_removal"`), []byte(`"terminal_remov\u0061l"`), 1),
		"case guard":      bytes.Replace(content, []byte(`"terminal_removal"`), []byte(`"Terminal_Removal"`), 1),
		"false guard":     bytes.Replace(content, []byte(`"terminal_removal":true`), []byte(`"terminal_removal":false`), 1),
		"duplicate owner": bytes.Replace(content, []byte(`"reference":`), []byte(`"reference":"foreign","reference":`), 1),
		"escaped owner":   bytes.Replace(content, []byte(`"reference"`), []byte(`"ref\u0065rence"`), 1),
		"trailing":        append(append([]byte{}, content...), '\n'),
		"malformed":       []byte(`{"terminal_removal":`),
	} {
		t.Run(name, func(t *testing.T) {
			terminalGuardMustV1(t, os.WriteFile(path, invalid, 0o600))
			if err := lock.RequireWritable(); err == nil {
				t.Fatal("malformed authority admitted writer")
			}
			if err := lock.ResumeTerminalRemovalV1(state); err == nil {
				t.Fatal("malformed authority authorized retirement")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, invalid) {
				t.Fatal("rejection changed authority")
			}
		})
	}
	legacy, _ := legacyComponentsStagingStateFixtureV1(t)
	terminalGuardMustV1(t, os.WriteFile(path, legacy, 0o600))
	terminalGuardMustV1(t, lock.Unlock())
	fresh, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatalf("recognized unguarded legacy admission: %v", err)
	}
	defer fresh.Unlock()
	terminalGuardMustV1(t, fresh.RequireWritable())
}

func TestTerminalQueueCleanupDoesNotPromoteV1(t *testing.T) {
	for _, cleanup := range []string{"remove marker", "recover abandoned marker"} {
		t.Run(cleanup, func(t *testing.T) {
			_, lock, state := terminalGuardFixtureV1(t)
			boot, err := CurrentBootSessionIDV1()
			if err != nil {
				t.Fatal(err)
			}
			queue := NewLiveRunQueueV1()
			queue.Runs = []LiveRunV1{{ID: "control-0000000000000001", Kind: LiveRunKindControlV1, Name: string(ControlOperationStageV1), GenerationReference: "generation", Status: LiveRunStatusActiveV1, Exclusive: true, BootSession: boot}, {ID: "run-0000000000000002", Kind: LiveRunKindAppV1, Name: "app", GenerationReference: "generation", Status: LiveRunStatusWaitingV1, BootSession: boot}}
			lease, err := lock.AcquireLiveRunLeaseV1(queue.Runs[1].ID)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			terminalGuardMustV1(t, lock.CommitLiveRunQueueV1(queue))
			terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
			if cleanup == "remove marker" {
				returned, _, removeErr := lock.RemoveControlMarkerV1(queue.Runs[0].ID)
				err = removeErr
				if err == nil && len(returned.Runs) != 0 {
					t.Fatal("returned queue admitted canceled owner")
				}
			} else {
				_, err = lock.RecoverLiveRunQueueV1()
			}
			if err != nil {
				t.Fatal(err)
			}
			after, _, err := lock.ReadLiveRunQueueV1()
			if err != nil || len(after.Runs) != 0 {
				t.Fatalf("cleanup admitted waiting owner: %#v err=%v", after, err)
			}
		})
	}
}

func terminalGuardMustV1(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
