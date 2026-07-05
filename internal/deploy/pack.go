package deploy

import (
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
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
	Target       InstallTargetConfig       `yaml:"target"`
	Owner        InstallOwnerConfig        `yaml:"owner"`
	Ports        InstallPortsConfig        `yaml:"ports"`
	ManagedPaths InstallManagedPathsConfig `yaml:"managed_paths"`
}

type InstallTargetConfig struct {
	DefaultPath  string            `yaml:"default_path"`
	DefaultPaths map[string]string `yaml:"default_paths"`
}

type InstallOwnerConfig struct {
	User      string                    `yaml:"user"`
	Group     string                    `yaml:"group"`
	OnMissing string                    `yaml:"on_missing"`
	Windows   InstallWindowsOwnerConfig `yaml:"windows"`
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

type InstallManagedPathsConfig struct {
	Files []InstallManagedPathConfig `yaml:"files"`
	Dirs  []InstallManagedPathConfig `yaml:"dirs"`
}

type InstallManagedPathConfig struct {
	Path   string `yaml:"path"`
	Update string `yaml:"update"`
	Mount  string `yaml:"mount,omitempty"`
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

type rawAppCommandConfig struct {
	Argv       []string `yaml:"argv"`
	ArgvPrefix []string `yaml:"argv_prefix"`
	ArgvSuffix []string `yaml:"argv_suffix"`
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

type rawDockerCommandConfig struct {
	Trigger      []string            `yaml:"trigger"`
	AppCommand   *bool               `yaml:"app_command"`
	Deployed     *bool               `yaml:"deployed_command"`
	ForwardArgs  bool                `yaml:"forward_args"`
	ForwardFlags []string            `yaml:"forward_flags"`
	Container    rawAppCommandConfig `yaml:"container"`
}

type rawDockerCommandDefaultsConfig struct {
	AppCommand *bool               `yaml:"app_command"`
	Deployed   *bool               `yaml:"deployed_command"`
	Container  rawAppCommandConfig `yaml:"container"`
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
	case "source":
		return loadSourcePack(ref)
	case "git":
		return loadGitPack(ref)
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

func loadSourcePack(ref PackRef) (AppPack, error) {
	if ref.Scheme != "source" {
		return AppPack{}, fmt.Errorf("blueprint scheme is not source: %s", ref.Scheme)
	}
	sourceRoot := ref.Source
	if !filepath.IsAbs(sourceRoot) {
		absolute, err := filepath.Abs(sourceRoot)
		if err != nil {
			return AppPack{}, err
		}
		sourceRoot = absolute
	}
	info, err := os.Stat(sourceRoot)
	if err != nil {
		return AppPack{}, err
	}
	if !info.IsDir() {
		return AppPack{}, fmt.Errorf("source blueprint reference must point at a directory: %s", ref.Source)
	}
	pack, subdir, err := loadSourceCheckout(sourceRoot, ref.Subdir)
	if err != nil {
		return AppPack{}, err
	}
	resolvedRef := ref
	resolvedRef.Source = sourceRoot
	resolvedRef.Subdir = filepath.ToSlash(subdir)
	resolvedRef.Raw = "source:" + sourceRoot + "#" + filepath.ToSlash(subdir)
	pack.Ref = resolvedRef
	pack.RequestedRef = ref
	pack.App.Provider.LocalSources = sourceLocalSources(pack, sourceRoot)
	return pack, nil
}

func loadSourceCheckout(sourceRoot string, requestedSubdir string) (AppPack, string, error) {
	blueprintPath := strings.Trim(requestedSubdir, "/")
	if blueprintPath == "" {
		projectName, err := sourceProjectName(sourceRoot)
		if err != nil {
			return AppPack{}, "", err
		}
		blueprintPath = defaultSourceBlueprintSubdir(projectName)
	}
	blueprintSource := filepath.Join(sourceRoot, filepath.FromSlash(blueprintPath))
	fileRef := PackRef{Raw: "file:" + blueprintSource, Scheme: "file", Source: blueprintSource}
	pack, err := loadFilePack(fileRef)
	if err != nil {
		return AppPack{}, "", err
	}
	pack.App.Provider.LocalSources = sourceLocalSources(pack, sourceRoot)
	return pack, filepath.ToSlash(blueprintPath), nil
}

func sourceProjectName(sourceRoot string) (string, error) {
	content, err := os.ReadFile(filepath.Join(sourceRoot, "pyproject.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("source blueprint ref without #PATH requires pyproject.toml with [project].name: %s", sourceRoot)
		}
		return "", err
	}
	inProject := false
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inProject = trimmed == "[project]"
			continue
		}
		if !inProject || !strings.HasPrefix(trimmed, "name") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(key) != "name" {
			continue
		}
		name := strings.Trim(strings.TrimSpace(value), `"'`)
		if name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("source blueprint ref without #PATH requires pyproject.toml with [project].name: %s", sourceRoot)
}

func sourceLocalSources(pack AppPack, sourceRoot string) map[string]string {
	localSources := map[string]string{}
	for name, source := range pack.App.Provider.LocalSources {
		localSources[name] = source
	}
	identifier := strings.TrimSpace(pack.App.Provider.Identifier)
	if identifier == "" {
		return localSources
	}
	identifierName, _, _ := strings.Cut(identifier, "==")
	identifierName, _, _ = strings.Cut(identifierName, "[")
	normalizedIdentifier := normalizePackageName(identifierName)
	for name := range localSources {
		if normalizePackageName(name) == normalizedIdentifier {
			return localSources
		}
	}
	if _, ok := localSources[identifier]; ok {
		return localSources
	}
	relativeSource, err := filepath.Rel(pack.Dir, sourceRoot)
	if err != nil {
		return localSources
	}
	localSources[identifier] = filepath.ToSlash(relativeSource)
	return localSources
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
			Commands:       parseDockerCommands(raw.Docker.Commands, raw.Docker.CommandDefaults, raw.Docker.DefaultCommand),
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
	required := []struct {
		key     string
		value   string
		example string
	}{
		{key: "docker.deployment_dirs.config", value: manifest.Docker.DeploymentDirs.Config, example: "conf"},
		{key: "docker.deployment_dirs.bundle", value: manifest.Docker.DeploymentDirs.Bundle, example: ".reploy/bundle"},
		{key: "docker.deployment_dirs.data", value: manifest.Docker.DeploymentDirs.Data, example: "data"},
	}
	for _, field := range required {
		if field.value == "" {
			return PackManifest{}, fmt.Errorf("missing %s", field.key)
		}
		if err := validateDeploymentDir(field.value, field.example); err != nil {
			return PackManifest{}, fmt.Errorf("%s: %w", field.key, err)
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
	if err := validateInstallTargetConfig(manifest.Install.Target, runtime.GOOS); err != nil {
		return err
	}
	manifest.Install.Owner.User = strings.TrimSpace(manifest.Install.Owner.User)
	manifest.Install.Owner.Group = strings.TrimSpace(manifest.Install.Owner.Group)
	manifest.Install.Owner.OnMissing = strings.TrimSpace(manifest.Install.Owner.OnMissing)
	manifest.Install.Owner.Windows.Account = strings.TrimSpace(manifest.Install.Owner.Windows.Account)
	if manifest.Install.Owner.OnMissing == "" {
		if installOwnerPartIsNumeric(manifest.Install.Owner.User) || installOwnerPartIsNumeric(manifest.Install.Owner.Group) {
			manifest.Install.Owner.OnMissing = "fail"
		} else {
			manifest.Install.Owner.OnMissing = "create"
		}
	}
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
	if err := normalizeAndValidateManagedPaths(&manifest.Install.ManagedPaths); err != nil {
		return err
	}
	return nil
}

var installTargetTemplatePattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

type InstallTargetRoots struct {
	UserHome          string
	UserData          string
	UserLocalData     string
	SystemData        string
	ReployInstallRoot string
}

func InstallTargetHostKey(goos string) string {
	switch goos {
	case "darwin":
		return "macos"
	default:
		return goos
	}
}

func ResolveInstallTargetDefault(target InstallTargetConfig, appID string, goos string, roots InstallTargetRoots) (string, string, bool, error) {
	hostKey := InstallTargetHostKey(goos)
	if path := strings.TrimSpace(target.DefaultPaths[hostKey]); path != "" {
		resolved, err := RenderInstallTargetPath(path, appID, roots)
		if err != nil {
			return "", "", false, fmt.Errorf("install.target.default_paths.%s: %w", hostKey, err)
		}
		if !installTargetPathIsAbs(resolved, goos) {
			return "", "", false, fmt.Errorf("install.target.default_paths.%s must resolve to an absolute path: %s", hostKey, resolved)
		}
		return resolved, "install.target.default_paths." + hostKey, true, nil
	}
	if path := strings.TrimSpace(target.DefaultPath); path != "" {
		resolved, err := RenderInstallTargetPath(path, appID, roots)
		if err != nil {
			return "", "", false, fmt.Errorf("install.target.default_path: %w", err)
		}
		if !installTargetPathIsAbs(resolved, goos) {
			return "", "", false, fmt.Errorf("install.target.default_path must resolve to an absolute path on %s: %s", InstallTargetHostKey(goos), resolved)
		}
		return resolved, "install.target.default_path", true, nil
	}
	return "", "", false, nil
}

func RenderInstallTargetPath(template string, appID string, roots InstallTargetRoots) (string, error) {
	var firstError error
	rendered := installTargetTemplatePattern.ReplaceAllStringFunc(template, func(match string) string {
		if firstError != nil {
			return match
		}
		submatches := installTargetTemplatePattern.FindStringSubmatch(match)
		if len(submatches) != 2 {
			firstError = fmt.Errorf("contains unsupported template expression: %s", match)
			return match
		}
		value, ok := installTargetTemplateValue(strings.TrimSpace(submatches[1]), appID, roots)
		if !ok {
			firstError = fmt.Errorf("contains unsupported template expression: %s", match)
			return match
		}
		if strings.TrimSpace(value) == "" {
			firstError = fmt.Errorf("template expression has no value: %s", match)
			return match
		}
		return value
	})
	if firstError != nil {
		return "", firstError
	}
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		return "", fmt.Errorf("contains unsupported template expression: %s", template)
	}
	return rendered, nil
}

func installTargetTemplateValue(name string, appID string, roots InstallTargetRoots) (string, bool) {
	switch name {
	case "app.id":
		return appID, true
	case "user.home":
		return roots.UserHome, true
	case "user.data":
		return roots.UserData, true
	case "user.local_data":
		return roots.UserLocalData, true
	case "system.data":
		return roots.SystemData, true
	case "reploy.install_root":
		return roots.ReployInstallRoot, true
	default:
		return "", false
	}
}

func validateInstallTargetConfig(target InstallTargetConfig, goos string) error {
	if strings.TrimSpace(target.DefaultPath) != "" {
		if err := validateInstallTargetPathSyntax("install.target.default_path", target.DefaultPath); err != nil {
			return err
		}
	}
	for osName, path := range target.DefaultPaths {
		if !supportedInstallTargetOS(osName) {
			return fmt.Errorf("install.target.default_paths contains unsupported OS: %s", osName)
		}
		if err := validateInstallTargetPathSyntax("install.target.default_paths."+osName, path); err != nil {
			return err
		}
	}
	_, _, _, err := ResolveInstallTargetDefault(target, "app", goos, sampleInstallTargetRoots(goos))
	return err
}

func validateInstallTargetPathSyntax(field string, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if containsLineOrFieldBreak(path) {
		return fmt.Errorf("%s must not contain tabs or newlines", field)
	}
	if _, err := RenderInstallTargetPath(path, "app", sampleInstallTargetRoots("linux")); err != nil {
		return fmt.Errorf("%s %w", field, err)
	}
	return nil
}

func supportedInstallTargetOS(osName string) bool {
	switch osName {
	case "linux", "macos", "windows":
		return true
	default:
		return false
	}
}

func sampleInstallTargetRoots(goos string) InstallTargetRoots {
	switch InstallTargetHostKey(goos) {
	case "windows":
		return InstallTargetRoots{
			UserHome:          `C:\Users\app`,
			UserData:          `C:\Users\app\AppData\Roaming`,
			UserLocalData:     `C:\Users\app\AppData\Local`,
			SystemData:        `C:\ProgramData`,
			ReployInstallRoot: `C:\Users\app\AppData\Local\Reploy\installs`,
		}
	case "macos":
		return InstallTargetRoots{
			UserHome:          "/Users/app",
			UserData:          "/Users/app/Library/Application Support",
			UserLocalData:     "/Users/app/Library/Application Support",
			SystemData:        "/Library/Application Support",
			ReployInstallRoot: "/Users/app/Library/Application Support/Reploy/installs",
		}
	default:
		return InstallTargetRoots{
			UserHome:          "/home/app",
			UserData:          "/home/app/.local/share",
			UserLocalData:     "/home/app/.local/share",
			SystemData:        "/var/lib",
			ReployInstallRoot: "/opt",
		}
	}
}

func installTargetPathIsAbs(path string, goos string) bool {
	if InstallTargetHostKey(goos) != "windows" {
		return pathpkg.IsAbs(path)
	}
	if len(path) >= 3 && path[1] == ':' && isWindowsPathSeparator(path[2]) {
		return true
	}
	return len(path) >= 2 && isWindowsPathSeparator(path[0]) && isWindowsPathSeparator(path[1])
}

func isWindowsPathSeparator(char byte) bool {
	return char == '\\' || char == '/'
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
	if containsLineOrFieldBreak(owner.OnMissing) {
		return fmt.Errorf("install.owner.on_missing must not contain tabs or newlines")
	}
	if containsLineOrFieldBreak(owner.Windows.Account) {
		return fmt.Errorf("install.owner.windows.account must not contain tabs or newlines")
	}
	switch owner.OnMissing {
	case "create", "fail":
	default:
		return fmt.Errorf("install.owner.on_missing must be create or fail")
	}
	if owner.OnMissing == "create" && (installOwnerPartIsNumeric(owner.User) || installOwnerPartIsNumeric(owner.Group)) {
		return fmt.Errorf("install.owner.on_missing=create requires named user and group")
	}
	if owner.OnMissing == "create" && !IsInstallSystemAccountName(owner.User) {
		return fmt.Errorf("install.owner.user must be a safe system account name when install.owner.on_missing=create")
	}
	if owner.OnMissing == "create" && !IsInstallSystemAccountName(owner.Group) {
		return fmt.Errorf("install.owner.group must be a safe system account name when install.owner.on_missing=create")
	}
	if owner.User == "root" || owner.User == "0" {
		return fmt.Errorf("install.owner.user must not be root")
	}
	if owner.Group == "root" || owner.Group == "0" {
		return fmt.Errorf("install.owner.group must not be root")
	}
	return nil
}

func installOwnerPartIsNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func IsInstallSystemAccountName(value string) bool {
	if value == "" {
		return false
	}
	for index, r := range value {
		switch {
		case index == 0:
			if r < 'a' || r > 'z' {
				return r == '_'
			}
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_', r == '-':
		case r == '$':
			return index == len(value)-1
		default:
			return false
		}
	}
	return true
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

func cleanManifestPath(path string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(path))))
}

func normalizeAndValidateManagedPaths(managedPaths *InstallManagedPathsConfig) error {
	seenPaths := map[string]string{}
	seenMounts := map[string]string{}
	if err := normalizeAndValidateManagedPathEntries("files", managedPaths.Files, seenPaths, seenMounts); err != nil {
		return err
	}
	if err := normalizeAndValidateManagedPathEntries("dirs", managedPaths.Dirs, seenPaths, seenMounts); err != nil {
		return err
	}
	return nil
}

func normalizeAndValidateManagedPathEntries(kind string, entries []InstallManagedPathConfig, seenPaths map[string]string, seenMounts map[string]string) error {
	for index, entry := range entries {
		field := fmt.Sprintf("install.managed_paths.%s[%d]", kind, index)
		entry.Path = cleanManifestPath(entry.Path)
		entry.Update = strings.TrimSpace(entry.Update)
		entry.Mount = strings.TrimSpace(entry.Mount)
		if entry.Path == "." {
			return fmt.Errorf("%s.path must not be empty", field)
		}
		if containsLineOrFieldBreak(entry.Path) {
			return fmt.Errorf("%s.path must not contain tabs or newlines", field)
		}
		if err := validateRelativeBlueprintPath(entry.Path); err != nil {
			return fmt.Errorf("%s.path: %w", field, err)
		}
		if entry.Path == ".reploy" || strings.HasPrefix(filepath.ToSlash(entry.Path), ".reploy/") {
			return fmt.Errorf("%s.path must not include .reploy; .reploy is Reploy-owned", field)
		}
		switch entry.Update {
		case "preserve", "replace":
		default:
			return fmt.Errorf("%s.update must be preserve or replace", field)
		}
		if containsLineOrFieldBreak(entry.Mount) {
			return fmt.Errorf("%s.mount must not contain tabs or newlines", field)
		}
		renderedMount, err := renderManagedPathMount(entry.Mount, entry.Path)
		if err != nil {
			return fmt.Errorf("%s.mount: %w", field, err)
		}
		entry.Mount, err = normalizeAndValidateManagedPathMount(field, renderedMount)
		if err != nil {
			return err
		}
		if entry.Mount != "" && strings.Contains(entry.Path, ":") {
			return fmt.Errorf("%s.path must not contain ':' when mount is set", field)
		}
		if previous, ok := seenPaths[entry.Path]; ok {
			return fmt.Errorf("%s.path duplicates %s.path: %s", field, previous, entry.Path)
		}
		for previousPath, previousField := range seenPaths {
			if managedPathOverlaps(previousPath, entry.Path) {
				return fmt.Errorf("%s.path overlaps %s.path: %s overlaps %s", field, previousField, entry.Path, previousPath)
			}
		}
		if entry.Mount != "" {
			if previous, ok := seenMounts[entry.Mount]; ok {
				return fmt.Errorf("%s.mount duplicates %s.mount: %s", field, previous, entry.Mount)
			}
			seenMounts[entry.Mount] = field
		}
		seenPaths[entry.Path] = field
		entries[index] = entry
	}
	return nil
}

func normalizeAndValidateManagedPathMount(field string, mount string) (string, error) {
	if mount == "" {
		return "", nil
	}
	if strings.Contains(mount, ":") {
		return "", fmt.Errorf("%s.mount must not contain ':'", field)
	}
	if !strings.HasPrefix(mount, "/") {
		return "", fmt.Errorf("%s.mount must start with /", field)
	}
	for _, segment := range strings.Split(mount, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%s.mount must not contain .. path segments", field)
		}
	}
	cleaned := pathpkg.Clean(mount)
	if cleaned == "/" {
		return "", fmt.Errorf("%s.mount must not be /", field)
	}
	return cleaned, nil
}

func managedPathOverlaps(left string, right string) bool {
	return strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func renderManagedPathMount(mount string, path string) (string, error) {
	if mount == "" {
		return "", nil
	}
	rendered := strings.ReplaceAll(mount, "{{ path }}", path)
	rendered = strings.ReplaceAll(rendered, "{{path}}", path)
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		return "", fmt.Errorf("contains unsupported template expression: %s", mount)
	}
	return rendered, nil
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
	DeploymentDirs  DockerDeploymentDirs              `yaml:"deployment_dirs"`
	Service         rawDockerServiceConfig            `yaml:"service"`
	Ports           map[string]any                    `yaml:"ports"`
	Install         DockerInstallConfig               `yaml:"install"`
	Environment     map[string]string                 `yaml:"environment"`
	Runtime         DockerRuntimeConfig               `yaml:"runtime"`
	DefaultCommand  string                            `yaml:"default_command"`
	CommandDefaults rawDockerCommandDefaultsConfig    `yaml:"command_defaults"`
	Health          DockerHealthConfig                `yaml:"health"`
	Commands        map[string]rawDockerCommandConfig `yaml:"commands"`
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

func parseDockerCommands(commands map[string]rawDockerCommandConfig, defaults rawDockerCommandDefaultsConfig, defaultCommand string) []DockerCommandConfig {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]DockerCommandConfig, 0, len(names))
	for _, name := range names {
		command := normalizeDockerCommand(name, commands[name], defaults, defaultCommand)
		result = append(result, command)
	}
	return result
}

func normalizeDockerCommand(name string, raw rawDockerCommandConfig, defaults rawDockerCommandDefaultsConfig, defaultCommand string) DockerCommandConfig {
	command := DockerCommandConfig{
		Name:         name,
		Trigger:      raw.Trigger,
		AppCommand:   boolDefault(raw.AppCommand, defaults.AppCommand),
		Deployed:     boolDefault(raw.Deployed, defaults.Deployed),
		ForwardArgs:  raw.ForwardArgs,
		ForwardFlags: raw.ForwardFlags,
		Container: AppCommandConfig{
			Argv: effectiveDockerCommandArgv(raw.Container, defaults.Container),
		},
	}
	if len(command.Trigger) == 0 && name != defaultCommand {
		command.Trigger = strings.Split(name, "_")
	}
	return command
}

func boolDefault(value *bool, defaultValue *bool) bool {
	if value != nil {
		return *value
	}
	if defaultValue != nil {
		return *defaultValue
	}
	return false
}

func effectiveDockerCommandArgv(command rawAppCommandConfig, defaults rawAppCommandConfig) []string {
	if len(command.Argv) > 0 {
		return command.Argv
	}
	prefix := command.ArgvPrefix
	if len(prefix) == 0 {
		prefix = defaults.ArgvPrefix
	}
	argv := make([]string, 0, len(prefix)+len(command.ArgvSuffix))
	argv = append(argv, prefix...)
	argv = append(argv, command.ArgvSuffix...)
	return argv
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

func validateDeploymentDir(relativePath string, example string) error {
	if filepath.IsAbs(relativePath) {
		return fmt.Errorf("must be a relative path under the deployment root, got %q", relativePath)
	}
	clean := filepath.Clean(relativePath)
	if clean == "." {
		return fmt.Errorf("must name a subdirectory under the deployment root, not %q; use a value like %q", relativePath, example)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must stay inside the deployment root, got %q", relativePath)
	}
	return validateRelativeBlueprintPath(relativePath)
}
