package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type AppCommandOptions struct {
	Dir                    string
	CommandArgs            []string
	DeployedOnly           bool
	OutputDir              string
	OutputFile             string
	Wait                   bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	DockerPreflightTimeout time.Duration
}

type AppCommandListOptions struct {
	Dir          string
	DeployedOnly bool
}

type ShellOptions struct {
	Dir                    string
	Wait                   bool
	ReadOnly               bool
	Stdin                  io.Reader
	Stdout                 io.Writer
	Stderr                 io.Writer
	DockerPreflightTimeout time.Duration
}

type AppCommandListResult struct {
	AppID    string                `json:"app_id"`
	Commands []AppCommandListEntry `json:"commands"`
}

type AppCommandListEntry struct {
	Trigger      []string `json:"trigger"`
	Name         string   `json:"name"`
	ForwardArgs  bool     `json:"forward_args"`
	ForwardFlags []string `json:"forward_flags,omitempty"`
}

var runCurrentAppCommand = RunCurrentAppCommandV1
var runCurrentShell = RunCurrentShellV1
var colorRuntimeGOOS = runtime.GOOS

type temporaryCommandRunner func(CommandSpec, RunOptions) error

func Shell(options ShellOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	stateSchema, err := runtimeStateSchema(options.Dir)
	if err != nil {
		return err
	}
	if stateSchema != deploy.StateSchemaV1 {
		return fmt.Errorf("shell state schema %q is unsupported; expected %q", stateSchema, deploy.StateSchemaV1)
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		return err
	}
	terminalOutput := options.Stdout
	if terminalOutput == nil {
		terminalOutput = os.Stdout
	}
	stdin, _, tty := shellCommandIO(options.Stdin, terminalOutput)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runCurrentShell(ctx, CurrentShellRunInputV1{
		DeploymentDir: options.Dir, Wait: options.Wait, ReadOnly: options.ReadOnly, Runtime: runtime, TTY: tty,
		RunOptions: RunOptions{Stdin: stdin, Stdout: options.Stdout, Stderr: options.Stderr, DockerPreflightTimeout: options.DockerPreflightTimeout},
	})
}

func shellCommandIO(input io.Reader, output io.Writer) (io.Reader, bool, bool) {
	if input == nil {
		input = os.Stdin
	}
	return input, true, readerLooksTerminal(input) && writerLooksTerminal(output)
}

func AppCommand(options AppCommandOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	terminalOutput := options.Stdout
	if terminalOutput == nil {
		terminalOutput = os.Stdout
	}
	stateSchema, err := runtimeStateSchema(options.Dir)
	if err != nil {
		return err
	}
	if stateSchema != deploy.StateSchemaV1 {
		return fmt.Errorf("app command state schema %q is unsupported; expected %q", stateSchema, deploy.StateSchemaV1)
	}
	if err := validateCurrentAppCommandRequestV1(options.Dir, options.CommandArgs, options.DeployedOnly); err != nil {
		return err
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runCurrentAppCommand(ctx, CurrentAppCommandRunInputV1{
		DeploymentDir: options.Dir, Arguments: append([]string(nil), options.CommandArgs...),
		DeployedOnly: options.DeployedOnly, OutputDir: options.OutputDir, OutputFile: options.OutputFile,
		Wait: options.Wait, Runtime: runtime, TTY: writerLooksTerminal(terminalOutput),
		RunOptions: RunOptions{Stdin: appCommandStdin(terminalOutput), Stdout: options.Stdout, Stderr: options.Stderr, DockerPreflightTimeout: options.DockerPreflightTimeout},
	})
}

func validateCurrentAppCommandRequestV1(dir string, arguments []string, deployedOnly bool) error {
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		return fmt.Errorf("read app command state: %w", err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		return err
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return fmt.Errorf("app command blueprint: %w", err)
	}
	_, _, err = MatchEnvironmentCommand(document, arguments, deployedOnly)
	return err
}

func appCommandStdin(output io.Writer) io.Reader {
	if !writerLooksTerminal(output) || !readerLooksTerminal(os.Stdin) {
		return nil
	}
	return os.Stdin
}

func appCommandError(err error) error {
	message := err.Error()
	if trimmed, ok := strings.CutPrefix(message, "docker failed: "); ok {
		return fmt.Errorf("app command failed: %s", trimmed)
	}
	return fmt.Errorf("app command failed: %w", err)
}

func runTemporaryContainerCommand(run temporaryCommandRunner, runSpec CommandSpec, cleanupSpec CommandSpec, runOptions RunOptions) error {
	parent := runOptions.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	temporaryRunOptions := runOptions
	temporaryRunOptions.Context = ctx
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan error, 1)
	go func() { done <- run(runSpec, temporaryRunOptions) }()
	var runErr error
	select {
	case runErr = <-done:
	case sig := <-signals:
		cancel()
		if err := <-done; err != nil {
			runErr = fmt.Errorf("interrupted by %s: %w", sig, err)
		} else {
			runErr = fmt.Errorf("interrupted by %s", sig)
		}
	}
	var cleanupErr error
	if cleanupSpec.Name != "" && runErr != nil {
		cleanupOptions := runOptions
		cleanupOptions.Context, cleanupOptions.Stdin, cleanupOptions.Stdout, cleanupOptions.Stderr = context.Background(), nil, nil, nil
		cleanupErr = run(cleanupSpec, cleanupOptions)
		if cleanupErr != nil && isMissingContainerCleanupError(cleanupErr) {
			cleanupErr = nil
		}
	}
	if runErr != nil && cleanupErr != nil {
		return fmt.Errorf("%w; cleanup failed: %v", runErr, cleanupErr)
	}
	if runErr != nil {
		return runErr
	}
	return cleanupErr
}

func AppCommandList(options AppCommandListOptions) (AppCommandListResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	stateSchema, err := runtimeStateSchema(options.Dir)
	if err != nil {
		return AppCommandListResult{}, err
	}
	if stateSchema != deploy.StateSchemaV1 {
		return AppCommandListResult{}, fmt.Errorf("app command list state schema %q is unsupported; expected %q", stateSchema, deploy.StateSchemaV1)
	}
	content, err := os.ReadFile(filepath.Join(options.Dir, StateFileName))
	if err != nil {
		return AppCommandListResult{}, fmt.Errorf("read app command list state: %w", err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		return AppCommandListResult{}, err
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return AppCommandListResult{}, err
	}
	return currentAppCommandListV1(document, options.DeployedOnly), nil
}

func terminalLooksColorCapable() bool {
	term := strings.TrimSpace(os.Getenv("TERM"))
	return term != "dumb" && (term != "" || colorRuntimeGOOS == "windows")
}

func writerLooksTerminal(output io.Writer) bool {
	if passthrough, ok := output.(interface{ TerminalOutput() io.Writer }); ok {
		return writerLooksTerminal(passthrough.TerminalOutput())
	}
	file, ok := output.(*os.File)
	return ok && fileLooksTerminal(file)
}

func readerLooksTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	return ok && fileLooksTerminal(file)
}

func fileLooksTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func TemporaryContainerCleanupCommand(containerName string) CommandSpec {
	return CommandSpec{Name: "docker", Args: []string{"container", "rm", "--force", "--volumes", containerName}}
}

func TemporaryContainerStopCommand(containerName string) CommandSpec {
	return CommandSpec{Name: "docker", Args: []string{"container", "stop", containerName}}
}

func withComposeRunName(spec CommandSpec, containerName string) CommandSpec {
	spec.Args = appendComposeRunOption(spec.Args, "--name", containerName)
	return spec
}

func appendComposeRunOption(args []string, values ...string) []string {
	serviceIndex := len(args) - 1
	if serviceIndex < 0 {
		return args
	}
	withOption := make([]string, 0, len(args)+len(values))
	withOption = append(withOption, args[:serviceIndex]...)
	withOption = append(withOption, values...)
	withOption = append(withOption, args[serviceIndex:]...)
	return withOption
}

func appendComposeRunEnv(args []string, values ...string) []string {
	serviceIndex := len(args) - 1
	if serviceIndex < 0 {
		return args
	}
	withEnv := make([]string, 0, len(args)+len(values)*2)
	withEnv = append(withEnv, args[:serviceIndex]...)
	for _, value := range values {
		withEnv = append(withEnv, "-e", value)
	}
	withEnv = append(withEnv, args[serviceIndex:]...)
	return withEnv
}

func quietComposeCommand(spec CommandSpec) CommandSpec {
	spec.Env = append(spec.Env, "COMPOSE_PROGRESS=quiet", "COMPOSE_ANSI=never")
	return spec
}

func temporaryConfigCheckProjectName() string {
	return fmt.Sprintf("reploy-config-check-%d-%d", os.Getpid(), time.Now().UnixNano())
}

func temporaryAppCommandProjectName() string {
	return fmt.Sprintf("reploy-app-command-%d-%d", os.Getpid(), time.Now().UnixNano())
}

func temporaryOneOffContainerName(projectName string, label string) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		projectName = "reploy"
	}
	return fmt.Sprintf("%s-%s-%d-%d", projectName, label, os.Getpid(), time.Now().UnixNano())
}

func isMissingContainerCleanupError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such container")
}
