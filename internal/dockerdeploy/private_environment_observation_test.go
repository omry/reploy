package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestPrivateEnvironmentObservationPreservesRuntimeSelection(t *testing.T) {
	for _, scope := range []string{"staging", "user", "system"} {
		for _, action := range []string{"status", "ps", "logs"} {
			for _, state := range []string{"private", "empty", "missing"} {
				t.Run(scope+"/"+action+"/"+state, func(t *testing.T) {
					dir := t.TempDir()
					current, _ := runtimeCurrentBuildFixture(t)
					if scope != "staging" {
						current.State.Deployment = &deploy.DeploymentStateV1{
							Installation: deploy.InstallationStateV1{Scope: scope},
						}
					}
					var content []byte
					if state == "private" {
						content = []byte("REPRO_MARKER=synthetic\n")
					}
					if state != "missing" {
						if _, err := publishPrivateWorkloadEnvironmentFileV1(filepath.Join(dir, ".env"), content, false); err != nil {
							t.Fatal(err)
						}
					}
					order := []string{}
					backend := currentRuntimeObservationTestBackend(t, dir, current, CurrentRuntimePlanV1{
						Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}},
					}, &order)
					original := backend.requireInputs
					backend.requireInputs = func(operation *deploy.OperationLock, got string, plan CurrentRuntimePlanV1) error {
						if plan.Docker.PrivateEnvironment != (state == "private") {
							t.Fatalf("observation selected private environment = %t", plan.Docker.PrivateEnvironment)
						}
						return original(operation, got, plan)
					}
					if err := runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{
						DeploymentDir: dir, Action: action,
					}, backend); err != nil {
						t.Fatal(err)
					}
					got, err := os.ReadFile(filepath.Join(dir, ".env"))
					if state == "missing" {
						if !os.IsNotExist(err) {
							t.Fatal("observation created a missing private environment file")
						}
					} else if err != nil || string(got) != string(content) {
						t.Fatal("observation changed the private environment file")
					}
				})
			}
		}
	}
}

func TestPrivateEnvironmentObservationPinsPublishedModeToConfirmedAttempt(t *testing.T) {
	for _, test := range []struct {
		name            string
		currentPrivate  bool
		handoffComplete bool
		transferClaimed bool
	}{
		{name: "private process after file removal", currentPrivate: false, transferClaimed: true, handoffComplete: true},
		{name: "no-secret process after file creation", currentPrivate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			current, _ := runtimeCurrentBuildFixture(t)
			if test.currentPrivate {
				if _, err := publishPrivateWorkloadEnvironmentFileV1(filepath.Join(dir, ".env"), []byte("TOKEN=synthetic\n"), false); err != nil {
					t.Fatal(err)
				}
			}
			order := []string{}
			backend := currentRuntimeObservationTestBackend(t, dir, current, CurrentRuntimePlanV1{
				Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}},
			}, &order)
			backend.readLifecycle = func(*deploy.OperationLock) (deploy.WorkloadLifecycleV1, bool, error) {
				return deploy.WorkloadLifecycleV1{
					State: "up", ContainerID: strings.Repeat("a", 64), StartedAt: "2026-10-22T00:00:00Z",
					StartupComplete: true, TransferClaimed: test.transferClaimed, HandoffComplete: test.handoffComplete,
				}, true, nil
			}
			backend.requireInputs = func(_ *deploy.OperationLock, _ string, plan CurrentRuntimePlanV1) error {
				if plan.Docker.PrivateEnvironment != test.handoffComplete {
					t.Fatalf("observation used current file presence=%t, want confirmed mode=%t", plan.Docker.PrivateEnvironment, test.handoffComplete)
				}
				return nil
			}
			if err := runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: "status"}, backend); err != nil {
				t.Fatal(err)
			}
		})
	}
}
