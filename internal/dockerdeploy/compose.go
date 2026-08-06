package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/deploy"
)

type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

type RunOptions struct {
	Context                context.Context
	Stdin                  io.Reader
	Stdout                 io.Writer
	Stderr                 io.Writer
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
	NoCache                bool
}

const commandOutputErrorLimit = 4000
const defaultDockerPreflightTimeout = 5 * time.Second

var dockerPreflight = checkDockerResponsive

func runCommand(spec CommandSpec, options RunOptions) error {
	if spec.Name == "docker" {
		return runDockerCommand(spec, options)
	}
	return runCommandWithoutDockerPreflight(spec, options)
}

func runDockerCommand(spec CommandSpec, options RunOptions) error {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	_, end := buildprofile.Start(ctx, "Docker preflight")
	err := dockerPreflight(ctx, spec, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
	end(err)
	if err != nil {
		return err
	}
	return runCommandWithoutDockerPreflight(spec, options)
}

// runCommandWithoutDockerPreflight is for follow-up commands in one
// higher-level Docker operation whose first command already passed preflight.
// Callers must not use it as the entry point to an independent operation.
func runCommandWithoutDockerPreflight(spec CommandSpec, options RunOptions) (resultErr error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if spec.Name == "docker" {
		var end func(error)
		ctx, end = buildprofile.Start(ctx, dockerProfileOperation(spec.Args))
		defer func() { end(resultErr) }()
	}
	command := exec.CommandContext(ctx, spec.Name, spec.Args...)
	command.Dir = spec.Dir
	if len(spec.Env) > 0 {
		command.Env = append(os.Environ(), spec.Env...)
	}
	command.Stdin = options.Stdin
	var capturedOutput bytes.Buffer
	if options.Stdout == nil && options.Stderr == nil {
		command.Stdout = &capturedOutput
		command.Stderr = &capturedOutput
	} else {
		command.Stdout = options.Stdout
		command.Stderr = options.Stderr
	}
	if err := command.Run(); err != nil {
		if output := trimmedCommandOutput(capturedOutput.String()); output != "" {
			return fmt.Errorf("%s failed: %w\ncommand output:\n%s", spec.Name, err, output)
		}
		return fmt.Errorf("%s failed: %w", spec.Name, err)
	}
	return nil
}

func dockerProfileOperation(args []string) string {
	if len(args) == 0 {
		return "Docker command"
	}
	switch args[0] {
	case "build", "create", "exec", "inspect", "pull", "start", "stop", "tag", "wait":
		return "Docker " + args[0]
	case "container", "image", "volume":
		if len(args) > 1 {
			switch args[1] {
			case "create", "inspect", "ls", "rm", "start", "stop", "tag":
				return "Docker " + args[0] + " " + args[1]
			}
		}
		return "Docker " + args[0]
	default:
		return "Docker command"
	}
}

func effectiveDockerPreflightTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return defaultDockerPreflightTimeout
}

func checkDockerResponsive(ctx context.Context, spec CommandSpec, timeout time.Duration) error {
	timeout = effectiveDockerPreflightTimeout(timeout)
	preflightCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := requireLocalDockerEndpointV1(preflightCtx, spec, timeout); err != nil {
		return err
	}

	command := exec.CommandContext(preflightCtx, spec.Name, "version", "--format", "{{.Server.Version}}")
	command.Dir = spec.Dir
	if len(spec.Env) > 0 {
		command.Env = append(os.Environ(), spec.Env...)
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if errors.Is(preflightCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("docker daemon did not respond within %s", timeout)
		}
		if output := trimmedCommandOutput(output.String()); output != "" {
			return fmt.Errorf("docker daemon check failed: %w\ncommand output:\n%s", err, output)
		}
		return fmt.Errorf("docker daemon check failed: %w", err)
	}
	return nil
}

func trimmedCommandOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) <= commandOutputErrorLimit {
		return output
	}
	return "[last 4000 bytes]\n" + output[len(output)-commandOutputErrorLimit:]
}

type commandRunner func(CommandSpec, RunOptions) error

func runInterruptibleCommand(run commandRunner, spec CommandSpec, options RunOptions) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runOptions := options
	runOptions.Context = ctx

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	done := make(chan error, 1)
	go func() {
		done <- run(spec, runOptions)
	}()

	select {
	case err := <-done:
		return err
	case sig := <-signals:
		cancel()
		if err := <-done; err != nil {
			return fmt.Errorf("interrupted by %s: %w", sig, err)
		}
		return fmt.Errorf("interrupted by %s", sig)
	}
}

func deploymentComposeProjectName(dir string) (string, error) {
	if content, err := os.ReadFile(filepath.Join(dir, StateFileName)); err == nil {
		state, decodeErr := deploy.DecodeStateV1(content)
		if decodeErr != nil {
			return "", decodeErr
		}
		if state.Deployment != nil && state.Deployment.Installation.ComposeProject != "" {
			return state.Deployment.Installation.ComposeProject, nil
		}
	}
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", DockerEnvFileName, err)
	}
	if projectName := envValue(values, "REPLOY_CONTAINER_NAME", ""); projectName != "" {
		return projectName, nil
	}
	if projectName := envValue(values, "REPLOY_DOCKER_NETWORK_NAME", ""); projectName != "" {
		return projectName, nil
	}
	return "", nil
}

func composeCommand(dir string, args ...string) CommandSpec {
	return composeCommandWithProject(dir, "", args...)
}

func composeCommandWithProject(dir string, projectName string, args ...string) CommandSpec {
	if absoluteDir, err := filepath.Abs(dir); err == nil {
		dir = absoluteDir
	}
	composeArgs := []string{"compose"}
	if projectName != "" {
		composeArgs = append(composeArgs, "--project-name", projectName)
	}
	composeArgs = append(
		composeArgs,
		"--project-directory",
		dir,
		"--env-file",
		filepath.Join(dir, DockerEnvFileName),
		"-f",
		filepath.Join(dir, ComposeFileName),
	)
	return CommandSpec{
		Name: "docker",
		Args: append(composeArgs, args...),
		Dir:  dir,
	}
}
