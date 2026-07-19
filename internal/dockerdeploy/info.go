package dockerdeploy

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/python"
)

type InfoOptions struct {
	Dir string
}

func Info(options InfoOptions) (string, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return "", err
	}
	absoluteDir, err := filepath.Abs(options.Dir)
	if err != nil {
		return "", err
	}
	lines := []string{
		fmt.Sprintf("deployment: %s", absoluteDir),
		fmt.Sprintf("target: %s", state.Target),
		fmt.Sprintf("phase: %s", state.Phase),
		fmt.Sprintf("blueprint: %s", state.Blueprint.Raw),
	}
	if state.RequestedBlueprintRef != "" && state.RequestedBlueprintRef != state.Blueprint.Raw {
		lines = append(lines, fmt.Sprintf("requested blueprint: %s", state.RequestedBlueprintRef))
	}
	if state.Install != nil && state.Install.Scope != "" {
		lines = append(lines, fmt.Sprintf("install scope: %s", state.Install.Scope))
	}
	lines = append(lines, "bundle roots:")
	if len(state.Bundle.Roots) == 0 {
		lines = append(lines, "  (none)")
	} else {
		for _, root := range state.Bundle.Roots {
			lines = append(lines, fmt.Sprintf("  - %s", formatArtifactRoot(root)))
		}
	}
	lines = append(lines, "bundle prepared:")
	prepared, ok, err := preparedBundlePackages(options.Dir, state.Bundle.Roots)
	if err != nil {
		return "", err
	}
	if !ok {
		lines = append(lines, "  not built")
	} else {
		for _, resolved := range prepared {
			lines = append(lines, fmt.Sprintf("  - %s %s", resolved.Kind, resolved.Requirement))
		}
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return "", err
	}
	if pack.Environment != nil {
		environmentLines, err := environmentInfoLines(options.Dir, state, *pack.Environment)
		if err != nil {
			return "", err
		}
		lines = append(lines, environmentLines...)
	} else {
		lines = append(lines, fmt.Sprintf("files: %s", filepath.Join(absoluteDir, ReployInternalDir)))
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func environmentInfoLines(dir string, state deploy.DeploymentState, document blueprint.Document) ([]string, error) {
	plan, planResolved, err := environmentInfoPlan(dir, state, document)
	if err != nil {
		return nil, err
	}
	preparedFingerprint := strings.TrimSpace(state.Bundle.PreparedFingerprint)
	inputsFingerprint := bundlePreparedFingerprint(state)
	inputsChanged := preparedFingerprint == "" || preparedFingerprint != inputsFingerprint
	bundleIdentity := preparedFingerprint
	if bundleIdentity == "" {
		bundleIdentity = "unresolved"
	}
	candidateIdentity := bundleIdentity
	if inputsChanged {
		candidateIdentity = "unresolved"
	}
	imageIdentity := "unresolved"
	if image := environmentInfoImage(state); image != nil {
		imageIdentity = image.Reference + " (" + image.Fingerprint + ")"
	}

	lines := []string{
		fmt.Sprintf("environment: %s", document.Environment.ID),
		fmt.Sprintf("bundle identity: %s", bundleIdentity),
		fmt.Sprintf("bundle inputs changed: %t", inputsChanged),
		fmt.Sprintf("candidate bundle identity: %s", candidateIdentity),
		fmt.Sprintf("materialized image: %s", imageIdentity),
		"phase order:",
	}
	for _, phase := range environmentPhaseOrder(document) {
		lines = append(lines, "  - "+phase)
	}
	lines = append(lines, "commands:")
	for _, name := range sortedBlueprintCommandNames(document.Environment.Commands) {
		command := document.Environment.Commands[name]
		exposure := "internal"
		if command.DeployedCommand {
			exposure = "staging,deployed"
		} else if command.NativeCommand {
			exposure = "staging"
		}
		trigger := strings.Join(command.Trigger, " ")
		if trigger == "" {
			trigger = "(internal " + name + ")"
		}
		argv := logicalEnvironmentCommandArgv(document, name)
		if planResolved && state.Materialization != nil {
			resolved, resolveErr := ResolveEnvironmentCommandForPlan(document, state.Materialization.Executables, plan, name, nil)
			if resolveErr == nil {
				argv = resolved.Argv
			}
		}
		lines = append(lines, fmt.Sprintf("  - %s [%s]: %s", trigger, exposure, formatArgv(argv)))
	}
	lines = append(lines, "endpoints:")
	if plan.Workload == nil || len(plan.Workload.Endpoints) == 0 {
		lines = append(lines, "  (none)")
	} else {
		names := make([]string, 0, len(plan.Workload.Endpoints))
		for name := range plan.Workload.Endpoints {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			endpoint := plan.Workload.Endpoints[name]
			published := endpoint.Scheme + "://" + net.JoinHostPort(endpoint.PublishAddress, strconv.Itoa(endpoint.PublishedPort))
			binding := net.JoinHostPort(endpoint.BindAddress, strconv.Itoa(endpoint.ContainerPort))
			line := fmt.Sprintf("  - %s: %s -> %s", name, published, binding)
			if endpoint.Readiness != nil {
				line += " readiness=" + endpoint.Readiness.Path
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "backend files:")
	for _, relative := range []string{StateFileName, ManifestFileName, DockerEnvFileName, ComposeFileName} {
		path := filepath.Join(dir, relative)
		status := "planned"
		if _, statErr := os.Stat(path); statErr == nil {
			status = "existing"
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		lines = append(lines, fmt.Sprintf("  - %s [%s]", absolute, status))
	}
	return lines, nil
}

func environmentInfoPlan(dir string, state deploy.DeploymentState, document blueprint.Document) (DockerExecutionPlan, bool, error) {
	if state.Materialization != nil && state.Images != nil {
		pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
		if err != nil {
			return DockerExecutionPlan{}, false, err
		}
		plan, err := ResolvedDockerExecutionPlan(dir, pack, state)
		return plan, err == nil, err
	}
	phase := blueprint.PhaseStaged
	var scope *blueprint.InstallScope
	installTarget := ""
	if state.Phase == deploy.PhaseInstalled {
		if state.Install == nil {
			return DockerExecutionPlan{}, false, fmt.Errorf("installed state is missing install metadata")
		}
		phase = blueprint.PhaseInstalled
		value := blueprint.InstallScope(state.Install.Scope)
		scope = &value
		installTarget = state.Install.TargetDir
	}
	host := blueprint.HostLinux
	switch runtime.GOOS {
	case "darwin":
		host = blueprint.HostMacOS
	case "windows":
		host = blueprint.HostWindows
	}
	context := DockerPlanContext{
		DeploymentDir: dir, InstallTarget: installTarget, Phase: phase, Scope: scope,
		GeneratedImage: "unresolved", Host: host, UID: os.Getuid(), GID: os.Getgid(),
	}
	if state.Install != nil {
		context.PortOverrides = map[string]int{}
		for name, binding := range state.Install.Ports {
			port, err := strconv.Atoi(binding.HostPort)
			if err != nil {
				return DockerExecutionPlan{}, false, err
			}
			context.PortOverrides[name] = port
		}
	}
	plan, err := PlanDockerExecution(document, context)
	return plan, false, err
}

func environmentInfoImage(state deploy.DeploymentState) *deploy.GeneratedImageState {
	if state.Images == nil {
		return nil
	}
	if state.Phase == deploy.PhaseInstalled {
		return state.Images.Deployed
	}
	return state.Images.Staging
}

func environmentPhaseOrder(document blueprint.Document) []string {
	result := []string{"resolve blueprint", "build closed bundle", "materialize Docker environment", "prepare managed paths"}
	if len(document.Environment.Install.AfterInstall) > 0 {
		result = append(result, "after_install actions")
	}
	if document.Environment.Workload == nil {
		return result
	}
	if len(document.Environment.Workload.Runtime.BeforeStart) > 0 {
		result = append(result, "before_start actions")
	}
	result = append(result, "start workload")
	hasReadiness := false
	for _, endpoint := range document.Environment.Workload.Endpoints {
		if endpoint.Readiness != nil {
			hasReadiness = true
			break
		}
	}
	if hasReadiness {
		result = append(result, "satisfy readiness requirements")
	}
	if len(document.Environment.Workload.Runtime.AfterStart) > 0 {
		result = append(result, "after_start actions")
	}
	return result
}

func sortedBlueprintCommandNames(commands map[string]blueprint.Command) []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func logicalEnvironmentCommandArgv(document blueprint.Document, name string) []string {
	command := document.Environment.Commands[name]
	executable := document.Environment.Executables[command.Executable]
	segments := map[blueprint.ArgumentSegment][]string{
		blueprint.ArgumentBinary:  {executable.Binary},
		blueprint.ArgumentPrefix:  executable.ArgvPrefix,
		blueprint.ArgumentCommand: command.Argv,
		blueprint.ArgumentSuffix:  executable.ArgvSuffix,
	}
	argv := []string{}
	for _, segment := range command.Order {
		argv = append(argv, segments[segment]...)
	}
	return argv
}

func formatArgv(argv []string) string {
	if len(argv) == 0 {
		return "(empty)"
	}
	return formatCommand(argv[0], argv[1:]...)
}

func formatArtifactRoot(root deploy.ArtifactRoot) string {
	if root.Provider == "" && root.Kind == "" {
		return root.Source
	}
	if root.Provider == "" {
		return root.Kind + " " + root.Source
	}
	if root.Kind == "" {
		return root.Provider + " " + root.Source
	}
	return root.Provider + " " + root.Kind + " " + root.Source
}

func preparedBundlePackages(dir string, roots []deploy.ArtifactRoot) ([]BundleResolvedPackage, bool, error) {
	bundleDir, err := deploymentBundleDir(dir)
	if err != nil {
		return nil, false, err
	}
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	rootRequirements := python.RootRequirements(roots)
	prepared := []BundleResolvedPackage{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".whl") {
			continue
		}
		requirement, ok := python.WheelFilenameRequirement(entry.Name())
		if !ok {
			continue
		}
		kind := "transitive"
		if rootRequirements[requirement] || rootRequirements[strings.Split(requirement, "==")[0]] {
			kind = "root"
		}
		prepared = append(prepared, BundleResolvedPackage{Kind: kind, Requirement: requirement})
	}
	if len(prepared) == 0 {
		return nil, false, nil
	}
	sortBundleResolvedPackages(prepared)
	return prepared, true, nil
}
