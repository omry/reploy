package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type ConfigCheckOptions struct {
	Dir         string
	CommandArgs []string
	Stdout      io.Writer
	Stderr      io.Writer
}

type AppCommandOptions struct {
	Dir         string
	CommandArgs []string
	Stdout      io.Writer
	Stderr      io.Writer
}

type AppCommandListOptions struct {
	Dir string
}

type AppCommandListResult struct {
	AppID    string
	Commands []string
}

var runConfigCheckCommand = runCommand
var runAppCommand = runCommand

type temporaryComposeRunner func(CommandSpec, RunOptions) error

func ConfigCheck(options ConfigCheckOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return err
	}
	stdout, stderr := deploymentOutputWritersForDeployment(options.Dir, state, options.Stdout, options.Stderr)
	runOptions := RunOptions{
		Stdout: stdout,
		Stderr: stderr,
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return err
	}
	if err := ensureOneOffCommandDirs(options.Dir, pack); err != nil {
		return err
	}
	if err := ensureRuntimeCompose(options.Dir); err != nil {
		return fmt.Errorf("ensure runtime compose: %w", err)
	}
	command, forwardedArgs, err := pack.Docker.MatchCommand(options.CommandArgs)
	if err != nil {
		return err
	}
	configDisplayDir := appConfigDisplayDir(options.Dir, pack)
	projectName := installComposeProjectName(state)
	return runTemporaryComposeCommand(
		runConfigCheckCommand,
		ConfigCheckCommandForProject(options.Dir, command.Name, forwardedArgs, projectName, configDisplayDir),
		CommandSpec{},
		runOptions,
	)
}

func AppCommand(options AppCommandOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	terminalOutput := options.Stdout
	if terminalOutput == nil {
		terminalOutput = os.Stdout
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return err
	}
	stdout, stderr := deploymentOutputWritersForDeployment(options.Dir, state, options.Stdout, options.Stderr)
	runOptions := RunOptions{
		Stdin:  appCommandStdin(terminalOutput),
		Stdout: stdout,
		Stderr: stderr,
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return err
	}
	if err := ensureOneOffCommandDirs(options.Dir, pack); err != nil {
		return err
	}
	command, forwardedArgs, err := pack.Docker.MatchAppCommand(options.CommandArgs)
	if err != nil {
		return err
	}
	if _, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: options.Dir, Stdout: options.Stdout, Stderr: options.Stderr}); err != nil {
		return fmt.Errorf("prepare installation bundle: %w", err)
	}
	if err := ensureRuntimeCompose(options.Dir); err != nil {
		return fmt.Errorf("ensure runtime compose: %w", err)
	}
	configDisplayDir := appConfigDisplayDir(options.Dir, pack)
	projectName := installComposeProjectName(state)
	spec := AppCommandForProject(options.Dir, command.Name, forwardedArgs, projectName, configDisplayDir)
	spec = withAppTerminalEnv(spec, pack.App.Terminal, terminalOutput)
	err = runTemporaryComposeCommand(
		runAppCommand,
		spec,
		CommandSpec{},
		runOptions,
	)
	if err != nil {
		return appCommandError(err)
	}
	return nil
}

func installComposeProjectName(state deploy.DeploymentState) string {
	if state.Install == nil {
		return ""
	}
	return state.Install.ComposeProject
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

func ensureOneOffCommandDirs(dir string, pack deploy.AppPack) error {
	if err := os.MkdirAll(filepath.Join(dir, pack.Docker.DeploymentDirs.Config), 0o700); err != nil {
		return err
	}
	return ensureWritableRuntimeDir(filepath.Join(dir, RuntimeDirName))
}

func ensureWritableRuntimeDir(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	probe := filepath.Join(path, ".reploy-write-test")
	if err := os.WriteFile(probe, []byte("ok\n"), 0o644); err == nil {
		return os.Remove(probe)
	}
	if err := os.RemoveAll(path); err != nil {
		if chmodErr := os.Chmod(path, 0o755); chmodErr != nil {
			return err
		}
		if retryErr := os.RemoveAll(path); retryErr != nil {
			return err
		}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(probe, []byte("ok\n"), 0o644); err != nil {
		return err
	}
	return os.Remove(probe)
}

func appConfigDisplayDir(dir string, pack deploy.AppPack) string {
	configDir := filepath.Join(dir, pack.Docker.DeploymentDirs.Config)
	absoluteConfigDir, err := filepath.Abs(configDir)
	if err != nil {
		return filepath.Clean(configDir)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return absoluteConfigDir
	}
	relativeConfigDir, err := filepath.Rel(workingDir, absoluteConfigDir)
	if err != nil || relativeConfigDir == ".." || strings.HasPrefix(relativeConfigDir, ".."+string(filepath.Separator)) {
		return absoluteConfigDir
	}
	return relativeConfigDir
}

func runTemporaryComposeCommand(run temporaryComposeRunner, runSpec CommandSpec, cleanupSpec CommandSpec, runOptions RunOptions) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	temporaryRunOptions := runOptions
	temporaryRunOptions.Context = ctx

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	done := make(chan error, 1)
	go func() {
		done <- run(runSpec, temporaryRunOptions)
	}()

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
	if cleanupSpec.Name != "" {
		cleanupErr = run(cleanupSpec, runOptions)
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
	state, err := loadState(options.Dir)
	if err != nil {
		return AppCommandListResult{}, err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return AppCommandListResult{}, err
	}
	commands := []string{}
	for _, command := range pack.Docker.AppCommands() {
		commands = append(commands, strings.Join(command.Trigger, " "))
	}
	return AppCommandListResult{AppID: pack.AppID, Commands: commands}, nil
}

func ConfigCheckCommand(dir string, commandName string, forwardedArgs []string) CommandSpec {
	return ConfigCheckCommandForProject(dir, commandName, forwardedArgs, "")
}

func ConfigCheckCommandForProject(dir string, commandName string, forwardedArgs []string, projectName string, configDisplayDir ...string) CommandSpec {
	args := []string{
		"run",
		"--rm",
		"--no-deps",
		"-e",
		fmt.Sprintf("REPLOY_CONTAINER_COMMAND=%s", commandName),
	}
	args = append(args, "-e", fmt.Sprintf("REPLOY_FORWARDED_ARGC=%d", len(forwardedArgs)))
	for index, arg := range forwardedArgs {
		args = append(args, "-e", fmt.Sprintf("REPLOY_FORWARDED_ARG_%d=%s", index, arg))
	}
	if len(configDisplayDir) > 0 && strings.TrimSpace(configDisplayDir[0]) != "" {
		args = append(args, "-e", "REPLOY_CONFIG_CONTAINER_DIR=/config")
		args = append(args, "-e", fmt.Sprintf("REPLOY_CONFIG_DISPLAY_DIR=%s", configDisplayDir[0]))
	}
	args = append(args, "app")
	return quietComposeCommand(composeCommandWithProject(dir, projectName, args...))
}

func AppCommandForProject(dir string, commandName string, forwardedArgs []string, projectName string, configDisplayDir ...string) CommandSpec {
	spec := ConfigCheckCommandForProject(dir, commandName, forwardedArgs, projectName, configDisplayDir...)
	spec.Env = withoutEnvValue(spec.Env, "COMPOSE_ANSI=never")
	spec.Env = append(spec.Env, "REPLOY_INCLUDE_RUNTIME_OVERRIDES=0", "REPLOY_CONFIG_MOUNT=rw")
	spec.Args = appendComposeRunEnv(
		spec.Args,
		"REPLOY_INCLUDE_RUNTIME_OVERRIDES=0",
		"REPLOY_CONFIG_MOUNT=rw",
		"REPLOY_APP_COMMAND_PREFIX=reploy app",
	)
	return spec
}

func withAppTerminalEnv(spec CommandSpec, terminal deploy.AppTerminalConfig, output io.Writer) CommandSpec {
	colorEnv := appTerminalColorEnv(terminal)
	env := []string{}
	if colorEnv != "" {
		env = append(env, colorEnv)
	}
	if columnsEnv := terminalColumnsEnv(output); columnsEnv != "" {
		env = append(env, columnsEnv)
	}
	if len(env) > 0 {
		spec.Args = appendComposeRunEnv(spec.Args, env...)
	}
	return spec
}

func appTerminalColorEnv(terminal deploy.AppTerminalConfig) string {
	name := strings.TrimSpace(terminal.ColorEnv)
	if name == "" {
		return ""
	}
	if value, ok := os.LookupEnv(name); ok {
		return name + "=" + value
	}
	value := reployColorValue()
	if value == "" {
		return ""
	}
	return name + "=" + value
}

func reployColorValue() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REPLOY_COLOR"))) {
	case "always":
		return "always"
	case "never":
		return "never"
	case "", "auto":
		if os.Getenv("NO_COLOR") != "" {
			return "never"
		}
		if terminalLooksColorCapable() {
			return "always"
		}
		return ""
	default:
		return ""
	}
}

func terminalLooksColorCapable() bool {
	term := strings.TrimSpace(os.Getenv("TERM"))
	return term != "" && term != "dumb"
}

func terminalColumnsEnv(output io.Writer) string {
	if columns := validTerminalColumns(os.Getenv("COLUMNS")); columns != "" {
		return "COLUMNS=" + columns
	}
	if !writerLooksTerminal(output) {
		return ""
	}
	command := exec.Command("stty", "size")
	command.Stdin = os.Stdin
	content, err := command.Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(content))
	if len(fields) != 2 {
		return ""
	}
	if columns := validTerminalColumns(fields[1]); columns != "" {
		return "COLUMNS=" + columns
	}
	return ""
}

func validTerminalColumns(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	columns, err := strconv.Atoi(value)
	if err != nil || columns < 20 {
		return ""
	}
	return strconv.Itoa(columns)
}

func writerLooksTerminal(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	return fileLooksTerminal(file)
}

func readerLooksTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	return fileLooksTerminal(file)
}

func fileLooksTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func withoutEnvValue(values []string, unwanted string) []string {
	filtered := values[:0]
	for _, value := range values {
		if value == unwanted {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}

func TemporaryComposeCleanupCommand(dir string, projectName string) CommandSpec {
	return quietComposeCommand(composeCommandWithProject(dir, projectName, "down", "--remove-orphans", "--volumes", "--timeout", "0"))
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
