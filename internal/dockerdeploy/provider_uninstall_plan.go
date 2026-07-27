package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type providerUninstallPlanningInputV1 struct {
	Operation     *deploy.OperationLock
	DeploymentDir string
	Runtime       StagedProviderBuildRuntimeV1
	Service       string
	RemoveDir     bool
}

type providerUninstallPlanV1 struct {
	State               deploy.StateV1
	Installation        deploy.InstallationStateV1
	Environment         string
	GenerationReference string
	Backend             installBackend
	RemoveDir           bool
}

func planProviderUninstallV1(input providerUninstallPlanningInputV1) (providerUninstallPlanV1, error) {
	if input.Operation == nil {
		return providerUninstallPlanV1{}, fmt.Errorf("plan provider uninstall requires an operation lock")
	}
	if strings.TrimSpace(input.DeploymentDir) == "" {
		return providerUninstallPlanV1{}, fmt.Errorf("plan provider uninstall requires a deployment directory")
	}
	deploymentDir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return providerUninstallPlanV1{}, fmt.Errorf("resolve provider uninstall deployment directory: %w", err)
	}
	state, found, err := input.Operation.ReadStateV1()
	if err != nil {
		return providerUninstallPlanV1{}, fmt.Errorf("read provider uninstall state: %w", err)
	}
	if !found || state.Deployment == nil || state.Current == nil {
		return providerUninstallPlanV1{}, fmt.Errorf("provider uninstall requires an installed state-v1 deployment: %s", deploymentDir)
	}
	installation := state.Deployment.Installation
	if installation.TargetDir != deploymentDir {
		return providerUninstallPlanV1{}, fmt.Errorf("installed target %q does not match locked deployment %q", installation.TargetDir, deploymentDir)
	}
	if input.Service != "" && input.Service != installation.Service {
		return providerUninstallPlanV1{}, fmt.Errorf("--service-name %q does not match installed service %q", input.Service, installation.Service)
	}
	scope, err := ParseInstallScope(installation.Scope)
	if err != nil {
		return providerUninstallPlanV1{}, fmt.Errorf("installed scope: %w", err)
	}
	platform, err := installHostPlatformV1(input.Runtime.Host)
	if err != nil {
		return providerUninstallPlanV1{}, err
	}
	backend := platform.installBackendForScope(scope)
	if err := validateInstallScopeForBackend(scope, backend, platform); err != nil {
		return providerUninstallPlanV1{}, fmt.Errorf("installed backend is unavailable: %w", err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return providerUninstallPlanV1{}, fmt.Errorf("decode provider uninstall blueprint: %w", err)
	}
	return providerUninstallPlanV1{
		State: state, Installation: installation, Environment: document.Environment.ID,
		GenerationReference: state.Current.Reference, Backend: backend, RemoveDir: input.RemoveDir,
	}, nil
}
