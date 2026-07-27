package dockerdeploy

import (
	"context"
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
			return CurrentRuntimePlanV1{}, nil
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
		plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) { return CurrentRuntimePlanV1{}, nil },
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
