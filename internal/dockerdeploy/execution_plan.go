package dockerdeploy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type DockerPlanContext struct {
	DeploymentDir     string
	InstallTarget     string
	Phase             blueprint.Phase
	Scope             *blueprint.InstallScope
	GeneratedImage    string
	Host              blueprint.HostOS
	UID               int
	GID               int
	SupplementaryGIDs []int
	SystemUser        string
	SystemGroup       string
	PortOverrides     map[string]int
	PortOverrideArgs  []PortOverride
}

type DockerExecutionPlan struct {
	EnvironmentID string
	// DeploymentDir is the host deployment root whose private runtime files
	// must be masked from every container-visible bind-mount alias.
	DeploymentDir string
	Phase         blueprint.Phase
	Scope         *blueprint.InstallScope
	Image         string
	ContainerName string
	NetworkName   string
	Restart       string
	// PrivateEnvironment is a deployment-local runtime property. It controls
	// launcher rendering only and is never persisted in build state or locks.
	PrivateEnvironment bool
	Workload           *WorkloadExecutionPlan
	Mounts             []MountExecutionPlan
	Sandbox            ApplicationSandboxPlanV1
}

const environmentTemporaryHome = "/mnt/reploy-home"

type WorkloadExecutionPlan struct {
	Command   string
	Argv      []string
	Endpoints map[string]EndpointExecutionPlan
}

type EndpointExecutionPlan struct {
	Scheme         string
	ContainerPort  int
	BindAddress    string
	PublishAddress string
	PublishedPort  int
	ProbeHost      string
	Readiness      *blueprint.Readiness
}

type MountExecutionPlan struct {
	Name       string
	Mode       blueprint.MountMode
	Source     string
	SourceKind string
	Target     string
	ReadOnly   bool
	Update     blueprint.UpdatePolicy
}

type RuntimeUserPlan struct {
	User              string
	Group             string
	LocalUser         string
	UID               int
	GID               int
	SupplementaryGIDs []int
	DockerUser        string
	Warnings          []string
}

func PlanDockerExecution(document blueprint.Document, context DockerPlanContext) (DockerExecutionPlan, error) {
	if context.DeploymentDir == "" {
		return DockerExecutionPlan{}, fmt.Errorf("Docker plan deployment directory is required")
	}
	if context.GeneratedImage == "" {
		return DockerExecutionPlan{}, fmt.Errorf("Docker plan generated image is required")
	}
	switch context.Host {
	case blueprint.HostLinux, blueprint.HostMacOS, blueprint.HostWindows:
	case "":
		// Host is optional for staged callers that do not need platform paths.
	default:
		return DockerExecutionPlan{}, fmt.Errorf("unsupported Docker host %q", context.Host)
	}
	if context.Phase == blueprint.PhaseStaged && context.Scope != nil {
		return DockerExecutionPlan{}, fmt.Errorf("staged environments do not have an install scope")
	}
	if context.Phase == blueprint.PhaseInstalled && context.Scope == nil {
		return DockerExecutionPlan{}, fmt.Errorf("installed environments require an install scope")
	}
	identityPath := context.DeploymentDir
	namePrefix := dockerNameSlug(document.Environment.ID, "environment")
	if context.Phase == blueprint.PhaseInstalled {
		if context.InstallTarget == "" {
			return DockerExecutionPlan{}, fmt.Errorf("installed Docker plan requires install target")
		}
		identityPath = context.InstallTarget
	} else {
		namePrefix += "-staging"
	}
	hash, err := pathIdentityHash(identityPath)
	if err != nil {
		return DockerExecutionPlan{}, err
	}
	containerName := namePrefix + "-" + hash
	plan := DockerExecutionPlan{
		EnvironmentID: document.Environment.ID, DeploymentDir: identityPath, Phase: context.Phase, Scope: context.Scope,
		Image: context.GeneratedImage, ContainerName: containerName, NetworkName: containerName,
	}
	if document.Environment.Workload != nil {
		plan.Workload = &WorkloadExecutionPlan{Command: document.Environment.Workload.Command, Endpoints: map[string]EndpointExecutionPlan{}}
		if document.Docker.Workload != nil {
			plan.Restart = document.Docker.Workload.Restart
		}
	}
	plan.Mounts, err = planDockerMounts(document, context)
	if err != nil {
		return DockerExecutionPlan{}, err
	}
	if err := planDockerEndpoints(document, context, &plan); err != nil {
		return DockerExecutionPlan{}, err
	}
	runtimeUser, err := planRuntimeUser(document, context)
	if err != nil {
		return DockerExecutionPlan{}, err
	}
	plan.Sandbox = newApplicationSandboxPlanV1(runtimeUser)
	if err := ValidateApplicationSandboxPlanV1(plan.Sandbox); err != nil {
		return DockerExecutionPlan{}, err
	}
	return plan, nil
}

func sortedMountPlans(plans map[string]MountExecutionPlan) []MountExecutionPlan {
	names := make([]string, 0, len(plans))
	for name := range plans {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]MountExecutionPlan, 0, len(names))
	for _, name := range names {
		result = append(result, plans[name])
	}
	return result
}

func planDockerMounts(document blueprint.Document, context DockerPlanContext) ([]MountExecutionPlan, error) {
	root := context.DeploymentDir
	if context.Phase == blueprint.PhaseInstalled {
		root = context.InstallTarget
	}
	plans := map[string]MountExecutionPlan{}
	rootHash, err := pathIdentityHash(root)
	if err != nil {
		return nil, err
	}
	for name, mount := range document.Docker.Mounts {
		planned := MountExecutionPlan{
			Name: name, Mode: mount.Mode, Target: mount.Contract.Target,
			ReadOnly: !mount.Contract.Writable, Update: mount.Contract.UpdatePolicy,
		}
		switch mount.Mode {
		case blueprint.MountManagedBind:
			planned.SourceKind = deploy.RuntimeMountSourceDirectory
			planned.Source = joinDockerHostPath(context.Host, root, mount.Source)
			if context.Host == blueprint.HostLinux || context.Host == blueprint.HostMacOS {
				cleanRoot := path.Clean(root)
				cleanSource := path.Clean(planned.Source)
				if cleanSource != cleanRoot && cleanRoot != "/" && !strings.HasPrefix(cleanSource, strings.TrimRight(cleanRoot, "/")+"/") {
					return nil, fmt.Errorf("managed bind %q escapes deployment root", name)
				}
			} else if context.Host != blueprint.HostWindows {
				relative, err := filepath.Rel(root, planned.Source)
				if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
					return nil, fmt.Errorf("managed bind %q escapes deployment root", name)
				}
			}
		case blueprint.MountBind:
			planned.Source = mount.Source
			if !filepath.IsAbs(planned.Source) {
				return nil, fmt.Errorf("unmanaged bind %q source must resolve to an absolute host path", name)
			}
			info, err := os.Stat(planned.Source)
			if err != nil {
				return nil, fmt.Errorf("unmanaged bind %q source must already exist: %w", name, err)
			}
			if info.IsDir() {
				planned.SourceKind = deploy.RuntimeMountSourceDirectory
			} else if info.Mode().IsRegular() {
				planned.SourceKind = deploy.RuntimeMountSourceFile
			} else {
				return nil, fmt.Errorf("unmanaged bind %q source must be a regular file or directory", name)
			}
		case blueprint.MountVolume:
			planned.SourceKind = deploy.RuntimeMountSourceGenerated
			planned.Source = dockerNameSlug(document.Environment.ID, "environment") + "-" + rootHash + "-" + dockerNameSlug(mount.Name, "volume")
		case blueprint.MountTmpfs:
			planned.SourceKind = deploy.RuntimeMountSourceGenerated
		default:
			return nil, fmt.Errorf("unsupported Docker mount mode %q", mount.Mode)
		}
		plans[name] = planned
	}
	return sortedMountPlans(plans), nil
}

func joinDockerHostPath(host blueprint.HostOS, root string, relative string) string {
	if host == blueprint.HostWindows {
		root = strings.TrimRight(root, `/\`)
		relative = strings.ReplaceAll(relative, "/", `\`)
		return root + `\` + relative
	}
	if host == blueprint.HostLinux || host == blueprint.HostMacOS {
		return path.Join(root, filepath.ToSlash(relative))
	}
	return filepath.Join(root, filepath.FromSlash(relative))
}

func planDockerEndpoints(document blueprint.Document, context DockerPlanContext, plan *DockerExecutionPlan) error {
	if plan.Workload == nil {
		return nil
	}
	effectiveOverrides := map[string]int{}
	for name, port := range context.PortOverrides {
		effectiveOverrides[name] = port
	}
	if len(context.PortOverrideArgs) > 0 {
		bindings := []dockerPortBinding{}
		names := make([]string, 0, len(document.Docker.Workload.Endpoints))
		for name := range document.Docker.Workload.Endpoints {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			endpoint := document.Docker.Workload.Endpoints[name]
			published := endpoint.Publish.Staging
			if context.Phase == blueprint.PhaseInstalled {
				published = endpoint.Publish.Deployed
			}
			bindings = append(bindings, dockerPortBinding{Name: name, HostPort: strconv.Itoa(published), Named: true})
		}
		resolved, err := applyPortOverrides(bindings, context.PortOverrideArgs)
		if err != nil {
			return err
		}
		for _, binding := range resolved {
			value, _ := strconv.Atoi(binding.HostPort)
			effectiveOverrides[binding.Name] = value
		}
	}
	for name, endpoint := range document.Docker.Workload.Endpoints {
		bindAddress := strings.TrimSpace(endpoint.Bind.Address)
		if bindAddress == "" {
			bindAddress = "0.0.0.0"
		}
		publishAddress := strings.TrimSpace(endpoint.Publish.Address)
		if publishAddress == "" {
			publishAddress = "127.0.0.1"
		}
		published := endpoint.Publish.Staging
		if context.Phase == blueprint.PhaseInstalled {
			published = endpoint.Publish.Deployed
		}
		if override, exists := effectiveOverrides[name]; exists {
			if override < 1 || override > 65535 {
				return fmt.Errorf("port override %q must be between 1 and 65535", name)
			}
			published = override
		}
		plan.Workload.Endpoints[name] = EndpointExecutionPlan{
			Scheme: endpoint.Endpoint.Scheme, ContainerPort: endpoint.Endpoint.Port,
			BindAddress: bindAddress, PublishAddress: publishAddress,
			PublishedPort: published, ProbeHost: normalizeProbeHost(publishAddress),
			Readiness: endpoint.Endpoint.Readiness,
		}
	}
	for name := range effectiveOverrides {
		if _, exists := plan.Workload.Endpoints[name]; !exists {
			return fmt.Errorf("port override references unknown endpoint %q", name)
		}
	}
	return nil
}

func normalizeProbeHost(address string) string {
	address = strings.TrimSpace(address)
	switch address {
	case "", "0.0.0.0", "*":
		return "127.0.0.1"
	case "::", "[::]":
		return "::1"
	default:
		return strings.Trim(address, "[]")
	}
}

func planRuntimeUser(document blueprint.Document, context DockerPlanContext) (RuntimeUserPlan, error) {
	supplementaryGIDs, err := normalizeSupplementaryGIDsV1(context.GID, context.SupplementaryGIDs)
	if err != nil {
		return RuntimeUserPlan{}, err
	}
	if context.Phase == blueprint.PhaseStaged || context.Scope != nil && *context.Scope == blueprint.InstallScopeUser {
		if context.UID < 0 || context.GID < 0 {
			return RuntimeUserPlan{}, fmt.Errorf("current-user Docker plan requires numeric UID and GID")
		}
		plan := RuntimeUserPlan{
			User: strconv.Itoa(context.UID), Group: strconv.Itoa(context.GID), UID: context.UID, GID: context.GID,
			SupplementaryGIDs: supplementaryGIDs,
			DockerUser:        strconv.Itoa(context.UID) + ":" + strconv.Itoa(context.GID),
			LocalUser:         runtimeLocalUserNameV1(document.Environment.Runtime.User, context.UID),
		}
		if context.Phase == blueprint.PhaseInstalled {
			plan.Warnings = append(plan.Warnings,
				fmt.Sprintf("current-user install overrides the image user with local account %q (UID/GID %d:%d)", plan.LocalUser, context.UID, context.GID),
			)
			if context.UID == 0 {
				plan.Warnings = append(plan.Warnings, rootRuntimeIdentityWarningV1)
			} else {
				plan.Warnings = append(plan.Warnings, "the image must tolerate an arbitrary non-root identity and may write persistently only to declared writable paths")
			}
			if document.Environment.Install.System.Account.User != "" || document.Environment.Install.System.Account.Group != "" {
				plan.Warnings = append(plan.Warnings, "environment.install.system.account does not apply to current-user scope")
			}
		} else if context.UID == 0 {
			plan.Warnings = append(plan.Warnings, rootRuntimeIdentityWarningV1)
		}
		return plan, nil
	}
	if context.Scope != nil && *context.Scope == blueprint.InstallScopeSystem {
		if context.Host != blueprint.HostLinux {
			return RuntimeUserPlan{}, fmt.Errorf("%s system Docker installs are not supported", context.Host)
		}
		if context.SystemUser == "" || context.SystemGroup == "" || context.UID < 0 || context.GID < 0 {
			return RuntimeUserPlan{}, fmt.Errorf("system Docker plan requires resolved service account and numeric UID/GID")
		}
		plan := RuntimeUserPlan{
			User: context.SystemUser, Group: context.SystemGroup, UID: context.UID, GID: context.GID,
			SupplementaryGIDs: supplementaryGIDs,
			DockerUser:        strconv.Itoa(context.UID) + ":" + strconv.Itoa(context.GID),
			LocalUser:         runtimeLocalUserNameV1(document.Environment.Runtime.User, context.UID),
		}
		if context.UID == 0 {
			plan.Warnings = append(plan.Warnings, rootRuntimeIdentityWarningV1)
		}
		return plan, nil
	}
	return RuntimeUserPlan{}, fmt.Errorf("cannot resolve Docker runtime user")
}

const rootRuntimeIdentityWarningV1 = "the application will run as root inside its container; root can bypass application-level file permissions, while Docker access, Linux capabilities, host filesystem access, and network access remain limited by the effective Reploy sandbox policy"

func runtimeLocalUserNameV1(configured string, uid int) string {
	if uid == 0 {
		return "root"
	}
	if configured == "" {
		return blueprint.DefaultRuntimeUser
	}
	return configured
}
