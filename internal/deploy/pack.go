package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const BlueprintManifestGlob = "*.blueprint.yaml"

type AppPack struct {
	Ref              PackRef
	RequestedRef     PackRef
	ResolvedArtifact *ResolvedPackArtifact
	Dir              string
	ManifestPath     string
	Pack             PackMetadata
	AppID            string
	App              AppPackConfig
	Install          InstallPackConfig
	Bundle           BundlePackConfig
	Docker           DockerPackConfig
}

type PackManifest struct {
	Pack    PackMetadata      `yaml:"blueprint"`
	App     AppPackConfig     `yaml:"app"`
	Install InstallPackConfig `yaml:"install"`
	Bundle  BundlePackConfig  `yaml:"bundle"`
	Docker  DockerPackConfig  `yaml:"docker"`
}

type PackMetadata struct {
	Schema         int    `yaml:"schema"`
	Version        string `yaml:"version"`
	RequiresReploy string `yaml:"requires_reploy"`
}

type AppPackConfig struct {
	ID       string            `yaml:"id"`
	Provider AppProviderConfig `yaml:"provider"`
	Terminal AppTerminalConfig `yaml:"terminal"`
}

type AppTerminalConfig struct {
	ColorEnv string `yaml:"color_env"`
}

type AppProviderConfig struct {
	Type         string            `yaml:"type"`
	Identifier   string            `yaml:"identifier"`
	LocalSources map[string]string `yaml:"local_sources"`
}

type InstallPackConfig struct {
	Target  InstallTargetConfig  `yaml:"target"`
	Owner   InstallOwnerConfig   `yaml:"owner"`
	Ports   InstallPortsConfig   `yaml:"ports"`
	Upgrade InstallUpgradeConfig `yaml:"upgrade"`
}

type InstallTargetConfig struct {
	DefaultPath string `yaml:"default_path"`
}

type InstallOwnerConfig struct {
	User    string                    `yaml:"user"`
	Group   string                    `yaml:"group"`
	Windows InstallWindowsOwnerConfig `yaml:"windows"`
}

type InstallWindowsOwnerConfig struct {
	Account string `yaml:"account"`
}

type InstallPortsConfig struct {
	Deployed map[string]InstallPortConfig `yaml:"deployed"`
	Staging  map[string]InstallPortConfig `yaml:"staging"`
}

type InstallPortConfig struct {
	HostBind      string `yaml:"host_bind"`
	HostPort      int    `yaml:"host_port"`
	ContainerPort int    `yaml:"container_port,omitempty"`
}

type InstallUpgradeConfig struct {
	Artifacts map[string]InstallArtifactPolicyConfig `yaml:"artifacts"`
}

type InstallArtifactPolicyConfig struct {
	Default string   `yaml:"default"`
	Paths   []string `yaml:"paths"`
}

type BundlePackConfig struct {
	Options map[string]BundleOptionConfig `yaml:"options"`
}

type BundleOptionConfig struct {
	Identifier  string `yaml:"identifier"`
	Group       string `yaml:"group"`
	Description string `yaml:"description"`
}

type AppCommandConfig struct {
	Argv []string `yaml:"argv"`
}

type DockerPackConfig struct {
	DeploymentDirs DockerDeploymentDirs  `yaml:"deployment_dirs"`
	Service        DockerServiceConfig   `yaml:"service"`
	Install        DockerInstallConfig   `yaml:"install"`
	Environment    map[string]string     `yaml:"environment"`
	Runtime        DockerRuntimeConfig   `yaml:"runtime"`
	DefaultCommand string                `yaml:"default_command"`
	Health         DockerHealthConfig    `yaml:"health"`
	Commands       []DockerCommandConfig `yaml:"-"`
}

type DockerServiceConfig struct {
	Image         string `yaml:"image"`
	ContainerName string `yaml:"-"`
	ContainerUser string `yaml:"container_user"`
	Restart       string `yaml:"restart"`
	ContainerHost string `yaml:"container_host"`
	PublicBaseURL string `yaml:"public_base_url"`
	NetworkName   string `yaml:"-"`
	RuntimeRoot   string `yaml:"runtime_root"`
	ContainerHome string `yaml:"container_home"`
	InstallOwner  string `yaml:"-"`
	ContainerPort string `yaml:"-"`
	HostBind      string `yaml:"-"`
	HostPort      string `yaml:"-"`
	PublicScheme  string `yaml:"-"`
}

type DockerRuntimeConfig struct {
	Overrides            []string          `yaml:"overrides"`
	OptionalEnvOverrides map[string]string `yaml:"optional_env_overrides"`
}

type DockerInstallConfig struct {
	Hooks   DockerInstallHooksConfig   `yaml:"hooks"`
	Success DockerInstallSuccessConfig `yaml:"success"`
}

type DockerInstallHooksConfig struct {
	BeforeStart []DockerInstallHookConfig `yaml:"before_start"`
	AfterStart  []DockerInstallHookConfig `yaml:"after_start"`
}

type DockerInstallHookConfig struct {
	App         []string                        `yaml:"app,omitempty"`
	HealthCheck *DockerInstallHealthCheckConfig `yaml:"health_check,omitempty"`
}

type DockerInstallHealthCheckConfig struct {
	Wait bool `yaml:"wait"`
}

type DockerInstallSuccessConfig struct {
	Vars  map[string]DockerInstallSuccessVarConfig `yaml:"vars"`
	Lines []string                                 `yaml:"lines"`
}

type DockerInstallSuccessVarConfig struct {
	App       []string `yaml:"app,omitempty"`
	ServerURL bool     `yaml:"server_url,omitempty"`
}

type DockerCommandConfig struct {
	Name         string           `yaml:"-"`
	Trigger      []string         `yaml:"trigger"`
	AppCommand   bool             `yaml:"app_command"`
	Deployed     bool             `yaml:"deployed_command"`
	ForwardArgs  bool             `yaml:"forward_args"`
	ForwardFlags []string         `yaml:"forward_flags"`
	Container    AppCommandConfig `yaml:"container"`
}

type DockerDeploymentDirs struct {
	Config string `yaml:"config"`
	Bundle string `yaml:"bundle"`
	Data   string `yaml:"data"`
}

type DockerHealthConfig struct {
	SchemeEnv     string `yaml:"scheme_env"`
	HostEnv       string `yaml:"host_env"`
	PortEnv       string `yaml:"port_env"`
	DefaultScheme string `yaml:"default_scheme"`
	DefaultHost   string `yaml:"default_host"`
	DefaultPort   string `yaml:"default_port"`
	Path          string `yaml:"path"`
	TLSVerify     *bool  `yaml:"tls_verify"`
}

func (dirs DockerDeploymentDirs) All() []string {
	return []string{dirs.Config, dirs.Bundle, dirs.Data}
}

func LoadPack(ref PackRef) (AppPack, error) {
	switch ref.Scheme {
	case "file":
		return loadFilePack(ref)
	case "pypi":
		return loadPyPIPack(ref)
	default:
		return AppPack{}, fmt.Errorf("blueprint scheme is not implemented yet: %s", ref.Scheme)
	}
}

func LoadResolvedPack(ref PackRef, requestedRaw string, artifact *ResolvedPackArtifact) (AppPack, error) {
	if artifact != nil && artifact.BlueprintPath != "" {
		requestedRef := ref
		if requestedRaw != "" {
			parsed, err := ParsePackRef(requestedRaw)
			if err == nil {
				requestedRef = parsed
			} else {
				requestedRef = PackRef{Raw: requestedRaw}
			}
		}
		return loadCachedPack(ref, requestedRef, artifact.BlueprintPath, artifact)
	}
	pack, err := LoadPack(ref)
	if err != nil {
		return AppPack{}, err
	}
	if requestedRaw != "" {
		parsed, err := ParsePackRef(requestedRaw)
		if err == nil {
			pack.RequestedRef = parsed
		} else {
			pack.RequestedRef = PackRef{Raw: requestedRaw}
		}
	}
	return pack, nil
}

func loadFilePack(ref PackRef) (AppPack, error) {
	if ref.Scheme != "file" {
		return AppPack{}, fmt.Errorf("blueprint scheme is not file: %s", ref.Scheme)
	}
	source := ref.Source
	if !filepath.IsAbs(source) {
		absolute, err := filepath.Abs(source)
		if err != nil {
			return AppPack{}, err
		}
		source = absolute
	}
	info, err := os.Stat(source)
	if err != nil {
		return AppPack{}, err
	}
	dir := source
	manifestPath := ""
	if info.IsDir() {
		manifestPath, err = findBlueprintManifest(source)
		if err != nil {
			return AppPack{}, err
		}
	} else {
		if !isBlueprintManifestPath(source) {
			return AppPack{}, fmt.Errorf("blueprint reference is not a %s file: %s", BlueprintManifestGlob, source)
		}
		manifestPath = source
		dir = filepath.Dir(source)
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return AppPack{}, fmt.Errorf("read blueprint manifest: %w", err)
	}
	manifest, err := ParsePackManifest(string(content))
	if err != nil {
		return AppPack{}, fmt.Errorf("parse blueprint manifest: %w", err)
	}
	if manifest.App.ID == "" {
		return AppPack{}, fmt.Errorf("blueprint manifest is missing app.id: %s", manifestPath)
	}
	resolvedRef := ref
	resolvedRef.Source = manifestPath
	resolvedRef.Raw = "file:" + manifestPath
	return AppPack{
		Ref:          resolvedRef,
		RequestedRef: ref,
		Dir:          dir,
		ManifestPath: manifestPath,
		Pack:         manifest.Pack,
		AppID:        manifest.App.ID,
		App:          manifest.App,
		Install:      manifest.Install,
		Bundle:       manifest.Bundle,
		Docker:       manifest.Docker,
	}, nil
}

func findBlueprintManifest(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, BlueprintManifestGlob))
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("blueprint manifest not found in %s; expected exactly one %s file", dir, BlueprintManifestGlob)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, match := range matches {
			names = append(names, filepath.Base(match))
		}
		return "", fmt.Errorf("multiple blueprint manifests found in %s; choose one explicitly: %s", dir, strings.Join(names, ", "))
	}
}

func isBlueprintManifestPath(path string) bool {
	return strings.HasSuffix(filepath.Base(path), ".blueprint.yaml")
}

func (pack AppPack) ReadFile(relativePath string) ([]byte, error) {
	if filepath.IsAbs(relativePath) {
		return nil, fmt.Errorf("blueprint file path must be relative: %s", relativePath)
	}
	clean := filepath.Clean(relativePath)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return nil, fmt.Errorf("blueprint file path escapes blueprint directory: %s", relativePath)
	}
	content, err := os.ReadFile(filepath.Join(pack.Dir, clean))
	if err != nil {
		return nil, err
	}
	return content, nil
}

func ParsePackManifest(content string) (PackManifest, error) {
	var raw rawPackManifest
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return PackManifest{}, err
	}
	if legacyFields := raw.Docker.Service.legacyFields(); len(legacyFields) > 0 {
		return PackManifest{}, fmt.Errorf("%s has moved to install.*", strings.Join(legacyFields, ", "))
	}
	if raw.Docker.Ports != nil {
		return PackManifest{}, fmt.Errorf("docker.ports has moved to install.ports")
	}
	manifest := PackManifest{
		Pack:    raw.Pack,
		App:     raw.App,
		Install: raw.Install,
		Bundle:  raw.Bundle,
		Docker: DockerPackConfig{
			DeploymentDirs: raw.Docker.DeploymentDirs,
			Service:        raw.Docker.Service.config(),
			Install:        raw.Docker.Install,
			Environment:    raw.Docker.Environment,
			Runtime:        raw.Docker.Runtime,
			DefaultCommand: raw.Docker.DefaultCommand,
			Health:         raw.Docker.Health,
			Commands:       parseDockerCommands(raw.Docker.Commands),
		},
	}
	if manifest.Pack.Schema == 0 {
		return PackManifest{}, fmt.Errorf("missing blueprint.schema")
	}
	if manifest.Pack.Schema != 1 {
		return PackManifest{}, fmt.Errorf("unsupported blueprint.schema: %d", manifest.Pack.Schema)
	}
	if strings.TrimSpace(manifest.Pack.Version) == "" {
		return PackManifest{}, fmt.Errorf("missing blueprint.version")
	}
	if err := validateRequiresReploy(manifest.Pack.RequiresReploy, ToolVersion); err != nil {
		return PackManifest{}, err
	}
	manifest.App.ID = strings.TrimSpace(manifest.App.ID)
	if manifest.App.ID == "" {
		return PackManifest{}, fmt.Errorf("missing app.id")
	}
	manifest.App.Provider.Type = strings.TrimSpace(manifest.App.Provider.Type)
	if manifest.App.Provider.Type == "" {
		return PackManifest{}, fmt.Errorf("missing app.provider.type")
	}
	manifest.App.Provider.Identifier = strings.TrimSpace(manifest.App.Provider.Identifier)
	if manifest.App.Provider.Identifier == "" {
		return PackManifest{}, fmt.Errorf("missing app.provider.identifier")
	}
	manifest.App.Terminal.ColorEnv = strings.TrimSpace(manifest.App.Terminal.ColorEnv)
	if manifest.App.Terminal.ColorEnv != "" && !isEnvironmentVariableName(manifest.App.Terminal.ColorEnv) {
		return PackManifest{}, fmt.Errorf("app.terminal.color_env must be an environment variable name")
	}
	if err := normalizeAndValidateInstallConfig(&manifest); err != nil {
		return PackManifest{}, err
	}
	required := map[string]string{
		"docker.deployment_dirs.config": manifest.Docker.DeploymentDirs.Config,
		"docker.deployment_dirs.bundle": manifest.Docker.DeploymentDirs.Bundle,
		"docker.deployment_dirs.data":   manifest.Docker.DeploymentDirs.Data,
	}
	for key, value := range required {
		if value == "" {
			return PackManifest{}, fmt.Errorf("missing %s", key)
		}
		if err := validateRelativeBlueprintPath(value); err != nil {
			return PackManifest{}, fmt.Errorf("%s: %w", key, err)
		}
	}
	for name, value := range manifest.App.Provider.LocalSources {
		if strings.TrimSpace(name) == "" {
			return PackManifest{}, fmt.Errorf("app.provider.local_sources contains an empty identifier")
		}
		if strings.TrimSpace(value) == "" {
			return PackManifest{}, fmt.Errorf("app.provider.local_sources.%s must not be empty", name)
		}
		if filepath.IsAbs(value) {
			return PackManifest{}, fmt.Errorf("app.provider.local_sources.%s must be relative to the blueprint directory", name)
		}
	}
	for name, option := range manifest.Bundle.Options {
		if strings.TrimSpace(name) == "" {
			return PackManifest{}, fmt.Errorf("bundle.options contains an empty option name")
		}
		if containsLineOrFieldBreak(name) {
			return PackManifest{}, fmt.Errorf("bundle.options contains an invalid option name: %q", name)
		}
		option.Identifier = strings.TrimSpace(option.Identifier)
		option.Group = strings.TrimSpace(option.Group)
		option.Description = strings.TrimSpace(option.Description)
		if option.Identifier == "" {
			return PackManifest{}, fmt.Errorf("bundle.options.%s.identifier is required", name)
		}
		for field, value := range map[string]string{
			"identifier":  option.Identifier,
			"group":       option.Group,
			"description": option.Description,
		} {
			if containsLineOrFieldBreak(value) {
				return PackManifest{}, fmt.Errorf("bundle.options.%s.%s must not contain tabs or newlines", name, field)
			}
		}
		manifest.Bundle.Options[name] = option
	}
	if manifest.Docker.Environment == nil {
		manifest.Docker.Environment = map[string]string{}
	}
	for name, value := range manifest.Docker.Environment {
		if !isEnvironmentVariableName(name) {
			return PackManifest{}, fmt.Errorf("docker.environment contains invalid environment variable name: %s", name)
		}
		if name == "REPLOY_DEPLOYMENT_SCOPE" {
			return PackManifest{}, fmt.Errorf("docker.environment.%s is reserved by Reploy", name)
		}
		if containsLineOrFieldBreak(value) {
			return PackManifest{}, fmt.Errorf("docker.environment.%s must not contain tabs or newlines", name)
		}
	}
	for _, override := range manifest.Docker.Runtime.Overrides {
		if strings.TrimSpace(override) == "" {
			return PackManifest{}, fmt.Errorf("docker.runtime.overrides must not contain empty values")
		}
		if containsLineOrFieldBreak(override) {
			return PackManifest{}, fmt.Errorf("docker.runtime.overrides must not contain tabs or newlines")
		}
	}
	if manifest.Docker.Runtime.OptionalEnvOverrides == nil {
		manifest.Docker.Runtime.OptionalEnvOverrides = map[string]string{}
	}
	for envName, overrideKey := range manifest.Docker.Runtime.OptionalEnvOverrides {
		if !isEnvironmentVariableName(envName) {
			return PackManifest{}, fmt.Errorf("docker.runtime.optional_env_overrides contains invalid environment variable name: %s", envName)
		}
		if strings.TrimSpace(overrideKey) == "" {
			return PackManifest{}, fmt.Errorf("docker.runtime.optional_env_overrides.%s must not be empty", envName)
		}
		if containsLineOrFieldBreak(overrideKey) {
			return PackManifest{}, fmt.Errorf("docker.runtime.optional_env_overrides.%s must not contain tabs or newlines", envName)
		}
	}
	if err := validateInstallHooks(manifest.Docker.Install.Hooks); err != nil {
		return PackManifest{}, err
	}
	if err := validateInstallSuccess(manifest.Docker.Install.Success, manifest.Docker.Health); err != nil {
		return PackManifest{}, err
	}
	if len(manifest.Docker.Commands) == 0 {
		return PackManifest{}, fmt.Errorf("missing docker.commands")
	}
	if manifest.Docker.DefaultCommand == "" {
		return PackManifest{}, fmt.Errorf("missing docker.default_command")
	}
	foundDefaultCommand := false
	for _, command := range manifest.Docker.Commands {
		if command.Name == manifest.Docker.DefaultCommand {
			foundDefaultCommand = true
		}
		if command.Name != manifest.Docker.DefaultCommand && len(command.Trigger) == 0 {
			return PackManifest{}, fmt.Errorf("missing docker.commands.%s.trigger", command.Name)
		}
		if len(command.Container.Argv) == 0 {
			return PackManifest{}, fmt.Errorf("missing docker.commands.%s.container.argv", command.Name)
		}
	}
	if !foundDefaultCommand {
		return PackManifest{}, fmt.Errorf("docker.default_command references unknown command: %s", manifest.Docker.DefaultCommand)
	}
	if manifest.Docker.Health.Path != "" && !strings.HasPrefix(manifest.Docker.Health.Path, "/") {
		return PackManifest{}, fmt.Errorf("docker.health.path must start with /")
	}
	return manifest, nil
}

func normalizeAndValidateInstallConfig(manifest *PackManifest) error {
	if manifest.Install.Target.DefaultPath == "" {
		manifest.Install.Target.DefaultPath = defaultInstallTargetPath()
	}
	if err := validateInstallTargetDefaultPath(manifest.Install.Target.DefaultPath); err != nil {
		return err
	}
	manifest.Install.Owner.User = strings.TrimSpace(manifest.Install.Owner.User)
	manifest.Install.Owner.Group = strings.TrimSpace(manifest.Install.Owner.Group)
	manifest.Install.Owner.Windows.Account = strings.TrimSpace(manifest.Install.Owner.Windows.Account)
	if err := validateInstallOwner(manifest.Install.Owner); err != nil {
		return err
	}
	if manifest.Install.Ports.Deployed == nil {
		manifest.Install.Ports.Deployed = map[string]InstallPortConfig{}
	}
	if manifest.Install.Ports.Staging == nil {
		manifest.Install.Ports.Staging = map[string]InstallPortConfig{}
	}
	if len(manifest.Install.Ports.Deployed) == 0 {
		return fmt.Errorf("install.ports.deployed must declare at least one port")
	}
	if len(manifest.Install.Ports.Staging) == 0 {
		return fmt.Errorf("install.ports.staging must declare at least one port")
	}
	if err := normalizeAndValidateInstallPorts("deployed", manifest.Install.Ports.Deployed); err != nil {
		return err
	}
	if err := normalizeAndValidateInstallPorts("staging", manifest.Install.Ports.Staging); err != nil {
		return err
	}
	if manifest.Install.Upgrade.Artifacts == nil {
		manifest.Install.Upgrade.Artifacts = map[string]InstallArtifactPolicyConfig{}
	}
	if err := normalizeAndValidateInstallArtifacts(manifest.Install.Upgrade.Artifacts); err != nil {
		return err
	}
	return nil
}

func defaultInstallTargetPath() string {
	switch runtime.GOOS {
	case "windows":
		return `%ProgramFiles%\{{ app.id }}`
	default:
		return "/opt/{{ app.id }}"
	}
}

func validateInstallTargetDefaultPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("install.target.default_path must not be empty")
	}
	if containsLineOrFieldBreak(path) {
		return fmt.Errorf("install.target.default_path must not contain tabs or newlines")
	}
	rendered := strings.ReplaceAll(path, "{{ app.id }}", "app")
	switch {
	case filepath.IsAbs(rendered):
		return nil
	case strings.HasPrefix(rendered, `%ProgramFiles%\`) || strings.HasPrefix(rendered, `%ProgramFiles(x86)%\`):
		return nil
	default:
		return fmt.Errorf("install.target.default_path must be absolute or use a supported platform root: %s", path)
	}
}

func validateInstallOwner(owner InstallOwnerConfig) error {
	if owner.User == "" {
		return fmt.Errorf("install.owner.user is required")
	}
	if owner.Group == "" {
		return fmt.Errorf("install.owner.group is required")
	}
	if containsLineOrFieldBreak(owner.User) {
		return fmt.Errorf("install.owner.user must not contain tabs or newlines")
	}
	if containsLineOrFieldBreak(owner.Group) {
		return fmt.Errorf("install.owner.group must not contain tabs or newlines")
	}
	if containsLineOrFieldBreak(owner.Windows.Account) {
		return fmt.Errorf("install.owner.windows.account must not contain tabs or newlines")
	}
	if owner.User == "root" || owner.User == "0" {
		return fmt.Errorf("install.owner.user must not be root")
	}
	if owner.Group == "root" || owner.Group == "0" {
		return fmt.Errorf("install.owner.group must not be root")
	}
	return nil
}

func normalizeAndValidateInstallPorts(environment string, ports map[string]InstallPortConfig) error {
	for name, port := range ports {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("install.ports.%s contains an empty port name", environment)
		}
		if containsLineOrFieldBreak(name) {
			return fmt.Errorf("install.ports.%s contains an invalid port name: %q", environment, name)
		}
		if containsLineOrFieldBreak(port.HostBind) {
			return fmt.Errorf("install.ports.%s.%s.host_bind must not contain tabs or newlines", environment, name)
		}
		if strings.TrimSpace(port.HostBind) == "" {
			return fmt.Errorf("install.ports.%s.%s.host_bind is required", environment, name)
		}
		if !validTCPPort(port.HostPort) {
			return fmt.Errorf("install.ports.%s.%s.host_port must be between 1 and 65535", environment, name)
		}
		if port.ContainerPort == 0 {
			port.ContainerPort = port.HostPort
		}
		if !validTCPPort(port.ContainerPort) {
			return fmt.Errorf("install.ports.%s.%s.container_port must be between 1 and 65535", environment, name)
		}
		ports[name] = port
	}
	return nil
}

func validTCPPort(port int) bool {
	return port >= 1 && port <= 65535
}

func normalizeAndValidateInstallArtifacts(artifacts map[string]InstallArtifactPolicyConfig) error {
	for name, artifact := range artifacts {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("install.upgrade.artifacts contains an empty artifact name")
		}
		if containsLineOrFieldBreak(name) {
			return fmt.Errorf("install.upgrade.artifacts contains an invalid artifact name: %q", name)
		}
		artifact.Default = strings.TrimSpace(artifact.Default)
		switch artifact.Default {
		case "preserve", "replace":
		default:
			return fmt.Errorf("install.upgrade.artifacts.%s.default must be preserve or replace", name)
		}
		if len(artifact.Paths) == 0 {
			return fmt.Errorf("install.upgrade.artifacts.%s.paths must not be empty", name)
		}
		for index, path := range artifact.Paths {
			if strings.TrimSpace(path) == "" {
				return fmt.Errorf("install.upgrade.artifacts.%s.paths[%d] must not be empty", name, index)
			}
			if containsLineOrFieldBreak(path) {
				return fmt.Errorf("install.upgrade.artifacts.%s.paths[%d] must not contain tabs or newlines", name, index)
			}
			if err := validateRelativeBlueprintPath(path); err != nil {
				return fmt.Errorf("install.upgrade.artifacts.%s.paths[%d]: %w", name, index, err)
			}
			if path == ".reploy" || strings.HasPrefix(filepath.ToSlash(path), ".reploy/") {
				return fmt.Errorf("install.upgrade.artifacts.%s.paths[%d] must not include .reploy; .reploy is Reploy-owned", name, index)
			}
		}
		artifacts[name] = artifact
	}
	return nil
}

func validateInstallHooks(hooks DockerInstallHooksConfig) error {
	for phase, phaseHooks := range map[string][]DockerInstallHookConfig{
		"before_start": hooks.BeforeStart,
		"after_start":  hooks.AfterStart,
	} {
		for index, hook := range phaseHooks {
			actionCount := 0
			if len(hook.App) > 0 {
				actionCount++
				for argIndex, arg := range hook.App {
					if strings.TrimSpace(arg) == "" {
						return fmt.Errorf("docker.install.hooks.%s[%d].app[%d] must not be empty", phase, index, argIndex)
					}
					if containsLineOrFieldBreak(arg) {
						return fmt.Errorf("docker.install.hooks.%s[%d].app[%d] must not contain tabs or newlines", phase, index, argIndex)
					}
				}
			}
			if hook.HealthCheck != nil {
				actionCount++
				if !hook.HealthCheck.Wait {
					return fmt.Errorf("docker.install.hooks.%s[%d].health_check.wait must be true", phase, index)
				}
			}
			if actionCount != 1 {
				return fmt.Errorf("docker.install.hooks.%s[%d] must declare exactly one action", phase, index)
			}
		}
	}
	return nil
}

func validateInstallSuccess(success DockerInstallSuccessConfig, _ DockerHealthConfig) error {
	for name, variable := range success.Vars {
		if !isInstallSuccessVariableName(name) {
			return fmt.Errorf("docker.install.success.vars contains invalid variable name: %s", name)
		}
		actionCount := 0
		if len(variable.App) > 0 {
			actionCount++
			for argIndex, arg := range variable.App {
				if strings.TrimSpace(arg) == "" {
					return fmt.Errorf("docker.install.success.vars.%s.app[%d] must not be empty", name, argIndex)
				}
				if containsLineOrFieldBreak(arg) {
					return fmt.Errorf("docker.install.success.vars.%s.app[%d] must not contain tabs or newlines", name, argIndex)
				}
			}
		}
		if variable.ServerURL {
			actionCount++
		}
		if actionCount != 1 {
			return fmt.Errorf("docker.install.success.vars.%s must declare exactly one source", name)
		}
	}
	for index, line := range success.Lines {
		line = strings.TrimSpace(line)
		if line == "" {
			return fmt.Errorf("docker.install.success.lines[%d] must not be empty", index)
		}
		if containsLineOrFieldBreak(line) {
			return fmt.Errorf("docker.install.success.lines[%d] must not contain tabs or newlines", index)
		}
		for _, name := range installSuccessLineVariables(line) {
			if _, ok := success.Vars[name]; !ok {
				return fmt.Errorf("docker.install.success.lines[%d] references unknown variable: %s", index, name)
			}
		}
	}
	return nil
}

func isInstallSuccessVariableName(name string) bool {
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

func installSuccessLineVariables(line string) []string {
	var names []string
	remaining := line
	for {
		start := strings.Index(remaining, "${")
		if start < 0 {
			return names
		}
		remaining = remaining[start+2:]
		end := strings.Index(remaining, "}")
		if end < 0 {
			return names
		}
		names = append(names, remaining[:end])
		remaining = remaining[end+1:]
	}
}

func containsLineOrFieldBreak(value string) bool {
	return strings.ContainsAny(value, "\t\r\n")
}

func isEnvironmentVariableName(name string) bool {
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

type rawPackManifest struct {
	Pack    PackMetadata        `yaml:"blueprint"`
	App     AppPackConfig       `yaml:"app"`
	Install InstallPackConfig   `yaml:"install"`
	Bundle  BundlePackConfig    `yaml:"bundle"`
	Docker  rawDockerPackConfig `yaml:"docker"`
}

type rawDockerPackConfig struct {
	DeploymentDirs DockerDeploymentDirs           `yaml:"deployment_dirs"`
	Service        rawDockerServiceConfig         `yaml:"service"`
	Ports          map[string]any                 `yaml:"ports"`
	Install        DockerInstallConfig            `yaml:"install"`
	Environment    map[string]string              `yaml:"environment"`
	Runtime        DockerRuntimeConfig            `yaml:"runtime"`
	DefaultCommand string                         `yaml:"default_command"`
	Health         DockerHealthConfig             `yaml:"health"`
	Commands       map[string]DockerCommandConfig `yaml:"commands"`
}

type rawDockerServiceConfig struct {
	Image         string `yaml:"image"`
	ContainerUser string `yaml:"container_user"`
	Restart       string `yaml:"restart"`
	ContainerHost string `yaml:"container_host"`
	PublicBaseURL string `yaml:"public_base_url"`
	RuntimeRoot   string `yaml:"runtime_root"`
	ContainerHome string `yaml:"container_home"`
	InstallOwner  string `yaml:"install_owner"`
	ContainerPort string `yaml:"container_port"`
	HostBind      string `yaml:"host_bind"`
	HostPort      string `yaml:"host_port"`
	PublicScheme  string `yaml:"public_scheme"`
}

func (service rawDockerServiceConfig) config() DockerServiceConfig {
	return DockerServiceConfig{
		Image:         service.Image,
		ContainerUser: service.ContainerUser,
		Restart:       service.Restart,
		ContainerHost: service.ContainerHost,
		PublicBaseURL: service.PublicBaseURL,
		RuntimeRoot:   service.RuntimeRoot,
		ContainerHome: service.ContainerHome,
	}
}

func (service rawDockerServiceConfig) legacyFields() []string {
	fields := []string{}
	if service.InstallOwner != "" {
		fields = append(fields, "docker.service.install_owner")
	}
	if service.ContainerPort != "" {
		fields = append(fields, "docker.service.container_port")
	}
	if service.HostBind != "" {
		fields = append(fields, "docker.service.host_bind")
	}
	if service.HostPort != "" {
		fields = append(fields, "docker.service.host_port")
	}
	if service.PublicScheme != "" {
		fields = append(fields, "docker.service.public_scheme")
	}
	return fields
}

func parseDockerCommands(commands map[string]DockerCommandConfig) []DockerCommandConfig {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]DockerCommandConfig, 0, len(names))
	for _, name := range names {
		command := commands[name]
		command.Name = name
		result = append(result, command)
	}
	return result
}

func validateRequiresReploy(requirement string, toolVersion string) error {
	requirement = strings.TrimSpace(requirement)
	if requirement == "" {
		return fmt.Errorf("missing blueprint.requires_reploy")
	}
	if !strings.HasPrefix(requirement, ">=") || strings.TrimSpace(strings.TrimPrefix(requirement, ">=")) == "" {
		return fmt.Errorf("unsupported blueprint.requires_reploy constraint: %s", requirement)
	}
	if toolVersion == "" || toolVersion == "dev" {
		return nil
	}
	minimum := strings.TrimSpace(strings.TrimPrefix(requirement, ">="))
	if compareDottedVersion(toolVersion, minimum) < 0 {
		return fmt.Errorf("blueprint requires reploy %s, current version is %s", requirement, toolVersion)
	}
	return nil
}

func compareDottedVersion(left string, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for len(leftParts) < len(rightParts) {
		leftParts = append(leftParts, "0")
	}
	for len(rightParts) < len(leftParts) {
		rightParts = append(rightParts, "0")
	}
	for index := range leftParts {
		leftValue := numericVersionPart(leftParts[index])
		rightValue := numericVersionPart(rightParts[index])
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func numericVersionPart(part string) int {
	value := 0
	for _, r := range part {
		if r < '0' || r > '9' {
			break
		}
		value = value*10 + int(r-'0')
	}
	return value
}

func (docker DockerPackConfig) MatchCommand(args []string) (DockerCommandConfig, []string, error) {
	return docker.matchCommand(args, false)
}

func (docker DockerPackConfig) MatchAppCommand(args []string) (DockerCommandConfig, []string, error) {
	return docker.matchCommand(args, true)
}

func (docker DockerPackConfig) AppCommands() []DockerCommandConfig {
	commands := []DockerCommandConfig{}
	for _, command := range docker.Commands {
		if command.AppCommand && len(command.Trigger) > 0 {
			commands = append(commands, command)
		}
	}
	sort.SliceStable(commands, func(left int, right int) bool {
		return appCommandSortKey(commands[left]) < appCommandSortKey(commands[right])
	})
	return commands
}

func (docker DockerPackConfig) DeployedCommands() []DockerCommandConfig {
	commands := []DockerCommandConfig{}
	for _, command := range docker.Commands {
		if command.Deployed && len(command.Trigger) > 0 {
			commands = append(commands, command)
		}
	}
	sort.SliceStable(commands, func(left int, right int) bool {
		return appCommandSortKey(commands[left]) < appCommandSortKey(commands[right])
	})
	return commands
}

func appCommandSortKey(command DockerCommandConfig) string {
	trigger := strings.Join(command.Trigger, " ")
	switch {
	case trigger == "bootstrap server":
		return "00 " + trigger
	case strings.HasPrefix(trigger, "bootstrap "):
		return "10 " + trigger
	case strings.HasPrefix(trigger, "config activate"):
		return "15 " + trigger
	case strings.HasPrefix(trigger, "config "):
		return "16 " + trigger
	case strings.HasPrefix(trigger, "env "):
		return "20 " + trigger
	default:
		return "90 " + trigger
	}
}

func (docker DockerPackConfig) matchCommand(args []string, appOnly bool) (DockerCommandConfig, []string, error) {
	var best *DockerCommandConfig
	for index := range docker.Commands {
		command := &docker.Commands[index]
		if appOnly && !command.AppCommand {
			continue
		}
		if len(command.Trigger) == 0 {
			continue
		}
		if !hasPrefix(args, command.Trigger) {
			continue
		}
		if best == nil || len(command.Trigger) > len(best.Trigger) {
			best = command
		}
	}
	if best == nil {
		if appOnly {
			return DockerCommandConfig{}, nil, fmt.Errorf("no app command matches: %s", strings.Join(args, " "))
		}
		return DockerCommandConfig{}, nil, fmt.Errorf("no docker command matches: %s", strings.Join(args, " "))
	}
	if best.ForwardArgs {
		return *best, args[len(best.Trigger):], nil
	}
	forwarded, err := validateForwardedArgs(args[len(best.Trigger):], best.ForwardFlags)
	if err != nil {
		return DockerCommandConfig{}, nil, err
	}
	return *best, forwarded, nil
}

func hasPrefix(values []string, prefix []string) bool {
	if len(values) < len(prefix) {
		return false
	}
	for index, value := range prefix {
		if values[index] != value {
			return false
		}
	}
	return true
}

func validateForwardedArgs(args []string, allowedFlags []string) ([]string, error) {
	allowed := map[string]bool{}
	for _, flag := range allowedFlags {
		allowed[flag] = true
	}
	forwarded := []string{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			return nil, fmt.Errorf("unexpected positional argument after docker command trigger: %s", arg)
		}
		name := arg
		if before, _, found := strings.Cut(arg, "="); found {
			name = before
		}
		if !allowed[name] {
			return nil, unknownForwardedFlagError(name, allowedFlags)
		}
		forwarded = append(forwarded, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		if index+1 < len(args) && !strings.HasPrefix(args[index+1], "--") {
			index++
			forwarded = append(forwarded, args[index])
		}
	}
	return forwarded, nil
}

func unknownForwardedFlagError(name string, allowedFlags []string) error {
	var suggestion string
	for _, allowedFlag := range allowedFlags {
		if suggestion == "" && editDistanceAtMostOne(strings.ToLower(name), strings.ToLower(allowedFlag)) {
			suggestion = allowedFlag
		}
	}
	details := []string{}
	if suggestion != "" {
		details = append(details, fmt.Sprintf("did you mean %s?", suggestion))
	}
	if len(allowedFlags) > 0 {
		details = append(details, "allowed forwarded flags:", "  "+strings.Join(allowedFlags, "\n  "))
	}
	if len(details) == 0 {
		return fmt.Errorf("unknown forwarded flag: %s", name)
	}
	return fmt.Errorf("unknown forwarded flag: %s\n%s", name, strings.Join(details, "\n"))
}

func editDistanceAtMostOne(left string, right string) bool {
	if left == right {
		return false
	}
	if len(left) > len(right)+1 || len(right) > len(left)+1 {
		return false
	}
	mismatches := 0
	for len(left) > 0 && len(right) > 0 {
		if left[0] == right[0] {
			left = left[1:]
			right = right[1:]
			continue
		}
		mismatches++
		if mismatches > 1 {
			return false
		}
		switch {
		case len(left) > len(right):
			left = left[1:]
		case len(right) > len(left):
			right = right[1:]
		default:
			left = left[1:]
			right = right[1:]
		}
	}
	return mismatches+len(left)+len(right) <= 1
}

func validateRelativeBlueprintPath(relativePath string) error {
	if filepath.IsAbs(relativePath) {
		return fmt.Errorf("path must be relative: %s", relativePath)
	}
	clean := filepath.Clean(relativePath)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return fmt.Errorf("path escapes blueprint or deployment root: %s", relativePath)
	}
	return nil
}
