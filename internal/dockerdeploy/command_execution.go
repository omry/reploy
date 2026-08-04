package dockerdeploy

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type ResolvedEnvironmentCommand struct {
	Name         string
	Trigger      []string
	Native       bool
	Deployed     bool
	ForwardFlags []string
	Argv         []string
}

type TransientContainerExecutionV1 struct {
	Container string
	Create    CommandSpec
	Start     CommandSpec
	Cleanup   CommandSpec
}

func resolveLockedEnvironmentCommandV1(document blueprint.Document, catalog []providers.RealizedOutput, name string, forwarded []string) (ResolvedEnvironmentCommand, error) {
	command, exists := document.Environment.Commands[name]
	if !exists {
		return ResolvedEnvironmentCommand{}, fmt.Errorf("unknown environment command %q", name)
	}
	component, executable, exists := document.Environment.ResolveExecutableProfile(command.Executable)
	if !exists {
		return ResolvedEnvironmentCommand{}, fmt.Errorf("command %q executable %q is not declared", name, command.Executable)
	}
	invocationPath := ""
	for _, output := range catalog {
		if output.SupplierComponent != component || output.Name != executable.Binary {
			continue
		}
		if invocationPath != "" {
			return ResolvedEnvironmentCommand{}, fmt.Errorf("command %q executable %q has multiple locked outputs", name, command.Executable)
		}
		if !path.IsAbs(output.Candidate.InvocationPath) || output.Evidence.InvocationPath != output.Candidate.InvocationPath {
			return ResolvedEnvironmentCommand{}, fmt.Errorf("command %q executable output does not match its locked evidence", name)
		}
		invocationPath = output.Candidate.InvocationPath
	}
	if invocationPath == "" {
		return ResolvedEnvironmentCommand{}, fmt.Errorf("command %q executable %q is not present in the locked output catalog", name, command.Executable)
	}
	return resolveEnvironmentCommandAtPath(document, name, forwarded, invocationPath)
}

func resolveEnvironmentCommandAtPath(document blueprint.Document, name string, forwarded []string, invocationPath string) (ResolvedEnvironmentCommand, error) {
	command := document.Environment.Commands[name]
	_, executable, _ := document.Environment.ResolveExecutableProfile(command.Executable)
	segments := map[blueprint.ArgumentSegment][]string{
		blueprint.ArgumentBinary:    {invocationPath},
		blueprint.ArgumentPrefix:    append([]string(nil), executable.ArgvPrefix...),
		blueprint.ArgumentCommand:   append([]string(nil), command.Argv...),
		blueprint.ArgumentForwarded: append([]string(nil), forwarded...),
		blueprint.ArgumentSuffix:    append([]string(nil), executable.ArgvSuffix...),
	}
	argv := []string{}
	usesForwarded := false
	for _, segment := range command.Order {
		if segment == blueprint.ArgumentForwarded {
			usesForwarded = true
		}
		argv = append(argv, segments[segment]...)
	}
	if len(forwarded) > 0 && !usesForwarded {
		return ResolvedEnvironmentCommand{}, fmt.Errorf("command %q does not accept forwarded arguments", name)
	}
	if len(argv) == 0 || argv[0] != invocationPath {
		return ResolvedEnvironmentCommand{}, fmt.Errorf("command %q must execute its resolved binary first", name)
	}
	return ResolvedEnvironmentCommand{
		Name: name, Trigger: append([]string(nil), command.Trigger...), Native: command.NativeCommand,
		Deployed: command.DeployedCommand, ForwardFlags: append([]string(nil), command.ForwardFlags...), Argv: argv,
	}, nil
}

func resolveLockedEnvironmentCommandForPlanV1(document blueprint.Document, catalog []providers.RealizedOutput, plan DockerExecutionPlan, name string, forwarded []string) (ResolvedEnvironmentCommand, error) {
	command, err := resolveLockedEnvironmentCommandV1(document, catalog, name, forwarded)
	if err != nil {
		return ResolvedEnvironmentCommand{}, err
	}
	command.Argv, err = resolveEnvironmentOperationStrings(document, plan, command.Argv)
	if err != nil {
		return ResolvedEnvironmentCommand{}, fmt.Errorf("command %q interpolation: %w", name, err)
	}
	return command, nil
}

func MatchEnvironmentCommand(document blueprint.Document, arguments []string, deployedOnly bool) (string, []string, error) {
	return matchEnvironmentCommand(document, arguments, deployedOnly, true)
}

func MatchLifecycleCommand(document blueprint.Document, arguments []string) (string, []string, error) {
	return matchEnvironmentCommand(document, arguments, false, false)
}

func matchEnvironmentCommand(document blueprint.Document, arguments []string, deployedOnly bool, nativeOnly bool) (string, []string, error) {
	type candidate struct {
		name    string
		command blueprint.Command
	}
	candidates := []candidate{}
	for name, command := range document.Environment.Commands {
		if nativeOnly && !command.NativeCommand || deployedOnly && !command.DeployedCommand || len(command.Trigger) == 0 || len(command.Trigger) > len(arguments) {
			continue
		}
		matched := true
		for index, token := range command.Trigger {
			if arguments[index] != token {
				matched = false
				break
			}
		}
		if matched {
			candidates = append(candidates, candidate{name: name, command: command})
		}
	}
	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("unknown environment command: %s", strings.Join(arguments, " "))
	}
	sort.Slice(candidates, func(i, j int) bool { return len(candidates[i].command.Trigger) > len(candidates[j].command.Trigger) })
	selected := candidates[0]
	forwarded, err := validateForwardedArguments(selected.name, selected.command.ForwardFlags, arguments[len(selected.command.Trigger):])
	if err != nil {
		return "", nil, err
	}
	return selected.name, forwarded, nil
}

func validateForwardedArguments(commandName string, allowedFlags []string, arguments []string) ([]string, error) {
	allowed := map[string]bool{}
	for _, flag := range allowedFlags {
		allowed[flag] = true
	}
	result := []string{}
	afterSeparator := false
	for _, argument := range arguments {
		if argument == "--" && !afterSeparator {
			afterSeparator = true
			continue
		}
		if !afterSeparator && strings.HasPrefix(argument, "-") {
			name, _, _ := strings.Cut(argument, "=")
			if !allowed[name] {
				return nil, fmt.Errorf("command %q does not allow forwarded flag %q", commandName, name)
			}
		} else if !afterSeparator {
			return nil, fmt.Errorf("command %q application arguments must follow --", commandName)
		}
		result = append(result, argument)
	}
	return result, nil
}

func TransientCommandSpec(plan DockerExecutionPlan, command ResolvedEnvironmentCommand, output *transientOutputMount, interactive bool, tty bool) (CommandSpec, error) {
	return transientContainerCommandSpecV1("run", transientCommandContainerName(plan), plan, command, output, interactive, tty)
}

func PlanTransientContainerExecutionV1(
	plan DockerExecutionPlan,
	command ResolvedEnvironmentCommand,
	output *transientOutputMount,
	runID string,
	interactive bool,
	tty bool,
) (TransientContainerExecutionV1, error) {
	if err := deploy.ValidateLiveRunIDV1(runID); err != nil {
		return TransientContainerExecutionV1{}, err
	}
	if strings.TrimSpace(plan.ContainerName) == "" {
		return TransientContainerExecutionV1{}, fmt.Errorf("transient container execution requires a base container name")
	}
	container := plan.ContainerName + "-" + runID
	create, err := transientContainerCommandSpecV1("create", container, plan, command, output, interactive, tty)
	if err != nil {
		return TransientContainerExecutionV1{}, err
	}
	startArgs := []string{"start", "--attach"}
	if interactive {
		startArgs = append(startArgs, "--interactive")
	}
	startArgs = append(startArgs, container)
	return TransientContainerExecutionV1{
		Container: container,
		Create:    create,
		Start:     CommandSpec{Name: "docker", Args: startArgs},
		Cleanup:   TemporaryContainerCleanupCommand(container),
	}, nil
}

func transientContainerCommandSpecV1(operation string, container string, plan DockerExecutionPlan, command ResolvedEnvironmentCommand, output *transientOutputMount, interactive bool, tty bool) (CommandSpec, error) {
	if len(command.Argv) == 0 || !path.IsAbs(command.Argv[0]) {
		return CommandSpec{}, fmt.Errorf("transient command requires an absolute resolved executable")
	}
	if operation != "run" && operation != "create" {
		return CommandSpec{}, fmt.Errorf("transient container operation must be run or create")
	}
	if strings.TrimSpace(container) == "" {
		return CommandSpec{}, fmt.Errorf("transient container name is required")
	}
	if err := ValidateApplicationSandboxPlanV1(plan.Sandbox); err != nil {
		return CommandSpec{}, fmt.Errorf("prepare transient application sandbox: %w", err)
	}
	home := temporaryHomeForPlan(plan)
	args := []string{
		operation, "--pull", "never", "--rm", "--name", container,
		"--user", plan.Sandbox.RuntimeUser.DockerUser,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges=true",
		"--security-opt", "seccomp=" + plan.Sandbox.Kernel.SeccompProfile,
	}
	for _, group := range dockerSupplementaryGroupsV1(plan.Sandbox.RuntimeUser.SupplementaryGIDs) {
		args = append(args, "--group-add", group)
	}
	if plan.Sandbox.ReadOnlyRoot {
		args = append(args, "--read-only")
	}
	args = append(args,
		"--tmpfs", transientHomeMountForPlan(plan),
		"--env", "HOME="+home, "--env", "TMPDIR="+home,
	)
	if interactive {
		args = append(args, "--interactive")
	}
	if tty {
		args = append(args, "--tty")
	}
	for _, mount := range plan.Mounts {
		fields := []string{"type=" + renderDockerMountType(mount.Mode), "target=" + mount.Target}
		if mount.Source != "" {
			fields = append(fields, "source="+mount.Source)
		}
		if mount.ReadOnly {
			fields = append(fields, "readonly")
		}
		value, err := dockerMountArgument(fields...)
		if err != nil {
			return CommandSpec{}, fmt.Errorf("render transient mount %q: %w", mount.Name, err)
		}
		args = append(args, "--mount", value)
	}
	masks, err := privateRuntimeMasksV1(plan)
	if err != nil {
		return CommandSpec{}, err
	}
	for _, mask := range masks {
		switch mask.Kind {
		case privateRuntimeMaskDirectoryV1:
			args = append(args, "--tmpfs", mask.Target+":"+privateRuntimeDirectoryMaskOptionsV1)
		case privateRuntimeMaskFileV1:
			value, err := dockerMountArgument("type=bind", "source=/dev/null", "target="+mask.Target, "readonly")
			if err != nil {
				return CommandSpec{}, fmt.Errorf("render private runtime file mask: %w", err)
			}
			args = append(args, "--mount", value)
		default:
			return CommandSpec{}, fmt.Errorf("unsupported private runtime mask kind %q", mask.Kind)
		}
	}
	if output != nil {
		if !filepath.IsAbs(output.HostDirectory) || output.Variable == "" || output.ContainerPath == "" {
			return CommandSpec{}, fmt.Errorf("transient output mount is incomplete")
		}
		outputMount, err := dockerMountArgument("type=bind", "source="+output.HostDirectory, "target="+runtimeOutputRoot)
		if err != nil {
			return CommandSpec{}, fmt.Errorf("render transient output mount: %w", err)
		}
		args = append(args,
			"--mount", outputMount,
			"--env", output.Variable+"="+output.ContainerPath,
		)
	}
	args = append(args,
		"--entrypoint", command.Argv[0],
		plan.Image,
	)
	args = append(args, command.Argv[1:]...)
	return CommandSpec{Name: "docker", Args: args}, nil
}

func transientCommandContainerName(plan DockerExecutionPlan) string {
	return temporaryOneOffContainerName(plan.ContainerName, "command")
}

func ShellCommandSpec(plan DockerExecutionPlan, interactive bool, tty bool) CommandSpec {
	command := ResolvedEnvironmentCommand{Argv: []string{"/bin/sh"}}
	spec, _ := TransientCommandSpec(plan, command, nil, interactive, tty)
	return spec
}

func renderDockerMountType(mode blueprint.MountMode) string {
	if mode == blueprint.MountManagedBind {
		return "bind"
	}
	return string(mode)
}
