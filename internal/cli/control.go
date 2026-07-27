package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/omry/reploy/internal/dockerdeploy"
)

type embeddedControlOptions struct {
	Dir        string
	ScriptName string
	Command    []string
}

const embeddedControlDefaultLogsTail = "100"

func runEmbeddedControl(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseEmbeddedControlOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy control usage error: %v\n", err)
		printEmbeddedControlUsage(stderr, embeddedControlUsageContext{ScriptName: "reployctl"})
		return 2
	}
	metadata, found, err := dockerdeploy.LoadEmbeddedControlMetadataV1(context.Background(), options.Dir)
	if err != nil {
		fmt.Fprintf(stderr, "reploy control error: %v\n", err)
		return 1
	}
	if !found {
		fmt.Fprintln(stderr, "reploy control error: installed state-v1 deployment metadata is missing")
		return 1
	}
	scriptName := options.ScriptName
	if strings.TrimSpace(scriptName) == "" || scriptName == "reployctl" {
		scriptName = embeddedControlDefaultString(metadata.ControlScript, "reployctl")
	}
	context := embeddedControlUsageContext{
		Dir:         options.Dir,
		ScriptName:  scriptName,
		SystemdUnit: metadata.SystemdUnit,
	}
	if len(options.Command) == 0 || isHelpArg(options.Command[0]) {
		printEmbeddedControlUsage(stdout, context)
		return 0
	}
	cmd := options.Command[0]
	rest := options.Command[1:]
	switch cmd {
	case "up":
		return runEmbeddedControlRuntime(context, "up", rest, stdout, stderr, globalOptions)
	case "stop":
		return runEmbeddedControlRuntime(context, "stop", rest, stdout, stderr, globalOptions)
	case "restart":
		return runEmbeddedControlRuntime(context, "restart", rest, stdout, stderr, globalOptions)
	case "status", "ps":
		return runEmbeddedControlRuntime(context, "status", rest, stdout, stderr, globalOptions)
	case "logs":
		if embeddedControlLogsHelpRequested(rest) {
			printEmbeddedControlLogsHelp(stdout, context)
			return 0
		}
		return runEmbeddedControlRuntime(context, "logs", rest, stdout, stderr, globalOptions)
	case "health":
		if len(rest) > 0 && isHelpArg(rest[0]) {
			fmt.Fprintf(stdout, "usage: %s health\n", context.ScriptName)
			return 0
		}
		if len(rest) > 0 {
			fmt.Fprintf(stderr, "health: unexpected argument: %s\n", rest[0])
			return 2
		}
		return runEmbeddedControlHealth(context, stdout, stderr)
	case "enable", "disable":
		if embeddedControlContextUsesSystemd(context) {
			return runEmbeddedControlSystemd(context, cmd, rest, stdout, stderr)
		}
	}
	appArgs, matched, appErr := embeddedControlAppArguments(context.Dir, options.Command)
	if appErr != nil {
		fmt.Fprintf(stderr, "%s: %v\n", context.ScriptName, appErr)
		return 2
	}
	if matched {
		return runEmbeddedControlAppCommand(context, appArgs, stdout, stderr, globalOptions)
	}
	printEmbeddedControlUsage(stderr, context)
	fmt.Fprintf(stderr, "unknown command: %s\n", cmd)
	return 2
}

func embeddedControlAppArguments(dir string, args []string) ([]string, bool, error) {
	parsed, err := parseDockerAppOptions(args)
	if err != nil {
		if appOptionWasPresent(args, "--output-dir") || appOptionWasPresent(args, "--output-file") {
			return nil, false, err
		}
		return nil, false, nil
	}
	if parsed.Commands || parsed.Format != "" || parsed.DirExplicit || parsed.DeployedOnly || len(parsed.CommandArgs) == 0 || !embeddedControlMatchesAppCommand(dir, parsed.CommandArgs) {
		return nil, false, nil
	}
	result := []string{"--deployed-only", "--dir", dir}
	if parsed.OutputDir != "" {
		result = append(result, "--output-dir", parsed.OutputDir)
	}
	if parsed.OutputFile != "" {
		result = append(result, "--output-file", parsed.OutputFile)
	}
	if parsed.Wait {
		result = append(result, "--wait")
	}
	result = append(result, parsed.CommandArgs...)
	return result, true, nil
}

func parseEmbeddedControlOptions(args []string) (embeddedControlOptions, error) {
	var options embeddedControlOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return embeddedControlOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
		case "--script-name":
			value, ok := optionValue(args, &index)
			if !ok {
				return embeddedControlOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.ScriptName = value
		case "--":
			options.Command = append(options.Command, args[index+1:]...)
			index = len(args)
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				continue
			}
			if strings.HasPrefix(arg, "--script-name=") {
				options.ScriptName = strings.TrimPrefix(arg, "--script-name=")
				continue
			}
			options.Command = append(options.Command, args[index:]...)
			index = len(args)
		}
	}
	if strings.TrimSpace(options.Dir) == "" {
		return embeddedControlOptions{}, fmt.Errorf("--dir is required")
	}
	if strings.TrimSpace(options.ScriptName) == "" {
		options.ScriptName = "reployctl"
	}
	return options, nil
}

type embeddedControlUsageContext struct {
	Dir         string
	ScriptName  string
	SystemdUnit string
}

func printEmbeddedControlUsage(output io.Writer, context embeddedControlUsageContext) {
	scriptName := embeddedControlDefaultString(context.ScriptName, "reployctl")
	fmt.Fprintf(output, "usage: %s COMMAND [ARGS...]\n", scriptName)
	fmt.Fprintln(output, "commands:")
	for _, command := range []string{"up", "stop", "restart", "status", "logs", "health"} {
		fmt.Fprintf(output, "  %s\n", command)
	}
	if embeddedControlContextUsesSystemd(context) {
		fmt.Fprintln(output, "  enable")
		fmt.Fprintln(output, "  disable")
	}
	hasAppCommands := false
	if context.Dir != "" {
		if result, err := dockerdeploy.AppCommandList(dockerdeploy.AppCommandListOptions{Dir: context.Dir, DeployedOnly: true}); err == nil {
			for _, command := range result.Commands {
				hasAppCommands = true
				fmt.Fprintf(output, "  %s\n", strings.Join(command.Trigger, " "))
			}
		}
	}
	fmt.Fprintln(output, "  help")
	if hasAppCommands {
		fmt.Fprintln(output, "app command options:")
		fmt.Fprintln(output, "  --output-dir DIR")
		fmt.Fprintln(output, "  --output-file FILE")
		fmt.Fprintln(output, "  --wait")
	}
	fmt.Fprintln(output, "lifecycle options:")
	fmt.Fprintln(output, "  up --wait|--drain|--force")
	fmt.Fprintln(output, "  stop/restart --wait")
}

func printEmbeddedControlLogsHelp(output io.Writer, context embeddedControlUsageContext) {
	scriptName := embeddedControlDefaultString(context.ScriptName, "reployctl")
	fmt.Fprintf(output, "Usage: %s logs [OPTIONS]\n", scriptName)
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Show deployed service logs with timestamps.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Options:")
	fmt.Fprintf(output, "  --tail N     Show only the last N log lines (default: %s)\n", embeddedControlDefaultLogsTail)
	fmt.Fprintln(output, "  --tail all   Show the complete available log")
	fmt.Fprintln(output, "  --follow, -f")
	fmt.Fprintln(output, "              Follow logs instead of exiting after current output")
	fmt.Fprintln(output, "  -h, --help   Show logs help")
}

func runEmbeddedControlRuntime(
	context embeddedControlUsageContext,
	action string,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	globalOptions globalDeploymentOptions,
) int {
	if action == "logs" {
		args = embeddedControlLogsArgs(args)
	}
	if embeddedControlContextUsesSystemd(context) {
		switch action {
		case "status":
			return runEmbeddedControlSystemd(context, action, args, stdout, stderr)
		case "logs":
			return runEmbeddedControlJournal(context, args, stdout, stderr)
		}
	}
	runtimeArgs := append([]string{}, args...)
	runtimeArgs = append(runtimeArgs, "--dir", context.Dir)
	return runDockerRuntimeControl(action, runtimeArgs, stdout, stderr, globalOptions)
}

func embeddedControlLogsHelpRequested(args []string) bool {
	return len(args) > 0 && isHelpArg(args[0])
}

func embeddedControlLogsArgs(args []string) []string {
	if withoutTailAll, ok := embeddedControlLogsWithoutTailAll(args); ok {
		return withoutTailAll
	}
	if embeddedControlLogsTailSpecified(args) {
		return args
	}
	withDefault := append([]string{}, args...)
	return append(withDefault, "--tail", embeddedControlDefaultLogsTail)
}

func embeddedControlLogsTailSpecified(args []string) bool {
	for _, arg := range args {
		if arg == "--tail" || strings.HasPrefix(arg, "--tail=") {
			return true
		}
	}
	return false
}

func embeddedControlLogsWithoutTailAll(args []string) ([]string, bool) {
	finalTail := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "--tail=") {
			finalTail = strings.TrimSpace(strings.TrimPrefix(arg, "--tail="))
			continue
		}
		if arg == "--tail" && index+1 < len(args) {
			finalTail = strings.TrimSpace(args[index+1])
			index++
		}
	}
	if finalTail != "all" {
		return nil, false
	}

	normalized := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "--tail=") {
			continue
		}
		if arg == "--tail" && index+1 < len(args) {
			index++
			continue
		}
		normalized = append(normalized, arg)
	}
	return normalized, true
}

func runEmbeddedControlHealth(context embeddedControlUsageContext, stdout io.Writer, stderr io.Writer) int {
	err := dockerdeploy.TestServer(dockerdeploy.TestOptions{Dir: context.Dir, Stdout: stdout, Timeout: 5 * time.Second})
	if err != nil {
		errorStderr, writerErr := deploymentErrorWriter(context.Dir, stderr)
		if writerErr != nil {
			errorStderr = stderr
		}
		fmt.Fprintf(errorStderr, "reploy control health error: %v\n", err)
		return 1
	}
	return 0
}

func runEmbeddedControlAppCommand(
	context embeddedControlUsageContext,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	globalOptions globalDeploymentOptions,
) int {
	return runDockerApp(args, stdout, stderr, globalOptions)
}

func runEmbeddedControlSystemd(
	context embeddedControlUsageContext,
	action string,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument: %s\n", action, args[0])
		return 2
	}
	service := embeddedControlContextSystemdUnit(context)
	if service == "" {
		fmt.Fprintln(stderr, "reploy control error: installed service is not recorded")
		return 1
	}
	return runEmbeddedControlExternal(context.Dir, stdout, stderr, "systemctl", action, service)
}

func runEmbeddedControlJournal(
	context embeddedControlUsageContext,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	options, err := parseDockerObservationOptions(append(args, "--dir", context.Dir))
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		return 2
	}
	service := embeddedControlContextSystemdUnit(context)
	if service == "" {
		fmt.Fprintln(stderr, "reploy control error: installed service is not recorded")
		return 1
	}
	journalArgs := []string{"-u", service}
	if options.Tail != "" {
		journalArgs = append(journalArgs, "-n", options.Tail)
	}
	if options.Follow {
		journalArgs = append(journalArgs, "-f")
	}
	return runEmbeddedControlExternal(context.Dir, stdout, stderr, "journalctl", journalArgs...)
}

func runEmbeddedControlExternal(dir string, stdout io.Writer, stderr io.Writer, name string, args ...string) int {
	wrappedStdout, wrappedStderr, err := dockerdeploy.DeploymentOutputWriters(dir, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy control error: %v\n", err)
		return 1
	}
	command := exec.Command(name, args...)
	command.Stdout = wrappedStdout
	command.Stderr = wrappedStderr
	if err := command.Run(); err != nil {
		if code, ok := externalCommandExitCode(err); ok {
			return code
		}
		fmt.Fprintf(wrappedStderr, "%s failed: %v\n", name, err)
		return 1
	}
	return 0
}

func externalCommandExitCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	code := exitErr.ExitCode()
	if code < 0 {
		return 0, false
	}
	return code, true
}

func embeddedControlMatchesAppCommand(dir string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	result, err := dockerdeploy.AppCommandList(dockerdeploy.AppCommandListOptions{Dir: dir, DeployedOnly: true})
	if err != nil {
		return false
	}
	for _, command := range result.Commands {
		if len(args) < len(command.Trigger) {
			continue
		}
		matches := true
		for index, part := range command.Trigger {
			if args[index] != part {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func embeddedControlContextUsesSystemd(context embeddedControlUsageContext) bool {
	return strings.TrimSpace(context.SystemdUnit) != ""
}

func embeddedControlContextSystemdUnit(context embeddedControlUsageContext) string {
	return strings.TrimSpace(context.SystemdUnit)
}

func embeddedControlDefaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
