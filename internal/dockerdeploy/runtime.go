package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/omry/reploy/internal/blueprint"
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
	var environmentPlan *DockerExecutionPlan
	var lifecycleBefore LifecyclePlan
	var lifecycleAfter LifecyclePlan
	var restartLifecycle LifecyclePlan
	if runtimeActionNeedsBundle(options.Action) || options.Action == "down" && state.EnvironmentModel {
		pack, err = deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
		if err != nil {
			return err
		}
	}
	if runtimeActionNeedsBundle(options.Action) {
		recordedEnvironmentRestart := options.Action == "restart" && pack.Environment != nil
		if options.Progress != nil && !recordedEnvironmentRestart {
			fmt.Fprintln(options.Progress, "prepare installation bundle")
		}
		if err := ensureManagedFileMountsForPack(options.Dir, pack); err != nil {
			return err
		}
		if !recordedEnvironmentRestart {
			if _, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: options.Dir, SkipWarmRuntime: pack.Environment != nil, Verbose: options.Verbose, Stdout: stdout, Stderr: stderr, Progress: options.Progress, DockerPreflightTimeout: options.DockerPreflightTimeout}); err != nil {
				return fmt.Errorf("prepare installation bundle: %w", err)
			}
		}
		if pack.Environment != nil {
			state, err = loadState(options.Dir)
			if err != nil {
				return err
			}
			if options.Progress != nil && !recordedEnvironmentRestart {
				fmt.Fprintln(options.Progress, "prepare generated environment image")
			}
			if recordedEnvironmentRestart {
				state, err = requireRecordedEnvironmentImage(context.Background(), options.Dir, *pack.Environment, state)
				if err != nil {
					return err
				}
			} else {
				state, err = BuildEnvironmentImage(context.Background(), options.Dir, pack, state, RunOptions{Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
				if err != nil {
					return fmt.Errorf("prepare generated environment image: %w", err)
				}
			}
			if _, err := WriteResolvedRuntimeInputs(options.Dir, pack, state); err != nil {
				return fmt.Errorf("write resolved runtime inputs: %w", err)
			}
			if _, err := writeUpdatedStateIfChanged(options.Dir, pack, state.Bundle, state); err != nil {
				return fmt.Errorf("record generated environment image: %w", err)
			}
			resolved, err := ResolvedDockerExecutionPlan(options.Dir, pack, state)
			if err != nil {
				return err
			}
			environmentPlan = &resolved
			if options.Action == "restart" {
				restartLifecycle, err = PlanRestartLifecycle(*pack.Environment, resolved, state.Materialization.Executables)
				if err != nil {
					return err
				}
			} else if options.Action == "up" {
				lifecycle, err := PlanStartLifecycle(*pack.Environment, resolved, state.Materialization.Executables)
				if err != nil {
					return err
				}
				lifecycleBefore, lifecycleAfter, err = splitLifecyclePlan(lifecycle, LifecycleStart)
				if err != nil {
					return err
				}
			}
		}
	}
	if options.Action == "down" && pack.Environment != nil && state.Materialization != nil && state.Images != nil {
		resolved, err := ResolvedDockerExecutionPlan(options.Dir, pack, state)
		if err != nil {
			return err
		}
		environmentPlan = &resolved
		lifecycle, err := PlanStopLifecycle(*pack.Environment, resolved, state.Materialization.Executables)
		if err != nil {
			return err
		}
		lifecycleBefore, lifecycleAfter, err = splitLifecyclePlan(lifecycle, LifecycleStop)
		if err != nil {
			return err
		}
	}
	if options.Progress != nil {
		fmt.Fprintln(options.Progress, "prepare runtime compose")
	}
	if err := ensureRuntimeCompose(options.Dir); err != nil {
		return fmt.Errorf("ensure runtime compose: %w", err)
	}
	if runtimeActionNeedsBundle(options.Action) && pack.Environment == nil {
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
	if options.Action == "status" && state.EnvironmentModel && commandStdout != nil {
		inspection, err := Info(InfoOptions{Dir: options.Dir})
		if err != nil {
			return fmt.Errorf("inspect environment status: %w", err)
		}
		if _, err := io.WriteString(commandStdout, inspection); err != nil {
			return err
		}
	}
	if environmentPlan != nil && options.Action == "restart" {
		return executeEnvironmentRestart(options, pack, *environmentPlan, restartLifecycle, stdout, stderr, commandStdout, commandStderr, logSince)
	}
	if options.Progress != nil {
		fmt.Fprintln(options.Progress, runtimeRunPhase(options.Action))
	}
	if environmentPlan != nil && len(lifecycleBefore.Operations) > 0 {
		if err := ExecuteLifecycle(context.Background(), lifecycleBefore, environmentLifecycleExecutor(options, *environmentPlan, stdout, stderr)); err != nil {
			return err
		}
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
	if environmentPlan != nil && len(lifecycleAfter.Operations) > 0 {
		if err := ExecuteLifecycle(context.Background(), lifecycleAfter, environmentLifecycleExecutor(options, *environmentPlan, stdout, stderr)); err != nil {
			if options.Action == "up" || options.Action == "restart" {
				return runtimePostStartError("environment after_start failed", err, options, logSince)
			}
			return fmt.Errorf("environment after_stop failed: %w", err)
		}
	}
	if options.Action == "up" {
		if err := verifyRuntimeServiceAfterUp(options, logSince, pack); err != nil {
			return err
		}
	}
	return nil
}

func requireRecordedEnvironmentImage(ctx context.Context, dir string, document blueprint.Document, state deploy.DeploymentState) (deploy.DeploymentState, error) {
	prepared, err := bundlePrepared(dir)
	if err != nil {
		return state, err
	}
	if !prepared {
		return state, fmt.Errorf("restart requires a prepared installation bundle; run reploy bundle build and reploy up")
	}
	if state.Materialization == nil || state.Images == nil {
		return state, fmt.Errorf("restart requires a recorded generated environment image; run reploy up")
	}
	reused, recovered := reuseEnvironmentImage(ctx, dir, document, state)
	if !reused {
		return state, fmt.Errorf("recorded generated environment image is missing or stale; run reploy up")
	}
	return recovered, nil
}

func executeEnvironmentRestart(options RuntimeOptions, pack deploy.AppPack, plan DockerExecutionPlan, lifecycle LifecyclePlan, stdout io.Writer, stderr io.Writer, commandStdout io.Writer, commandStderr io.Writer, logSince time.Time) error {
	if options.Progress != nil {
		fmt.Fprintln(options.Progress, runtimeRunPhase(options.Action))
	}
	executor := environmentLifecycleExecutor(options, plan, stdout, stderr)
	executor.Stop = func(ctx context.Context) error {
		spec, err := RuntimeCommand(options.Dir, "down")
		if err != nil {
			return err
		}
		return runRuntimeCommand(spec, RunOptions{Context: ctx, Stdout: commandStdout, Stderr: commandStderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
	}
	executor.Start = func(ctx context.Context) error {
		spec, err := RuntimeCommand(options.Dir, "up")
		if err != nil {
			return err
		}
		return runRuntimeCommand(spec, RunOptions{Context: ctx, Stdout: commandStdout, Stderr: commandStderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
	}
	if err := ExecuteLifecycle(context.Background(), lifecycle, executor); err != nil {
		if strings.Contains(err.Error(), "lifecycle start ") || strings.Contains(err.Error(), "lifecycle after_start ") {
			return runtimePostStartError("environment restart failed", err, options, logSince)
		}
		return err
	}
	return verifyRuntimeServiceAfterUp(options, logSince, pack)
}

func environmentLifecycleExecutor(options RuntimeOptions, plan DockerExecutionPlan, stdout io.Writer, stderr io.Writer) LifecycleExecutor {
	return LifecycleExecutor{
		RunCommand: func(ctx context.Context, command ResolvedEnvironmentCommand) error {
			spec, err := TransientCommandSpec(plan, command, false, false)
			if err != nil {
				return err
			}
			return runRuntimeCommand(spec, RunOptions{Context: ctx, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
		},
		Readiness: func(ctx context.Context, endpoint EndpointExecutionPlan) error {
			return WaitForHTTPReadinessWithServiceCheck(ctx, endpoint, func(context.Context) error {
				return requireComposeServiceRunning(options.Dir, "", options.DockerPreflightTimeout)
			})
		},
	}
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
