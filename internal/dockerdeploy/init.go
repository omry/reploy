package dockerdeploy

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/python"
)

//go:embed templates/compose.yaml
var composeTemplate string

const (
	ReployInternalDir       = ".reploy"
	DockerEnvFileName       = ".reploy/docker.env"
	BundleDirName           = ".reploy/bundle"
	RuntimeDirName          = ".reploy/runtime"
	ComposeFileName         = RuntimeDirName + "/compose.yaml"
	ComposeOverrideFileName = ".reploy/compose.override.yaml"
	ManifestFileName        = ".reploy/manifest.json"
	StateFileName           = ".reploy/state.json"
	RequirementsFileName    = ".reploy/requirements.txt"
	ToolBinaryFileName      = ".reploy/bin/reploy"
	DefaultDeploymentDir    = "reploy-staging"

	reployDeploymentScopeEnv      = "REPLOY_DEPLOYMENT_SCOPE"
	reployDeploymentScopeStaging  = "staging"
	reployDeploymentScopeDeployed = "deployed"
	reployInstallOwnerEnv         = "REPLOY_INSTALL_OWNER"
	reployInstallOwnerOnMissing   = "REPLOY_INSTALL_OWNER_ON_MISSING"
)

type InitOptions struct {
	Dir          string
	Pack         deploy.PackRef
	Requirements []string
}

type ExistingDeploymentFileError struct {
	Path string
}

func (err ExistingDeploymentFileError) Error() string {
	return fmt.Sprintf("refusing to overwrite existing deployment file: %s", err.Path)
}

func Init(options InitOptions) ([]UpdateResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if options.Pack.Raw == "" {
		return nil, fmt.Errorf("blueprint reference is required")
	}

	pack, err := deploy.LoadPack(options.Pack)
	if err != nil {
		return nil, err
	}

	initPaths := []string{
		controlScriptName(pack.AppID),
		DockerEnvFileName,
		RequirementsFileName,
		ManifestFileName,
		StateFileName,
	}
	for _, relativePath := range initPaths {
		path := filepath.Join(options.Dir, relativePath)
		if _, err := os.Stat(path); err == nil {
			return nil, ExistingDeploymentFileError{Path: path}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	bundleRoots, err := initBundleRoots(pack, options.Requirements)
	if err != nil {
		return nil, err
	}
	requirements, err := runtimeRequirementsContent(pack, bundleRoots)
	if err != nil {
		return nil, err
	}
	stagingID, err := stagingInstanceID(pack, options.Dir)
	if err != nil {
		return nil, err
	}
	deployedCommands := pack.Docker.DeployedCommands()
	if err := validateDeployedControlCommands(deployedCommands); err != nil {
		return nil, err
	}
	manifest := deploy.NewDeploymentManifest("reploy stage")
	results := []UpdateResult{}
	writeGenerated := func(relativePath string, content []byte, executable bool) error {
		if err := deploy.WriteGeneratedFile(options.Dir, relativePath, content, executable, &manifest); err != nil {
			return err
		}
		results = append(results, UpdateResult{Path: filepath.Join(options.Dir, relativePath), Status: deploy.UpdateStatusUpdated, Ownership: "generated", Reason: "created from blueprint and reploy templates"})
		return nil
	}
	writeLocal := func(relativePath string, content []byte, mode os.FileMode) error {
		path := filepath.Join(options.Dir, relativePath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			return err
		}
		results = append(results, UpdateResult{Path: path, Status: deploy.UpdateStatusUpdated, Ownership: "local", Reason: "created operator-owned deployment state"})
		return nil
	}

	if err := writeGenerated(controlScriptName(pack.AppID), []byte(stagingControlScriptContent(pack, deployedCommands)), true); err != nil {
		return nil, err
	}
	dockerEnv, err := defaultDockerEnv(pack, stagingID)
	if err != nil {
		return nil, err
	}
	if err := writeLocal(DockerEnvFileName, []byte(dockerEnv), 0o644); err != nil {
		return nil, err
	}
	if err := writeLocal(RequirementsFileName, ensureTrailingNewline(requirements), 0o644); err != nil {
		return nil, err
	}
	runtimeState, err := writeEmbeddedRuntime(options.Dir, &manifest)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(options.Dir, runtimeState.Path), Status: deploy.UpdateStatusUpdated, Ownership: "runtime", Reason: "embedded Reploy runtime for generated control scripts"})
	if err := writeState(options.Dir, pack, deploy.BundleState{Roots: bundleRoots}, &runtimeState); err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(options.Dir, StateFileName), Status: deploy.UpdateStatusUpdated, Ownership: "state", Reason: "recorded resolved deployment state"})
	if err := deploy.WriteDeploymentManifest(filepath.Join(options.Dir, ManifestFileName), manifest); err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(options.Dir, ManifestFileName), Status: deploy.UpdateStatusUpdated, Ownership: "state", Reason: "recorded generated file hashes"})

	initResults := []UpdateResult{}
	if err := ensureInstallDirs(options.Dir, pack.Docker.DeploymentDirs, pack.Install.ManagedPaths, &initResults); err != nil {
		return nil, err
	}
	results = append(results, initResults...)
	composeResult, err := writeRuntimeCompose(options.Dir, pack, bundleRoots, stagingID)
	if err != nil {
		return nil, err
	}
	results = append(results, composeResult)

	return results, nil
}

func writeState(dir string, pack deploy.AppPack, bundle deploy.BundleState, runtimeState *deploy.RuntimeState) error {
	content, err := stateContent(pack, bundle, runtimeState)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, StateFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func writeStateIfChanged(dir string, pack deploy.AppPack, bundle deploy.BundleState) (deploy.UpdateStatus, error) {
	runtimeState, err := embeddedRuntimeStateForDir(dir)
	if err != nil {
		return "", err
	}
	content, err := stateContent(pack, bundle, runtimeState)
	if err != nil {
		return "", err
	}
	return deploy.WriteFileIfChanged(filepath.Join(dir, StateFileName), content, 0o644)
}

func writeUpdatedStateIfChanged(dir string, pack deploy.AppPack, bundle deploy.BundleState, existing deploy.DeploymentState) (deploy.UpdateStatus, error) {
	content, err := updatedStateContent(pack, bundle, existing)
	if err != nil {
		return "", err
	}
	return deploy.WriteFileIfChanged(filepath.Join(dir, StateFileName), content, 0o644)
}

func stateContent(pack deploy.AppPack, bundle deploy.BundleState, runtimeState *deploy.RuntimeState) ([]byte, error) {
	state := deploy.DeploymentState{
		SchemaVersion:         1,
		ToolVersion:           deploy.ToolVersion,
		Target:                "docker",
		Phase:                 deploy.PhaseStaged,
		AppID:                 pack.AppID,
		Blueprint:             pack.Ref,
		RequestedBlueprintRef: pack.RequestedRef.Raw,
		ResolvedArtifact:      pack.ResolvedArtifact,
		Runtime:               runtimeState,
		Bundle:                bundle,
	}
	return marshalState(state)
}

func updatedStateContent(pack deploy.AppPack, bundle deploy.BundleState, existing deploy.DeploymentState) ([]byte, error) {
	state := deploy.DeploymentState{
		SchemaVersion:         1,
		ToolVersion:           deploy.ToolVersion,
		Target:                "docker",
		Phase:                 deploy.PhaseStaged,
		AppID:                 pack.AppID,
		Blueprint:             pack.Ref,
		RequestedBlueprintRef: pack.RequestedRef.Raw,
		ResolvedArtifact:      pack.ResolvedArtifact,
		Runtime:               existing.Runtime,
		Bundle:                bundle,
	}
	if existing.Phase != "" {
		state.Phase = existing.Phase
	}
	state.Install = existing.Install
	return marshalState(state)
}

func marshalState(state deploy.DeploymentState) ([]byte, error) {
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func initBundleRoots(pack deploy.AppPack, explicitRequirements []string) ([]deploy.ArtifactRoot, error) {
	requirements, err := initRequirements(pack, explicitRequirements)
	if err != nil {
		return nil, err
	}
	return bundleRootsFromRequirements(requirements, len(explicitRequirements) > 0)
}

func initRequirements(pack deploy.AppPack, explicitRequirements []string) ([]byte, error) {
	if len(explicitRequirements) > 0 {
		for _, requirement := range explicitRequirements {
			if err := validateExplicitRequirement(requirement); err != nil {
				return nil, err
			}
		}
		return []byte(strings.Join(explicitRequirements, "\n") + "\n"), nil
	}
	requirement, ok, err := resolvedPackRequirement(pack)
	if err != nil {
		return nil, err
	}
	if ok {
		return []byte(requirement + "\n"), nil
	}
	identifier := strings.TrimSpace(pack.App.Provider.Identifier)
	if identifier == "" {
		return nil, fmt.Errorf("blueprint has no app.provider.identifier and no resolved package artifact")
	}
	if pack.App.Provider.Type != python.ProviderName {
		return nil, fmt.Errorf("provider %q cannot be projected into Python requirements", pack.App.Provider.Type)
	}
	if _, err := classifyBundleRoot(identifier); err != nil {
		return nil, fmt.Errorf("app.provider.identifier is invalid: %w", err)
	}
	return []byte(identifier + "\n"), nil
}

func resolvedPackRequirement(pack deploy.AppPack) (string, bool, error) {
	if pack.Ref.Scheme != "pypi" || pack.ResolvedArtifact == nil {
		return "", false, nil
	}
	if pack.ResolvedArtifact.Package == "" || pack.ResolvedArtifact.Version == "" {
		return "", false, nil
	}
	requirement := pack.ResolvedArtifact.Package + "==" + pack.ResolvedArtifact.Version
	if err := validateExplicitRequirement(requirement); err != nil {
		return "", false, fmt.Errorf("resolved blueprint requirement is invalid: %w", err)
	}
	return requirement, true, nil
}

func validateExplicitRequirement(requirement string) error {
	return python.ValidateExplicitRequirement(requirement)
}

func ensureTrailingNewline(content []byte) []byte {
	if len(content) == 0 || content[len(content)-1] == '\n' {
		return content
	}
	return append(content, '\n')
}

func defaultDockerEnv(pack deploy.AppPack, dockerIdentity string) (string, error) {
	dirs := pack.Docker.DeploymentDirs
	service := dockerServiceDefaults(pack, dockerIdentity)
	ports, err := stagingPortBindings(pack)
	if err != nil {
		return "", err
	}
	applyPrimaryPortDefaults(&service, ports)
	primaryPort := ports[0]
	lines := []string{
		"# Docker Compose settings for the Reploy deployment.",
		"# These values control the container wrapper, not app runtime config.",
		"",
		fmt.Sprintf("REPLOY_IMAGE=%s", service.Image),
		fmt.Sprintf("REPLOY_CONTAINER_NAME=%s", service.ContainerName),
		fmt.Sprintf("%s=%s", reployDeploymentScopeEnv, reployDeploymentScopeStaging),
		fmt.Sprintf("REPLOY_CONTAINER_USER=%s", service.ContainerUser),
		fmt.Sprintf("REPLOY_RESTART=%s", service.Restart),
		fmt.Sprintf("REPLOY_CONFIG_DIR=./%s", dirs.Config),
		"REPLOY_REQUIREMENTS_FILE=./" + RequirementsFileName,
		fmt.Sprintf("REPLOY_BUNDLE_DIR=./%s", dirs.Bundle),
		"REPLOY_RUNTIME_DIR=" + dockerRuntimeVolumeName(dockerIdentity),
		fmt.Sprintf("REPLOY_DATA_DIR=./%s", dirs.Data),
		fmt.Sprintf("REPLOY_CONTAINER_HOST=%s", service.ContainerHost),
		fmt.Sprintf("REPLOY_HOST_BIND=%s", primaryPort.HostBind),
		fmt.Sprintf("REPLOY_HOST_PORT=%s", primaryPort.HostPort),
		fmt.Sprintf("REPLOY_CONTAINER_PORT=%s", primaryPort.ContainerPort),
		fmt.Sprintf("REPLOY_PUBLIC_SCHEME=%s", service.PublicScheme),
		fmt.Sprintf("REPLOY_PUBLIC_BASE_URL=%s", service.PublicBaseURL),
		fmt.Sprintf("REPLOY_DOCKER_NETWORK_NAME=%s", service.NetworkName),
		fmt.Sprintf("REPLOY_RUNTIME_ROOT=%s", service.RuntimeRoot),
		fmt.Sprintf("REPLOY_CONTAINER_HOME=%s", service.ContainerHome),
		"REPLOY_PIP_VERBOSE=",
	}
	if service.InstallOwner != "" {
		lines = append(lines,
			fmt.Sprintf("%s=%s", reployInstallOwnerEnv, service.InstallOwner),
			fmt.Sprintf("%s=%s", reployInstallOwnerOnMissing, pack.Install.System.RunAs.OnMissing),
		)
	}
	if hasNamedPortBindings(ports) {
		lines = append(lines, "", "# Named Docker port bindings declared by the blueprint.")
		for _, port := range ports {
			hostBindEnv, hostPortEnv, containerPortEnv := portEnvNames(port)
			lines = append(lines,
				fmt.Sprintf("%s=%s", hostBindEnv, port.HostBind),
				fmt.Sprintf("%s=%s", hostPortEnv, port.HostPort),
				fmt.Sprintf("%s=%s", containerPortEnv, port.ContainerPort),
			)
		}
	}
	if len(pack.Docker.Environment) > 0 {
		lines = append(lines, "", "# App environment defaults declared by the blueprint.")
		names := make([]string, 0, len(pack.Docker.Environment))
		for name := range pack.Docker.Environment {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			lines = append(lines, name+"="+pack.Docker.Environment[name])
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func defaultContainerUser() string {
	uid := os.Getuid()
	gid := os.Getgid()
	if uid < 0 || gid < 0 {
		return "10001:10001"
	}
	return fmt.Sprintf("%d:%d", uid, gid)
}

func renderComposeTemplate(pack deploy.AppPack, roots []deploy.ArtifactRoot, dockerIdentity string) (string, error) {
	containerCommands, err := shellContainerCommands(pack.Docker.Commands, pack.Docker.DefaultCommand)
	if err != nil {
		return "", err
	}
	defaultCommandName, err := containerCommandFunctionName(pack.Docker.DefaultCommand)
	if err != nil {
		return "", err
	}
	configCheckFunction, err := configCheckCommandFunction(pack.Docker.Commands)
	if err != nil {
		return "", err
	}
	sourceVolumes, err := localSourceComposeVolumes(pack, roots)
	if err != nil {
		return "", err
	}
	service := dockerServiceDefaults(pack, dockerIdentity)
	ports, err := stagingPortBindings(pack)
	if err != nil {
		return "", err
	}
	applyPrimaryPortDefaults(&service, ports)
	portBindings := renderComposePortBindings(ports)
	configVolumes := renderConfigComposeVolumes(pack)
	appEnvironment := renderComposeAppEnvironment(pack.Docker.Environment)
	runtimeOverrides, err := renderRuntimeOverrides(pack.Docker.Runtime)
	if err != nil {
		return "", err
	}
	rendered := strings.ReplaceAll(composeTemplate, "{{CONTAINER_COMMANDS}}", containerCommands)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_CONTAINER_COMMAND}}", pack.Docker.DefaultCommand)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_CONTAINER_COMMAND_FUNCTION}}", defaultCommandName)
	rendered = strings.ReplaceAll(rendered, "{{CONFIG_CHECK_COMMAND_FUNCTION}}", configCheckFunction)
	rendered = strings.ReplaceAll(rendered, "{{LOCAL_SOURCE_VOLUMES}}", sourceVolumes)
	rendered = strings.ReplaceAll(rendered, "{{PORT_BINDINGS}}", portBindings)
	rendered = strings.ReplaceAll(rendered, "{{CONFIG_VOLUMES}}", configVolumes)
	rendered = strings.ReplaceAll(rendered, "{{APP_ENVIRONMENT}}", appEnvironment)
	rendered = strings.ReplaceAll(rendered, "{{RUNTIME_OVERRIDES}}", runtimeOverrides)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_IMAGE}}", service.Image)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_CONTAINER_NAME}}", service.ContainerName)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_CONTAINER_USER}}", service.ContainerUser)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_RESTART}}", service.Restart)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_CONTAINER_HOST}}", service.ContainerHost)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_HOST_BIND}}", service.HostBind)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_HOST_PORT}}", service.HostPort)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_CONTAINER_PORT}}", service.ContainerPort)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_PUBLIC_SCHEME}}", service.PublicScheme)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_PUBLIC_BASE_URL}}", service.PublicBaseURL)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_NETWORK_NAME}}", service.NetworkName)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_RUNTIME_ROOT}}", service.RuntimeRoot)
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_RUNTIME_DIR}}", dockerRuntimeVolumeName(dockerIdentity))
	rendered = strings.ReplaceAll(rendered, "{{DEFAULT_CONTAINER_HOME}}", service.ContainerHome)
	if strings.Contains(rendered, "{{CONTAINER_COMMANDS}}") ||
		strings.Contains(rendered, "{{DEFAULT_CONTAINER_COMMAND}}") ||
		strings.Contains(rendered, "{{DEFAULT_CONTAINER_COMMAND_FUNCTION}}") ||
		strings.Contains(rendered, "{{CONFIG_CHECK_COMMAND_FUNCTION}}") ||
		strings.Contains(rendered, "{{LOCAL_SOURCE_VOLUMES}}") ||
		strings.Contains(rendered, "{{PORT_BINDINGS}}") ||
		strings.Contains(rendered, "{{CONFIG_VOLUMES}}") ||
		strings.Contains(rendered, "{{APP_ENVIRONMENT}}") ||
		strings.Contains(rendered, "{{RUNTIME_OVERRIDES}}") ||
		strings.Contains(rendered, "{{DEFAULT_") {
		return "", fmt.Errorf("compose template still contains command placeholder")
	}
	return rendered, nil
}

func renderConfigComposeVolumes(pack deploy.AppPack) string {
	layout := configMountLayoutForPack(pack)
	lines := []string{}
	for _, mount := range layout.Mounts {
		lines = append(lines, fmt.Sprintf("      - %s", strconv.Quote(fmt.Sprintf("./%s:%s:%s", mount.HostRelative, mount.ContainerPath, mount.Mode))))
	}
	return strings.Join(lines, "\n")
}

func localSourceComposeVolumes(pack deploy.AppPack, roots []deploy.ArtifactRoot) (string, error) {
	sources, err := selectedPackLocalSources(pack, roots, localSourceContainerRoot())
	if err != nil {
		return "", err
	}
	if len(sources) == 0 {
		return "", nil
	}
	lines := make([]string, 0, len(sources))
	sort.Slice(sources, func(i int, j int) bool {
		return sources[i].ContainerDir < sources[j].ContainerDir
	})
	for _, source := range sources {
		lines = append(lines, "      - "+strconv.Quote(source.HostDir+":"+source.ContainerDir+":rw"))
	}
	return strings.Join(lines, "\n"), nil
}

func dockerServiceDefaults(pack deploy.AppPack, dockerIdentity string) deploy.DockerServiceConfig {
	slug := dockerNameSlug(pack.AppID, "app")
	service := pack.Docker.Service
	service.Image = defaultString(service.Image, "python:3.11-slim")
	service.ContainerName = defaultString(dockerIdentity, slug+"-staging")
	service.ContainerUser = defaultString(service.ContainerUser, defaultContainerUser())
	service.Restart = defaultString(service.Restart, "on-failure")
	service.ContainerHost = defaultString(service.ContainerHost, "0.0.0.0")
	service.ContainerPort = defaultString(service.ContainerPort, "8080")
	service.HostBind = defaultString(service.HostBind, "127.0.0.1")
	service.HostPort = defaultString(service.HostPort, "18080")
	service.InstallOwner = installOwnerSpec(pack.Install.System.RunAs)
	service.PublicScheme = defaultString(service.PublicScheme, "http")
	service.NetworkName = defaultString(dockerIdentity, slug+"-staging")
	service.RuntimeRoot = defaultString(service.RuntimeRoot, "/reploy-runtime/python-venv")
	service.ContainerHome = defaultString(service.ContainerHome, "/tmp/reploy-home")
	return service
}

func stagingPortBindings(pack deploy.AppPack) ([]dockerPortBinding, error) {
	ports, err := installPortBindings(pack.Install.Ports.Staging)
	if err != nil {
		return nil, err
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("install.ports.staging must declare at least one port")
	}
	return ports, nil
}

func applyPrimaryPortDefaults(service *deploy.DockerServiceConfig, ports []dockerPortBinding) {
	if primary := installPrimaryPort(ports); primary.Name != "" {
		service.HostBind = primary.HostBind
		service.HostPort = primary.HostPort
		service.ContainerPort = primary.ContainerPort
		service.PublicScheme = defaultString(publicSchemeForPortName(primary.Name), service.PublicScheme)
	}
}

func dockerNameSlug(value string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if valid {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return fallback
	}
	return result
}

func renderComposeAppEnvironment(environment map[string]string) string {
	if len(environment) == 0 {
		return ""
	}
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		value := environment[name]
		lines = append(lines, "      "+name+": "+strconv.Quote("${"+name+":-"+value+"}"))
	}
	return strings.Join(lines, "\n")
}

func renderRuntimeOverrides(runtime deploy.DockerRuntimeConfig) (string, error) {
	lines := []string{}
	for _, override := range runtime.Overrides {
		rendered, err := renderRuntimeOverride(override)
		if err != nil {
			return "", err
		}
		lines = append(lines, "        set -- \"$$@\" "+strconv.Quote(rendered)+" &&")
	}
	envNames := make([]string, 0, len(runtime.OptionalEnvOverrides))
	for envName := range runtime.OptionalEnvOverrides {
		envNames = append(envNames, envName)
	}
	sort.Strings(envNames)
	for _, envName := range envNames {
		key := strings.TrimSpace(runtime.OptionalEnvOverrides[envName])
		lines = append(lines, fmt.Sprintf("        if [ -n \"$${%s:-}\" ]; then set -- \"$$@\" %s; fi &&", envName, strconv.Quote(key+"=$${"+envName+"}")))
	}
	if len(lines) == 0 {
		return "        : &&", nil
	}
	return strings.Join(lines, "\n"), nil
}

func renderRuntimeOverride(value string) (string, error) {
	var builder strings.Builder
	for index := 0; index < len(value); {
		if value[index] != '$' {
			builder.WriteByte(value[index])
			index++
			continue
		}
		if index+1 >= len(value) || value[index+1] != '{' {
			return "", fmt.Errorf("docker.runtime.overrides must use ${ENV} placeholders only: %s", value)
		}
		end := strings.IndexByte(value[index+2:], '}')
		if end < 0 {
			return "", fmt.Errorf("docker.runtime.overrides contains unterminated placeholder: %s", value)
		}
		name := value[index+2 : index+2+end]
		if !isRuntimeEnvPlaceholderName(name) {
			return "", fmt.Errorf("docker.runtime.overrides contains invalid placeholder: %s", value)
		}
		builder.WriteString("$${")
		builder.WriteString(name)
		builder.WriteByte('}')
		index += end + 3
	}
	return builder.String(), nil
}

func isRuntimeEnvPlaceholderName(name string) bool {
	if name == "" {
		return false
	}
	for index, r := range name {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || index > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func localSourceContainerRoot() string {
	return "/source/app"
}

func shellContainerCommands(commands []deploy.DockerCommandConfig, defaultCommand string) (string, error) {
	if _, err := containerCommandFunctionName(defaultCommand); err != nil {
		return "", err
	}
	functions := []string{}
	dispatchCases := []string{}
	for _, command := range commands {
		functionName, err := containerCommandFunctionName(command.Name)
		if err != nil {
			return "", err
		}
		function, err := shellFunction(functionName, command.Container.Argv)
		if err != nil {
			return "", err
		}
		functions = append(functions, function)
		dispatchCases = append(dispatchCases, fmt.Sprintf(`        %s) %s "$$@" ;;`, command.Name, functionName))
	}
	if len(functions) == 0 {
		return "", fmt.Errorf("at least one container command is required")
	}
	functions = append(functions, "run_reploy_container_command() {")
	functions = append(functions, fmt.Sprintf(`      case "$${REPLOY_CONTAINER_COMMAND:-%s}" in`, defaultCommand))
	functions = append(functions, dispatchCases...)
	functions = append(functions, fmt.Sprintf(`        *) echo "unknown container command: $${REPLOY_CONTAINER_COMMAND:-%s}" >&2; exit 2 ;;`, defaultCommand))
	functions = append(functions, "      esac")
	functions = append(functions, "      };")
	return strings.Join(functions, "\n      "), nil
}

func configCheckCommandFunction(commands []deploy.DockerCommandConfig) (string, error) {
	for _, command := range commands {
		if equalStrings(command.Trigger, []string{"config", "check"}) {
			return containerCommandFunctionName(command.Name)
		}
	}
	return "", fmt.Errorf("missing docker command trigger: config check")
}

func containerCommandFunctionName(commandName string) (string, error) {
	if commandName == "" {
		return "", fmt.Errorf("container command name must not be empty")
	}
	for index, char := range commandName {
		valid := char == '_' || ('A' <= char && char <= 'Z') || ('a' <= char && char <= 'z') || (index > 0 && '0' <= char && char <= '9')
		if !valid {
			return "", fmt.Errorf("container command name must be shell-safe: %s", commandName)
		}
	}
	return "container_command_" + commandName, nil
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index, value := range left {
		if value != right[index] {
			return false
		}
	}
	return true
}

func shellFunction(name string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("%s argv must not be empty", name)
	}
	parts := make([]string, 0, len(argv)+1)
	for _, arg := range argv {
		rendered, err := shellArg(arg)
		if err != nil {
			return "", err
		}
		parts = append(parts, rendered)
	}
	parts = append(parts, `"$$@"`)
	return fmt.Sprintf("%s() { %s; };", name, strings.Join(parts, " ")), nil
}

func shellArg(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("command argv must not contain newlines")
	}
	if envName, ok := envPlaceholder(value); ok {
		return fmt.Sprintf(`"$${%s}"`, envName), nil
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`")
	return `"` + replacer.Replace(value) + `"`, nil
}

func envPlaceholder(value string) (string, bool) {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	if name == "" {
		return "", false
	}
	for index, char := range name {
		valid := char == '_' || ('A' <= char && char <= 'Z') || ('a' <= char && char <= 'z') || (index > 0 && '0' <= char && char <= '9')
		if !valid {
			return "", false
		}
	}
	return name, true
}
