package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type providerInstallStopBackendV1 struct {
	load    currentBuildLoader
	plan    func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
	inspect func(context.Context, installBackend) (providerInstallHostToolsV1, error)
	run     func(context.Context, CurrentWorkloadLifecycleInputV1) error
}

func stopProviderInstallDestinationV1(ctx context.Context, locked lockedProviderInstallV1, state deploy.StateV1) error {
	return stopProviderInstallDestinationWithV1(ctx, locked, state, providerInstallStopBackendV1{
		load: ValidateCurrentBuild, plan: PlanCurrentRuntimeV1,
		inspect: inspectProviderInstallHostToolsV1, run: RunCurrentWorkloadLifecycleV1,
	})
}

func stopProviderInstallDestinationWithV1(ctx context.Context, locked lockedProviderInstallV1, state deploy.StateV1, backend providerInstallStopBackendV1) error {
	if ctx == nil {
		return fmt.Errorf("stop provider install destination requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if locked.DestinationOperation == nil {
		return fmt.Errorf("stop provider install destination requires the destination operation lock")
	}
	if backend.load == nil || backend.plan == nil || backend.inspect == nil || backend.run == nil {
		return fmt.Errorf("stop provider install destination requires a complete backend")
	}
	if state.Deployment == nil || state.Current == nil {
		return fmt.Errorf("installed destination has no current generation")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return fmt.Errorf("decode installed destination blueprint: %w", err)
	}
	current, found, err := backend.load(ctx, locked.DestinationOperation, locked.DestinationStore, document.Environment.ID, locked.Input.DestinationDeploymentDir)
	if err != nil {
		return fmt.Errorf("validate installed destination build: %w", err)
	}
	if !found || current.Generation.Reference != state.Current.Reference {
		return fmt.Errorf("installed destination current build is missing or changed")
	}
	planned, err := backend.plan(CurrentRuntimePlanInputV1{
		DeploymentDir:          locked.Input.DestinationDeploymentDir,
		Current:                current,
		Runtime:                locked.Input.Runtime,
		AllowConfiguringRepair: true,
	})
	if err != nil {
		return err
	}
	var stopCommand *CommandSpec
	installation := state.Deployment.Installation
	if installation.UnitPath != "" {
		tools := locked.HostTools
		if tools.SystemctlPath == "" {
			tools, err = backend.inspect(ctx, installBackendLinuxSystemd)
			if err != nil {
				return fmt.Errorf("inspect existing systemd host tools: %w", err)
			}
		}
		spec := CommandSpec{Name: tools.SystemctlPath, Args: []string{"stop", installation.Service + ".service"}}
		stopCommand = &spec
	}
	return backend.run(ctx, CurrentWorkloadLifecycleInputV1{
		Operation: locked.DestinationOperation, Store: locked.DestinationStore,
		Current: current, Plan: planned, Environment: document.Environment.ID,
		DeploymentDir: locked.Input.DestinationDeploymentDir,
		Action:        "down", StopCommand: stopCommand, RunOptions: locked.Input.RunOptions,
	})
}
