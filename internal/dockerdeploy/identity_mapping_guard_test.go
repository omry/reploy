package dockerdeploy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/fsowner"
	"github.com/omry/reploy/internal/providerstore"
)

func identityGuardDocumentV1(t *testing.T, state deploy.StateV1, declared, missing bool) deploy.StateV1 {
	t.Helper()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Runtime.IdentityMapping = nil
	if declared {
		start := "100000"
		if missing {
			start = `"???"`
		}
		source, err := blueprint.Decode([]byte(fmt.Sprintf("blueprint: {schema: 1}\nenvironment:\n  runtime:\n    identity_mapping: {schema: 1, profile: exact, uids: [{container_start: 0, host_start: %s, count: 16}], gids: [{container_start: 0, host_start: 200000, count: 16}]}\n", start)))
		if err != nil {
			t.Fatal(err)
		}
		document.Environment.Runtime.IdentityMapping = source.Environment.Runtime.IdentityMapping
	}
	document.Environment.Commands = map[string]blueprint.Command{
		"config": {Executable: "application.main", Trigger: []string{"config"}, NativeCommand: true, DeployedCommand: true},
	}
	state.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func identityGuardFixtureV1(t *testing.T, missing, installed bool) (string, deploy.StateV1) {
	t.Helper()
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	state, found, err := operation.ReadStateV1()
	if err != nil || !found {
		t.Fatal("missing fixture state", err)
	}
	state = identityGuardDocumentV1(t, state, true, missing)
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "blueprint.yaml"
	if installed {
		installation := installedBuildPublicationInstallation(dir)
		installation.Scope = "user"
		state.Staging = nil
		state.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: installation}
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	return dir, state
}

func identityGuardTreeV1(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("snapshot fixture requires a regular file: %s", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		// NTFS directory entries can retain stale attributes for a hard link.
		// Compare actual handle metadata and bytes, including the file mode.
		info, err = file.Stat()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.Dir(relative) == ".reploy" && (entry.Name() == "operation.lock" || strings.HasSuffix(entry.Name(), ".lease")) {
			// Windows denies reads through a held byte-range lock. These files
			// carry no data; retain their metadata and reject any content.
			if info.Size() != 0 {
				return fmt.Errorf("lock fixture contains unexpected data: %s", relative)
			}
			uid, gid := fsowner.FromInfo(info).IDs()
			result[strings.TrimPrefix(path, root)] = fmt.Sprintf("%o:%d:%d:%d:empty-lock", info.Mode(), uid, gid, info.ModTime().UnixNano())
			return nil
		}
		content, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		uid, gid := fsowner.FromInfo(info).IDs()
		result[strings.TrimPrefix(path, root)] = fmt.Sprintf("%o:%d:%d:%d:%x", info.Mode(), uid, gid, info.ModTime().UnixNano(), sha256.Sum256(content))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireIdentityGuardErrorV1(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "environment.runtime.identity_mapping") || !strings.Contains(err.Error(), "not yet available") {
		t.Fatalf("expected explicit mapping admission inhibition, got %v", err)
	}
}

func TestIdentityMappingGuardV1RealPayloadEntriesPreserveMetadata(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, installed := range []bool{false, true} {
			for _, entry := range []string{"shell", "shell wait", "app native", "app deployed", "runtime test", "up", "restart", "down", "public shell", "public app", "public up", "public restart"} {
				t.Run(fmt.Sprintf("missing=%t/installed=%t/%s", missing, installed, entry), func(t *testing.T) {
					if entry == "app deployed" && !installed {
						return
					}
					dir, _ := identityGuardFixtureV1(t, missing, installed)
					before := identityGuardTreeV1(t, dir)
					var err error
					switch entry {
					case "shell", "shell wait":
						err = RunCurrentShellV1(t.Context(), CurrentShellRunInputV1{DeploymentDir: dir, Wait: entry == "shell wait"})
					case "app native", "app deployed":
						err = RunCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"config", "init"}, DeployedOnly: entry == "app deployed"})
					case "runtime test":
						err = RunCurrentRuntimeTestV1(t.Context(), CurrentRuntimeTestInputV1{DeploymentDir: dir})
					case "up", "restart", "down":
						err = RunCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: entry, ControlMode: ControlAdmissionForceV1})
					case "public shell":
						err = Shell(ShellOptions{Dir: dir, Stdout: io.Discard})
					case "public app":
						err = AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"config", "init"}, Stdout: io.Discard})
					case "public up", "public restart":
						err = Runtime(RuntimeOptions{Dir: dir, Action: strings.TrimPrefix(entry, "public "), Stdout: io.Discard, Stderr: io.Discard})
					}
					requireIdentityGuardErrorV1(t, err)
					if after := identityGuardTreeV1(t, dir); !reflect.DeepEqual(before, after) {
						t.Fatalf("entry changed deployment metadata: before=%v after=%v", before, after)
					}
				})
			}
		}
	}
}

func TestIdentityMappingGuardV1EntryCallbacksStopBeforeRuntimeWork(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, entry := range []string{"shell", "app", "test", "workload"} {
			t.Run(fmt.Sprintf("%s/missing=%t", entry, missing), func(t *testing.T) {
				dir := t.TempDir()
				current, _ := runtimeCurrentBuildFixture(t)
				current.State = identityGuardDocumentV1(t, current.State, true, missing)
				order := []string{}
				var err error
				switch entry {
				case "shell":
					err = runCurrentShellV1(t.Context(), CurrentShellRunInputV1{DeploymentDir: dir}, currentShellRunTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order))
				case "app":
					err = runCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"config"}}, currentAppCommandRunTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order))
				case "test":
					err = runCurrentRuntimeTestV1(t.Context(), CurrentRuntimeTestInputV1{DeploymentDir: dir}, currentRuntimeTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order))
				case "workload":
					err = runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "up"}, currentWorkloadRunTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order))
				}
				requireIdentityGuardErrorV1(t, err)
				if !reflect.DeepEqual(order, []string{"acquire", "store", "state"}) {
					t.Fatalf("mapped entry reached runtime or admission callback: %v", order)
				}
			})
		}
	}
}

func TestIdentityMappingGuardV1AutomaticBuildStopsBeforeRecoveryAndExplicitBuildContinues(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, automatic := range []bool{false, true} {
			t.Run(fmt.Sprintf("missing=%t/automatic=%t", missing, automatic), func(t *testing.T) {
				dir, _ := identityGuardFixtureV1(t, missing, false)
				before := identityGuardTreeV1(t, dir)
				recovered := false
				reached := fmt.Errorf("portable build reached preparation")
				_, err := runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir, Automatic: automatic, Runtime: blueprintRuntimeFixtureV1()}, providerBuildRunBackend{
					acquire: deploy.AcquireOperationLock, newStore: providerstore.NewStore,
					recoverValidated: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (bool, error) {
						recovered = true
						return false, reached
					},
					prepare: func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
						t.Fatal("unexpected provider preparation")
						return LockedProviderBuildPreparationV1{}, nil
					},
					execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
						t.Fatal("unexpected provider execution")
						return LockedProviderBuildExecutionResultV1{}, nil
					},
				})
				if automatic {
					requireIdentityGuardErrorV1(t, err)
					if recovered {
						t.Fatal("automatic mapped build recovered provider state")
					}
				} else if err == nil || !strings.Contains(err.Error(), reached.Error()) || !recovered {
					t.Fatalf("explicit portable build was inhibited: %v", err)
				}
				if !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
					t.Fatal("build guard changed source state")
				}
			})
		}
	}
}

func TestIdentityMappingGuardV1StagedInstallRejectsBeforeBuildAndDestinationCreation(t *testing.T) {
	for _, missing := range []bool{false, true} {
		source, _ := identityGuardFixtureV1(t, missing, false)
		target := filepath.Join(t.TempDir(), "must-not-exist")
		before := identityGuardTreeV1(t, source)
		_, err := RunProviderInstallV1(t.Context(), ProviderInstallInputV1{SourceDeploymentDir: source, DestinationDeploymentDir: target, Scope: InstallScopeUser, Runtime: blueprintRuntimeFixtureV1(), Start: true})
		requireIdentityGuardErrorV1(t, err)
		if !reflect.DeepEqual(before, identityGuardTreeV1(t, source)) {
			t.Fatal("mapped install changed its source")
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("mapped install prepared destination", err)
		}
	}
}

func TestIdentityMappingGuardV1DestinationPreflightAndLockedCheckReject(t *testing.T) {
	for _, missing := range []bool{false, true} {
		dir, _ := identityGuardFixtureV1(t, missing, true)
		before := identityGuardTreeV1(t, dir)
		requireIdentityGuardErrorV1(t, preflightProviderInstallDestinationV1(dir, ""))
		operation, err := deploy.AcquireOperationLock(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		requireIdentityGuardErrorV1(t, validateProviderInstallDestinationServiceV1(operation, ""))
		if err := operation.Unlock(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
			t.Fatal("destination inspection changed metadata")
		}
	}
}

func TestIdentityMappingGuardV1ExplicitBuildPlansAndExecutesPortableInputs(t *testing.T) {
	for _, missing := range []bool{false, true} {
		dir, state := identityGuardFixtureV1(t, missing, false)
		before := identityGuardTreeV1(t, dir)
		document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
		if err != nil {
			t.Fatal(err)
		}
		wantInputs, err := ValidatedBuildInputs(document, state.Overlay, deploy.EmptyPackageOverridesV1(document.Environment.ID), dir, state.Platform)
		if err != nil {
			t.Fatal(err)
		}
		prepared, executed := false, false
		_, err = runProviderBuildV1(t.Context(), ProviderBuildRunInputV1{DeploymentDir: dir, NoCache: true, Runtime: blueprintRuntimeFixtureV1()}, providerBuildRunBackend{
			acquire: deploy.AcquireOperationLock, newStore: providerstore.NewStore,
			prepare: func(_ context.Context, input LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
				prepared = true
				if !reflect.DeepEqual(input.ValidatedInputs, wantInputs) {
					t.Fatal("mapping changed canonical build authority")
				}
				if err := input.Operation.RequireHeld(); err != nil {
					t.Fatal(err)
				}
				return LockedProviderBuildPreparationV1{Operation: input.Operation, Store: input.Store}, nil
			},
			execute: func(_ context.Context, input LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
				executed = true
				if err := input.Preparation.Operation.RequireHeld(); err != nil {
					t.Fatal(err)
				}
				return LockedProviderBuildExecutionResultV1{Reused: true}, nil
			},
		})
		if err != nil || !prepared || !executed {
			t.Fatalf("explicit portable build: prepared=%t executed=%t err=%v", prepared, executed, err)
		}
		if !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
			t.Fatal("portable build altered declaration authority")
		}
	}
}

func TestIdentityMappingGuardV1InvalidAuthorityFailsClosed(t *testing.T) {
	if _, err := unmappedRuntimeDocumentV1(deploy.StateV1{Blueprint: "broken"}); err == nil {
		t.Fatal("guard accepted an invalid resolved blueprint")
	}
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := requireUnmappedDeploymentV1(operation); err != nil {
		t.Fatal("queue-only owner requires no state", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(operation.Path()), "state.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireUnmappedDeploymentV1(operation); err == nil {
		t.Fatal("admission ignored invalid persisted state")
	}
}
