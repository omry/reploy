package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

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
	testRemovalGuardRejectsOwnerWritesAndPersistsAcrossRestartV1(t, false)
}

func TestInstalledRemovalGuardRejectsOwnerWritesAndPersistsAcrossRestartV1(t *testing.T) {
	testRemovalGuardRejectsOwnerWritesAndPersistsAcrossRestartV1(t, true)
}

func removalGuardFixtureV1(t *testing.T, installed bool) (string, *OperationLock, StateV1) {
	t.Helper()
	if !installed {
		return stagedRemovalGuardFixtureV1(t)
	}
	dir := t.TempDir()
	writeOverlayTestState(t, dir)
	op, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.Unlock() })
	state, _, err := op.SetInstallationStateV1(installationStateV1Fixture(dir))
	if err != nil {
		t.Fatal(err)
	}
	return dir, op, state
}

func testRemovalGuardRejectsOwnerWritesAndPersistsAcrossRestartV1(t *testing.T, installed bool) {
	dir, op, state := removalGuardFixtureV1(t, installed)
	begin, acquire := op.BeginStagedRemovalV1, AcquireStagedRemovalOperationLock
	if installed {
		begin, acquire = op.BeginInstalledRemovalV1, AcquireInstalledRemovalOperationLock
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := begin(state); err != nil {
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
	if err != nil || !found || !(guarded.Staging != nil && guarded.Staging.TerminalRemoval || guarded.Deployment != nil && guarded.Deployment.TerminalRemoval) {
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
	retry, err := acquire(t.Context(), dir)
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

func TestStagedRemovalGuardRejectsDuplicateOwnerMembersBeforeWritesV1(t *testing.T) {
	variants := []struct {
		name string
		make func(string, string) string
	}{
		{
			name: "null last",
			make: func(raw, field string) string {
				return strings.TrimSuffix(raw, "}") + `,"` + field + `":null}`
			},
		},
		{
			name: "escaped null last",
			make: func(raw, field string) string {
				escaped := strings.Replace(field, "n", `\u006e`, 1)
				return strings.TrimSuffix(raw, "}") + `,"` + escaped + `":null}`
			},
		},
		{
			name: "null first",
			make: func(raw, field string) string {
				member := `"` + field + `":`
				index := strings.Index(raw, member)
				if index < 0 {
					return raw
				}
				return raw[:index] + member + "null," + raw[index:]
			},
		},
		{
			name: "case aliases on both members",
			make: func(raw, field string) string {
				first := strings.ToUpper(field[:1]) + field[1:]
				raw = replaceStagedRemovalOwnerMemberNameV1(raw, field, first)
				return strings.TrimSuffix(raw, "}") + `,"` + strings.ToUpper(field) + `":null}`
			},
		},
		{
			name: "escaped case aliases on both members",
			make: func(raw, field string) string {
				upper := strings.ToUpper(field)
				first := strings.Replace(upper, "T", `\u0054`, 1)
				second := strings.Replace(upper, "N", `\u004e`, 1)
				raw = replaceStagedRemovalOwnerMemberNameV1(raw, field, first)
				return strings.TrimSuffix(raw, "}") + `,"` + second + `":null}`
			},
		},
	}
	for _, installed := range []bool{false, true} {
		kind, field := "staged", "staging"
		if installed {
			kind, field = "installed", "deployment"
		}
		fieldVariants := append([]struct {
			name string
			make func(string, string) string
		}{}, variants...)
		if alias := unicodeFoldEquivalentOwnerAliasV1(field); alias != "" {
			alias := alias
			fieldVariants = append(fieldVariants, struct {
				name string
				make func(string, string) string
			}{
				name: "unicode fold alias " + alias,
				make: func(raw, field string) string {
					raw = replaceStagedRemovalOwnerMemberNameV1(raw, field, alias)
					return strings.TrimSuffix(raw, "}") + `,"` + field + `":null}`
				},
			})
		}
		for _, variant := range fieldVariants {
			for _, callerHeld := range []bool{false, true} {
				access := "ordinary acquisition"
				if callerHeld {
					access = "diagnostic-held mutation"
				}
				name := kind + "/" + variant.name + "/" + access
				t.Run(name, func(t *testing.T) {
					dir, op, state := removalGuardFixtureV1(t, installed)
					begin := op.BeginStagedRemovalV1
					if installed {
						begin = op.BeginInstalledRemovalV1
					}
					if err := begin(state); err != nil {
						t.Fatal(err)
					}
					if err := op.Unlock(); err != nil {
						t.Fatal(err)
					}

					statePath := filepath.Join(dir, ".reploy", "state.json")
					canonical, err := os.ReadFile(statePath)
					if err != nil {
						t.Fatal(err)
					}
					duplicate := []byte(variant.make(string(canonical), field))
					if _, err := DecodeStateV1(duplicate); err == nil {
						t.Fatal("duplicate owner member unexpectedly decoded as canonical state")
					}
					if err := os.WriteFile(statePath, duplicate, 0o600); err != nil {
						t.Fatal(err)
					}

					lockPath := filepath.Join(dir, ".reploy", "operation.lock")
					if !callerHeld {
						if err := os.Remove(lockPath); err != nil {
							t.Fatal(err)
						}
						writer, err := AcquireOperationLock(t.Context(), dir)
						if writer != nil {
							_ = writer.Unlock()
						}
						if err == nil || !strings.Contains(err.Error(), "terminal removal") {
							t.Fatalf("ordinary acquisition error=%v", err)
						}
						if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
							t.Fatalf("denied acquisition created operation.lock: %v", err)
						}
						return
					}

					diagnostic, err := AcquireExistingOperationLock(t.Context(), dir)
					if err != nil {
						t.Fatal(err)
					}
					defer diagnostic.Unlock()
					checks := []struct {
						name string
						run  func() error
					}{
						{
							name: "build lock publication",
							run: func() error {
								_, err := diagnostic.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
								return err
							},
						},
						{
							name: "state replacement",
							run:  func() error { return diagnostic.CommitStateV1(nil, state) },
						},
					}
					for _, check := range checks {
						if err := check.run(); err == nil || !strings.Contains(err.Error(), "terminal removal") {
							t.Errorf("%s error=%v", check.name, err)
						}
					}
					after, err := os.ReadFile(statePath)
					if err != nil {
						t.Fatal(err)
					}
					if string(after) != string(duplicate) {
						t.Fatal("diagnostic-held owner mutation changed duplicate state")
					}
					if _, err := os.Lstat(filepath.Join(dir, ".reploy", buildLockDirectoryName)); !os.IsNotExist(err) {
						t.Fatalf("denied diagnostic mutation created build lock files: %v", err)
					}
				})
			}
		}
	}
}

func replaceStagedRemovalOwnerMemberNameV1(raw, field, alias string) string {
	return strings.Replace(raw, `"`+field+`":`, `"`+alias+`":`, 1)
}

func unicodeFoldEquivalentOwnerAliasV1(field string) string {
	for index, r := range field {
		for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
			if folded < 0x80 {
				continue
			}
			alias := field[:index] + string(folded) + field[index+len(string(r)):]
			if stagedRemovalOwnerAliasMatchesJSONV1(field, alias) {
				return alias
			}
		}
	}
	return ""
}

func stagedRemovalOwnerAliasMatchesJSONV1(field, alias string) bool {
	content := []byte(`{"` + alias + `":{"terminal_removal":true}}`)
	var probe struct {
		Staging    map[string]json.RawMessage `json:"staging"`
		Deployment map[string]json.RawMessage `json:"deployment"`
	}
	if err := json.Unmarshal(content, &probe); err != nil {
		return false
	}
	switch field {
	case "staging":
		_, found := probe.Staging["terminal_removal"]
		return found
	case "deployment":
		_, found := probe.Deployment["terminal_removal"]
		return found
	default:
		return false
	}
}

func TestStagedRemovalGuardRejectsCaseMatchedNestedMembersBeforeWritesV1(t *testing.T) {
	variants := []struct {
		name string
		make func(string) string
	}{
		{
			name: "mixed case",
			make: func(raw string) string {
				return strings.Replace(raw, `"terminal_removal":true`, `"Terminal_Removal":true`, 1)
			},
		},
		{
			name: "uppercase",
			make: func(raw string) string {
				return strings.Replace(raw, `"terminal_removal":true`, `"TERMINAL_REMOVAL":true`, 1)
			},
		},
		{
			name: "escaped uppercase",
			make: func(raw string) string {
				return strings.Replace(raw, `"terminal_removal":true`, `"TERMINAL_\u0052EMOVAL":true`, 1)
			},
		},
		{
			name: "conflict alias first",
			make: func(raw string) string {
				return strings.Replace(raw, `"terminal_removal":true`, `"TERMINAL_REMOVAL":false,"terminal_removal":true`, 1)
			},
		},
		{
			name: "conflict alias last",
			make: func(raw string) string {
				return strings.Replace(raw, `"terminal_removal":true`, `"terminal_removal":true,"TERMINAL_REMOVAL":false`, 1)
			},
		},
	}
	for _, installed := range []bool{false, true} {
		kind := "staged"
		if installed {
			kind = "installed"
		}
		for _, variant := range variants {
			for _, callerHeld := range []bool{false, true} {
				access := "ordinary acquisition"
				if callerHeld {
					access = "diagnostic-held mutation"
				}
				name := kind + "/" + variant.name + "/" + access
				it := installed
				t.Run(name, func(t *testing.T) {
					dir, op, state := removalGuardFixtureV1(t, it)
					begin := op.BeginStagedRemovalV1
					if it {
						begin = op.BeginInstalledRemovalV1
					}
					if err := begin(state); err != nil {
						t.Fatal(err)
					}
					if err := op.Unlock(); err != nil {
						t.Fatal(err)
					}

					statePath := filepath.Join(dir, ".reploy", "state.json")
					canonical, err := os.ReadFile(statePath)
					if err != nil {
						t.Fatal(err)
					}
					conflicting := []byte(variant.make(string(canonical)))
					if bytes.Equal(conflicting, canonical) {
						t.Fatal("fixture did not replace or conflict with the terminal member")
					}
					if _, err := DecodeStateV1(conflicting); err == nil {
						t.Fatal("noncanonical nested terminal member unexpectedly decoded as canonical state")
					}
					if err := os.WriteFile(statePath, conflicting, 0o600); err != nil {
						t.Fatal(err)
					}

					lockPath := filepath.Join(dir, ".reploy", "operation.lock")
					if !callerHeld {
						if err := os.Remove(lockPath); err != nil {
							t.Fatal(err)
						}
						writer, err := AcquireOperationLock(t.Context(), dir)
						if writer != nil {
							_ = writer.Unlock()
						}
						if err == nil || !strings.Contains(err.Error(), "terminal removal") {
							t.Fatalf("ordinary acquisition error=%v", err)
						}
						if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
							t.Fatalf("denied acquisition created operation.lock: %v", err)
						}
						return
					}

					diagnostic, err := AcquireExistingOperationLock(t.Context(), dir)
					if err != nil {
						t.Fatal(err)
					}
					defer diagnostic.Unlock()
					checks := []struct {
						name string
						run  func() error
					}{
						{
							name: "build lock publication",
							run: func() error {
								_, err := diagnostic.PublishBuildLock(validBuildLock(t), acceptBuildLockProfile)
								return err
							},
						},
						{
							name: "state replacement",
							run:  func() error { return diagnostic.CommitStateV1(nil, state) },
						},
					}
					for _, check := range checks {
						if err := check.run(); err == nil || !strings.Contains(err.Error(), "terminal removal") {
							t.Errorf("%s error=%v", check.name, err)
						}
					}
					after, err := os.ReadFile(statePath)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(after, conflicting) {
						t.Fatal("diagnostic-held owner mutation changed conflicting state")
					}
					if _, err := os.Lstat(filepath.Join(dir, ".reploy", buildLockDirectoryName)); !os.IsNotExist(err) {
						t.Fatalf("denied diagnostic mutation created build lock files: %v", err)
					}
				})
			}
		}
	}
}

func TestStagedRemovalGuardWriteFailureAuthorizesNoRetirementV1(t *testing.T) {
	testRemovalGuardWriteFailureAuthorizesNoRetirementV1(t, false)
}

func TestInstalledRemovalGuardWriteFailureAuthorizesNoRetirementV1(t *testing.T) {
	testRemovalGuardWriteFailureAuthorizesNoRetirementV1(t, true)
}

func testRemovalGuardWriteFailureAuthorizesNoRetirementV1(t *testing.T, installed bool) {
	for _, afterRename := range []bool{false, true} {
		t.Run(map[bool]string{false: "before replace", true: "directory sync"}[afterRename], func(t *testing.T) {
			dir, op, state := removalGuardFixtureV1(t, installed)
			begin, acquire := op.BeginStagedRemovalV1, AcquireStagedRemovalOperationLock
			if installed {
				begin, acquire = op.BeginInstalledRemovalV1, AcquireInstalledRemovalOperationLock
			}
			want := errors.New("injected guard durability failure")
			originalReplace, originalSync := replaceAtomicStateFile, syncAtomicStateFileDirectory
			t.Cleanup(func() { replaceAtomicStateFile = originalReplace; syncAtomicStateFileDirectory = originalSync })
			if afterRename {
				syncAtomicStateFileDirectory = func(string) error { return want }
			} else {
				replaceAtomicStateFile = func(string, string) error { return want }
			}
			if err := begin(state); !errors.Is(err, want) {
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
			guarded := observed.Staging != nil && observed.Staging.TerminalRemoval || observed.Deployment != nil && observed.Deployment.TerminalRemoval
			if guarded != afterRename {
				t.Fatal("applied-then-error guard state did not remain explicit")
			}
			if afterRename {
				if err := op.RequireWritable(); err == nil {
					t.Fatal("uncertain committed guard admitted mutation")
				}
				if err := op.Unlock(); err != nil {
					t.Fatal(err)
				}
				retry, err := acquire(t.Context(), dir)
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
