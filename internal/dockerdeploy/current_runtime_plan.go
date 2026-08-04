package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

type CurrentRuntimePlanInputV1 struct {
	DeploymentDir string
	Current       CurrentBuild
	Runtime       StagedProviderBuildRuntimeV1
	// AllowConfiguringRepair permits install/uninstall recovery to reconstruct
	// the recorded plan while ordinary runtime operations remain blocked.
	AllowConfiguringRepair bool
}

type CurrentRuntimePlanV1 struct {
	Document blueprint.Document
	Docker   DockerExecutionPlan
}

type currentRuntimePlanBackendV1 struct {
	resolveSystemOwner func(map[string]string) (resolvedInstallOwner, error)
}

// PlanCurrentRuntimeV1 reconstructs the exact container plan represented by a
// current state-v1 generation. It is read-only: system-account lookup is
// allowed, but account creation, provider work, image construction, and Docker
// commands are not.
func PlanCurrentRuntimeV1(input CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
	return planCurrentRuntimeV1(input, currentRuntimePlanBackendV1{resolveSystemOwner: resolveInstallOwner})
}

func planCurrentRuntimeV1(input CurrentRuntimePlanInputV1, backend currentRuntimePlanBackendV1) (CurrentRuntimePlanV1, error) {
	if input.DeploymentDir == "" {
		return CurrentRuntimePlanV1{}, fmt.Errorf("plan current runtime requires a deployment directory")
	}
	dir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return CurrentRuntimePlanV1{}, fmt.Errorf("resolve current runtime deployment directory: %w", err)
	}
	if err := deploy.ValidateStateV1(input.Current.State); err != nil {
		return CurrentRuntimePlanV1{}, fmt.Errorf("plan current runtime state: %w", err)
	}
	if input.Current.State.Current == nil || !reflect.DeepEqual(*input.Current.State.Current, input.Current.Generation) {
		return CurrentRuntimePlanV1{}, fmt.Errorf("plan current runtime state does not name the supplied generation")
	}
	if err := validateGenerationBuildLock(input.Current.Generation, input.Current.Lock, registry.ValidateRequirementProfileV1); err != nil {
		return CurrentRuntimePlanV1{}, fmt.Errorf("plan current runtime build: %w", err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(input.Current.State.Blueprint)
	if err != nil {
		return CurrentRuntimePlanV1{}, fmt.Errorf("plan current runtime blueprint: %w", err)
	}
	context := DockerPlanContext{
		DeploymentDir: dir, Phase: blueprint.PhaseStaged,
		GeneratedImage: input.Current.Generation.Reference,
		Host:           input.Runtime.Host, UID: input.Runtime.UID, GID: input.Runtime.GID,
		SupplementaryGIDs: append([]int(nil), input.Runtime.SupplementaryGIDs...),
	}
	if deployment := input.Current.State.Deployment; deployment != nil {
		installation := deployment.Installation
		if installation.Status != deploy.InstallationStatusReady && !(input.AllowConfiguringRepair && installation.Status == deploy.InstallationStatusConfiguring) {
			return CurrentRuntimePlanV1{}, fmt.Errorf("installed runtime is %s; rerun `reploy install`", installation.Status)
		}
		if installation.TargetDir != dir {
			return CurrentRuntimePlanV1{}, fmt.Errorf("installed runtime target %q does not match deployment %q", installation.TargetDir, dir)
		}
		scope, err := ParseInstallScope(installation.Scope)
		if err != nil {
			return CurrentRuntimePlanV1{}, err
		}
		blueprintScope := blueprint.InstallScope(scope)
		context.Phase = blueprint.PhaseInstalled
		context.Scope = &blueprintScope
		context.InstallTarget = dir
		context.PortOverrides, err = installedRuntimePortOverridesV1(installation)
		if err != nil {
			return CurrentRuntimePlanV1{}, err
		}
		if scope == InstallScopeSystem {
			if backend.resolveSystemOwner == nil {
				return CurrentRuntimePlanV1{}, fmt.Errorf("plan current system runtime requires an account resolver")
			}
			values, err := providerInstallAccountValuesV1(document.Environment.Install.System.Account)
			if err != nil {
				return CurrentRuntimePlanV1{}, err
			}
			owner, err := backend.resolveSystemOwner(values)
			if err != nil {
				return CurrentRuntimePlanV1{}, fmt.Errorf("resolve installed runtime account: %w", err)
			}
			context.SystemUser = document.Environment.Install.System.Account.User
			context.SystemGroup = document.Environment.Install.System.Account.Group
			context.UID = owner.UID
			context.GID = owner.GID
			context.SupplementaryGIDs = append([]int(nil), owner.SupplementaryGIDs...)
		}
	}
	plan, err := PlanDockerExecution(document, context)
	if err != nil {
		return CurrentRuntimePlanV1{}, fmt.Errorf("plan current Docker runtime: %w", err)
	}
	if plan.Workload != nil {
		command, err := resolveLockedEnvironmentCommandForPlanV1(document, input.Current.Lock.Catalog, plan, plan.Workload.Command, nil)
		if err != nil {
			return CurrentRuntimePlanV1{}, fmt.Errorf("plan current workload command: %w", err)
		}
		plan.Workload.Argv = command.Argv
	}
	if input.Current.State.Deployment != nil {
		if err := validateInstalledRuntimePlanV1(input.Current.State.Deployment.Installation, plan); err != nil {
			return CurrentRuntimePlanV1{}, err
		}
	}
	return CurrentRuntimePlanV1{Document: document, Docker: plan}, nil
}

func installedRuntimePortOverridesV1(installation deploy.InstallationStateV1) (map[string]int, error) {
	result := make(map[string]int, len(installation.Ports))
	for _, port := range installation.Ports {
		value, err := strconv.Atoi(port.HostPort)
		if err != nil {
			return nil, fmt.Errorf("installed runtime port %q is invalid: %w", port.Name, err)
		}
		result[port.Name] = value
	}
	return result, nil
}

func validateInstalledRuntimePlanV1(installation deploy.InstallationStateV1, plan DockerExecutionPlan) error {
	if installation.ContainerName != plan.ContainerName || installation.NetworkName != plan.NetworkName || installation.ComposeProject != plan.NetworkName {
		return fmt.Errorf("installed runtime identity does not match the current plan; rerun `reploy install`")
	}
	if !reflect.DeepEqual(installation.Ports, installationPortsForDockerPlanV1(plan)) {
		return fmt.Errorf("installed runtime ports do not match the current plan; rerun `reploy install`")
	}
	return nil
}
