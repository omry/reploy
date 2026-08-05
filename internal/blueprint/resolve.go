package blueprint

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

var builtInControlOperations = map[string]bool{
	"up": true, "stop": true, "restart": true, "status": true, "logs": true,
	"enable": true, "disable": true,
}

const DefaultRuntimeUser = "reploy"

func Resolve(source Syntax) (Document, error) {
	compatibility, err := ParseCompatibility(source.Blueprint.Compatibility.Platforms)
	if err != nil {
		return Document{}, err
	}
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
	allowConcurrent, err := resolveConcurrentRunPolicy(source.Environment.AllowConcurrent)
	if err != nil {
		return Document{}, err
	}
	runtimeUser, err := resolveRuntimeUser(source.Environment.Runtime.User)
	if err != nil {
		return Document{}, err
	}
	runtimeNetwork, err := resolveRuntimeNetwork(source.Environment.Runtime.Network)
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
			Compatibility:  compatibility,
		},
		Environment: Environment{
			ID:              id,
			ControlScript:   controlScript,
			Vars:            variables,
			Applications:    map[string]Application{},
			Components:      map[string]Component{},
			AllowConcurrent: allowConcurrent,
			Runtime:         EnvironmentRuntime{User: runtimeUser, Network: runtimeNetwork},
			Terminal:        Terminal{ColorEnv: strings.TrimSpace(source.Environment.Terminal.ColorEnv)},
			Install:         resolveInstallSyntax(source.Environment.Install, variables),
			Mounts:          map[string]EnvironmentMount{},
			Commands:        map[string]Command{},
		},
		Docker: Docker{
			Mounts: map[string]DockerMount{},
		},
	}
	if document.Blueprint.Version == "" {
		return Document{}, fmt.Errorf("blueprint.version is required")
	}
	if err := resolveApplications(source, &document); err != nil {
		return Document{}, err
	}
	if err := resolveMounts(source, extended, &document); err != nil {
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

func resolveRuntimeNetwork(source RuntimeNetworkSyntax) (RuntimeNetwork, error) {
	public, err := resolveNetworkAccess("environment.runtime.network.public", source.Public)
	if err != nil {
		return RuntimeNetwork{}, err
	}
	local, err := resolveNetworkAccess("environment.runtime.network.local", source.Local)
	if err != nil {
		return RuntimeNetwork{}, err
	}
	return RuntimeNetwork{Public: public, Local: local}, nil
}

func resolveNetworkAccess(field string, value string) (NetworkAccess, error) {
	access := NetworkAccess(strings.TrimSpace(value))
	if access == "" {
		return NetworkAccessDeny, nil
	}
	switch access {
	case NetworkAccessDeny, NetworkAccessAllow:
		return access, nil
	default:
		return "", fmt.Errorf("%s must be allow or deny", field)
	}
}

func resolveRuntimeUser(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultRuntimeUser, nil
	}
	if err := ValidateRuntimeUserName(value); err != nil {
		return "", fmt.Errorf("environment.runtime.user %w", err)
	}
	if value == "root" {
		return "", fmt.Errorf("environment.runtime.user names the non-root local account and must not be root")
	}
	return value, nil
}

func ValidateRuntimeUserName(value string) error {
	if value == "" || len(value) > 32 {
		return fmt.Errorf("must be a nonempty portable Unix user name no longer than 32 bytes")
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character == '_' && index == 0 ||
			index > 0 && (character >= '0' && character <= '9' || character == '_' || character == '-') {
			continue
		}
		return fmt.Errorf("must be a portable lowercase Unix user name")
	}
	return nil
}

func resolveConcurrentRunPolicy(value string) (ConcurrentRunPolicy, error) {
	policy := ConcurrentRunPolicy(strings.TrimSpace(value))
	if policy == "" {
		return ConcurrentRunAuto, nil
	}
	switch policy {
	case ConcurrentRunAuto, ConcurrentRunYes, ConcurrentRunNo:
		return policy, nil
	default:
		return "", fmt.Errorf("environment.allow_concurrent must be yes, no, or auto")
	}
}

func resolveApplications(source Syntax, document *Document) error {
	base, err := resolveBaseComponent("environment.base", source.Environment.Base)
	if err != nil {
		return err
	}
	document.Environment.Base = *base.Base
	document.Docker.Image = base.Base.Image

	environmentOS, err := resolveAPTPackageRequests("environment.packages.os", source.Environment.Packages.OS)
	if err != nil {
		return err
	}
	document.Environment.Packages.OS = environmentOS
	for _, name := range sortedKeys(source.Environment.Applications) {
		if err := validateProviderIdentifier("environment.applications", name); err != nil {
			return err
		}
		field := "environment.applications." + name
		item := source.Environment.Applications[name]
		application, err := resolveApplication(field, name, item)
		if err != nil {
			return err
		}
		document.Environment.Applications[name] = application
	}
	return document.Environment.RebuildProviderContributions()
}

func resolveBaseComponent(field string, item BaseSyntax) (Component, error) {
	image := strings.TrimSpace(item.Image)
	if image == "" {
		return Component{}, fmt.Errorf("%s.image is required", field)
	}
	exports := make(map[string]BaseExecutableExport, len(item.Exports))
	for _, name := range sortedKeys(item.Exports) {
		if err := validateProviderIdentifier(field+".exports", name); err != nil {
			return Component{}, err
		}
		executable := strings.TrimSpace(item.Exports[name].Executable)
		if err := validateExecutablePath(field+".exports."+name+".executable", executable); err != nil {
			return Component{}, err
		}
		exports[name] = BaseExecutableExport{Executable: executable}
	}
	return Component{
		Type: ComponentTypeBase, Base: &BaseComponent{Image: image, Exports: exports}, Options: map[string]ComponentOption{},
	}, nil
}

func resolveApplication(field string, name string, item ApplicationSyntax) (Application, error) {
	application := Application{
		Options: map[string]ApplicationOption{}, Executables: map[string]Executable{},
	}
	osPackages, err := resolveAPTPackageRequests(field+".packages.os", item.Packages.OS)
	if err != nil {
		return Application{}, err
	}
	application.Packages.OS = osPackages
	python, err := resolvePythonPackages(field+".packages.python", name, item.Packages.Python)
	if err != nil {
		return Application{}, err
	}
	application.Packages.Python = python
	hasOS := len(osPackages) != 0
	hasPython := python != nil
	for _, optionName := range sortedKeys(item.Options) {
		optionField := field + ".options." + optionName
		if err := validateProviderIdentifier(field+".options", optionName); err != nil {
			return Application{}, err
		}
		optionSource := item.Options[optionName]
		description := strings.TrimSpace(optionSource.Description)
		if description == "" {
			return Application{}, fmt.Errorf("%s.description is required", optionField)
		}
		if optionSource.Packages.Python != nil && optionSource.Packages.Python.Interpreter != nil {
			return Application{}, fmt.Errorf(
				"%s.packages.python.interpreter is not valid; options inherit the application's Python interpreter",
				optionField,
			)
		}
		optionOS, err := resolveAPTPackageRequests(optionField+".packages.os", optionSource.Packages.OS)
		if err != nil {
			return Application{}, err
		}
		optionPython, err := resolvePythonOptionPackages(optionField+".packages.python", optionSource.Packages.Python)
		if err != nil {
			return Application{}, err
		}
		if len(optionOS) == 0 && optionPython == nil {
			return Application{}, fmt.Errorf("%s.packages must not be empty", optionField)
		}
		application.Options[optionName] = ApplicationOption{
			Description: description,
			Packages:    ApplicationOptionPackages{OS: optionOS, Python: optionPython},
		}
		if len(optionOS) != 0 {
			hasOS = true
		}
		if optionPython != nil {
			hasPython = true
		}
	}
	application.Executables, err = resolveExecutableProfiles(field+".executables", item.Executables)
	if err != nil {
		return Application{}, err
	}
	for executableName, executable := range application.Executables {
		if executable.Source == ContributionProviderOS && !hasOS ||
			executable.Source == ContributionProviderPython && !hasPython {
			return Application{}, fmt.Errorf(
				"%s.executables.%s.source references missing contribution %q",
				field,
				executableName,
				executable.Source,
			)
		}
	}
	if !hasOS && !hasPython {
		return Application{}, fmt.Errorf("%s must declare packages or package options", field)
	}
	return application, nil
}

func resolvePythonPackages(field string, application string, item *PythonPackagesSyntax) (*PythonComponent, error) {
	if item == nil {
		return nil, nil
	}
	interpreter := CommandRequirement{Command: "python"}
	if item.Interpreter != nil {
		supplier := strings.TrimSpace(item.Interpreter.Supplier)
		if supplier != "" && supplier != "base" {
			if err := validateContributionProvider(field+".interpreter.supplier", supplier); err != nil {
				return nil, fmt.Errorf("%s must be base or an application package provider: %w", field+".interpreter.supplier", err)
			}
			supplier = ApplicationContributionID(application, supplier)
		}
		interpreter = CommandRequirement{
			Command: strings.TrimSpace(item.Interpreter.Command), Version: strings.TrimSpace(item.Interpreter.Version),
			Supplier: supplier,
		}
	}
	if err := interpreter.Validate(field + ".interpreter"); err != nil {
		return nil, err
	}
	requirements, err := normalizeStringSet(field+".requirements", item.Requirements)
	if err != nil {
		return nil, err
	}
	if len(requirements) == 0 {
		return nil, fmt.Errorf("%s.requirements must not be empty", field)
	}
	return &PythonComponent{Interpreter: interpreter, Requirements: requirements}, nil
}

func resolvePythonOptionPackages(field string, item *PythonPackagesSyntax) (*PythonOptionPackages, error) {
	if item == nil {
		return nil, nil
	}
	requirements, err := normalizeStringSet(field+".requirements", item.Requirements)
	if err != nil {
		return nil, err
	}
	if len(requirements) == 0 {
		return nil, fmt.Errorf("%s.requirements must not be empty", field)
	}
	return &PythonOptionPackages{Requirements: requirements}, nil
}

func resolveAPTPackageRequests(field string, source []APTPackageRequestSyntax) ([]APTPackageRequest, error) {
	requests := make([]APTPackageRequest, 0, len(source))
	byName := map[string]APTPackageRequest{}
	for index, item := range source {
		request, err := ParseAPTPackageRequest(item.Package)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, index, err)
		}
		for _, name := range sortedKeys(item.Exports) {
			executable := strings.TrimSpace(item.Exports[name].Executable)
			request.Exports[name] = ExecutableExport{Executable: executable}
		}
		if err := ValidateAPTPackageRequest(request); err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, index, err)
		}
		if previous, exists := byName[request.Name]; exists {
			if aptPackageRequestsEqual(previous, request) {
				continue
			}
			return nil, fmt.Errorf("%s contains conflicting declarations for package %q", field, request.Name)
		}
		byName[request.Name] = request
		requests = append(requests, request)
	}
	sort.Slice(requests, func(left int, right int) bool {
		return aptPackageRequestSortKey(requests[left]) < aptPackageRequestSortKey(requests[right])
	})
	return requests, nil
}

func normalizeStringSet(field string, values []string) ([]string, error) {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s must not contain an empty value", field)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result, nil
}

func aptPackageRequestsEqual(left APTPackageRequest, right APTPackageRequest) bool {
	return aptPackageRequestSortKey(left) == aptPackageRequestSortKey(right)
}

func aptPackageRequestSortKey(request APTPackageRequest) string {
	var key strings.Builder
	key.WriteString(request.Name)
	key.WriteByte(0)
	key.WriteString(request.Version)
	for _, name := range sortedKeys(request.Exports) {
		key.WriteByte(0)
		key.WriteString(name)
		key.WriteByte(0)
		key.WriteString(request.Exports[name].Executable)
	}
	return key.String()
}

func resolveMounts(source Syntax, extended extendedSyntax, document *Document) error {
	for _, name := range sortedKeys(source.Environment.Mounts) {
		item := source.Environment.Mounts[name]
		if err := validateObjectName("environment.mounts", name); err != nil {
			return err
		}
		target, err := resolveStaticString(item.Target, document.Environment.Vars)
		if err != nil {
			return fmt.Errorf("environment.mounts.%s.target: %w", name, err)
		}
		if target == "" {
			target = path.Join("/mnt", name)
		}
		if !path.IsAbs(target) || path.Clean(target) != target || strings.Contains(target, `\`) {
			return fmt.Errorf("environment.mounts.%s.target must be a normalized absolute Linux path", name)
		}
		if err := validateEnvironmentMountTarget(target); err != nil {
			return fmt.Errorf("environment.mounts.%s.target: %w", name, err)
		}
		update := UpdatePolicy(item.UpdatePolicy)
		if update != UpdatePreserve && update != UpdateReplace && update != UpdateUnmanaged {
			return fmt.Errorf("environment.mounts.%s.update_policy must be preserve, replace, or unmanaged", name)
		}
		writable, err := resolveSyntaxBool(item.Writable, "environment.mounts."+name+".writable")
		if err != nil {
			return err
		}
		document.Environment.Mounts[name] = EnvironmentMount{Target: target, Writable: writable, UpdatePolicy: update}
	}
	mountReferences := map[string]int{}
	for _, name := range sortedKeys(extended.Mounts) {
		item := extended.Mounts[name]
		contractName, _ := referencedName("extends", item.Docker.Extends, environmentMountReferencePrefix)
		mountReferences[contractName]++
		contract := document.Environment.Mounts[contractName]
		mount := DockerMount{
			Extends:  item.Docker.Extends,
			Mode:     MountMode(item.Docker.Mode),
			Source:   strings.TrimSpace(item.Docker.Source),
			Name:     strings.TrimSpace(item.Docker.Name),
			Contract: contract,
		}
		if err := validateMount("docker.mounts."+name, mount); err != nil {
			return err
		}
		document.Docker.Mounts[name] = mount
	}
	for _, name := range sortedKeys(document.Environment.Mounts) {
		if mountReferences[name] != 1 {
			return fmt.Errorf("environment mount %q must have exactly one Docker mount; found %d", name, mountReferences[name])
		}
	}
	return nil
}

func validateEnvironmentMountTarget(target string) error {
	if err := ValidateRuntimeMountDestination(target); err != nil {
		return err
	}
	reserved := []string{"/opt/reploy", "/mnt/reploy-home", "/mnt/reploy-output"}
	for _, protected := range reserved {
		if runtimeMountPathsOverlap(target, protected) {
			return fmt.Errorf("overlaps reserved container path %q", protected)
		}
	}
	return nil
}

// ValidateRuntimeMountDestination applies the platform-independent container
// path restrictions shared by blueprint validation and canonical runtime plans.
func ValidateRuntimeMountDestination(target string) error {
	if target == "/" {
		return fmt.Errorf("must not be the container filesystem root")
	}
	for _, reserved := range []string{"/dev", "/proc", "/sys", "/run/secrets", "/etc/hostname", "/etc/hosts", "/etc/resolv.conf", "/etc/passwd", "/etc/group"} {
		if runtimeMountPathsOverlap(target, reserved) {
			return fmt.Errorf("overlaps reserved container path %q", reserved)
		}
	}
	return nil
}

func runtimeMountPathsOverlap(left string, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func validateMount(field string, mount DockerMount) error {
	switch mount.Mode {
	case MountManagedBind:
		cleanSource := path.Clean(mount.Source)
		if mount.Contract.UpdatePolicy == UpdateUnmanaged || mount.Source == "" || path.IsAbs(mount.Source) || cleanSource == ".." || strings.HasPrefix(cleanSource, "../") {
			return fmt.Errorf("%s managed-bind requires managed update policy and relative source", field)
		}
	case MountVolume:
		if mount.Contract.UpdatePolicy == UpdateUnmanaged || mount.Name == "" {
			return fmt.Errorf("%s volume requires managed update policy and name", field)
		}
	case MountBind:
		if mount.Contract.UpdatePolicy != UpdateUnmanaged || mount.Source == "" {
			return fmt.Errorf("%s bind requires update_policy: unmanaged and source", field)
		}
		if !containsInterpolation(mount.Source) && !isAnyAbsolutePath(mount.Source) {
			return fmt.Errorf("%s bind source must be absolute", field)
		}
	case MountTmpfs:
		if mount.Contract.UpdatePolicy == UpdateUnmanaged {
			return fmt.Errorf("%s tmpfs does not support update_policy: unmanaged", field)
		}
	default:
		return fmt.Errorf("%s.mode is invalid: %s", field, mount.Mode)
	}
	return nil
}

func resolveExecutableProfiles(field string, source map[string]ExecutableSyntax) (map[string]Executable, error) {
	profiles := make(map[string]Executable, len(source))
	for _, name := range sortedKeys(source) {
		if err := validateProviderIdentifier(field, name); err != nil {
			return nil, err
		}
		item := source[name]
		contributionSource := strings.TrimSpace(item.Source)
		if contributionSource != ContributionProviderOS && contributionSource != ContributionProviderPython {
			return nil, fmt.Errorf("%s.%s.source must be os or python", field, name)
		}
		order, err := resolveOrder(item.Order)
		if err != nil {
			return nil, fmt.Errorf("%s.%s.order: %w", field, name, err)
		}
		binary := strings.TrimSpace(item.Binary)
		if err := validateProviderIdentifier(field+"."+name+".binary", binary); err != nil {
			return nil, err
		}
		profiles[name] = Executable{
			Source: contributionSource, Binary: binary, Order: order,
			ArgvPrefix: append([]string(nil), item.ArgvPrefix...), ArgvSuffix: append([]string(nil), item.ArgvSuffix...),
		}
	}
	return profiles, nil
}

func resolveExecutablesAndCommands(source Syntax, document *Document) error {
	triggerOwner := map[string]string{}
	for _, name := range sortedKeys(source.Environment.Commands) {
		if err := validateProviderIdentifier("environment.commands", name); err != nil {
			return err
		}
		item := source.Environment.Commands[name]
		_, executable, ok := document.Environment.ResolveExecutableProfile(item.Executable)
		if !ok {
			return fmt.Errorf("environment.commands.%s references missing qualified executable %q", name, item.Executable)
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
		System:       SystemInstall{Account: SystemAccount{User: item.System.Account.User, Group: item.System.Account.Group, OnMissing: item.System.Account.OnMissing}},
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
