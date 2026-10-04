package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providerstore"
)

func stagedRemovalGuardFixtureV1(t *testing.T) (string, *OperationLock, StateV1) {
	t.Helper()
	dir := t.TempDir()
	op, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	state := StateV1{Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t), Overlay: EmptyRequestOverlayV1(), BlueprintSource: "retained source", Staging: &StagingStateV1{Schema: StagingStateSchemaV1}}
	if err := op.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.Unlock() })
	return dir, op, state
}

func TestStagedRemovalGuardRejectsOwnerWritesAndPersistsAcrossRestartV1(t *testing.T) {
	dir, op, state := stagedRemovalGuardFixtureV1(t)
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := op.BeginStagedRemovalV1(state); err != nil {
		t.Fatal(err)
	}
	if err := op.RequireOwnerWritable(); err == nil {
		t.Fatal("removal continuation admitted owner publication")
	}
	if _, err := op.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile); err == nil {
		t.Fatal("removal continuation published a new lock")
	}
	if err := op.CommitStateV1(nil, state); err == nil {
		t.Fatal("removal continuation cleared its guard")
	}
	if err := op.Unlock(); err != nil {
		t.Fatal(err)
	}
	if writer, err := AcquireOperationLock(t.Context(), dir); err == nil {
		_ = writer.Unlock()
		t.Fatal("writer admitted after restart")
	}
	diagnostic, err := AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	guarded, found, err := diagnostic.ReadStateV1()
	if err != nil || !found || !guarded.Staging.TerminalRemoval {
		t.Fatalf("read-only state: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, ".reploy", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		run  func() error
	}{
		{"state replacement", func() error { return diagnostic.CommitStateV1(nil, state) }},
		{"lock publication", func() error {
			_, err := diagnostic.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
			return err
		}},
		{"lock pruning", func() error { return diagnostic.RemoveAllBuildLocks(acceptBuildLockProfile) }},
		{"pending removal", diagnostic.RemovePendingBuild},
		{"validated removal", diagnostic.RemoveValidatedBuildV1},
		{"validated intent removal", diagnostic.RemovePendingValidatedBuildV1},
		{"queue publication", func() error { return diagnostic.CommitLiveRunQueueV1(NewLiveRunQueueV1()) }},
		{"stale queue recovery", func() error { _, _, err := diagnostic.RecoverAbandonedControlMarkerV1(); return err }},
		{"ready run activation", func() error { return diagnostic.ActivateReadyLiveRunV1("run-0000000000000001") }},
		{"ready control activation", func() error { return diagnostic.ActivateReadyControlMarkerV1("control-0000000000000001") }},
		{"control lease admission", func() error { _, err := diagnostic.AcquireControlLeaseV1("control-0000000000000001"); return err }},
		{"live run lease admission", func() error { _, err := diagnostic.AcquireLiveRunLeaseV1("run-0000000000000001"); return err }},
		{"validated publication", func() error { return diagnostic.CommitValidatedBuildV1(pendingValidatedFixtureV1(t).Candidate) }},
		{"validated intent publication", func() error { return diagnostic.WritePendingValidatedBuildV1(pendingValidatedFixtureV1(t)) }},
		{"store deletion", func() error {
			_, err := diagnostic.RemoveProviderStore(store)
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil || !strings.Contains(err.Error(), "terminal removal") {
				t.Fatalf("guard error=%v", err)
			}
		})
	}
	after, err := os.ReadFile(filepath.Join(dir, ".reploy", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("ordinary mutation or queue recovery changed terminal state")
	}
	if err := diagnostic.Unlock(); err != nil {
		t.Fatal(err)
	}
	retry, err := AcquireStagedRemovalOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Unlock()
	if err := retry.RequireWritable(); err != nil {
		t.Fatal(err)
	}
	if err := retry.RequireOwnerWritable(); err == nil {
		t.Fatal("explicit retry reopened ordinary admission")
	}
}

func TestStagedRemovalGuardRequiresExactRetainedAuthorityV1(t *testing.T) {
	for _, damage := range []string{"changed", "missing", "malformed"} {
		t.Run(damage, func(t *testing.T) {
			dir, op, state := stagedRemovalGuardFixtureV1(t)
			if err := op.BeginStagedRemovalV1(state); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, ".reploy", "state.json")
			if damage == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				content := []byte(`{"staging":{"terminal_removal":true}}`)
				if damage == "changed" {
					state.Staging.TerminalRemoval = true
					state.BlueprintSource = "changed source"
					var err error
					content, err = EncodeStateV1(state)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := op.RequireWritable(); err == nil {
				t.Fatal("removal continued after losing exact state authority")
			}
		})
	}
}

func TestStagedRemovalGuardPreflightDoesNotCreateLockV1(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical", true: "malformed"}[malformed], func(t *testing.T) {
			dir, op, state := stagedRemovalGuardFixtureV1(t)
			if err := op.BeginStagedRemovalV1(state); err != nil {
				t.Fatal(err)
			}
			if err := op.Unlock(); err != nil {
				t.Fatal(err)
			}
			if malformed {
				if err := os.WriteFile(filepath.Join(dir, ".reploy", "state.json"), []byte(`{"staging":{"terminal_removal":true}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lockPath := filepath.Join(dir, ".reploy", "operation.lock")
			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
			if writer, err := AcquireOperationLock(t.Context(), dir); err == nil {
				_ = writer.Unlock()
				t.Fatal("guarded or malformed state admitted writer")
			}
			if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
				t.Fatal("denied writer created lock file", err)
			}
		})
	}
}

func TestStagedRemovalGuardAllowsOrdinarySourceValueV1(t *testing.T) {
	dir, op, state := stagedRemovalGuardFixtureV1(t)
	state.BlueprintSource = "terminal_removal"
	if err := op.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	if err := op.Unlock(); err != nil {
		t.Fatal(err)
	}
	writer, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatalf("canonical unguarded source value rejected: %v", err)
	}
	defer writer.Unlock()
	if _, err := writer.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile); err != nil {
		t.Fatalf("unguarded owner publication rejected: %v", err)
	}
}

func TestStagedRemovalGuardRejectsMalformedEscapedStateV1(t *testing.T) {
	for _, callerHeld := range []bool{false, true} {
		t.Run(map[bool]string{false: "acquisition", true: "caller-held publication"}[callerHeld], func(t *testing.T) {
			dir, op, state := stagedRemovalGuardFixtureV1(t)
			if err := op.BeginStagedRemovalV1(state); err != nil {
				t.Fatal(err)
			}
			if err := op.Unlock(); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(dir, ".reploy", "state.json")
			content, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			escaped := strings.Replace(string(content), `"terminal_removal":true`, `"terminal_\u0072emoval":true`, 1)
			if escaped == string(content) {
				t.Fatal("fixture did not escape the guard key")
			}
			if err := os.WriteFile(statePath, []byte(escaped+"!"), 0o600); err != nil {
				t.Fatal(err)
			}
			if callerHeld {
				diagnostic, err := AcquireExistingOperationLock(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer diagnostic.Unlock()
				if _, err := diagnostic.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile); err == nil {
					t.Fatal("malformed escaped terminal state admitted caller-held publication")
				}
				return
			}
			lockPath := filepath.Join(dir, ".reploy", "operation.lock")
			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
			if writer, err := AcquireOperationLock(t.Context(), dir); err == nil {
				_ = writer.Unlock()
				t.Fatal("malformed escaped terminal state admitted a writer")
			}
			if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
				t.Fatalf("denied writer created a lock file: %v", err)
			}
		})
	}
}

func TestStagedRemovalGuardWriteFailureAuthorizesNoRetirementV1(t *testing.T) {
	for _, afterRename := range []bool{false, true} {
		t.Run(map[bool]string{false: "before replace", true: "directory sync"}[afterRename], func(t *testing.T) {
			dir, op, state := stagedRemovalGuardFixtureV1(t)
			want := errors.New("injected guard durability failure")
			originalReplace, originalSync := replaceAtomicStateFile, syncAtomicStateFileDirectory
			t.Cleanup(func() { replaceAtomicStateFile = originalReplace; syncAtomicStateFileDirectory = originalSync })
			if afterRename {
				syncAtomicStateFileDirectory = func(string) error { return want }
			} else {
				replaceAtomicStateFile = func(string, string) error { return want }
			}
			if err := op.BeginStagedRemovalV1(state); !errors.Is(err, want) {
				t.Fatalf("guard failure=%v", err)
			}
			if len(op.terminalRemovalState) != 0 {
				t.Fatal("failed guard write granted retirement continuation")
			}
			replaceAtomicStateFile, syncAtomicStateFileDirectory = originalReplace, originalSync
			observed, found, err := op.ReadStateV1()
			if err != nil || !found {
				t.Fatal(err)
			}
			if observed.Staging.TerminalRemoval != afterRename {
				t.Fatal("applied-then-error guard state did not remain explicit")
			}
			if afterRename {
				if err := op.RequireWritable(); err == nil {
					t.Fatal("uncertain committed guard admitted mutation")
				}
				if err := op.Unlock(); err != nil {
					t.Fatal(err)
				}
				retry, err := AcquireStagedRemovalOperationLock(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer retry.Unlock()
				if err := retry.RequireWritable(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
