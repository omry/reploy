package dockerdeploy

import (
	"context"
	"fmt"
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
	service := strings.TrimSpace(input.Input.Install.Service)
	if service == "" {
		service = dockerNameSlug(document.Environment.ID, "reploy")
	}
	if !validServiceName(service) {
		return providerInstallationPlanV1{}, fmt.Errorf("--service contains unsupported characters: %s", service)
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
	} else {
		dockerContext.UID = input.Input.Runtime.UID
		dockerContext.GID = input.Input.Runtime.GID
	}
	dockerPlan, err := PlanDockerExecution(document, dockerContext)
	if err != nil {
		return providerInstallationPlanV1{}, fmt.Errorf("plan installed Docker runtime: %w", err)
	}
	if dockerPlan.Workload != nil {
		command, err := resolveLockedEnvironmentCommandForPlanV1(document, input.SourceBuild.Lock.Catalog, dockerPlan, dockerPlan.Workload.Command, nil)
		if err != nil {
			return providerInstallationPlanV1{}, fmt.Errorf("plan installed workload command: %w", err)
		}
		dockerPlan.Workload.Argv = command.Argv
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
	plan := providerInstallationPlanV1{Installation: installation, Docker: dockerPlan, Rendered: rendered, Backend: backend}
	if err := validateProviderInstallationPlanV1(plan, input.References); err != nil {
		return providerInstallationPlanV1{}, err
	}
	return plan, nil
}

func validateProviderInstallationPlanV1(plan providerInstallationPlanV1, references EnvironmentImageReferences) error {
	if err := deploy.ValidateInstallationStateV1(plan.Installation); err != nil {
		return err
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
