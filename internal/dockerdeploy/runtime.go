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
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
}

var runRuntimeCommand = runCommand
var runRuntimePostStartServiceRunningCheck = requireComposeServiceRunning
var runRuntimePostStartHealthCheck = TestServer

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
	var pack deploy.AppPack
	if runtimeActionNeedsBundle(options.Action) {
		if options.Progress != nil {
			fmt.Fprintln(options.Progress, "prepare installation bundle")
		}
		pack, err = deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
		if err != nil {
			return err
		}
		if err := ensureManagedFileMountsForPack(options.Dir, pack); err != nil {
			return err
		}
		if _, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: options.Dir, Verbose: options.Verbose, Stdout: stdout, Stderr: stderr, Progress: options.Progress, DockerPreflightTimeout: options.DockerPreflightTimeout}); err != nil {
			return fmt.Errorf("prepare installation bundle: %w", err)
		}
	}
	if options.Progress != nil {
		fmt.Fprintln(options.Progress, "prepare runtime compose")
	}
	if err := ensureRuntimeCompose(options.Dir); err != nil {
		return fmt.Errorf("ensure runtime compose: %w", err)
	}
	if runtimeActionNeedsBundle(options.Action) {
		projectName, err := deploymentComposeProjectName(options.Dir)
		if err != nil {
			return err
		}
		if options.Progress != nil {
			fmt.Fprintln(options.Progress, "prepare runtime cache")
		}
		if err := ensureRuntimeNamedVolumeWritable(options.Dir, projectName, options.DockerPreflightTimeout); err != nil {
			return fmt.Errorf("prepare runtime cache: %w", err)
		}
	}
	spec, err := RuntimeCommandWithOptions(options.Dir, options.Action, RuntimeCommandOptions{Follow: options.Follow, Tail: options.Tail})
	if err != nil {
		return err
	}
	var logSince time.Time
	if runtimeActionUsesStartupLogSnippet(options.Action) {
		logSince = runtimeLogSinceTime()
	}
	if !options.Verbose && !runtimeActionStreamsOutput(options.Action) {
		commandStdout = nil
		commandStderr = nil
	}
	if options.Progress != nil {
		fmt.Fprintln(options.Progress, runtimeRunPhase(options.Action))
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
		err = runRuntimeCommand(spec, RunOptions{Stdout: commandStdout, Stderr: commandStderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
	}
	if err != nil {
		return err
	}
	if options.Action == "up" {
		if err := verifyRuntimeServiceAfterUp(options, logSince, pack); err != nil {
			return err
		}
	}
	return nil
}

func verifyRuntimeServiceAfterUp(options RuntimeOptions, logSince time.Time, pack deploy.AppPack) error {
	if options.Progress != nil {
		fmt.Fprintln(options.Progress, "check app state")
	}
	if err := runRuntimePostStartServiceRunningCheck(options.Dir, "", options.DockerPreflightTimeout); err != nil {
		return runtimePostStartError("service failed after start", err, options, logSince)
	}
	if !runtimeAfterStartHealthCheckEnabled(pack) {
		return nil
	}
	if options.Progress != nil {
		fmt.Fprintln(options.Progress, "check app health")
	}
	err := runRuntimePostStartHealthCheck(TestOptions{
		Dir:                    options.Dir,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	})
	if err == nil {
		return nil
	}
	return runtimePostStartError("service health check failed after start", err, options, logSince)
}

func runtimeAfterStartHealthCheckEnabled(pack deploy.AppPack) bool {
	for _, hook := range pack.Docker.Runtime.Hooks.AfterStart {
		if hook.HealthCheck != nil && hook.HealthCheck.Wait {
			return true
		}
	}
	return false
}

func runtimePostStartError(message string, err error, options RuntimeOptions, logSince time.Time) error {
	diagnostics := runtimeStartupLogDiagnosticsFor(options.Dir, logSince, options.DockerPreflightTimeout)
	if diagnostics.Failure != "" {
		message += ": " + diagnostics.Failure
	}
	if diagnostics.Snippet != "" {
		return fmt.Errorf("%s: %w\nstartup log snippet:\n%s", message, err, diagnostics.Snippet)
	}
	return fmt.Errorf("%s: %w", message, err)
}

func runtimeRunPhase(action string) string {
	switch action {
	case "up":
		return "start app"
	case "restart":
		return "restart app"
	case "down":
		return "stop app"
	default:
		return "run " + action
	}
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
	Since  string
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
	case "ps":
		return composeCommandWithProject(dir, projectName, "ps"), nil
	case "status":
		return composeCommandWithProject(dir, projectName, "ps", "--all"), nil
	case "logs":
		args := []string{"logs", "--timestamps"}
		if options.Since != "" {
			args = append(args, "--since", options.Since)
		}
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
