package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
)

func identityGuardSystemServiceFixtureV1(t *testing.T, declared, missing bool) string {
	t.Helper()
	dir, _ := stageProviderBuildRunState(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	state, found, err := operation.ReadStateV1()
	if err != nil || !found {
		_ = operation.Unlock()
		t.Fatalf("missing fixture state: found=%t err=%v", found, err)
	}
	state = identityGuardDocumentV1(t, state, declared, missing)
	state.Deployment = &deploy.DeploymentStateV1{
		Schema:       deploy.DeploymentStateSchemaV1,
		Installation: installedBuildPublicationInstallation(dir),
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		_ = operation.Unlock()
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIdentityMappingGuardV1InstalledServiceContainerRejectsCompleteAndUnresolvedWithoutEffects(t *testing.T) {
	for _, missing := range []bool{false, true} {
		missing := missing
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			dir := identityGuardSystemServiceFixtureV1(t, true, missing)
			before := identityGuardTreeV1(t, dir)
			called := false
			previous := bindInstalledServiceCommandRunner
			bindInstalledServiceCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
				called = true
				return nil, fmt.Errorf("installed service Docker runner reached")
			}
			defer func() { bindInstalledServiceCommandRunner = previous }()

			for _, entry := range []string{"start", "restart"} {
				t.Run(entry, func(t *testing.T) {
					err := RunInstalledServiceContainerV1(t.Context(), dir, "run", "/usr/bin/docker", RunOptions{})
					requireIdentityGuardErrorV1(t, err)
					if called {
						t.Fatal("mapped system-service entry reached the Docker runner")
					}
					if after := identityGuardTreeV1(t, dir); !reflect.DeepEqual(before, after) {
						t.Fatalf("mapped system-service entry changed deployment/runtime files: before=%v after=%v", before, after)
					}
				})
			}
		})
	}
}

func TestIdentityMappingGuardV1InstalledServiceContainerAbsentDeclarationUsesOrdinaryPath(t *testing.T) {
	dir := identityGuardSystemServiceFixtureV1(t, false, false)
	before := identityGuardTreeV1(t, dir)
	called := false
	previous := bindInstalledServiceCommandRunner
	bindInstalledServiceCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		called = true
		return nil, fmt.Errorf("installed service Docker runner reached")
	}
	defer func() { bindInstalledServiceCommandRunner = previous }()

	err := RunInstalledServiceContainerV1(t.Context(), dir, "run", filepath.Join(t.TempDir(), "docker"), RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "installed service build is missing") {
		t.Fatalf("ordinary system-service path error = %v", err)
	}
	if strings.Contains(err.Error(), "environment.runtime.identity_mapping") {
		t.Fatalf("ordinary system-service path was treated as mapped: %v", err)
	}
	if called {
		t.Fatal("ordinary system-service path reached the Docker runner before its missing-build error")
	}
	if after := identityGuardTreeV1(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("ordinary system-service path changed deployment/runtime files: before=%v after=%v", before, after)
	}
}

func TestIdentityMappingGuardV1BothSessionRolesStopBeforePreparation(t *testing.T) {
	for _, role := range []string{"controller", "workload"} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/missing=%t", role, missing), func(t *testing.T) {
				mapped, _ := identityGuardFixtureV1(t, missing, false)
				ordinary, _ := stageProviderBuildRunState(t, false)
				controller, workload := mapped, ordinary
				if role == "workload" {
					controller, workload = ordinary, mapped
				}
				before := identityGuardTreeV1(t, mapped)
				fixture, _ := controlledSessionPlanFixtureV1(t)
				_, err := runCurrentControlledSessionV1(t.Context(), CurrentControlledSessionRunInputV1{
					ControllerDeploymentDir: controller, WorkloadDeploymentDir: workload,
					OutputDir: filepath.Join(t.TempDir(), "not-created"),
				}, currentControlledSessionRunBackendV1{
					acquire: deploy.AcquireOperationLock,
					loadRuntime: func(ctx context.Context, operation *deploy.OperationLock, dir string, runtime StagedProviderBuildRuntimeV1) (currentControlledSessionRuntimeV1, error) {
						if dir == mapped {
							return loadCurrentControlledSessionRuntimeV1(ctx, operation, dir, runtime)
						}
						return currentControlledSessionRuntimeV1{current: fixture.ControllerCurrent, plan: fixture.ControllerRuntime}, nil
					},
					privateEnv: func(string) (privateWorkloadEnvironmentV1, error) {
						t.Fatal("session reached private environment preparation")
						return privateWorkloadEnvironmentV1{}, nil
					},
					prepareController: prepareControlledSessionControllerPackageV1,
					cleanupController: cleanupControlledSessionControllerPackageV1,
					prepareOutput:     prepareOneShotOutput,
					abortOutput:       func(output *oneShotOutputSession) error { return output.abort() },
					publishOutput:     func(output *oneShotOutputSession) error { return output.publish() },
					concurrency:       PlanLiveRunConcurrencyV1,
					newRunID:          deploy.NewLiveRunIDV1, newHandle: controlledsession.NewHandleV1,
					acquireLease: func(operation *deploy.OperationLock, id string) (*deploy.QueueEntryLeaseV1, error) {
						return operation.AcquireLiveRunLeaseV1(id)
					},
					await: AwaitLiveRunAdmissionWithNoticeV1, plan: PlanControlledSessionV1,
					run: runControlledSessionWithControllerReservationV1,
				})
				requireIdentityGuardErrorV1(t, err)
				if !strings.Contains(err.Error(), role+" runtime") {
					t.Fatalf("wrong session role rejected: %v", err)
				}
				if !reflect.DeepEqual(before, identityGuardTreeV1(t, mapped)) {
					t.Fatal("session changed mapped deployment")
				}
			})
		}
	}
}

func TestIdentityMappingGuardV1DirectInstallStagesPortableIntentThenRejects(t *testing.T) {
	for _, missing := range []bool{false, true} {
		ref, path := writeDesiredStateStagePack(t, "0.1.0")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		start := "100000"
		if missing {
			start = `"???"`
		}
		mapping := fmt.Sprintf("\n    identity_mapping: {schema: 1, profile: exact, uids: [{container_start: 0, host_start: %s, count: 16}], gids: [{container_start: 0, host_start: 200000, count: 16}]}", start)
		text := strings.ReplaceAll(string(content), "\r\n", "\n")
		updated := strings.Replace(text, "environment:\n", "environment:\n  runtime:"+mapping+"\n", 1)
		if updated == text {
			t.Fatal("fixture did not insert the mapping declaration")
		}
		content = []byte(updated)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		workspace := ""
		_, err = runDirectProviderInstallEntryV1(t.Context(), DirectProviderInstallInputV1{
			Pack: ref, ExplicitPlatform: "linux/amd64", Scope: InstallScopeUser, Runtime: blueprintRuntimeFixtureV1(),
		}, directProviderInstallEntryBackendV1{
			effectiveUID: func() int { return 1000 },
			withSource: func(ctx context.Context, input directProviderInstallSourceInputV1, run func(context.Context, string) error) error {
				return withDirectProviderInstallSourceV1(ctx, input, func(ctx context.Context, dir string) error {
					workspace = filepath.Dir(dir)
					state, err := readProviderInstallSourceStateV1(ctx, dir)
					if err != nil {
						return err
					}
					_, guardErr := unmappedRuntimeDocumentV1(state)
					requireIdentityGuardErrorV1(t, guardErr)
					return run(ctx, dir)
				})
			},
			readState: readProviderInstallSourceStateV1,
			roots: func(string) (installTargetRootsV1, error) {
				t.Fatal("direct install resolved destination roots")
				return installTargetRootsV1{}, nil
			},
			install: func(context.Context, ProviderInstallInputV1) (deploy.StateV1, error) {
				t.Fatal("direct install prepared providers or destination")
				return deploy.StateV1{}, nil
			},
		})
		requireIdentityGuardErrorV1(t, err)
		if workspace == "" {
			t.Fatal("portable staging was not exercised")
		}
		if _, err := os.Stat(workspace); !os.IsNotExist(err) {
			t.Fatal("private staging survived", err)
		}
	}
}

func TestIdentityMappingGuardV1RechecksAfterAdmissionWait(t *testing.T) {
	for _, control := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("control=%t/missing=%t", control, missing), func(t *testing.T) {
				dir, _ := stageProviderBuildRunState(t, false)
				operation, err := deploy.AcquireOperationLock(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				first := liveRunAdmissionFixtureV1("run-0000000000000001", true)
				holdLiveRunLeaseV1(t, operation, first.ID)
				if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
					t.Fatal(err)
				}
				waits := 0
				wait := func(ctx context.Context) error {
					waits++
					if waits > 1 {
						t.Fatal("mapped candidate waited again")
					}
					writer, err := deploy.AcquireOperationLock(ctx, dir)
					if err != nil {
						return err
					}
					defer writer.Unlock()
					state, _, err := writer.ReadStateV1()
					if err != nil {
						return err
					}
					state = identityGuardDocumentV1(t, state, true, missing)
					return writer.CommitStateV1(nil, state)
				}
				remove := func(CommandSpec, RunOptions) error {
					t.Fatal("mapping guard performed container cleanup")
					return nil
				}
				var returned *deploy.OperationLock
				if control {
					marker := controlAdmissionFixtureV1("control-0000000000000001", deploy.ControlOperationInstallV1)
					holdControlLeaseV1(t, operation, marker.ID)
					returned, err = awaitControlAdmissionV1(t.Context(), dir, operation, marker, true, io.Discard, controlAdmissionBackendV1{acquire: deploy.AcquireOperationLock, wait: wait, removeContainer: remove})
				} else {
					candidate := liveRunAdmissionFixtureV1("run-0000000000000002", true)
					holdLiveRunLeaseV1(t, operation, candidate.ID)
					returned, err = awaitLiveRunAdmissionV1(t.Context(), dir, operation, candidate, true, io.Discard, liveRunAdmissionBackendV1{acquire: deploy.AcquireOperationLock, wait: wait, removeContainer: remove})
				}
				requireIdentityGuardErrorV1(t, err)
				if returned != nil || waits != 1 {
					t.Fatal("returned admission after mapping change")
				}
				inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer inspection.Unlock()
				queue, _, err := inspection.ReadLiveRunQueueV1()
				if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != first.ID || queue.Runs[0].Status != deploy.LiveRunStatusActiveV1 {
					t.Fatalf("guard changed an existing run or retained its own candidate: %#v %v", queue, err)
				}
				requireIdentityGuardErrorV1(t, requireUnmappedDeploymentV1(inspection))
			})
		}
	}
}

func TestIdentityMappingGuardV1AllControlModesRejectBeforeQueueOrCleanup(t *testing.T) {
	for _, mode := range []ControlAdmissionModeV1{ControlAdmissionImmediateV1, ControlAdmissionWaitV1, ControlAdmissionDrainV1, ControlAdmissionForceV1} {
		t.Run(string(mode), func(t *testing.T) {
			dir, _ := identityGuardFixtureV1(t, true, false)
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			first := liveRunAdmissionFixtureV1("run-0000000000000001", true)
			holdLiveRunLeaseV1(t, operation, first.ID)
			if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
				t.Fatal(err)
			}
			before := identityGuardTreeV1(t, dir)
			_, err = admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{Mode: mode, Operation: deploy.ControlOperationInstallV1}, controlOperationAdmissionBackendV1{
				newID: func() (string, error) { t.Fatal("mapping guard generated control marker"); return "", nil },
				pause: func(context.Context, time.Duration) error { t.Fatal("mapping guard started disruption"); return nil },
				await: func(context.Context, string, *deploy.OperationLock, deploy.ControlMarkerV1, bool, io.Writer) (*deploy.OperationLock, error) {
					t.Fatal("mapping guard reached admission")
					return nil, nil
				},
				removeContainer: func(CommandSpec, RunOptions) error { t.Fatal("mapping guard removed container"); return nil },
			})
			requireIdentityGuardErrorV1(t, err)
			if !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
				t.Fatal("mapping guard changed active control state")
			}
		})
	}
}
