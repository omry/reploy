package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func planProviderInstallationV1(ctx context.Context, input providerInstallPlanningV1) (providerInstallationPlanV1, error) {
	if ctx == nil {
		return providerInstallationPlanV1{}, fmt.Errorf("plan provider installation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providerInstallationPlanV1{}, err
	}
	document, err := blueprint.DecodeResolvedDocumentV1(input.SourceBuild.State.Blueprint)
	if err != nil {
		return providerInstallationPlanV1{}, fmt.Errorf("decode source blueprint: %w", err)
	}
	destinationDir, err := filepath.Abs(input.Input.DestinationDeploymentDir)
	if err != nil {
		return providerInstallationPlanV1{}, fmt.Errorf("resolve installation destination: %w", err)
	}
	if err := ValidateEnvironmentImageReferences(input.References, document.Environment.ID, destinationDir); err != nil {
		return providerInstallationPlanV1{}, fmt.Errorf("installation image references: %w", err)
	}
	scope, err := ParseInstallScope(string(input.Input.Install.Scope))
	if err != nil {
		return providerInstallationPlanV1{}, err
	}
	platform, err := installHostPlatformV1(input.Input.Runtime.Host)
	if err != nil {
		return providerInstallationPlanV1{}, err
	}
	backend := platform.installBackendForScope(scope)
	if err := validateInstallScopeForBackend(scope, backend, platform); err != nil {
		return providerInstallationPlanV1{}, err
	}
	service, err := providerInstallServiceV1(document.Environment.ID, input.Input.Install.Service)
	if err != nil {
		return providerInstallationPlanV1{}, err
	}
	instanceID, err := installedInstanceID(service, destinationDir)
	if err != nil {
		return providerInstallationPlanV1{}, err
	}
	dockerScope := blueprint.InstallScope(scope)
	dockerContext := DockerPlanContext{
		DeploymentDir: input.Input.SourceDeploymentDir, InstallTarget: destinationDir,
		Phase: blueprint.PhaseInstalled, Scope: &dockerScope, GeneratedImage: input.References.Generation,
		Host: input.Input.Runtime.Host, PortOverrideArgs: append([]PortOverride(nil), input.Input.Install.PortOverrides...),
	}
	if scope == InstallScopeSystem {
		dockerContext.SystemUser = input.Input.Install.SystemUser
		dockerContext.SystemGroup = input.Input.Install.SystemGroup
		dockerContext.UID = input.Input.Install.SystemUID
		dockerContext.GID = input.Input.Install.SystemGID
		dockerContext.SupplementaryGIDs = append([]int(nil), input.Input.Install.SystemSupplementaryGIDs...)
	} else {
		dockerContext.UID = input.Input.Runtime.UID
		dockerContext.GID = input.Input.Runtime.GID
		dockerContext.SupplementaryGIDs = append([]int(nil), input.Input.Runtime.SupplementaryGIDs...)
	}
	dockerPlan, err := PlanDockerExecution(document, dockerContext)
	if err != nil {
		return providerInstallationPlanV1{}, fmt.Errorf("plan installed Docker runtime: %w", err)
	}
	pathUpdates, preservePaths, err := planEnvironmentInstallPathUpdates(
		document,
		input.Input.SourceDeploymentDir,
		destinationDir,
		scope,
		input.Input.Install.Replace,
		input.Input.Install.Clean,
		platform.GOOS,
	)
	if err != nil {
		return providerInstallationPlanV1{}, fmt.Errorf("plan installed path updates: %w", err)
	}
	for _, action := range pathUpdates {
		if action.Kind != PathPreservePrivateEnv && action.Kind != PathReplacePrivateEnv {
			continue
		}
		environmentDir := filepath.Dir(action.Source)
		if action.Kind == PathPreservePrivateEnv {
			if _, statErr := os.Lstat(action.Target); statErr == nil {
				environmentDir = filepath.Dir(action.Target)
			} else if !os.IsNotExist(statErr) {
				return providerInstallationPlanV1{}, fmt.Errorf("inspect installed %s: %w", PrivateWorkloadEnvironmentFileName, statErr)
			}
		}
		environment, loadErr := loadPrivateWorkloadEnvironmentV1(environmentDir)
		if loadErr != nil {
			return providerInstallationPlanV1{}, fmt.Errorf("read installed %s plan input: %w", PrivateWorkloadEnvironmentFileName, loadErr)
		}
		if !environment.Exists {
			return providerInstallationPlanV1{}, fmt.Errorf("installed %s plan input disappeared", PrivateWorkloadEnvironmentFileName)
		}
		if !environment.Present {
			break
		}
		if err := validatePrivateWorkloadEnvironmentIsolationV1ForPlan(destinationDir, dockerPlan); err != nil {
			return providerInstallationPlanV1{}, err
		}
		dockerPlan.PrivateEnvironment = true
		break
	}
	if dockerPlan.Workload != nil {
		command, err := resolveLockedEnvironmentCommandForPlanV1(document, input.SourceBuild.Lock.Catalog, dockerPlan, dockerPlan.Workload.Command, nil)
		if err != nil {
			return providerInstallationPlanV1{}, fmt.Errorf("plan installed workload command: %w", err)
		}
		dockerPlan.Workload.Argv = command.Argv
	}
	afterInstall, err := planLockedAfterInstallLifecycleV1(document, dockerPlan, input.SourceBuild.Lock.Catalog)
	if err != nil {
		return providerInstallationPlanV1{}, fmt.Errorf("plan installed after_install lifecycle: %w", err)
	}
	start := LifecyclePlan{}
	if input.Input.Install.Start {
		start, err = planLockedStartLifecycleV1(document, dockerPlan, input.SourceBuild.Lock.Catalog)
		if err != nil {
			return providerInstallationPlanV1{}, fmt.Errorf("plan installed start lifecycle: %w", err)
		}
	}
	rendered, err := RenderDockerInputs(dockerPlan, document.Environment.ControlScript)
	if err != nil {
		return providerInstallationPlanV1{}, fmt.Errorf("render installed Docker runtime: %w", err)
	}
	unitPath := ""
	if backend == installBackendLinuxSystemd {
		unitPath = systemdPath(installSystemdUnitDir, service+".service")
	}
	installation := deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
		TargetDir: destinationDir, Scope: string(scope),
		Service: service, UnitPath: unitPath, InstanceID: instanceID,
		ComposeProject: dockerPlan.NetworkName, ContainerName: dockerPlan.ContainerName, NetworkName: dockerPlan.NetworkName,
		Ports: installationPortsForDockerPlanV1(dockerPlan),
	}
	plan := providerInstallationPlanV1{
		Installation: installation, ControlScript: document.Environment.ControlScript,
		Docker: dockerPlan, Rendered: rendered,
		PathUpdates: pathUpdates, PreservePaths: preservePaths,
		AfterInstall: afterInstall, Start: start, Backend: backend,
	}
	if err := validateProviderInstallationPlanV1(plan, input.References); err != nil {
		return providerInstallationPlanV1{}, err
	}
	return plan, nil
}

func providerInstallServiceV1(environmentID string, requested string) (string, error) {
	service := strings.TrimSpace(requested)
	if service == "" {
		service = dockerNameSlug(environmentID, "reploy")
	}
	if !validServiceName(service) {
		return "", fmt.Errorf("--service contains unsupported characters: %s", service)
	}
	return service, nil
}

func validateProviderInstallationPlanV1(plan providerInstallationPlanV1, references EnvironmentImageReferences) error {
	if err := deploy.ValidateInstallationStateV1(plan.Installation); err != nil {
		return err
	}
	if strings.TrimSpace(plan.ControlScript) == "" {
		return fmt.Errorf("provider installation control script is required")
	}
	if plan.Docker.Phase != blueprint.PhaseInstalled || plan.Docker.Scope == nil {
		return fmt.Errorf("provider installation Docker plan must be installed and scoped")
	}
	if plan.Docker.Image != references.Generation {
		return fmt.Errorf("provider installation Docker plan must use the selected destination generation reference")
	}
	if plan.Installation.ContainerName != plan.Docker.ContainerName || plan.Installation.NetworkName != plan.Docker.NetworkName || plan.Installation.ComposeProject != plan.Docker.NetworkName {
		return fmt.Errorf("provider installation resource identities do not match the Docker plan")
	}
	if len(plan.Rendered.Compose) == 0 || len(plan.Rendered.Environment) == 0 {
		return fmt.Errorf("provider installation rendered Docker inputs are incomplete")
	}
	if plan.Rendered.Environment["REPLOY_IMAGE"] != references.Generation {
		return fmt.Errorf("provider installation rendered image does not match the selected destination generation reference")
	}
	return nil
}

func installationPortsForDockerPlanV1(plan DockerExecutionPlan) []deploy.InstallationPortBindingV1 {
	ports := []deploy.InstallationPortBindingV1{}
	if plan.Workload == nil {
		return ports
	}
	names := make([]string, 0, len(plan.Workload.Endpoints))
	for name := range plan.Workload.Endpoints {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		endpoint := plan.Workload.Endpoints[name]
		ports = append(ports, deploy.InstallationPortBindingV1{
			Name: name, HostBind: endpoint.PublishAddress,
			HostPort: strconv.Itoa(endpoint.PublishedPort), ContainerPort: strconv.Itoa(endpoint.ContainerPort),
		})
	}
	return ports
}

func installHostPlatformV1(host blueprint.HostOS) (hostPlatform, error) {
	switch host {
	case blueprint.HostLinux:
		return hostPlatform{GOOS: "linux"}, nil
	case blueprint.HostMacOS:
		return hostPlatform{GOOS: "darwin"}, nil
	case blueprint.HostWindows:
		return hostPlatform{GOOS: "windows"}, nil
	default:
		return hostPlatform{}, fmt.Errorf("provider installation requires a supported host OS")
	}
}
