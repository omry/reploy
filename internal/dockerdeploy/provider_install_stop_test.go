package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestStopProviderInstallDestinationV1UsesExistingSystemdOwnerAndGeneration(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	current, _ := runtimeCurrentBuildFixture(t)
	state := current.State
	state.Deployment = &deploy.DeploymentStateV1{Installation: deploy.InstallationStateV1{
		Service: "demo", UnitPath: "/etc/systemd/system/demo.service",
	}}
	locked := lockedProviderInstallV1{
		DestinationOperation: operation,
		DestinationStore:     providerstore.Store{},
		HostTools:            providerInstallHostToolsV1{SystemctlPath: "/usr/bin/systemctl"},
		Input: providerInstallRunInputV1{
			DestinationDeploymentDir: dir,
			Runtime:                  StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
		},
	}
	run := false
	err = stopProviderInstallDestinationWithV1(t.Context(), locked, state, providerInstallStopBackendV1{
		load: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
			return current, true, nil
		},
		plan: func(input CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			if !input.AllowConfiguringRepair || input.Current.Generation.Reference != current.Generation.Reference {
				t.Fatalf("stop runtime plan input = %#v", input)
			}
			return CurrentRuntimePlanV1{Document: blueprint.Document{Environment: blueprint.Environment{Workload: &blueprint.Workload{}}}, Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}}}, nil
		},
		inspect: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
			t.Fatal("existing systemd tools were inspected again")
			return providerInstallHostToolsV1{}, nil
		},
		run: func(_ context.Context, input CurrentWorkloadLifecycleInputV1) error {
			run = true
			if input.Action != "down" || input.Current.Generation.Reference != current.Generation.Reference || input.StopCommand == nil || input.StopCommand.Name != "/usr/bin/systemctl" || len(input.StopCommand.Args) != 2 || input.StopCommand.Args[0] != "stop" || input.StopCommand.Args[1] != "demo.service" {
				t.Fatalf("stop lifecycle input = %#v", input)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !run {
		t.Fatal("stop lifecycle was not run")
	}
}

func TestStopProviderInstallDestinationV1UsesComposeForManagedInstall(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	current, _ := runtimeCurrentBuildFixture(t)
	state := current.State
	state.Deployment = &deploy.DeploymentStateV1{Installation: deploy.InstallationStateV1{Service: "demo"}}
	err = stopProviderInstallDestinationWithV1(t.Context(), lockedProviderInstallV1{
		DestinationOperation: operation, Input: providerInstallRunInputV1{DestinationDeploymentDir: dir, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}},
	}, state, providerInstallStopBackendV1{
		load: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
			return current, true, nil
		},
		plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			return CurrentRuntimePlanV1{Document: blueprint.Document{Environment: blueprint.Environment{Workload: &blueprint.Workload{}}}, Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}}}, nil
		},
		inspect: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
			t.Fatal("managed install inspected systemd")
			return providerInstallHostToolsV1{}, nil
		},
		run: func(_ context.Context, input CurrentWorkloadLifecycleInputV1) error {
			if input.StopCommand != nil {
				t.Fatalf("managed stop command = %#v", input.StopCommand)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStopProviderInstallCommandOnlyDestinationValidatesBeforeSkipping(t *testing.T) {
	for _, scenario := range []string{"command-only", "missing-lock", "released-lock", "cancelled", "missing-current", "malformed-blueprint", "load-error", "missing-build", "changed-generation", "plan-error", "cancel-during-plan", "document-workload-only", "docker-workload-only"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dir := t.TempDir()
			operation, err := deploy.AcquireOperationLock(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = operation.Unlock() })
			current, _ := runtimeCurrentBuildFixture(t)
			state := current.State
			state.Deployment = &deploy.DeploymentStateV1{Installation: deploy.InstallationStateV1{Service: "demo", UnitPath: "/old/demo.service"}}
			locked := lockedProviderInstallV1{DestinationOperation: operation, Input: providerInstallRunInputV1{DestinationDeploymentDir: dir}}
			sentinel := errors.New("validation refused")
			loaded, planned, ran := false, false, false
			backend := providerInstallStopBackendV1{
				load: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
					loaded = true
					if scenario == "load-error" {
						return CurrentBuild{}, false, sentinel
					}
					if scenario == "missing-build" {
						return current, false, nil
					}
					if scenario == "changed-generation" {
						changed := current
						changed.Generation.Reference += "-changed"
						return changed, true, nil
					}
					return current, true, nil
				},
				plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
					planned = true
					if scenario == "plan-error" {
						return CurrentRuntimePlanV1{}, sentinel
					}
					if scenario == "cancel-during-plan" {
						cancel()
					}
					result := CurrentRuntimePlanV1{}
					if scenario == "document-workload-only" {
						result.Document.Environment.Workload = &blueprint.Workload{}
					}
					if scenario == "docker-workload-only" {
						result.Docker.Workload = &WorkloadExecutionPlan{}
					}
					return result, nil
				},
				inspect: func(context.Context, installBackend) (providerInstallHostToolsV1, error) {
					if !strings.HasSuffix(scenario, "workload-only") {
						t.Fatal("command-only update accessed systemd")
					}
					return providerInstallHostToolsV1{SystemctlPath: "/systemctl"}, nil
				},
				run: func(context.Context, CurrentWorkloadLifecycleInputV1) error {
					ran = true
					if !strings.HasSuffix(scenario, "workload-only") {
						t.Fatal("command-only update dispatched workload lifecycle")
					}
					return sentinel
				},
			}
			switch scenario {
			case "missing-lock":
				locked.DestinationOperation = nil
			case "released-lock":
				if err := operation.Unlock(); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "missing-current":
				state.Current = nil
			case "malformed-blueprint":
				state.Blueprint = blueprint.ResolvedDocumentV1(`{"invalid":true}`)
			}
			err = stopProviderInstallDestinationWithV1(ctx, locked, state, backend)
			if scenario == "command-only" {
				if err != nil || !loaded || !planned || ran {
					t.Fatalf("error=%v loaded=%v planned=%v ran=%v", err, loaded, planned, ran)
				}
			} else if err == nil {
				t.Fatal("invalid or workload destination accepted as command-only")
			}
			if strings.HasSuffix(scenario, "workload-only") && (!ran || !errors.Is(err, sentinel)) {
				t.Fatalf("mismatch bypassed workload validation: ran=%v error=%v", ran, err)
			}
		})
	}
}
