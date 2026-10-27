package dockerdeploy

import (
	"context"
	"github.com/omry/reploy/internal/controlledsession"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func policyGuardDocumentV1(t *testing.T, document blueprint.Document) blueprint.Document {
	t.Helper()
	source, err := blueprint.Decode([]byte("blueprint: {schema: 1}\nenvironment:\n  runtime:\n    identity_policy: {schema: 1, users: {worker: 200}, groups: {worker: 200}, roles: {app: {user: worker}}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Runtime.User = blueprint.DefaultRuntimeUser
	document.Environment.Runtime.IdentityMapping = nil
	document.Environment.Runtime.IdentityPolicy = source.Environment.Runtime.IdentityPolicy
	return document
}

func policyGuardStateV1(t *testing.T, state deploy.StateV1) deploy.StateV1 {
	t.Helper()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	state.Blueprint, err = blueprint.EncodeResolvedDocumentV1(policyGuardDocumentV1(t, document))
	if err != nil {
		t.Fatal(err)
	}
	state.IdentityMapping = nil
	return state
}

func policyGuardFixtureV1(t *testing.T, installed bool) string {
	t.Helper()
	dir, state := identityGuardFixtureV1(t, false, installed)
	state = policyGuardStateV1(t, state)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	return dir
}

func requirePolicyGuardErrorV1(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "environment.runtime.identity_policy") || !strings.Contains(err.Error(), "not yet available") {
		t.Fatalf("expected policy inhibition, got %v", err)
	}
}

func TestIdentityPolicyGuardV1PayloadEntriesHaveNoEffects(t *testing.T) {
	for _, installed := range []bool{false, true} {
		for _, entry := range []string{"shell", "app", "test", "up", "restart", "down", "public shell", "public app", "public up", "public restart"} {
			t.Run(entry+map[bool]string{true: "/installed", false: "/staged"}[installed], func(t *testing.T) {
				dir := policyGuardFixtureV1(t, installed)
				before := identityGuardTreeV1(t, dir)
				var err error
				switch entry {
				case "shell":
					err = RunCurrentShellV1(t.Context(), CurrentShellRunInputV1{DeploymentDir: dir, Wait: true})
				case "app":
					err = RunCurrentAppCommandV1(t.Context(), CurrentAppCommandRunInputV1{DeploymentDir: dir, Arguments: []string{"config"}})
				case "test":
					err = RunCurrentRuntimeTestV1(t.Context(), CurrentRuntimeTestInputV1{DeploymentDir: dir})
				case "up", "restart", "down":
					err = RunCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: entry, ControlMode: ControlAdmissionForceV1})
				case "public shell":
					err = Shell(ShellOptions{Dir: dir, Stdout: io.Discard})
				case "public app":
					err = AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"config"}, Stdout: io.Discard})
				case "public up", "public restart":
					err = Runtime(RuntimeOptions{Dir: dir, Action: strings.TrimPrefix(entry, "public "), Stdout: io.Discard, Stderr: io.Discard})
				}
				requirePolicyGuardErrorV1(t, err)
				if !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
					t.Fatal("policy-only entry changed deployment files")
				}
			})
		}
	}
}

func TestIdentityPolicyGuardV1EntryCallbacksStopBeforeRuntimeWork(t *testing.T) {
	for _, entry := range []string{"shell", "app", "test", "workload"} {
		t.Run(entry, func(t *testing.T) {
			dir := t.TempDir()
			current, _ := runtimeCurrentBuildFixture(t)
			current.State = policyGuardStateV1(t, current.State)
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
			requirePolicyGuardErrorV1(t, err)
			if !reflect.DeepEqual(order, []string{"acquire", "store", "state"}) {
				t.Fatal("reached runtime effects", order)
			}
		})
	}
}

func TestIdentityPolicyGuardV1CompletionStopsBeforeFinalizeAssembleAndPublish(t *testing.T) {
	for _, validate := range []bool{false, true} {
		input, operation, store := providerBuildCompletionFixture(t)
		input.Document = policyGuardDocumentV1(t, input.Document)
		input.ValidateChoices = validate
		before := identityGuardTreeV1(t, input.DeploymentDir)
		backend := providerBuildCompletionBackend{
			validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, deploy.ApplicationStartupVerifierV1, deploy.ApplicationLocalAccountV1, RunOptions) (FinalizedBuildValidationResult, error) {
				t.Fatal("finalized policy-bearing image without evidence")
				return FinalizedBuildValidationResult{}, nil
			},
			assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
				t.Fatal("assembled evidence-free lock")
				return deploy.BuildLockV1{}, nil
			},
			publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
				t.Fatal("published evidence-free build")
				return deploy.StateV1{}, nil
			},
			publishValidated: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string, deploy.BuildLockV1, ValidatedBuildInputsV1) (deploy.ValidatedBuildV1, error) {
				t.Fatal("published evidence-free validated build")
				return deploy.ValidatedBuildV1{}, nil
			},
			removeFinalized: func(context.Context, BuiltImageCandidate) error {
				t.Fatal("cleanup ran without a finalized image")
				return nil
			},
		}
		_, err := completeProviderBuild(t.Context(), operation, store, input, backend)
		requirePolicyGuardErrorV1(t, err)
		if !reflect.DeepEqual(before, identityGuardTreeV1(t, input.DeploymentDir)) {
			t.Fatal("completion changed deployment files")
		}
		if err := operation.Unlock(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIdentityPolicyGuardV1DirectPublicationAndReuseHaveNoEffects(t *testing.T) {
	dir := policyGuardFixtureV1(t, false)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	state, _, err := operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	before := identityGuardTreeV1(t, dir)
	_, err = publishBuild(t.Context(), operation, providerstore.Store{}, BuildPublicationInput{Document: document}, buildPublicationBackend{
		newReferences: func(string, string) (EnvironmentImageReferences, error) {
			t.Fatal("created reference names")
			return EnvironmentImageReferences{}, nil
		},
		createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			t.Fatal("created reference")
			return nil
		},
		removeReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
			t.Fatal("removed reference")
			return nil
		},
	})
	requirePolicyGuardErrorV1(t, err)
	// Validated publication has no document argument. It must check its selected state.
	_, err = publishValidatedBuild(t.Context(), operation, providerstore.Store{}, "demo", dir, deploy.BuildLockV1{}, ValidatedBuildInputsV1{}, newValidatedPublicationBackendV1())
	requirePolicyGuardErrorV1(t, err)
	current, input := currentBuildReuseFixture(t)
	input.Document = policyGuardDocumentV1(t, input.Document)
	match, err := CurrentBuildMatches(current, input)
	requirePolicyGuardErrorV1(t, err)
	if match {
		t.Fatal("reused policy without evidence")
	}
	_, err = rebindCurrentBuildLockV1(current.Lock, input.Document)
	requirePolicyGuardErrorV1(t, err)
	if !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
		t.Fatal("publication/reuse changed deployment files")
	}
}

func TestIdentityPolicyGuardV1StagingCleanupRemainsAvailable(t *testing.T) {
	dir := policyGuardFixtureV1(t, false)
	_, err := RemoveStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: dir, ControlMode: ControlAdmissionImmediateV1})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIdentityPolicyGuardV1BothSessionRolesStopBeforePreparation(t *testing.T) {
	for _, role := range []string{"controller", "workload"} {
		t.Run(role, func(t *testing.T) {
			mapped := policyGuardFixtureV1(t, false)
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
			requirePolicyGuardErrorV1(t, err)
			if !strings.Contains(err.Error(), role+" runtime") {
				t.Fatalf("wrong session role rejected: %v", err)
			}
			if !reflect.DeepEqual(before, identityGuardTreeV1(t, mapped)) {
				t.Fatal("session changed mapped deployment")
			}
		})
	}
}

func TestIdentityPolicyGuardV1InstallRejectsBeforeDestinationCreation(t *testing.T) {
	source := policyGuardFixtureV1(t, false)
	target := filepath.Join(t.TempDir(), "must-not-exist")
	before := identityGuardTreeV1(t, source)
	_, err := RunProviderInstallV1(t.Context(), ProviderInstallInputV1{SourceDeploymentDir: source, DestinationDeploymentDir: target, Scope: InstallScopeUser, Runtime: blueprintRuntimeFixtureV1(), Start: true})
	requirePolicyGuardErrorV1(t, err)
	if !reflect.DeepEqual(before, identityGuardTreeV1(t, source)) {
		t.Fatal("install changed source")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("install created destination", err)
	}
}

func TestIdentityPolicyGuardV1AllControlModesRejectBeforeQueueOrCleanup(t *testing.T) {
	for _, mode := range []ControlAdmissionModeV1{ControlAdmissionImmediateV1, ControlAdmissionWaitV1, ControlAdmissionDrainV1, ControlAdmissionForceV1} {
		t.Run(string(mode), func(t *testing.T) {
			dir := policyGuardFixtureV1(t, false)
			operation, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			before := identityGuardTreeV1(t, dir)
			_, err = admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{Mode: mode, Operation: deploy.ControlOperationInstallV1}, controlOperationAdmissionBackendV1{
				newID: func() (string, error) { t.Fatal("created control marker"); return "", nil },
				pause: func(context.Context, time.Duration) error { t.Fatal("started disruption"); return nil },
				await: func(context.Context, string, *deploy.OperationLock, deploy.ControlMarkerV1, bool, io.Writer) (*deploy.OperationLock, error) {
					t.Fatal("reached admission")
					return nil, nil
				},
				removeContainer: func(CommandSpec, RunOptions) error { t.Fatal("removed container"); return nil },
			})
			requirePolicyGuardErrorV1(t, err)
			if !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
				t.Fatal("control mode changed state")
			}
		})
	}
}
