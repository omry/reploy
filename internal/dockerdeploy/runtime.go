package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type RuntimeOptions struct {
	Dir                    string
	Action                 string
	ControlMode            ControlAdmissionModeV1
	Follow                 bool
	Tail                   string
	Timestamps             bool
	Verbose                bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
}

var runRuntimeCommand = runCommand
var runCurrentWorkload = RunCurrentWorkloadV1
var runCurrentRuntimeObservation = RunCurrentRuntimeObservationV1
var runRuntimeProviderBuild = RunProviderBuildV1

func Runtime(options RuntimeOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	stateSchema, err := runtimeStateSchema(options.Dir)
	if err != nil {
		return err
	}
	if stateSchema != deploy.StateSchemaV1 {
		return fmt.Errorf("runtime state schema %q is unsupported; expected %q", stateSchema, deploy.StateSchemaV1)
	}
	if runtimeActionUsesCurrentWorkloadV1(options.Action) {
		runtime, err := CurrentStagedProviderBuildRuntimeV1()
		if err != nil {
			return err
		}
		installed, err := runtimeStateHasDeploymentV1(options.Dir)
		if err != nil {
			return err
		}
		if runtimeActionEnsuresCurrentBuildV1(options.Action) && !installed {
			if options.Progress != nil {
				fmt.Fprintln(options.Progress, "prepare current build")
			}
			if _, err := runRuntimeProviderBuild(context.Background(), ProviderBuildRunInputV1{
				DeploymentDir: options.Dir,
				Runtime:       runtime,
				Automatic:     true,
				RunOptions: RunOptions{
					Stdout: options.Stdout, Stderr: options.Stderr,
					DockerPreflightTimeout: options.DockerPreflightTimeout,
				},
			}); err != nil {
				return fmt.Errorf("prepare current build: %w", err)
			}
		}
		stdout, stderr := options.Stdout, options.Stderr
		if !options.Verbose {
			stdout, stderr = nil, nil
		}
		if options.Progress != nil {
			fmt.Fprintln(options.Progress, "validate current build")
		}
		return runCurrentWorkload(context.Background(), CurrentWorkloadRunInputV1{
			DeploymentDir: options.Dir, Action: options.Action, ControlMode: options.ControlMode, Runtime: runtime,
			Progress: options.Progress, Notice: options.Stderr,
			RunOptions: RunOptions{
				Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout,
			},
		})
	}
	if runtimeActionUsesCurrentObservationV1(options.Action) {
		runtime, err := CurrentStagedProviderBuildRuntimeV1()
		if err != nil {
			return err
		}
		if options.Progress != nil {
			fmt.Fprintln(options.Progress, "validate current build")
			fmt.Fprintln(options.Progress, runtimeRunPhase(options.Action))
		}
		return runCurrentRuntimeObservation(context.Background(), CurrentRuntimeObservationInputV1{
			DeploymentDir: options.Dir,
			Action:        options.Action,
			Runtime:       runtime,
			Command: RuntimeCommandOptions{
				Follow: options.Follow, Tail: options.Tail, Timestamps: options.Timestamps,
			},
			RunOptions: RunOptions{
				Stdout: options.Stdout, Stderr: options.Stderr,
				DockerPreflightTimeout: options.DockerPreflightTimeout,
			},
		})
	}
	return fmt.Errorf("unsupported runtime action: %s", options.Action)
}

func runtimeStateHasDeploymentV1(dir string) (bool, error) {
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		return false, fmt.Errorf("read runtime state: %w", err)
	}
	var envelope struct {
		Deployment json.RawMessage `json:"deployment"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		return false, fmt.Errorf("inspect runtime deployment state: %w", err)
	}
	value := string(envelope.Deployment)
	return value != "" && value != "null", nil
}

func runtimeStateSchema(dir string) (string, error) {
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read runtime state: %w", err)
	}
	var envelope struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		return "", fmt.Errorf("inspect runtime state schema: %w", err)
	}
	return envelope.Schema, nil
}

func runtimeActionUsesCurrentWorkloadV1(action string) bool {
	return action == "up" || action == "down" || action == "restart"
}

func runtimeActionEnsuresCurrentBuildV1(action string) bool {
	return action == "up" || action == "restart"
}

func runtimeActionUsesCurrentObservationV1(action string) bool {
	return action == "ps" || action == "status" || action == "logs"
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

type RuntimeCommandOptions struct {
	Follow     bool
	Tail       string
	Since      string
	Timestamps bool
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
		return composeCommandWithProject(dir, projectName, "up", "--pull", "never", "-d"), nil
	case "restart":
		return composeCommandWithProject(dir, projectName, "up", "--pull", "never", "-d", "--force-recreate"), nil
	case "down":
		return composeCommandWithProject(dir, projectName, "down", "--remove-orphans"), nil
	case "ps":
		return composeCommandWithProject(dir, projectName, "ps"), nil
	case "status":
		return composeCommandWithProject(dir, projectName, "ps", "--all"), nil
	case "logs":
		args := []string{"logs"}
		if options.Timestamps {
			args = append(args, "--timestamps")
		}
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
