package dockerdeploy

import (
	"fmt"
	"io"
)

type RuntimeOptions struct {
	Dir    string
	Action string
	Follow bool
	Stdout io.Writer
	Stderr io.Writer
}

func Runtime(options RuntimeOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if err := validateRuntimeInputs(options.Dir); err != nil {
		return err
	}
	spec, err := RuntimeCommandWithOptions(options.Dir, options.Action, RuntimeCommandOptions{Follow: options.Follow})
	if err != nil {
		return err
	}
	return runCommand(spec, RunOptions{Stdout: options.Stdout, Stderr: options.Stderr})
}

type RuntimeCommandOptions struct {
	Follow bool
}

func RuntimeCommand(dir string, action string) (CommandSpec, error) {
	return RuntimeCommandWithOptions(dir, action, RuntimeCommandOptions{})
}

func RuntimeCommandWithOptions(dir string, action string, options RuntimeCommandOptions) (CommandSpec, error) {
	switch action {
	case "up":
		return composeCommand(dir, "up", "-d"), nil
	case "restart":
		return composeCommand(dir, "up", "-d", "--force-recreate"), nil
	case "down":
		return composeCommand(dir, "down", "--remove-orphans"), nil
	case "ps", "status":
		return composeCommand(dir, "ps"), nil
	case "logs":
		args := []string{"logs", "--timestamps"}
		if options.Follow {
			args = append(args, "-f")
		}
		return composeCommand(dir, args...), nil
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
