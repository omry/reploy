package dockerdeploy

import (
	"fmt"
	"io"
)

type RuntimeOptions struct {
	Dir     string
	Action  string
	Follow  bool
	Verbose bool
	Stdout  io.Writer
	Stderr  io.Writer
}

var runRuntimeCommand = runCommand

func Runtime(options RuntimeOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if err := validateRuntimeInputs(options.Dir); err != nil {
		return err
	}
	if runtimeActionNeedsBundle(options.Action) {
		if _, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: options.Dir, Verbose: options.Verbose, Stdout: options.Stdout, Stderr: options.Stderr}); err != nil {
			return fmt.Errorf("prepare installation bundle: %w", err)
		}
	}
	if err := ensureRuntimeCompose(options.Dir); err != nil {
		return fmt.Errorf("ensure runtime compose: %w", err)
	}
	spec, err := RuntimeCommandWithOptions(options.Dir, options.Action, RuntimeCommandOptions{Follow: options.Follow})
	if err != nil {
		return err
	}
	return runRuntimeCommand(spec, RunOptions{Stdout: options.Stdout, Stderr: options.Stderr})
}

func runtimeActionNeedsBundle(action string) bool {
	return action == "up" || action == "restart"
}

type RuntimeCommandOptions struct {
	Follow bool
}

func RuntimeCommand(dir string, action string) (CommandSpec, error) {
	return RuntimeCommandWithOptions(dir, action, RuntimeCommandOptions{})
}

func RuntimeCommandWithOptions(dir string, action string, options RuntimeCommandOptions) (CommandSpec, error) {
	projectName, err := deploymentComposeProjectName(dir)
	if err != nil {
		return CommandSpec{}, err
	}
	switch action {
	case "up":
		return composeCommandWithProject(dir, projectName, "up", "-d"), nil
	case "restart":
		return composeCommandWithProject(dir, projectName, "up", "-d", "--force-recreate"), nil
	case "down":
		return composeCommandWithProject(dir, projectName, "down", "--remove-orphans"), nil
	case "ps", "status":
		return composeCommandWithProject(dir, projectName, "ps"), nil
	case "logs":
		args := []string{"logs", "--timestamps"}
		if options.Follow {
			args = append(args, "-f")
		}
		return composeCommandWithProject(dir, projectName, args...), nil
	default:
		return CommandSpec{}, fmt.Errorf("unsupported runtime action: %s", action)
	}
}

func validateRuntimeInputs(dir string) error {
	if _, err := readDockerEnv(dir); err != nil {
		return fmt.Errorf("read %s: %w", DockerEnvFileName, err)
	}
	return nil
}
