package deploy

import (
	"fmt"
	pathpkg "path"
	"sort"

	blueprintmodel "github.com/omry/reploy/internal/blueprint"
	"gopkg.in/yaml.v3"
)

func parseAnyPackManifest(content []byte, blueprintDir string) (PackManifest, *blueprintmodel.Document, error) {
	var shape struct {
		Environment yaml.Node `yaml:"environment"`
	}
	if err := yaml.Unmarshal(content, &shape); err != nil {
		return PackManifest{}, nil, err
	}
	if shape.Environment.Kind == 0 {
		legacy, err := ParsePackManifest(string(content))
		if err != nil {
			return PackManifest{}, nil, err
		}
		return legacy, nil, nil
	}
	source, err := blueprintmodel.Decode(content)
	if err != nil {
		return PackManifest{}, nil, err
	}
	document, err := blueprintmodel.Resolve(source)
	if err != nil {
		return PackManifest{}, nil, err
	}
	manifest, err := projectEnvironmentManifest(document, blueprintDir)
	if err != nil {
		return PackManifest{}, nil, err
	}
	return manifest, &document, nil
}

// projectEnvironmentManifest is a temporary compatibility boundary for
// retained deployment UX while callers move from AppPack to resolved plans.
func projectEnvironmentManifest(document blueprintmodel.Document, _ string) (PackManifest, error) {
	manifest := PackManifest{
		Pack:   PackMetadata{Schema: document.Blueprint.Schema, Version: document.Blueprint.Version, RequiresReploy: document.Blueprint.RequiresReploy},
		App:    AppPackConfig{ID: document.Environment.ID, Terminal: AppTerminalConfig{ColorEnv: document.Environment.Terminal.ColorEnv}},
		Bundle: BundlePackConfig{Options: map[string]BundleOptionConfig{}},
		Docker: DockerPackConfig{
			DeploymentDirs: DockerDeploymentDirs{Config: "conf", Bundle: ".reploy/bundle", Data: "data"},
			Service:        DockerServiceConfig{Image: document.Docker.Image},
			Environment:    map[string]string{},
		},
	}
	manifest.Install.Target = InstallTargetConfig{DefaultPath: document.Environment.Install.Target.DefaultPath, DefaultPaths: cloneStringMap(document.Environment.Install.Target.DefaultPaths)}
	manifest.Install.System.RunAs = InstallOwnerConfig{
		User: document.Environment.Install.System.RunAs.User, Group: document.Environment.Install.System.RunAs.Group,
		OnMissing: document.Environment.Install.System.RunAs.OnMissing,
	}
	componentName, component, err := projectedApplicationComponent(document)
	if err != nil {
		return PackManifest{}, err
	}
	manifest.App.Provider.Type = string(component.Type)
	manifest.App.Provider.Identifier = component.Requirements[0]
	manifest.App.Provider.LocalSources = projectedLocalSources(document)
	_ = componentName

	for name, item := range document.Environment.Components {
		if item.Optional == nil {
			continue
		}
		manifest.Bundle.Options[name] = BundleOptionConfig{
			Identifier: item.Requirements[0], Group: item.Optional.Group, Description: item.Optional.Description,
		}
	}
	manifest.Install.Ports.Staging = map[string]InstallPortConfig{}
	manifest.Install.Ports.Deployed = map[string]InstallPortConfig{}
	if document.Docker.Workload != nil {
		for name, endpoint := range document.Docker.Workload.Endpoints {
			config := InstallPortConfig{HostBind: endpoint.Publish.Address, ContainerPort: endpoint.Endpoint.Port}
			staging := config
			staging.HostPort = endpoint.Publish.Staging
			deployed := config
			deployed.HostPort = endpoint.Publish.Deployed
			manifest.Install.Ports.Staging[name] = staging
			manifest.Install.Ports.Deployed[name] = deployed
			if manifest.Docker.Service.ContainerHost == "" {
				manifest.Docker.Service.ContainerHost = endpoint.Bind.Address
			}
			if endpoint.Endpoint.Readiness != nil && manifest.Docker.Health.Path == "" {
				manifest.Docker.Health = DockerHealthConfig{
					DefaultScheme: endpoint.Endpoint.Scheme, DefaultHost: endpoint.Publish.Address,
					DefaultPort: fmt.Sprint(endpoint.Publish.Staging), Path: endpoint.Endpoint.Readiness.Path,
					TLSVerify: boolPointer(endpoint.Endpoint.Readiness.TLSVerify),
				}
			}
		}
	}
	for name, mount := range document.Docker.Mounts {
		if mount.Mode != blueprintmodel.MountManagedBind {
			continue
		}
		writable := mount.Path.Writable
		manifest.Install.ManagedPaths.Dirs = append(manifest.Install.ManagedPaths.Dirs, InstallManagedPathConfig{
			Path: mount.Source, Update: string(mount.Path.Update), Mount: mount.Path.Container, Writeable: &writable,
		})
		switch name {
		case "config":
			manifest.Docker.DeploymentDirs.Config = mount.Source
		case "data":
			manifest.Docker.DeploymentDirs.Data = mount.Source
		}
	}
	manifest.Docker.Commands = projectedCommands(document)
	if document.Environment.Workload != nil {
		manifest.Docker.DefaultCommand = document.Environment.Workload.Command
		manifest.Docker.Install.Hooks.BeforeStart = projectedInstallHooks(document.Environment.Workload.Runtime.BeforeStart)
		manifest.Docker.Install.Hooks.AfterStart = projectedInstallHooks(document.Environment.Workload.Runtime.AfterStart)
	}
	return manifest, nil
}

func projectedInstallHooks(steps []blueprintmodel.Step) []DockerInstallHookConfig {
	result := []DockerInstallHookConfig{}
	for _, step := range steps {
		for range step.Requires.Endpoints {
			result = append(result, DockerInstallHookConfig{HealthCheck: &DockerInstallHealthCheckConfig{Wait: true}})
		}
		for _, action := range step.Actions {
			result = append(result, DockerInstallHookConfig{App: append([]string(nil), action.Environment...)})
		}
	}
	return result
}

func projectedApplicationComponent(document blueprintmodel.Document) (string, blueprintmodel.Component, error) {
	if component, ok := document.Environment.Components["application"]; ok && component.Optional == nil {
		return "application", component, nil
	}
	names := make([]string, 0, len(document.Environment.Components))
	for name, component := range document.Environment.Components {
		if component.Optional == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", blueprintmodel.Component{}, fmt.Errorf("environment has no required component")
	}
	return names[0], document.Environment.Components[names[0]], nil
}

func projectedLocalSources(document blueprintmodel.Document) map[string]string {
	result := map[string]string{}
	for _, translation := range document.Environment.Translations {
		if translation.Type != blueprintmodel.ComponentTypePython {
			continue
		}
		for name, relative := range translation.Mappings {
			result[name] = pathpkg.Clean(pathpkg.Join(translation.Root, relative))
		}
	}
	return result
}

func projectedCommands(document blueprintmodel.Document) []DockerCommandConfig {
	names := make([]string, 0, len(document.Environment.Commands))
	for name := range document.Environment.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]DockerCommandConfig, 0, len(names))
	for _, name := range names {
		command := document.Environment.Commands[name]
		executable := document.Environment.Executables[command.Executable]
		argv := []string{executable.Binary}
		argv = append(argv, executable.ArgvPrefix...)
		argv = append(argv, command.Argv...)
		argv = append(argv, executable.ArgvSuffix...)
		result = append(result, DockerCommandConfig{
			Name: name, Trigger: append([]string(nil), command.Trigger...), AppCommand: command.NativeCommand,
			Deployed: command.DeployedCommand, ForwardArgs: true, ForwardFlags: append([]string(nil), command.ForwardFlags...),
			Container: AppCommandConfig{Argv: argv},
		})
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func boolPointer(value bool) *bool { return &value }
