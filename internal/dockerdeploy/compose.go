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
	"github.com/omry/reploy/internal/providers"
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
	Capture                *CommandOutputCapture
}

// CommandOutputCapture identifies one build invocation's explicit diagnostic
// channel. Its non-zero-sized token makes each operation's owner distinct.
type CommandOutputCapture struct {
	token byte
}

func effectiveCommandOutputCapture(options RunOptions) *CommandOutputCapture {
	return options.Capture
}

// NewCommandOutputCapture creates an operation-owned diagnostic channel.
func NewCommandOutputCapture() *CommandOutputCapture {
	return &CommandOutputCapture{token: 1}
}

type commandOutputCaptureError struct {
	cause  error
	owner  *CommandOutputCapture
	output string
}

func (err *commandOutputCaptureError) Error() string {
	if err == nil || err.cause == nil {
		return ""
	}
	return err.cause.Error()
}

func (err *commandOutputCaptureError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

// Wrap attaches bounded live output to an error without changing its rendered
// message or its errors.Is/errors.As behavior.
func (capture *CommandOutputCapture) Wrap(err error, output string) error {
	if capture == nil || err == nil {
		return err
	}
	output = trimmedCommandOutput(output)
	if output == "" {
		return err
	}
	return &commandOutputCaptureError{cause: err, owner: capture, output: output}
}

// CommandOutputCaptureDiagnostic returns only output owned by capture in the
// returned error chain. Structured provider errors remain a trust boundary;
// their arbitrary hidden causes are never inspected for this diagnostic.
func CommandOutputCaptureDiagnostic(err error, capture *CommandOutputCapture) string {
	if err == nil || capture == nil {
		return ""
	}
	var visit func(error) string
	visit = func(active error) string {
		if active == nil {
			return ""
		}
		switch typed := active.(type) {
		case *commandOutputCaptureError:
			if typed.owner == capture {
				return typed.output
			}
			return ""
		case *providers.BuildErrorV1:
			return ""
		}
		if joined, ok := active.(interface{ Unwrap() []error }); ok {
			for _, cause := range joined.Unwrap() {
				if diagnostic := visit(cause); diagnostic != "" {
					return diagnostic
				}
			}
			return ""
		}
		return visit(errors.Unwrap(active))
	}
	return visit(err)
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
	run, err := bindDockerCommandRunnerV1(ctx, spec, options.DockerPreflightTimeout)
	if err != nil {
		return err
	}
	return run(spec, options)
}

func bindDockerCommandRunnerV1(ctx context.Context, spec CommandSpec, timeout time.Duration) (commandRunner, error) {
	_, run, err := bindPinnedDockerCommandRunnerV1(ctx, spec, timeout)
	return run, err
}

func bindPinnedDockerCommandRunnerV1(
	ctx context.Context,
	spec CommandSpec,
	timeout time.Duration,
) (CommandSpec, commandRunner, error) {
	if ctx == nil {
		return CommandSpec{}, nil, fmt.Errorf("bind Docker command runner requires a context")
	}
	_, end := buildprofile.Start(ctx, "Docker preflight")
	endpoint, err := dockerPreflight(ctx, spec, effectiveDockerPreflightTimeout(timeout))
	end(err)
	if err != nil {
		return CommandSpec{}, nil, err
	}
	executable := spec.Name
	pinned := pinDockerEndpointV1(spec, endpoint)
	run := func(command CommandSpec, options RunOptions) error {
		if command.Name != executable {
			return fmt.Errorf("Docker operation changed executable from %q to %q", executable, command.Name)
		}
		return runCommandWithoutDockerPreflight(pinDockerEndpointV1(command, endpoint), options)
	}
	return pinned, run, nil
}

func commandRunnerForPinnedDockerEndpointV1(endpoint string, run commandRunner) (commandRunner, error) {
	if run == nil {
		return nil, fmt.Errorf("pin Docker endpoint requires a command runner")
	}
	if !localDockerEndpointV1(endpoint) {
		return nil, fmt.Errorf("controlled-session Docker endpoint %q is not local", endpoint)
	}
	return func(spec CommandSpec, options RunOptions) error {
		return run(pinDockerEndpointV1(spec, endpoint), options)
	}, nil
}

// runCommandWithoutDockerPreflight executes non-Docker commands and Docker
// commands whose exact local endpoint was already pinned by runDockerCommand.
// Recognizable unpinned Docker commands fail closed.
func runCommandWithoutDockerPreflight(spec CommandSpec, options RunOptions) (resultErr error) {
	if dockerCommandExecutableV1(spec.Name) && !pinnedDockerEndpointV1(spec) {
		return fmt.Errorf("Docker command %q requires a verified pinned local endpoint", spec.Name)
	}
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
	capture := effectiveCommandOutputCapture(options)
	var capturedOutput bytes.Buffer
	var capturedStdout, capturedStderr bytes.Buffer
	var diagnosticTail dockerBuildOutputTail
	stdout, stderr := options.Stdout, options.Stderr
	if capture != nil {
		if stdout != nil {
			stdout = io.MultiWriter(stdout, &diagnosticTail)
		} else {
			stdout = &diagnosticTail
		}
		if stderr != nil {
			stderr = io.MultiWriter(stderr, &diagnosticTail)
		} else {
			stderr = &diagnosticTail
		}
	}
	if options.Stdout == nil && options.Stderr == nil {
		if capture == nil {
			command.Stdout = &capturedOutput
			command.Stderr = &capturedOutput
		} else {
			command.Stdout = io.MultiWriter(&capturedStdout, &diagnosticTail)
			command.Stderr = io.MultiWriter(&capturedStderr, &diagnosticTail)
		}
	} else {
		command.Stdout = stdout
		command.Stderr = stderr
	}
	if err := command.Run(); err != nil {
		var result error
		output := capturedOutput.String()
		if capture != nil {
			output = capturedStdout.String() + capturedStderr.String()
		}
		if output := trimmedCommandOutput(output); output != "" {
			result = fmt.Errorf("%s failed: %w\ncommand output:\n%s", spec.Name, err, output)
		} else {
			result = fmt.Errorf("%s failed: %w", spec.Name, err)
		}
		if capture != nil {
			return capture.Wrap(result, diagnosticTail.String())
		}
		return result
	}
	return nil
}

func dockerCommandExecutableV1(name string) bool {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.TrimSpace(name))), ".exe")
	return base == "docker"
}

func pinnedDockerEndpointV1(spec CommandSpec) bool {
	host, hostSet := commandSpecEnvironmentValueV1(spec, "DOCKER_HOST")
	contextName, contextSet := commandSpecEnvironmentValueV1(spec, "DOCKER_CONTEXT")
	return hostSet && contextSet && contextName == "" && localDockerEndpointV1(host)
}

func commandSpecEnvironmentValueV1(spec CommandSpec, name string) (string, bool) {
	prefix := name + "="
	for index := len(spec.Env) - 1; index >= 0; index-- {
		if strings.HasPrefix(spec.Env[index], prefix) {
			return strings.TrimSpace(strings.TrimPrefix(spec.Env[index], prefix)), true
		}
	}
	return "", false
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

func checkDockerResponsive(ctx context.Context, spec CommandSpec, timeout time.Duration) (string, error) {
	timeout = effectiveDockerPreflightTimeout(timeout)
	preflightCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint, err := verifiedLocalDockerEndpointV1(preflightCtx, spec, timeout)
	if err != nil {
		return "", err
	}
	spec = pinDockerEndpointV1(spec, endpoint)

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
			return "", fmt.Errorf("docker daemon did not respond within %s", timeout)
		}
		if output := trimmedCommandOutput(output.String()); output != "" {
			return "", fmt.Errorf("docker daemon check failed: %w\ncommand output:\n%s", err, output)
		}
		return "", fmt.Errorf("docker daemon check failed: %w", err)
	}
	return endpoint, nil
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
