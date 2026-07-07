package dockerdeploy

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type RuntimeOptions struct {
	Dir                    string
	Action                 string
	Follow                 bool
	Tail                   string
	Verbose                bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	DockerPreflightTimeout time.Duration
}

var runRuntimeCommand = runCommand

func Runtime(options RuntimeOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if err := validateRuntimeInputs(options.Dir); err != nil {
		return err
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return err
	}
	stdout, stderr := deploymentOutputWritersForDeployment(options.Dir, state, options.Stdout, options.Stderr)
	commandStdout := stdout
	commandStderr := stderr
	if runtimeActionUsesRawOutput(options.Action) {
		commandStdout = options.Stdout
		commandStderr = options.Stderr
	}
	if runtimeActionNeedsBundle(options.Action) {
		pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
		if err != nil {
			return err
		}
		if err := ensureManagedFileMountsForPack(options.Dir, pack); err != nil {
			return err
		}
		if _, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: options.Dir, Verbose: options.Verbose, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout}); err != nil {
			return fmt.Errorf("prepare installation bundle: %w", err)
		}
	}
	if err := ensureRuntimeCompose(options.Dir); err != nil {
		return fmt.Errorf("ensure runtime compose: %w", err)
	}
	if runtimeActionNeedsBundle(options.Action) {
		projectName, err := deploymentComposeProjectName(options.Dir)
		if err != nil {
			return err
		}
		if err := ensureRuntimeNamedVolumeWritable(options.Dir, projectName, options.DockerPreflightTimeout); err != nil {
			return err
		}
	}
	spec, err := RuntimeCommandWithOptions(options.Dir, options.Action, RuntimeCommandOptions{Follow: options.Follow, Tail: options.Tail})
	if err != nil {
		return err
	}
	if !options.Verbose && !runtimeActionStreamsOutput(options.Action) {
		commandStdout = nil
		commandStderr = nil
	}
	err = runRuntimeCommand(spec, RunOptions{Stdout: commandStdout, Stderr: commandStderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
	if err != nil && options.Action == "up" && isStaleDockerNetworkError(err) {
		if stderr != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			fmt.Fprintln(stderr, "detected stale Docker network state; running down --remove-orphans and retrying up")
		}
		downSpec, downErr := RuntimeCommand(options.Dir, "down")
		if downErr != nil {
			return downErr
		}
		if downErr := runRuntimeCommand(downSpec, RunOptions{DockerPreflightTimeout: options.DockerPreflightTimeout}); downErr != nil {
			return fmt.Errorf("recover stale Docker network state: %w", downErr)
		}
		return runRuntimeCommand(spec, RunOptions{Stdout: commandStdout, Stderr: commandStderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
	}
	return err
}

func isStaleDockerNetworkError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "network ") && strings.Contains(err.Error(), " not found")
}

func runtimeActionNeedsBundle(action string) bool {
	return action == "up" || action == "restart"
}

func runtimeActionStreamsOutput(action string) bool {
	return action == "ps" || action == "status" || action == "logs"
}

func runtimeActionUsesRawOutput(action string) bool {
	return action == "logs"
}

type RuntimeCommandOptions struct {
	Follow bool
	Tail   string
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
		if options.Tail != "" {
			args = append(args, "--tail", options.Tail)
		}
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
