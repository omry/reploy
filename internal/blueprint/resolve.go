package blueprint

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

var builtInControlOperations = map[string]bool{
	"up": true, "down": true, "restart": true, "status": true, "logs": true,
	"enable": true, "disable": true,
}

func Resolve(source Syntax) (Document, error) {
	variables, err := resolveVariables(source.Environment.Vars)
	if err != nil {
		return Document{}, err
	}
	source, err = resolveSyntaxVariables(source, variables)
	if err != nil {
		return Document{}, err
	}
	id, controlScript, err := resolveNames(source.Environment)
	if err != nil {
		return Document{}, err
	}
	extended, err := resolveExtends(source)
	if err != nil {
		return Document{}, err
	}

	document := Document{
		Blueprint: Metadata{
			Schema:         source.Blueprint.Schema,
			Version:        strings.TrimSpace(source.Blueprint.Version),
			RequiresReploy: strings.TrimSpace(source.Blueprint.RequiresReploy),
		},
		Environment: Environment{
			ID:            id,
			ControlScript: controlScript,
			Vars:          variables,
			Translations:  map[string]Translation{},
			Components:    map[string]Component{},
			Terminal:      Terminal{ColorEnv: strings.TrimSpace(source.Environment.Terminal.ColorEnv)},
			Install:       resolveInstallSyntax(source.Environment.Install, variables),
			Paths:         map[string]Path{},
			Executables:   map[string]Executable{},
			Commands:      map[string]Command{},
		},
		Docker: Docker{
			Image:  strings.TrimSpace(source.Docker.Image),
			Mounts: map[string]DockerMount{},
		},
	}
	if document.Blueprint.Version == "" {
		return Document{}, fmt.Errorf("blueprint.version is required")
	}
	if document.Docker.Image == "" {
		return Document{}, fmt.Errorf("docker.image is required")
	}

	if err := resolveTranslations(source, &document); err != nil {
		return Document{}, err
	}
	if err := resolveComponents(source, &document); err != nil {
		return Document{}, err
	}
	if err := resolvePathsAndMounts(source, extended, &document); err != nil {
		return Document{}, err
	}
	if err := resolveExecutablesAndCommands(source, &document); err != nil {
		return Document{}, err
	}
	if err := resolveWorkloads(source, extended, &document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func resolveTranslations(source Syntax, document *Document) error {
	for _, name := range sortedKeys(source.Environment.Translations) {
		item := source.Environment.Translations[name]
		if err := validateObjectName("environment.translations", name); err != nil {
			return err
		}
		if item.Type != string(ComponentTypePython) {
			return fmt.Errorf("environment.translations.%s.type must be python", name)
		}
		if item.Scope != string(TranslationScopeDevelopment) {
			return fmt.Errorf("environment.translations.%s.scope must be development", name)
		}
		root, err := resolveStaticString(item.Root, document.Environment.Vars)
		if err != nil {
			return fmt.Errorf("environment.translations.%s.root: %w", name, err)
		}
		if strings.TrimSpace(root) == "" {
			return fmt.Errorf("environment.translations.%s.root is required", name)
		}
		mappings := map[string]string{}
		for distribution, relative := range item.Mappings {
			if strings.TrimSpace(distribution) == "" || strings.TrimSpace(relative) == "" {
				return fmt.Errorf("environment.translations.%s.mappings must not contain empty names or paths", name)
			}
			if path.IsAbs(relative) || path.Clean(relative) == ".." || strings.HasPrefix(path.Clean(relative), "../") {
				return fmt.Errorf("environment.translations.%s.mappings.%s must stay within root", name, distribution)
			}
			mappings[distribution] = path.Clean(relative)
		}
		document.Environment.Translations[name] = Translation{
			Type: ComponentTypePython, Scope: TranslationScopeDevelopment, Root: root, Mappings: mappings,
		}
	}
	return nil
}

func resolveComponents(source Syntax, document *Document) error {
	for _, name := range sortedKeys(source.Environment.Components) {
		item := source.Environment.Components[name]
		if err := validateObjectName("environment.components", name); err != nil {
			return err
		}
		if item.Type != string(ComponentTypePython) {
			return fmt.Errorf("environment.components.%s.type must be python in the initial implementation", name)
		}
		if len(item.Requirements) == 0 {
			return fmt.Errorf("environment.components.%s.requirements must not be empty", name)
		}
		component := Component{Type: ComponentTypePython, Requirements: append([]string(nil), item.Requirements...)}
		if item.Optional != nil {
			component.Optional = &OptionalComponent{
				Group: strings.TrimSpace(item.Optional.Group), Description: strings.TrimSpace(item.Optional.Description),
			}
			if component.Optional.Description == "" {
				return fmt.Errorf("environment.components.%s.optional.description is required", name)
			}
		}
		document.Environment.Components[name] = component
	}
	if len(document.Environment.Components) == 0 {
		return fmt.Errorf("environment.components must not be empty")
	}
	return nil
}

func resolvePathsAndMounts(source Syntax, extended extendedSyntax, document *Document) error {
	for _, name := range sortedKeys(source.Environment.Paths) {
		item := source.Environment.Paths[name]
		if err := validateObjectName("environment.paths", name); err != nil {
			return err
		}
		container, err := resolveStaticString(item.Container, document.Environment.Vars)
		if err != nil {
			return fmt.Errorf("environment.paths.%s.container: %w", name, err)
		}
		if !strings.HasPrefix(container, "/") {
			return fmt.Errorf("environment.paths.%s.container must be absolute", name)
		}
		update := UpdatePolicy(item.Update)
		if update != UpdatePreserve && update != UpdateReplace && update != UpdateUnmanaged {
			return fmt.Errorf("environment.paths.%s.update must be preserve, replace, or unmanaged", name)
		}
		writable, err := resolveSyntaxBool(item.Writable, "environment.paths."+name+".writable")
		if err != nil {
			return err
		}
		document.Environment.Paths[name] = Path{Container: container, Writable: writable, Update: update}
	}
	pathReferences := map[string]int{}
	for _, name := range sortedKeys(extended.Mounts) {
		item := extended.Mounts[name]
		pathName, _ := referencedName("extends", item.Docker.Extends, environmentPathReferencePrefix)
		pathReferences[pathName]++
		resolvedPath := document.Environment.Paths[pathName]
		mount := DockerMount{
			Extends: item.Docker.Extends,
			Mode:    MountMode(item.Docker.Mode),
			Source:  strings.TrimSpace(item.Docker.Source),
			Name:    strings.TrimSpace(item.Docker.Name),
			Path:    resolvedPath,
		}
		if err := validateMount("docker.mounts."+name, mount); err != nil {
			return err
		}
		document.Docker.Mounts[name] = mount
	}
	for _, name := range sortedKeys(document.Environment.Paths) {
		if pathReferences[name] != 1 {
			return fmt.Errorf("environment path %q must have exactly one Docker mount; found %d", name, pathReferences[name])
		}
	}
	return nil
}

func validateMount(field string, mount DockerMount) error {
	switch mount.Mode {
	case MountManagedBind:
		cleanSource := path.Clean(mount.Source)
		if mount.Path.Update == UpdateUnmanaged || mount.Source == "" || path.IsAbs(mount.Source) || cleanSource == ".." || strings.HasPrefix(cleanSource, "../") {
			return fmt.Errorf("%s managed-bind requires managed update policy and relative source", field)
		}
	case MountVolume:
		if mount.Path.Update == UpdateUnmanaged || mount.Name == "" {
			return fmt.Errorf("%s volume requires managed update policy and name", field)
		}
	case MountBind:
		if mount.Path.Update != UpdateUnmanaged || mount.Source == "" {
			return fmt.Errorf("%s bind requires update: unmanaged and source", field)
		}
		if !containsInterpolation(mount.Source) && !isAnyAbsolutePath(mount.Source) {
			return fmt.Errorf("%s bind source must be absolute", field)
		}
	case MountTmpfs:
		if mount.Path.Update == UpdateUnmanaged {
			return fmt.Errorf("%s tmpfs does not support update: unmanaged", field)
		}
	default:
		return fmt.Errorf("%s.mode is invalid: %s", field, mount.Mode)
	}
	return nil
}

func resolveExecutablesAndCommands(source Syntax, document *Document) error {
	for _, name := range sortedKeys(source.Environment.Executables) {
		item := source.Environment.Executables[name]
		if _, ok := document.Environment.Components[item.Component]; !ok {
			return fmt.Errorf("environment.executables.%s references missing component %q", name, item.Component)
		}
		order, err := resolveOrder(item.Order)
		if err != nil {
			return fmt.Errorf("environment.executables.%s.order: %w", name, err)
		}
		binary := strings.TrimSpace(item.Binary)
		if binary == "" {
			return fmt.Errorf("environment.executables.%s.binary is required", name)
		}
		document.Environment.Executables[name] = Executable{
			Component: item.Component, Binary: binary, Order: order,
			ArgvPrefix: append([]string(nil), item.ArgvPrefix...), ArgvSuffix: append([]string(nil), item.ArgvSuffix...),
		}
	}
	triggerOwner := map[string]string{}
	for _, name := range sortedKeys(source.Environment.Commands) {
		item := source.Environment.Commands[name]
		executable, ok := document.Environment.Executables[item.Executable]
		if !ok {
			return fmt.Errorf("environment.commands.%s references missing executable %q", name, item.Executable)
		}
		order := append([]ArgumentSegment(nil), executable.Order...)
		if len(item.Order) > 0 {
			var err error
			order, err = resolveOrder(item.Order)
			if err != nil {
				return fmt.Errorf("environment.commands.%s.order: %w", name, err)
			}
		}
		native, err := resolveSyntaxBool(item.NativeCommand, "environment.commands."+name+".native_command")
		if err != nil {
			return err
		}
		deployed, err := resolveSyntaxBool(item.DeployedCommand, "environment.commands."+name+".deployed_command")
		if err != nil {
			return err
		}
		if deployed && !native {
			return fmt.Errorf("environment.commands.%s.deployed_command requires native_command", name)
		}
		triggerKey := strings.Join(item.Trigger, "\x00")
		if native && len(item.Trigger) == 0 {
			return fmt.Errorf("environment.commands.%s.trigger is required for a native command", name)
		}
		if len(item.Trigger) > 0 {
			if owner, exists := triggerOwner[triggerKey]; exists {
				return fmt.Errorf("environment.commands.%s duplicates trigger owned by %s", name, owner)
			}
			if builtInControlOperations[item.Trigger[0]] {
				return fmt.Errorf("environment.commands.%s trigger collides with built-in operation %q", name, item.Trigger[0])
			}
			triggerOwner[triggerKey] = name
		}
		document.Environment.Commands[name] = Command{
			Executable: item.Executable, Trigger: append([]string(nil), item.Trigger...),
			NativeCommand: native, DeployedCommand: deployed,
			ForwardFlags: append([]string(nil), item.ForwardFlags...), Argv: append([]string(nil), item.Argv...), Order: order,
		}
	}
	return nil
}

func resolveWorkloads(source Syntax, extended extendedSyntax, document *Document) error {
	if source.Environment.Workload == nil {
		if source.Docker.Workload != nil {
			return fmt.Errorf("docker.workload requires environment.workload")
		}
		return nil
	}
	command, ok := document.Environment.Commands[source.Environment.Workload.Command]
	if !ok {
		return fmt.Errorf("environment.workload.command references missing command %q", source.Environment.Workload.Command)
	}
	_ = command
	workload := Workload{Command: source.Environment.Workload.Command, Endpoints: map[string]Endpoint{}}
	for _, name := range sortedKeys(source.Environment.Workload.Endpoints) {
		endpoint, err := resolveEndpoint("environment.workload.endpoints."+name, source.Environment.Workload.Endpoints[name])
		if err != nil {
			return err
		}
		workload.Endpoints[name] = endpoint
	}
	workload.Runtime = resolveRuntimeEvents(source.Environment.Workload.Runtime)
	document.Environment.Workload = &workload

	if source.Docker.Workload == nil {
		return fmt.Errorf("environment.workload requires docker.workload")
	}
	dockerWorkload := DockerWorkload{Restart: strings.TrimSpace(source.Docker.Workload.Restart), Endpoints: map[string]DockerEndpoint{}}
	endpointReferences := map[string]int{}
	for _, name := range sortedKeys(extended.Endpoints) {
		item := extended.Endpoints[name]
		endpointName, _ := referencedName("extends", item.Docker.Extends, environmentEndpointReferencePrefix)
		endpointReferences[endpointName]++
		resolvedEndpoint := workload.Endpoints[endpointName]
		stagingPort, err := resolveSyntaxInt(item.Docker.Publish.Staging, "docker.workload.endpoints."+name+".publish.staging")
		if err != nil {
			return err
		}
		deployedPort, err := resolveSyntaxInt(item.Docker.Publish.Deployed, "docker.workload.endpoints."+name+".publish.deployed")
		if err != nil {
			return err
		}
		if stagingPort < 1 || stagingPort > 65535 || deployedPort < 1 || deployedPort > 65535 {
			return fmt.Errorf("docker.workload.endpoints.%s published ports must be between 1 and 65535", name)
		}
		dockerWorkload.Endpoints[name] = DockerEndpoint{
			Extends:  item.Docker.Extends,
			Bind:     Bind{Address: strings.TrimSpace(item.Docker.Bind.Address)},
			Publish:  Publication{Address: strings.TrimSpace(item.Docker.Publish.Address), Staging: stagingPort, Deployed: deployedPort},
			Endpoint: resolvedEndpoint,
		}
	}
	for _, name := range sortedKeys(workload.Endpoints) {
		if endpointReferences[name] != 1 {
			return fmt.Errorf("workload endpoint %q must have exactly one Docker endpoint; found %d", name, endpointReferences[name])
		}
	}
	document.Docker.Workload = &dockerWorkload
	return nil
}

func resolveEndpoint(field string, item EndpointSyntax) (Endpoint, error) {
	port, err := resolveSyntaxInt(item.Port, field+".port")
	if err != nil {
		return Endpoint{}, err
	}
	if port < 1 || port > 65535 {
		return Endpoint{}, fmt.Errorf("%s.port must be between 1 and 65535", field)
	}
	endpoint := Endpoint{Scheme: strings.TrimSpace(item.Scheme), Port: port}
	if endpoint.Scheme == "" {
		return Endpoint{}, fmt.Errorf("%s.scheme is required", field)
	}
	if item.Readiness != nil {
		if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
			return Endpoint{}, fmt.Errorf("%s readiness requires http or https scheme", field)
		}
		if !strings.HasPrefix(item.Readiness.Path, "/") {
			return Endpoint{}, fmt.Errorf("%s.readiness.path must begin with /", field)
		}
		timeout, err := resolveDuration(item.Readiness.Timeout, DefaultReadinessTimeout)
		if err != nil {
			return Endpoint{}, fmt.Errorf("%s.readiness.timeout: %w", field, err)
		}
		interval, err := resolveDuration(item.Readiness.Interval, DefaultReadinessInterval)
		if err != nil {
			return Endpoint{}, fmt.Errorf("%s.readiness.interval: %w", field, err)
		}
		tlsVerify, err := resolveSyntaxBool(item.Readiness.TLSVerify, field+".readiness.tls_verify")
		if err != nil {
			return Endpoint{}, err
		}
		endpoint.Readiness = &Readiness{Path: item.Readiness.Path, Timeout: timeout, Interval: interval, TLSVerify: tlsVerify}
	}
	return endpoint, nil
}

func resolveSyntaxInt(value any, field string) (int, error) {
	resolved, ok := value.(int)
	if !ok {
		return 0, fmt.Errorf("%s must resolve to an integer, got %T", field, value)
	}
	return resolved, nil
}

func resolveSyntaxBool(value any, field string) (bool, error) {
	if value == nil {
		return false, nil
	}
	resolved, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must resolve to a boolean, got %T", field, value)
	}
	return resolved, nil
}

func resolveDuration(value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("must be a positive duration")
	}
	return duration, nil
}

func resolveOrder(values []string) ([]ArgumentSegment, error) {
	if len(values) == 0 {
		return append([]ArgumentSegment(nil), DefaultArgumentOrder...), nil
	}
	result := make([]ArgumentSegment, len(values))
	seen := map[ArgumentSegment]bool{}
	for index, value := range values {
		segment := ArgumentSegment(value)
		switch segment {
		case ArgumentBinary, ArgumentPrefix, ArgumentCommand, ArgumentForwarded, ArgumentSuffix:
		default:
			return nil, fmt.Errorf("unknown segment %q", value)
		}
		if seen[segment] {
			return nil, fmt.Errorf("segment %q appears more than once", value)
		}
		seen[segment] = true
		result[index] = segment
	}
	if len(result) == 0 || result[0] != ArgumentBinary || !seen[ArgumentBinary] {
		return nil, fmt.Errorf("binary must appear exactly once and first")
	}
	return result, nil
}

func resolveInstallSyntax(item InstallSyntax, variables map[string]any) Install {
	return Install{
		Target:       InstallTarget{DefaultPath: item.Target.DefaultPath, DefaultPaths: cloneMap(item.Target.DefaultPaths)},
		System:       SystemInstall{RunAs: RunAs{User: item.System.RunAs.User, Group: item.System.RunAs.Group, OnMissing: item.System.RunAs.OnMissing}},
		AfterInstall: resolveSteps(item.AfterInstall),
		Success:      InstallSuccess{Lines: append([]string(nil), item.Success.Lines...)},
	}
}

func resolveRuntimeEvents(item RuntimeEventsSyntax) RuntimeEvents {
	return RuntimeEvents{BeforeStart: resolveSteps(item.BeforeStart), AfterStart: resolveSteps(item.AfterStart), BeforeStop: resolveSteps(item.BeforeStop), AfterStop: resolveSteps(item.AfterStop)}
}

func resolveSteps(items []StepSyntax) []Step {
	result := make([]Step, len(items))
	for index, item := range items {
		actions := make([]Action, len(item.Actions))
		for actionIndex, action := range item.Actions {
			actions[actionIndex] = Action{Environment: append([]string(nil), action.Environment...)}
		}
		result[index] = Step{Requires: Requirements{Endpoints: append([]string(nil), item.Requires.Endpoints...)}, Actions: actions}
	}
	return result
}

func resolveStaticString(value string, variables map[string]any) (string, error) {
	var interpolationErr error
	resolved := interpolationPattern.ReplaceAllStringFunc(value, func(token string) string {
		if interpolationErr != nil {
			return token
		}
		match := interpolationPattern.FindStringSubmatch(token)
		if strings.Contains(match[1], ".") {
			interpolationErr = fmt.Errorf("reference %q is unavailable in this static field", match[1])
			return token
		}
		item, ok := variables[match[1]]
		if !ok {
			interpolationErr = fmt.Errorf("unknown blueprint variable %q", match[1])
			return token
		}
		switch item.(type) {
		case []any, []string, map[string]any:
			interpolationErr = fmt.Errorf("variable %q is not scalar", match[1])
			return token
		}
		return fmt.Sprint(item)
	})
	if interpolationErr != nil {
		return "", interpolationErr
	}
	return resolved, nil
}

func validateObjectName(prefix string, name string) error {
	if !variableNamePattern.MatchString(name) {
		return fmt.Errorf("%s.%s must use an identifier name", prefix, name)
	}
	return nil
}

func containsInterpolation(value string) bool { return interpolationPattern.MatchString(value) }

func isAnyAbsolutePath(value string) bool {
	return strings.HasPrefix(value, "/") || windowsAbsolutePathPattern.MatchString(value)
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}
	result := make(map[K]V, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func sortedCommandNames(commands map[string]Command) []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
