package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	reploy "github.com/omry/reploy"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/dockerdeploy"
)

const defaultPackIndexURL = "https://raw.githubusercontent.com/omry/reploy/main/blueprint-index.json"
const packIndexURLEnv = "REPLOY_BLUEPRINT_INDEX_URL"
const appRefUsageHint = "use an indexed shorthand such as arbiter-server or arbiter-server==VERSION, a provider ref such as pypi://PACKAGE/PATH/APP.blueprint.yaml or github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF, or a local path starting with . or /"

var dockerDirectInstall = dockerdeploy.DirectInstall
var dockerInstall = dockerdeploy.Install
var dockerPrintInstallSuccess = dockerdeploy.PrintInstallSuccess
var dockerUninstall = dockerdeploy.Uninstall
var printReploySystemdServices = dockerdeploy.PrintReploySystemdServices
var dockerUninstallNeedsRoot = dockerdeploy.UninstallNeedsRoot
var dockerRuntime = dockerdeploy.Runtime
var dockerTestServer = dockerdeploy.TestServer
var dockerShell = dockerdeploy.Shell
var dockerStageInit = dockerdeploy.Init
var dockerStageUpdate = dockerdeploy.Update

func Main(args []string, stdout io.Writer, stderr io.Writer) int {
	if message := windowsWSLBoundaryError(runtime.GOOS, os.LookupEnv, os.Getwd); message != "" {
		fmt.Fprintln(stderr, message)
		return 1
	}

	bare := len(args) == 0
	globalOptions, remainingArgs, err := parseGlobalDeploymentOptions(args)
	if err != nil {
		return printTopLevelUsageError(stderr, "%v", err)
	}
	args = remainingArgs
	if len(args) == 0 {
		if !bare {
			return printTopLevelUsageError(stderr, "expected command")
		}
		return runNoCommand(globalOptions.Target, stdout, stderr)
	}
	switch args[0] {
	case "-h", "--help", "help":
		printHelp(stdout)
		return 0
	case "--version", "version":
		fmt.Fprintf(stdout, "reploy %s\n", reploy.DisplayVersion())
		return 0
	case "_control":
		return runEmbeddedControl(args[1:], stdout, stderr, globalOptions)
	case "index":
		return runPackIndex(args[0], args[1:], stdout, stderr)
	case "services":
		return runServices(args[1:], stdout, stderr)
	default:
		if globalOptions.Target == "docker" && isDeploymentCommand(args[0]) {
			return runDocker(args, stdout, stderr, globalOptions)
		}
		if strings.HasPrefix(args[0], "-") {
			return printTopLevelUsageError(stderr, "unknown option: %s", args[0])
		}
		if globalOptions.Target == "docker" {
			if suggestion := topLevelAppCommandSuggestion(args); suggestion != "" {
				return printTopLevelUsageError(stderr, "unknown command: %s; did you mean `%s`?", args[0], suggestion)
			}
		}
		return printTopLevelUsageError(stderr, "unknown command: %s", args[0])
	}
}

func windowsWSLBoundaryError(goos string, lookupEnv func(string) (string, bool), getwd func() (string, error)) string {
	if goos != "windows" {
		return ""
	}
	for _, name := range []string{"WSL_DISTRO_NAME", "WSL_INTEROP"} {
		if value, ok := lookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return windowsWSLBoundaryMessage
		}
	}
	if cwd, err := getwd(); err == nil && isWSLWindowsPath(cwd) {
		return windowsWSLBoundaryMessage
	}
	return ""
}

const windowsWSLBoundaryMessage = "reploy error: reploy.exe is running from WSL or a WSL filesystem; run reploy.exe from PowerShell or cmd.exe in a Windows path, or use the Linux reploy binary inside WSL"

func isWSLWindowsPath(path string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(path)), "/", `\`)
	return strings.HasPrefix(normalized, `\\wsl.localhost\`) ||
		strings.HasPrefix(normalized, `\\wsl$\`) ||
		strings.HasPrefix(normalized, `\\?\unc\wsl.localhost\`) ||
		strings.HasPrefix(normalized, `\\?\unc\wsl$\`)
}

func printTopLevelUsageError(stderr io.Writer, format string, values ...any) int {
	fmt.Fprintf(stderr, "reploy usage error: "+format+"\n", values...)
	printDockerShortUsage(stderr)
	return 2
}

func runNoCommand(target string, stdout io.Writer, stderr io.Writer) int {
	if target == "docker" && implicitDeploymentStateExists(dockerdeploy.DefaultDeploymentDir, false) {
		return runDockerDeploymentSummary(stdout, stderr)
	}
	printShortUsage(stdout)
	return 0
}

func runDockerDeploymentSummary(stdout io.Writer, stderr io.Writer) int {
	dir := resolveImplicitDeploymentDir(dockerdeploy.DefaultDeploymentDir, false, io.Discard)
	state, err := dockerdeploy.LoadState(dir)
	if err != nil {
		fmt.Fprintf(stderr, "reploy error: %v\n", err)
		return 1
	}
	stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(dir, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy error: %v\n", err)
		return 1
	}
	deployedOnly := state.Phase == deploy.PhaseInstalled
	result, err := dockerdeploy.AppCommandList(dockerdeploy.AppCommandListOptions{Dir: dir, DeployedOnly: deployedOnly})
	if err != nil {
		fmt.Fprintf(stderr, "reploy error: %v\n", err)
		return 1
	}
	printDeploymentSummary(stdout, dir, state, result.Commands)
	return 0
}

func printDeploymentSummary(output io.Writer, dir string, state deploy.DeploymentState, appCommands []dockerdeploy.AppCommandListEntry) {
	appID := strings.TrimSpace(state.AppID)
	if appID == "" {
		appID = "unknown"
	}
	fmt.Fprintf(output, "app: %s\n", appID)
	fmt.Fprintf(output, "reploy: %s\n", reploy.DisplayVersion())
	fmt.Fprintf(output, "context: %s deployment\n", deploymentSummaryContext(state.Phase))
	fmt.Fprintf(output, "directory: %s\n", deploymentSummaryDir(dir))
	fmt.Fprintln(output, "useful commands:")
	for _, command := range deploymentSummaryCommands(state) {
		fmt.Fprintf(output, "  %s\n", command)
	}
	if len(appCommands) > 0 {
		fmt.Fprintln(output, "app command examples:")
		for _, command := range deploymentSummaryAppCommandExamples(state, appCommands) {
			fmt.Fprintf(output, "  %s\n", command)
		}
	}
	fmt.Fprintf(output, "Run '%s' for all app commands.\n", deploymentSummaryAppCommandListCommand(state))
}

func deploymentSummaryContext(phase deploy.Phase) string {
	switch phase {
	case deploy.PhaseInstalled:
		return "installed"
	case deploy.PhaseStaged:
		return "staged"
	default:
		return string(phase)
	}
}

func deploymentSummaryDir(dir string) string {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return absolute
}

func deploymentSummaryCommands(state deploy.DeploymentState) []string {
	switch state.Phase {
	case deploy.PhaseInstalled:
		return []string{
			"reploy up|down|status",
			"reploy logs --tail 100",
			"reploy restart",
			"reploy uninstall --from .",
		}
	default:
		return []string{
			"reploy info",
			"reploy bundle list",
			"reploy up|down|status",
			"reploy logs --tail 50",
			"reploy install --scope user --to DIR",
		}
	}
}

func deploymentSummaryAppCommandExamples(state deploy.DeploymentState, commands []dockerdeploy.AppCommandListEntry) []string {
	prefix := deploymentSummaryAppCommandListCommand(state)
	limit := 3
	if len(commands) < limit {
		limit = len(commands)
	}
	examples := make([]string, 0, limit+1)
	for index := 0; index < limit; index++ {
		trigger := strings.Join(commands[index].Trigger, " ")
		if trigger == "" {
			continue
		}
		examples = append(examples, prefix+" "+trigger)
	}
	if len(commands) > limit {
		examples = append(examples, prefix+" ...")
	}
	return examples
}

func deploymentSummaryAppCommandListCommand(state deploy.DeploymentState) string {
	if state.Phase == deploy.PhaseInstalled {
		return "reploy app --deployed-only"
	}
	return "reploy app"
}

func topLevelAppCommandSuggestion(args []string) string {
	dir, err := resolveImplicitStagingDeploymentDir(dockerdeploy.DefaultDeploymentDir, false, io.Discard)
	if err != nil {
		return ""
	}
	result, err := dockerdeploy.AppCommandList(dockerdeploy.AppCommandListOptions{Dir: dir})
	if err != nil {
		return ""
	}
	for _, command := range result.Commands {
		if len(command.Trigger) == 0 || len(args) < len(command.Trigger) {
			continue
		}
		match := true
		for index, trigger := range command.Trigger {
			if args[index] != trigger {
				match = false
				break
			}
		}
		if match {
			return "reploy app " + strings.Join(args, " ")
		}
	}
	return ""
}

type globalDeploymentOptions struct {
	Target           string
	DockerTimeout    time.Duration
	DockerTimeoutSet bool
}

func parseGlobalDeploymentOptions(args []string) (globalDeploymentOptions, []string, error) {
	options := globalDeploymentOptions{Target: "docker"}
	for len(args) > 0 {
		arg := args[0]
		switch arg {
		case "--docker":
			options.Target = "docker"
			args = args[1:]
		case "--aws":
			return globalDeploymentOptions{}, nil, fmt.Errorf("deployment target aws is not supported yet")
		case "--docker-timeout":
			if len(args) < 2 {
				return globalDeploymentOptions{}, nil, fmt.Errorf("%s requires a value", arg)
			}
			timeout, err := parseDockerTimeout(args[1])
			if err != nil {
				return globalDeploymentOptions{}, nil, err
			}
			options.DockerTimeout = timeout
			options.DockerTimeoutSet = true
			args = args[2:]
		default:
			if strings.HasPrefix(arg, "--docker-timeout=") {
				timeout, err := parseDockerTimeout(strings.TrimPrefix(arg, "--docker-timeout="))
				if err != nil {
					return globalDeploymentOptions{}, nil, err
				}
				options.DockerTimeout = timeout
				options.DockerTimeoutSet = true
				args = args[1:]
				continue
			}
			return options, args, nil
		}
	}
	return options, args, nil
}

func parseDockerTimeout(value string) (time.Duration, error) {
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid --docker-timeout duration: %s", value)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("--docker-timeout must be greater than zero")
	}
	return timeout, nil
}

func isDeploymentCommand(command string) bool {
	switch command {
	case "stage", "info", "app", "shell", "bundle", "up", "restart", "down", "ps", "status", "logs", "test", "doctor", "install", "uninstall":
		return true
	default:
		return false
	}
}

func runPackIndex(commandName string, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "reploy %s usage error: expected command\n", commandName)
		printPackIndexShortUsage(commandName, stderr)
		return 2
	}
	if isHelpArg(args[0]) {
		printPackIndexHelp(commandName, stdout)
		return 0
	}
	switch args[0] {
	case "update":
		options, err := parsePackIndexRefreshOptions(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s usage error: %v\n", commandName, err)
			printPackIndexShortUsage(commandName, stderr)
			return 2
		}
		_, cachePath, err := refreshPackIndex(options.URL)
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s update error: %v\n", commandName, err)
			return 1
		}
		if cachePath != "" {
			fmt.Fprintf(stdout, "updated blueprint index: %s\n", filepath.Dir(cachePath))
		} else {
			fmt.Fprintln(stdout, "updated blueprint index")
		}
		return 0
	case "search":
		query, err := parsePackIndexQuery(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s usage error: %v\n", commandName, err)
			printPackIndexShortUsage(commandName, stderr)
			return 2
		}
		index, err := loadPackIndex(packIndexURL())
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s search error: %v\n", commandName, err)
			return 1
		}
		for _, name := range matchingPackIndexNames(index, query) {
			entry := index.Blueprints[name]
			fmt.Fprintf(stdout, "%s\t%s\n", name, entry.Ref)
		}
		return 0
	case "show":
		shorthand, err := parsePackIndexQuery(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s usage error: %v\n", commandName, err)
			printPackIndexShortUsage(commandName, stderr)
			return 2
		}
		index, err := loadPackIndex(packIndexURL())
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s show error: %v\n", commandName, err)
			return 1
		}
		resolvedRef, found, err := expandPackShorthandFromIndex(shorthand, index)
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s show error: %v\n", commandName, err)
			return 1
		}
		name := packShorthandName(shorthand)
		if !found {
			fmt.Fprintf(stderr, "reploy %s show error: unknown blueprint shorthand %q\n", commandName, name)
			return 1
		}
		entry := index.Blueprints[name]
		fmt.Fprintf(stdout, "name: %s\nref: %s\n", name, entry.Ref)
		if resolvedRef != entry.Ref {
			fmt.Fprintf(stdout, "resolved ref: %s\n", resolvedRef)
		}
		return 0
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "reploy %s usage error: unknown option: %s\n", commandName, args[0])
			printPackIndexShortUsage(commandName, stderr)
			return 2
		}
		fmt.Fprintf(stderr, "reploy %s usage error: unknown command: %s\n", commandName, args[0])
		printPackIndexShortUsage(commandName, stderr)
		return 2
	}
}

func runServices(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "reploy services usage error: expected command")
		printServicesShortUsage(stderr)
		return 2
	}
	if isHelpArg(args[0]) {
		printServicesHelp(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		if len(args) > 1 {
			if isHelpArg(args[1]) {
				fmt.Fprintln(stdout, "Usage: reploy services list")
				return 0
			}
			fmt.Fprintf(stderr, "reploy services list usage error: unknown option: %s\n", args[1])
			printServicesShortUsage(stderr)
			return 2
		}
		if err := printReploySystemdServices(stdout); err != nil {
			fmt.Fprintf(stderr, "reploy services list error: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "reploy services usage error: unknown command: %s\n", args[0])
		printServicesShortUsage(stderr)
		return 2
	}
}

func parsePackIndexQuery(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("expected exactly one query")
	}
	query := strings.TrimSpace(args[0])
	if query == "" {
		return "", fmt.Errorf("query must not be empty")
	}
	return query, nil
}

func matchingPackIndexNames(index packIndex, query string) []string {
	query = strings.ToLower(query)
	names := make([]string, 0, len(index.Blueprints))
	for name, entry := range index.Blueprints {
		if strings.Contains(strings.ToLower(name), query) || strings.Contains(strings.ToLower(entry.Ref), query) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

type packIndexRefreshOptions struct {
	URL string
}

func parsePackIndexRefreshOptions(args []string) (packIndexRefreshOptions, error) {
	options := packIndexRefreshOptions{URL: packIndexURL()}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--url":
			value, ok := optionValue(args, &index)
			if !ok {
				return packIndexRefreshOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.URL = value
		default:
			if strings.HasPrefix(arg, "--url=") {
				options.URL = strings.TrimPrefix(arg, "--url=")
				continue
			}
			return packIndexRefreshOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if strings.TrimSpace(options.URL) == "" {
		return packIndexRefreshOptions{}, fmt.Errorf("--url must not be empty")
	}
	return options, nil
}

func runDocker(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	if len(args) == 0 {
		printDockerShortUsage(stderr)
		return 2
	}
	if args[0] == "bundle" {
		return runDockerBundle(args[1:], stdout, stderr, globalOptions)
	}
	if args[0] == "app" {
		return runDockerApp(args[1:], stdout, stderr, globalOptions)
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		printDockerCommandHelp(args[0], stdout)
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		printDockerHelp(stdout)
		return 0
	case "stage":
		options, err := parseDockerCommandOptions(args[1:], true, dockerCommandParseConfig{
			AllowUpdate:  true,
			AllowVerbose: true,
		})
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printDockerStageHelp(stderr)
			return 2
		}
		printWarnings(stderr, options.Warnings)
		if options.Update {
			options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
			if err != nil {
				fmt.Fprintf(stderr, "reploy stage --update error: %v\n", err)
				return 1
			}
			stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(options.Dir, stdout, stderr)
			if err != nil {
				fmt.Fprintf(stderr, "reploy stage --update error: %v\n", err)
				return 1
			}
			results, err := dockerStageUpdate(dockerdeploy.UpdateOptions{
				Dir: options.Dir, Pack: options.Pack, Force: options.Force,
				MaterializeEnvironment: true, Verbose: options.Verbose, Stdout: stdout, Stderr: stderr,
				Progress:               stderr,
				DockerPreflightTimeout: globalOptions.DockerTimeout,
			})
			if err != nil {
				fmt.Fprintf(stderr, "reploy stage --update error: %v\n", err)
				return 1
			}
			printStageUpdateResults(stdout, options.Dir, results, options.Verbose)
			return 0
		}
		results, err := dockerStageInit(dockerdeploy.InitOptions{
			Dir:                    options.Dir,
			Pack:                   options.Pack,
			Requirements:           options.Requirements,
			MaterializeEnvironment: true,
			Verbose:                options.Verbose,
			Stdout:                 stdout,
			Stderr:                 stderr,
			Progress:               stderr,
			DockerPreflightTimeout: globalOptions.DockerTimeout,
		})
		if err != nil {
			var existingFileError dockerdeploy.ExistingDeploymentFileError
			if errors.As(err, &existingFileError) {
				fmt.Fprintf(stderr, "reploy stage error: staging directory already exists at %s (found %s). use --update to update it\n", options.Dir, existingFileError.Path)
				return 1
			}
			fmt.Fprintf(stderr, "reploy stage error: %v\n", err)
			return 1
		}
		stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(options.Dir, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy stage error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "created staging directory for %s: %s\n", packDisplayName(options.Pack), options.Dir)
		if options.Verbose {
			printUpdateResults(stdout, results)
		}
		return 0
	case "info":
		options, err := parseDockerCommandOptions(args[1:], false)
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printDockerShortUsage(stderr)
			return 2
		}
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy info error: %v\n", err)
			return 1
		}
		stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(options.Dir, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy info error: %v\n", err)
			return 1
		}
		info, err := dockerdeploy.Info(dockerdeploy.InfoOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy info error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, info)
		return 0
	case "up", "restart", "down", "ps", "status", "logs":
		return runDockerRuntime(args[0], args[1:], stdout, stderr, globalOptions)
	case "shell":
		options, err := parseDockerRuntimeOptions(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			return 2
		}
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy shell error: %v\n", err)
			return 1
		}
		if err := dockerShell(dockerdeploy.ShellOptions{Dir: options.Dir, Stdin: os.Stdin, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: globalOptions.DockerTimeout}); err != nil {
			fmt.Fprintf(stderr, "reploy shell error: %v\n", err)
			if code, ok := externalCommandExitCode(err); ok {
				return code
			}
			return 1
		}
		return 0
	case "test":
		return runDockerTest(args[1:], stdout, stderr, globalOptions)
	case "doctor":
		return runDockerDoctor(args[1:], stdout, stderr, globalOptions)
	case "install":
		return runDockerInstall(args[1:], stdout, stderr, globalOptions)
	case "uninstall":
		return runDockerUninstall(args[1:], stdout, stderr, globalOptions)
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "reploy usage error: unknown option: %s\n", args[0])
			printDockerShortUsage(stderr)
			return 2
		}
		fmt.Fprintf(stderr, "reploy usage error: unknown command: %s\n", args[0])
		printDockerShortUsage(stderr)
		return 2
	}
}

func runDockerApp(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	if len(args) > 0 && isHelpArg(args[0]) {
		printAppHelp(stdout)
		return 0
	}
	options, err := parseDockerAppOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printAppShortUsage(stderr)
		return 2
	}
	if options.Format != "" && !options.Commands {
		fmt.Fprintln(stderr, "reploy usage error: --format is only supported with --commands")
		printAppShortUsage(stderr)
		return 2
	}
	if options.Commands {
		if len(options.CommandArgs) != 0 {
			fmt.Fprintln(stderr, "reploy usage error: --commands does not accept app command arguments")
			printAppShortUsage(stderr)
			return 2
		}
		return runDockerAppSummaryForOptions(dockerAppSummaryOptions{
			Dir:          options.Dir,
			DirExplicit:  options.DirExplicit,
			DeployedOnly: options.DeployedOnly,
			Format:       options.Format,
		}, stdout, stderr)
	}
	if len(options.CommandArgs) == 0 {
		return runDockerAppSummaryForOptions(dockerAppSummaryOptions{
			Dir:          options.Dir,
			DirExplicit:  options.DirExplicit,
			DeployedOnly: options.DeployedOnly,
		}, stdout, stderr)
	}
	if strings.HasPrefix(options.CommandArgs[0], "-") {
		fmt.Fprintf(stderr, "reploy usage error: unknown option: %s\n", options.CommandArgs[0])
		printAppShortUsage(stderr)
		return 2
	}
	if options.DeployedOnly {
		options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	} else {
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy app error: %v\n", err)
			return 1
		}
	}
	if err := dockerdeploy.AppCommand(dockerdeploy.AppCommandOptions{
		Dir:                    options.Dir,
		CommandArgs:            options.CommandArgs,
		DeployedOnly:           options.DeployedOnly,
		Stdout:                 stdout,
		Stderr:                 stderr,
		DockerPreflightTimeout: globalOptions.DockerTimeout,
	}); err != nil {
		fmt.Fprintf(stderr, "reploy app error: %v\n", err)
		if code, ok := externalCommandExitCode(err); ok {
			return code
		}
		return 1
	}
	return 0
}

func runDockerAppSummary(args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerAppSummaryOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printAppShortUsage(stderr)
		return 2
	}
	return runDockerAppSummaryForOptions(options, stdout, stderr)
}

func runDockerAppSummaryForOptions(options dockerAppSummaryOptions, stdout io.Writer, stderr io.Writer) int {
	var err error
	if options.DeployedOnly {
		options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	} else {
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy app error: %v\n", err)
			return 1
		}
	}
	if options.Format != "json" {
		stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(options.Dir, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy app error: %v\n", err)
			return 1
		}
	}
	result, err := dockerdeploy.AppCommandList(dockerdeploy.AppCommandListOptions{Dir: options.Dir, DeployedOnly: options.DeployedOnly})
	if err != nil {
		fmt.Fprintf(stderr, "reploy app error: %v\n", err)
		return 1
	}
	if options.Format == "json" {
		content, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "reploy app error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(content))
		return 0
	}
	if result.AppID != "" {
		fmt.Fprintf(stdout, "app: %s\n", result.AppID)
	}
	fmt.Fprintln(stdout, "app subcommands:")
	for _, command := range result.Commands {
		fmt.Fprintf(stdout, "  %s\n", strings.Join(command.Trigger, " "))
	}
	return 0
}

type dockerAppOptions struct {
	Dir          string
	DirExplicit  bool
	Commands     bool
	DeployedOnly bool
	Format       string
	CommandArgs  []string
}

type dockerAppSummaryOptions struct {
	Dir          string
	DirExplicit  bool
	DeployedOnly bool
	Format       string
}

func parseDockerAppOptions(args []string) (dockerAppOptions, error) {
	options := dockerAppOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerAppOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--commands":
			options.Commands = true
		case "--deployed-only":
			options.DeployedOnly = true
		case "--format":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerAppOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Format = value
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--format=") {
				options.Format = strings.TrimPrefix(arg, "--format=")
				continue
			}
			options.CommandArgs = append(options.CommandArgs, arg)
		}
	}
	if options.Dir == "" {
		return dockerAppOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if options.Format != "" && options.Format != "json" {
		return dockerAppOptions{}, fmt.Errorf("unsupported --format: %s", options.Format)
	}
	return options, nil
}

func parseDockerAppSummaryOptions(args []string) (dockerAppSummaryOptions, error) {
	options := dockerAppSummaryOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerAppSummaryOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			return dockerAppSummaryOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerAppSummaryOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
}

func isHelpArg(arg string) bool {
	switch arg {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

func resolveImplicitDeploymentDir(dir string, explicit bool, _ io.Writer) string {
	if explicit || dir != dockerdeploy.DefaultDeploymentDir {
		return dir
	}
	if _, err := os.Stat(dockerdeploy.StateFileName); err != nil {
		return dir
	}
	return "."
}

func implicitDeploymentStateExists(dir string, explicit bool) bool {
	dir = resolveImplicitDeploymentDir(dir, explicit, io.Discard)
	_, err := os.Stat(filepath.Join(dir, dockerdeploy.StateFileName))
	return err == nil || !os.IsNotExist(err)
}

func resolveImplicitStagingDeploymentDir(dir string, explicit bool, output io.Writer) (string, error) {
	dir = resolveImplicitDeploymentDir(dir, explicit, output)
	if err := dockerdeploy.RequireStagingDeployment(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func runDockerBundle(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "reploy usage error: expected bundle command")
		printBundleShortUsage(stderr)
		return 2
	}
	action := args[0]
	if isHelpArg(action) {
		printBundleHelp(stdout)
		return 0
	}
	if strings.HasPrefix(action, "-") {
		fmt.Fprintf(stderr, "reploy usage error: unknown option: %s\n", action)
		printBundleShortUsage(stderr)
		return 2
	}
	if action == "upgrade" {
		options, err := parseDockerBundleUpgradeOptions(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printBundleShortUsage(stderr)
			return 2
		}
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle upgrade error: %v\n", err)
			return 1
		}
		stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(options.Dir, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle upgrade error: %v\n", err)
			return 1
		}
		results, err := dockerdeploy.BundleUpgrade(dockerdeploy.BundleUpgradeOptions{
			Dir:                    options.Dir,
			Target:                 options.Root,
			PyPIOnly:               options.PyPIOnly,
			Stdout:                 stdout,
			Stderr:                 stderr,
			DockerPreflightTimeout: globalOptions.DockerTimeout,
		})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle upgrade error: %v\n", err)
			return 1
		}
		printUpdateResults(stdout, results)
		return 0
	}
	if action == "list" && len(args) > 1 && args[1] == "all" {
		options, err := parseDockerBundleOptions(args[2:], dockerBundleParseOptions{})
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printBundleShortUsage(stderr)
			return 2
		}
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle list all error: %v\n", err)
			return 1
		}
		stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(options.Dir, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle list all error: %v\n", err)
			return 1
		}
		packages, err := dockerdeploy.BundleListAll(dockerdeploy.BundleListOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle list all error: %v\n", err)
			return 1
		}
		for _, resolved := range packages {
			fmt.Fprintf(stdout, "%s\t%s\n", resolved.Kind, resolved.Requirement)
		}
		return 0
	}
	if !isDockerBundleCommand(action) {
		fmt.Fprintf(stderr, "reploy usage error: unknown bundle command: %s\n", action)
		printBundleShortUsage(stderr)
		return 2
	}
	options, err := parseDockerBundleOptions(args[1:], dockerBundleParseOptions{
		RequireRoot:            action != "list" && action != "list-options" && action != "check" && action != "build" && action != "clean",
		AllowDryRun:            action == "check" || action == "build",
		AllowPyPIOnly:          action == "build",
		AllowWheelhouseBackend: action == "check" || action == "build",
		AllowBuildBackend:      action == "check" || action == "build",
		AllowVerbose:           action == "check" || action == "build" || action == "clean",
		AllowMultiple:          action == "add" || action == "remove",
		AllowNames:             action == "add" || action == "remove",
		AllowExtra:             action == "add" || action == "remove",
		Command:                action,
	})
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printBundleShortUsage(stderr)
		return 2
	}
	options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy bundle %s error: %v\n", action, err)
		return 1
	}
	spinnerStderr := stderr
	stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(options.Dir, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy bundle %s error: %v\n", action, err)
		return 1
	}
	switch action {
	case "list":
		roots, err := dockerdeploy.BundleList(dockerdeploy.BundleListOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle list error: %v\n", err)
			return 1
		}
		for _, root := range roots {
			fmt.Fprintln(stdout, root.Source)
		}
		return 0
	case "list-options":
		bundleOptions, err := dockerdeploy.BundleOptions(dockerdeploy.BundleListOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle list-options error: %v\n", err)
			return 1
		}
		for _, option := range bundleOptions {
			fmt.Fprintf(stdout, "%s\t%s\n", option.Name, option.Description)
		}
		return 0
	case "add":
		beforeRoots, beforeErr := dockerdeploy.BundleList(dockerdeploy.BundleListOptions{Dir: options.Dir})
		results, err := dockerdeploy.BundleAddMany(dockerdeploy.BundleRootsOptions{Dir: options.Dir, Sources: options.Extras, Names: options.Names})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle add error: %v\n", err)
			return 1
		}
		printBundleAddSummary(stdout, options, beforeRoots, beforeErr)
		printUpdateResults(stdout, results)
		return 0
	case "check":
		stopSpinner := func(bool) {}
		progress := io.Discard
		if !options.DryRun && !options.Verbose {
			label, err := deploymentSpinnerLabel(options.Dir, "validating installation bundle", spinnerStderr)
			if err != nil {
				fmt.Fprintf(stderr, "reploy bundle check error: %v\n", err)
				return 1
			}
			stopSpinner, progress = startProgressSpinner(spinnerStderr, label)
		}
		built := false
		if !options.DryRun {
			built, err = dockerdeploy.EnsureBundlePrepared(dockerdeploy.BundleEnsureOptions{
				Dir:                    options.Dir,
				WheelhouseBackend:      options.WheelhouseBackend,
				BuildBackend:           options.BuildBackend,
				Verbose:                options.Verbose,
				Stdout:                 stdout,
				Stderr:                 stderr,
				Progress:               progress,
				DockerPreflightTimeout: globalOptions.DockerTimeout,
			})
		}
		if err == nil && !built {
			err = dockerdeploy.BundleCheck(dockerdeploy.BundleCheckOptions{
				Dir:                    options.Dir,
				DryRun:                 options.DryRun,
				Verbose:                options.Verbose,
				Stdout:                 stdout,
				Stderr:                 stderr,
				Progress:               progress,
				DockerPreflightTimeout: globalOptions.DockerTimeout,
			})
		}
		if err != nil {
			stopSpinner(false)
			if options.DryRun || options.Verbose {
				fmt.Fprintf(stderr, "reploy bundle check error: %v\n", err)
			} else if bundleErrorHasEnoughOutput(err) {
				fmt.Fprintf(stderr, "reploy bundle check error: %v\n", err)
			} else {
				fmt.Fprintf(stderr, "reploy bundle check error: %v; rerun with --verbose for command output\n", err)
			}
			return 1
		}
		stopSpinner(true)
		if !options.DryRun && options.Verbose {
			fmt.Fprintln(stdout, "bundle check passed")
		}
		return 0
	case "build":
		stopSpinner := func(bool) {}
		progress := io.Discard
		if !options.DryRun && !options.Verbose {
			label, err := deploymentSpinnerLabel(options.Dir, "building installation bundle", spinnerStderr)
			if err != nil {
				fmt.Fprintf(stderr, "reploy bundle build error: %v\n", err)
				return 1
			}
			stopSpinner, progress = startProgressSpinner(spinnerStderr, label)
		}
		if err := dockerdeploy.BundlePrepare(dockerdeploy.BundlePrepareOptions{
			Dir:                    options.Dir,
			DryRun:                 options.DryRun,
			PyPIOnly:               options.PyPIOnly,
			WheelhouseBackend:      options.WheelhouseBackend,
			BuildBackend:           options.BuildBackend,
			Verbose:                options.Verbose,
			Stdout:                 stdout,
			Stderr:                 stderr,
			Progress:               progress,
			DockerPreflightTimeout: globalOptions.DockerTimeout,
		}); err != nil {
			stopSpinner(false)
			if options.DryRun || options.Verbose {
				fmt.Fprintf(stderr, "reploy bundle build error: %v\n", err)
			} else if bundleErrorHasEnoughOutput(err) {
				fmt.Fprintf(stderr, "reploy bundle build error: %v\n", err)
			} else {
				fmt.Fprintf(stderr, "reploy bundle build error: %v; rerun with --verbose for command output\n", err)
			}
			return 1
		}
		stopSpinner(true)
		return 0
	case "clean":
		results, err := dockerdeploy.BundleClean(dockerdeploy.BundleCleanOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle clean error: %v\n", err)
			return 1
		}
		if options.Verbose {
			printUpdateResults(stdout, results)
		}
		return 0
	case "remove":
		results, err := dockerdeploy.BundleRemoveMany(dockerdeploy.BundleRootsOptions{Dir: options.Dir, Sources: options.Extras, Names: options.Names})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle remove error: %v\n", err)
			return 1
		}
		printUpdateResults(stdout, results)
		return 0
	default:
		fmt.Fprintf(stderr, "reploy usage error: unknown bundle command: %s\n", action)
		printBundleShortUsage(stderr)
		return 2
	}
}

func isDockerBundleCommand(action string) bool {
	switch action {
	case "list", "list-options", "add", "remove", "check", "build", "clean":
		return true
	default:
		return false
	}
}

type dockerBundleOptions struct {
	Dir               string
	DirExplicit       bool
	Root              string
	Roots             []string
	Names             []string
	Extras            []string
	DryRun            bool
	PyPIOnly          bool
	WheelhouseBackend string
	BuildBackend      string
	Verbose           bool
}

type dockerBundleParseOptions struct {
	RequireRoot            bool
	AllowDryRun            bool
	AllowPyPIOnly          bool
	AllowWheelhouseBackend bool
	AllowBuildBackend      bool
	AllowVerbose           bool
	AllowMultiple          bool
	AllowNames             bool
	AllowExtra             bool
	Command                string
}

func parseDockerBundleUpgradeOptions(args []string) (dockerBundleOptions, error) {
	options := dockerBundleOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--pypi-only":
			options.PyPIOnly = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			if options.Root != "" {
				return dockerBundleOptions{}, fmt.Errorf("bundle upgrade accepts at most one target")
			}
			options.Root = arg
		}
	}
	if options.Dir == "" {
		return dockerBundleOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
}

func parseDockerBundleOptions(args []string, parseOptions dockerBundleParseOptions) (dockerBundleOptions, error) {
	options := dockerBundleOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--extra":
			if !parseOptions.AllowExtra {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			extras := splitBundleRoots(value)
			if len(extras) == 0 {
				return dockerBundleOptions{}, fmt.Errorf("bundle extra root must not be empty")
			}
			options.Extras = append(options.Extras, extras...)
		case "--dry-run":
			if !parseOptions.AllowDryRun {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.DryRun = true
		case "--pypi-only":
			if !parseOptions.AllowPyPIOnly {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.PyPIOnly = true
		case "--wheelhouse-backend":
			if !parseOptions.AllowWheelhouseBackend {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.WheelhouseBackend = value
		case "--build-backend":
			if !parseOptions.AllowBuildBackend {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.BuildBackend = value
		case "--verbose":
			if !parseOptions.AllowVerbose {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Verbose = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--name":
			if !parseOptions.AllowNames {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			names := splitBundleRoots(value)
			if len(names) == 0 {
				return dockerBundleOptions{}, fmt.Errorf("bundle option name must not be empty")
			}
			options.Names = append(options.Names, names...)
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--name=") {
				if !parseOptions.AllowNames {
					return dockerBundleOptions{}, fmt.Errorf("unknown option: --name")
				}
				names := splitBundleRoots(strings.TrimPrefix(arg, "--name="))
				if len(names) == 0 {
					return dockerBundleOptions{}, fmt.Errorf("bundle option name must not be empty")
				}
				options.Names = append(options.Names, names...)
				continue
			}
			if strings.HasPrefix(arg, "--extra=") {
				if !parseOptions.AllowExtra {
					return dockerBundleOptions{}, fmt.Errorf("unknown option: --extra")
				}
				extras := splitBundleRoots(strings.TrimPrefix(arg, "--extra="))
				if len(extras) == 0 {
					return dockerBundleOptions{}, fmt.Errorf("bundle extra root must not be empty")
				}
				options.Extras = append(options.Extras, extras...)
				continue
			}
			if strings.HasPrefix(arg, "--wheelhouse-backend=") {
				if !parseOptions.AllowWheelhouseBackend {
					return dockerBundleOptions{}, fmt.Errorf("unknown option: --wheelhouse-backend")
				}
				options.WheelhouseBackend = strings.TrimPrefix(arg, "--wheelhouse-backend=")
				continue
			}
			if strings.HasPrefix(arg, "--build-backend=") {
				if !parseOptions.AllowBuildBackend {
					return dockerBundleOptions{}, fmt.Errorf("unknown option: --build-backend")
				}
				options.BuildBackend = strings.TrimPrefix(arg, "--build-backend=")
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			roots := splitBundleRoots(arg)
			if len(roots) == 0 {
				return dockerBundleOptions{}, fmt.Errorf("bundle root must not be empty")
			}
			if !parseOptions.AllowMultiple && (options.Root != "" || len(roots) > 1) {
				return dockerBundleOptions{}, fmt.Errorf("expected one bundle root")
			}
			if options.Root == "" {
				options.Root = roots[0]
			}
			if parseOptions.AllowNames {
				options.Names = append(options.Names, roots...)
			} else {
				options.Roots = append(options.Roots, roots...)
			}
		}
	}
	if options.Dir == "" {
		return dockerBundleOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if parseOptions.RequireRoot && len(options.Roots) == 0 && len(options.Names) == 0 && len(options.Extras) == 0 {
		if parseOptions.AllowNames {
			command := parseOptions.Command
			if command == "" {
				command = "command"
			}
			return dockerBundleOptions{}, fmt.Errorf("bundle %s expects option names or --extra ROOT; examples: reploy bundle %s imap,smtp; reploy bundle %s --extra PACKAGE[==VERSION]", command, command, command)
		}
		return dockerBundleOptions{}, fmt.Errorf("expected bundle root")
	}
	if !parseOptions.RequireRoot && (len(options.Roots) > 0 || len(options.Names) > 0 || len(options.Extras) > 0) {
		return dockerBundleOptions{}, fmt.Errorf("bundle list does not accept a root")
	}
	return options, nil
}

func splitBundleRoots(arg string) []string {
	parts := strings.Split(arg, ",")
	roots := []string{}
	for _, part := range parts {
		root := strings.TrimSpace(part)
		if root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

func printUpdateResults(output io.Writer, results []dockerdeploy.UpdateResult) {
	anyAction := false
	for _, result := range results {
		if result.Status == deploy.UpdateStatusUpToDate {
			continue
		}
		anyAction = true
		fmt.Fprintf(output, "%s %s\n", result.Status, result.Path)
	}
	if !anyAction {
		fmt.Fprintln(output, deploy.UpdateStatusUpToDate)
	}
}

func printStageUpdateResults(output io.Writer, dir string, results []dockerdeploy.UpdateResult, verbose bool) {
	allUpToDate := true
	for _, result := range results {
		if result.Status != deploy.UpdateStatusUpToDate {
			allUpToDate = false
			break
		}
	}
	if allUpToDate {
		fmt.Fprintln(output, deploy.UpdateStatusUpToDate)
		return
	}
	fmt.Fprintf(output, "updated staging directory: %s\n", dir)
	if verbose {
		printUpdateResults(output, results)
	}
}

func printBundleAddSummary(output io.Writer, options dockerBundleOptions, beforeRoots []deploy.ArtifactRoot, beforeErr error) {
	roots := selectedBundleRoots(options)
	if len(roots) == 0 {
		return
	}
	if beforeErr == nil {
		alreadySelected := map[string]bool{}
		for _, root := range beforeRoots {
			alreadySelected[root.Source] = true
		}
		added := []string{}
		existing := []string{}
		for _, root := range roots {
			if alreadySelected[root] {
				existing = append(existing, root)
			} else {
				added = append(added, root)
			}
		}
		if len(added) > 0 {
			printBundleRootSummary(output, "selected", added)
		}
		if len(existing) > 0 {
			printBundleRootSummary(output, "already selected", existing)
		}
		return
	}
	printBundleRootSummary(output, "selected", roots)
}

func printBundleRootSummary(output io.Writer, verb string, roots []string) {
	if allPythonPackageRoots(roots) {
		fmt.Fprintf(output, "%s Python packages: %s (dependencies included when the bundle is prepared)\n", verb, strings.Join(roots, ", "))
		return
	}
	fmt.Fprintf(output, "%s installation roots: %s\n", verb, strings.Join(roots, ", "))
}

func selectedBundleRoots(options dockerBundleOptions) []string {
	bundleOptions, err := dockerdeploy.BundleOptions(dockerdeploy.BundleListOptions{Dir: options.Dir})
	byName := map[string]string{}
	if err == nil {
		for _, option := range bundleOptions {
			byName[option.Name] = option.Identifier
		}
	}
	roots := []string{}
	roots = append(roots, options.Extras...)
	for _, name := range options.Names {
		if root := byName[name]; root != "" {
			roots = append(roots, root)
		} else {
			roots = append(roots, name)
		}
	}
	return roots
}

func allPythonPackageRoots(roots []string) bool {
	for _, root := range roots {
		if root == "" || strings.HasPrefix(root, "/") || strings.ContainsAny(root, " \t\n") {
			return false
		}
	}
	return true
}

func runDockerDoctor(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerDoctorOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy doctor error: %v\n", err)
		return 1
	}
	return dockerdeploy.Doctor(dockerdeploy.DoctorOptions{
		Dir:                    options.Dir,
		Preinstall:             options.Preinstall,
		Scope:                  options.Scope,
		Quiet:                  options.Quiet,
		Stdout:                 stdout,
		DockerPreflightTimeout: globalOptions.DockerTimeout,
	})
}

func runDockerInstall(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerInstallOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	printWarnings(stderr, options.Warnings)
	stopSpinner := func(bool) {}
	progress := io.Discard
	installStdout := stdout
	installTarget := ""
	if options.Pack.Raw != "" {
		if !options.DryRun {
			var logOutput io.Writer
			stopSpinner, progress, logOutput = startProgressSpinnerWithLogs(stderr, "installing app")
			installStdout = logOutput
		}
		installTarget, err = dockerDirectInstall(dockerdeploy.DirectInstallOptions{
			Pack:                   options.Pack,
			Target:                 options.Target,
			Scope:                  options.Scope,
			Service:                options.Service,
			PortOverrides:          options.PortOverrides,
			Replace:                options.Replace,
			Clean:                  options.Clean,
			InPlace:                options.InPlace,
			Start:                  options.Start,
			DryRun:                 options.DryRun,
			Stdout:                 installStdout,
			Progress:               progress,
			DockerPreflightTimeout: globalOptions.DockerTimeout,
		})
	} else {
		installTarget = options.Target
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err == nil {
			if !options.DryRun {
				var label string
				label, err = deploymentSpinnerLabel(options.Dir, "installing", stderr)
				if err == nil {
					var logOutput io.Writer
					stopSpinner, progress, logOutput = startProgressSpinnerWithLogs(stderr, label)
					installStdout = logOutput
				}
			} else {
				installStdout = deploymentStdoutOrFallback(options.Dir, stdout)
			}
		}
		if err == nil {
			err = dockerInstall(dockerdeploy.InstallOptions{
				Dir:                    options.Dir,
				Target:                 options.Target,
				Scope:                  options.Scope,
				Service:                options.Service,
				PortOverrides:          options.PortOverrides,
				Replace:                options.Replace,
				Clean:                  options.Clean,
				InPlace:                options.InPlace,
				Start:                  options.Start,
				DryRun:                 options.DryRun,
				Stdout:                 installStdout,
				Progress:               progress,
				DockerPreflightTimeout: globalOptions.DockerTimeout,
			})
		}
	}
	if err != nil {
		stopSpinner(false)
		fmt.Fprintf(stderr, "reploy install error: %v\n", err)
		return 1
	}
	stopSpinner(true)
	if !options.DryRun && installTarget != "" {
		successStdout := deploymentStdoutOrFallback(installTarget, stdout)
		if err := dockerPrintInstallSuccess(installTarget, successStdout, globalOptions.DockerTimeout); err != nil {
			fmt.Fprintf(stderr, "reploy install warning: success output: %v\n", err)
		}
	}
	return 0
}

func runDockerUninstall(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerUninstallOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	stopSpinner := func(bool) {}
	if dockerUninstallNeedsRoot(dockerdeploy.UninstallOptions{
		From:        options.From,
		ServiceName: options.ServiceName,
		RemoveDir:   options.RemoveDir,
		DryRun:      options.DryRun,
	}) && os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "reploy uninstall error: root privileges are required to stop systemd services and remove Docker resources")
		fmt.Fprintln(stderr, "rerun with sudo, or add --dry-run to inspect the uninstall plan")
		return 1
	}
	uninstallStdout := stdout
	uninstallDir := options.From
	if strings.TrimSpace(uninstallDir) == "" {
		uninstallDir = "."
	}
	if !options.DryRun {
		uninstallStdout = deploymentStdoutOrFallback(uninstallDir, stdout)
		label := "uninstalling deployment"
		if prefixedLabel, err := deploymentSpinnerLabel(uninstallDir, "uninstalling", stderr); err == nil {
			label = prefixedLabel
		}
		var logOutput io.Writer
		stopSpinner, _, logOutput = startProgressSpinnerWithLogs(stderr, label)
		if terminalAnimationsEnabled() {
			uninstallStdout = deploymentStdoutOrFallback(uninstallDir, logOutput)
		}
	}
	if err := dockerUninstall(dockerdeploy.UninstallOptions{
		From:                   options.From,
		ServiceName:            options.ServiceName,
		RemoveDir:              options.RemoveDir,
		DryRun:                 options.DryRun,
		Stdout:                 uninstallStdout,
		DockerPreflightTimeout: globalOptions.DockerTimeout,
	}); err != nil {
		stopSpinner(false)
		fmt.Fprintf(stderr, "reploy uninstall error: %v\n", err)
		return 1
	}
	stopSpinner(true)
	return 0
}

func deploymentStdoutOrFallback(dir string, stdout io.Writer) io.Writer {
	wrappedStdout, _, err := dockerdeploy.DeploymentOutputWriters(dir, stdout, nil)
	if err != nil {
		return stdout
	}
	return wrappedStdout
}

type dockerInstallOptions struct {
	Dir           string
	DirExplicit   bool
	Pack          deploy.PackRef
	Warnings      []string
	Target        string
	Scope         dockerdeploy.InstallScope
	Service       string
	PortOverrides []dockerdeploy.PortOverride
	Replace       []string
	Clean         bool
	InPlace       bool
	Start         bool
	DryRun        bool
}

type dockerUninstallOptions struct {
	From        string
	ServiceName string
	RemoveDir   bool
	DryRun      bool
}

func parseDockerInstallOptions(args []string) (dockerInstallOptions, error) {
	options := dockerInstallOptions{Dir: dockerdeploy.DefaultDeploymentDir, Start: true}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dry-run":
			options.DryRun = true
		case "--clean":
			options.Clean = true
		case "--in-place":
			options.InPlace = true
		case "--start":
			options.Start = true
		case "--no-start":
			options.Start = false
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--to":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Target = value
		case "--scope":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			scope, err := dockerdeploy.ParseInstallScope(value)
			if err != nil {
				return dockerInstallOptions{}, err
			}
			options.Scope = scope
		case "--service":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Service = value
		case "--port":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			override, err := parseInstallPortOverride(value)
			if err != nil {
				return dockerInstallOptions{}, err
			}
			options.PortOverrides = append(options.PortOverrides, override)
		case "--replace":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Replace = append(options.Replace, value)
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--to=") {
				options.Target = strings.TrimPrefix(arg, "--to=")
				continue
			}
			if strings.HasPrefix(arg, "--scope=") {
				scope, err := dockerdeploy.ParseInstallScope(strings.TrimPrefix(arg, "--scope="))
				if err != nil {
					return dockerInstallOptions{}, err
				}
				options.Scope = scope
				continue
			}
			if strings.HasPrefix(arg, "--service=") {
				options.Service = strings.TrimPrefix(arg, "--service=")
				continue
			}
			if strings.HasPrefix(arg, "--port=") {
				override, err := parseInstallPortOverride(strings.TrimPrefix(arg, "--port="))
				if err != nil {
					return dockerInstallOptions{}, err
				}
				options.PortOverrides = append(options.PortOverrides, override)
				continue
			}
			if strings.HasPrefix(arg, "--replace=") {
				options.Replace = append(options.Replace, strings.TrimPrefix(arg, "--replace="))
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return dockerInstallOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			if options.Pack.Raw != "" {
				return dockerInstallOptions{}, fmt.Errorf("install app ref may only be provided once")
			}
			ref, warning, err := parsePackRefArgumentWithWarning(arg)
			if err != nil {
				return dockerInstallOptions{}, err
			}
			options.Pack = ref
			if warning != "" {
				options.Warnings = append(options.Warnings, warning)
			}
		}
	}
	if options.Dir == "" {
		return dockerInstallOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if options.Scope == "" {
		return dockerInstallOptions{}, fmt.Errorf("--scope is required and must be user or system")
	}
	if options.Pack.Raw != "" && options.DirExplicit {
		return dockerInstallOptions{}, fmt.Errorf("--dir is only supported when installing from an existing staging directory")
	}
	if options.InPlace && options.Pack.Raw == "" {
		return dockerInstallOptions{}, fmt.Errorf("--in-place is only supported with direct app install")
	}
	return options, nil
}

func parseDockerUninstallOptions(args []string) (dockerUninstallOptions, error) {
	var options dockerUninstallOptions
	fromSet := false
	serviceNameSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dry-run":
			options.DryRun = true
		case "--remove-dir":
			options.RemoveDir = true
		case "--from":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerUninstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.From = value
			fromSet = true
		case "--service-name":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerUninstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.ServiceName = value
			serviceNameSet = true
		default:
			if strings.HasPrefix(arg, "--from=") {
				options.From = strings.TrimPrefix(arg, "--from=")
				fromSet = true
				continue
			}
			if strings.HasPrefix(arg, "--service-name=") {
				options.ServiceName = strings.TrimPrefix(arg, "--service-name=")
				serviceNameSet = true
				continue
			}
			return dockerUninstallOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if strings.TrimSpace(options.From) != options.From {
		return dockerUninstallOptions{}, fmt.Errorf("--from must not contain leading or trailing whitespace")
	}
	if strings.TrimSpace(options.ServiceName) != options.ServiceName {
		return dockerUninstallOptions{}, fmt.Errorf("--service-name must not contain leading or trailing whitespace")
	}
	if options.From == "" && fromSet {
		return dockerUninstallOptions{}, fmt.Errorf("--from must not be empty")
	}
	if options.ServiceName == "" && serviceNameSet {
		return dockerUninstallOptions{}, fmt.Errorf("--service-name must not be empty")
	}
	return options, nil
}

func parseInstallPortOverride(value string) (dockerdeploy.PortOverride, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return dockerdeploy.PortOverride{}, fmt.Errorf("--port must not be empty")
	}
	name, hostPort, ok := strings.Cut(value, "=")
	if !ok {
		return dockerdeploy.PortOverride{HostPort: value}, nil
	}
	name = strings.TrimSpace(name)
	hostPort = strings.TrimSpace(hostPort)
	if name == "" {
		return dockerdeploy.PortOverride{}, fmt.Errorf("--port name must not be empty")
	}
	if hostPort == "" {
		return dockerdeploy.PortOverride{}, fmt.Errorf("--port host port must not be empty")
	}
	return dockerdeploy.PortOverride{Name: name, HostPort: hostPort}, nil
}

type dockerDoctorOptions struct {
	Dir         string
	DirExplicit bool
	Preinstall  bool
	Scope       dockerdeploy.InstallScope
	Quiet       bool
}

func parseDockerDoctorOptions(args []string) (dockerDoctorOptions, error) {
	options := dockerDoctorOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--preinstall":
			options.Preinstall = true
		case "--scope":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerDoctorOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			scope, err := dockerdeploy.ParseInstallScope(value)
			if err != nil {
				return dockerDoctorOptions{}, err
			}
			options.Scope = scope
		case "--quiet":
			options.Quiet = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerDoctorOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--scope=") {
				scope, err := dockerdeploy.ParseInstallScope(strings.TrimPrefix(arg, "--scope="))
				if err != nil {
					return dockerDoctorOptions{}, err
				}
				options.Scope = scope
				continue
			}
			return dockerDoctorOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerDoctorOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if options.Scope != "" && !options.Preinstall {
		return dockerDoctorOptions{}, fmt.Errorf("--scope requires --preinstall")
	}
	return options, nil
}

func runDockerTest(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerTestOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy test error: %v\n", err)
		return 1
	}
	errorStderr, err := deploymentErrorWriter(options.Dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy test error: %v\n", err)
		return 1
	}
	if err := dockerTestServer(dockerdeploy.TestOptions{
		Dir:                    options.Dir,
		Timeout:                options.Timeout,
		Stdout:                 stdout,
		DockerPreflightTimeout: globalOptions.DockerTimeout,
	}); err != nil {
		fmt.Fprintf(errorStderr, "reploy test error: %v\n", err)
		return 1
	}
	return 0
}

type dockerTestOptions struct {
	Dir         string
	DirExplicit bool
	Timeout     time.Duration
}

func parseDockerTestOptions(args []string) (dockerTestOptions, error) {
	options := dockerTestOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--timeout":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerTestOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			timeout, err := time.ParseDuration(value)
			if err != nil {
				return dockerTestOptions{}, fmt.Errorf("invalid --timeout duration: %s", value)
			}
			options.Timeout = timeout
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerTestOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--timeout=") {
				value := strings.TrimPrefix(arg, "--timeout=")
				timeout, err := time.ParseDuration(value)
				if err != nil {
					return dockerTestOptions{}, fmt.Errorf("invalid --timeout duration: %s", value)
				}
				options.Timeout = timeout
				continue
			}
			return dockerTestOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerTestOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if options.Timeout < 0 {
		return dockerTestOptions{}, fmt.Errorf("--timeout must not be negative")
	}
	return options, nil
}

func runDockerRuntime(action string, args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	return runDockerRuntimeCommand(action, args, stdout, stderr, globalOptions, false)
}

func runDockerRuntimeControl(action string, args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	return runDockerRuntimeCommand(action, args, stdout, stderr, globalOptions, true)
}

func runDockerRuntimeCommand(action string, args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions, allowInstalledDir bool) int {
	options, err := parseDockerRuntimeOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	if options.Follow && action != "logs" {
		fmt.Fprintln(stderr, "reploy usage error: --follow is only supported with logs")
		printDockerShortUsage(stderr)
		return 2
	}
	if options.Tail != "" && action != "logs" {
		fmt.Fprintln(stderr, "reploy usage error: --tail is only supported with logs")
		printDockerShortUsage(stderr)
		return 2
	}
	if !allowInstalledDir {
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s error: %v\n", action, err)
			return 1
		}
	}
	errorStderr, err := deploymentErrorWriter(options.Dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy %s error: %v\n", action, err)
		return 1
	}
	stopSpinner := func(bool) {}
	progress := io.Discard
	if runtimeActionShowsSpinner(action, options.Verbose) {
		label, err := runtimeSpinnerLabel(options.Dir, action, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s error: %v\n", action, err)
			return 1
		}
		stopSpinner, progress = startProgressSpinner(stderr, label)
	}
	if err := dockerRuntime(dockerdeploy.RuntimeOptions{
		Dir:                    options.Dir,
		Action:                 action,
		Follow:                 options.Follow,
		Tail:                   options.Tail,
		Verbose:                options.Verbose,
		Stdout:                 stdout,
		Stderr:                 stderr,
		Progress:               progress,
		DockerPreflightTimeout: globalOptions.DockerTimeout,
	}); err != nil {
		stopSpinner(false)
		fmt.Fprintf(errorStderr, "reploy %s error: %v\n", action, err)
		if runtimeBundlePrepareFailed(err) && !options.Verbose {
			fmt.Fprintf(errorStderr, "next step: run `%s` to inspect and fix the bundle build, then rerun `reploy %s`.\n", runtimeBundleBuildVerboseCommand(options.Dir, options.DirExplicit), action)
		}
		return 1
	}
	stopSpinner(true)
	printRuntimeUpServiceURL(action, options.Dir, stdout)
	return 0
}

func printRuntimeUpServiceURL(action string, dir string, stdout io.Writer) {
	if action != "up" || stdout == nil {
		return
	}
	serviceURL, err := dockerdeploy.InstallServerURL(dir)
	if err != nil {
		return
	}
	fmt.Fprintf(deploymentStdoutOrFallback(dir, stdout), "service url: %s\n", serviceURL)
}

func runtimeActionShowsSpinner(action string, verbose bool) bool {
	if verbose {
		return false
	}
	return action == "up" || action == "restart" || action == "down"
}

func deploymentErrorWriter(dir string, stderr io.Writer) (io.Writer, error) {
	_, wrappedStderr, err := dockerdeploy.DeploymentOutputWriters(dir, nil, stderr)
	if err != nil {
		return nil, err
	}
	return wrappedStderr, nil
}

func runtimeSpinnerLabel(dir string, action string, output io.Writer) (string, error) {
	return deploymentSpinnerLabel(dir, action, output)
}

func deploymentSpinnerLabel(dir string, label string, output io.Writer) (string, error) {
	prefix, err := dockerdeploy.DeploymentOutputPrefix(dir, output)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return label, nil
	}
	return prefix + " " + label, nil
}

type dockerRuntimeOptions struct {
	Dir         string
	DirExplicit bool
	Follow      bool
	Tail        string
	Verbose     bool
}

func parseDockerRuntimeOptions(args []string) (dockerRuntimeOptions, error) {
	options := dockerRuntimeOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--follow", "-f":
			options.Follow = true
		case "--tail":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerRuntimeOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			if value == "" {
				return dockerRuntimeOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Tail = value
		case "--verbose":
			options.Verbose = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerRuntimeOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--tail=") {
				options.Tail = strings.TrimPrefix(arg, "--tail=")
				if options.Tail == "" {
					return dockerRuntimeOptions{}, fmt.Errorf("--tail requires a value")
				}
				continue
			}
			return dockerRuntimeOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerRuntimeOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
}

func runtimeBundlePrepareFailed(err error) bool {
	return strings.HasPrefix(err.Error(), "prepare installation bundle:")
}

func bundleErrorHasEnoughOutput(err error) bool {
	message := err.Error()
	return strings.Contains(message, "docker daemon check failed") ||
		strings.Contains(message, "docker daemon did not respond")
}

func runtimeBundleBuildVerboseCommand(dir string, dirExplicit bool) string {
	if !dirExplicit || dir == dockerdeploy.DefaultDeploymentDir {
		return "reploy bundle build --verbose"
	}
	return "reploy bundle build --verbose --dir " + shellQuoteArg(dir)
}

func shellQuoteArg(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n'\"\\$`;&|<>*?()[]{}!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type dockerCommandOptions struct {
	Dir          string
	DirExplicit  bool
	Pack         deploy.PackRef
	Warnings     []string
	Force        bool
	Update       bool
	Verbose      bool
	Requirements []string
}

type dockerCommandParseConfig struct {
	AllowUpdate  bool
	AllowVerbose bool
}

func parseDockerCommandOptions(args []string, requirePack bool, configs ...dockerCommandParseConfig) (dockerCommandOptions, error) {
	config := dockerCommandParseConfig{}
	if len(configs) > 0 {
		config = configs[0]
	}
	options := dockerCommandOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	packSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--force":
			options.Force = true
		case "--update":
			if !config.AllowUpdate {
				return dockerCommandOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Update = true
		case "--verbose":
			if !config.AllowVerbose {
				return dockerCommandOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Verbose = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--requirement":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Requirements = append(options.Requirements, value)
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--requirement=") {
				options.Requirements = append(options.Requirements, strings.TrimPrefix(arg, "--requirement="))
				continue
			}
			if requirePack && !strings.HasPrefix(arg, "-") {
				if packSet {
					return dockerCommandOptions{}, fmt.Errorf("APP_REF may only be provided once")
				}
				ref, warning, err := parsePackRefArgumentWithWarning(arg)
				if err != nil {
					return dockerCommandOptions{}, err
				}
				options.Pack = ref
				if warning != "" {
					options.Warnings = append(options.Warnings, warning)
				}
				packSet = true
				continue
			}
			if !requirePack && !strings.HasPrefix(arg, "-") {
				return dockerCommandOptions{}, fmt.Errorf("APP_REF is only supported with stage or stage --update")
			}
			return dockerCommandOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerCommandOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if requirePack && options.Force && !options.Update {
		return dockerCommandOptions{}, fmt.Errorf("--force is only supported with stage --update")
	}
	if !requirePack && len(options.Requirements) > 0 {
		return dockerCommandOptions{}, fmt.Errorf("--requirement is only supported with stage")
	}
	if options.Update && len(options.Requirements) > 0 {
		return dockerCommandOptions{}, fmt.Errorf("--requirement is only supported when creating a staging directory")
	}
	for _, requirement := range options.Requirements {
		if strings.TrimSpace(requirement) == "" {
			return dockerCommandOptions{}, fmt.Errorf("--requirement must not be empty")
		}
	}
	if requirePack && !options.Update && options.Pack.Raw == "" {
		return dockerCommandOptions{}, fmt.Errorf("APP_REF is required; %s", appRefUsageHint)
	}
	return options, nil
}

func packDisplayName(ref deploy.PackRef) string {
	if ref.Scheme == "file" || ref.Scheme == "source" {
		if pack, err := deploy.LoadPack(ref); err == nil && strings.TrimSpace(pack.App.ID) != "" {
			return pack.App.ID
		}
	}
	if subdir := strings.Trim(ref.Subdir, "/"); subdir != "" {
		parts := strings.Split(subdir, "/")
		return parts[len(parts)-1]
	}
	source := ref.Source
	if packageName, _, hasVersion := strings.Cut(source, "=="); hasVersion {
		source = packageName
	}
	source = strings.TrimRight(source, "/")
	if source == "" {
		return ref.Raw
	}
	if strings.Contains(source, "/") {
		parts := strings.Split(source, "/")
		return parts[len(parts)-1]
	}
	return source
}

func parsePackRefArgument(value string) (deploy.PackRef, error) {
	ref, _, err := parsePackRefArgumentWithWarning(value)
	return ref, err
}

func parsePackRefArgumentWithWarning(value string) (deploy.PackRef, string, error) {
	original := strings.TrimSpace(value)
	expanded := original
	warning := ""
	if localRef, ok := localPathPackRef(original); ok {
		expanded = localRef
	} else if !hasPackRefScheme(original) {
		warning = shorthandLocalPathWarning(original)
		indexExpanded, found, err := expandPackShorthand(original)
		if err != nil {
			return deploy.PackRef{}, "", err
		}
		if !found {
			return deploy.PackRef{}, "", fmt.Errorf("unknown blueprint shorthand %q in Reploy blueprint index %s; %s", packShorthandName(original), packIndexURL(), appRefUsageHint)
		}
		expanded = indexExpanded
	}
	ref, err := deploy.ParsePackRef(expanded)
	if err != nil {
		return deploy.PackRef{}, "", err
	}
	if expanded != original {
		ref.Raw = original
	}
	return ref, warning, nil
}

func localPathPackRef(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, ".") {
		return "file:" + value, true
	}
	return "", false
}

func shorthandLocalPathWarning(value string) string {
	if value == "" {
		return ""
	}
	if _, err := os.Stat(value); err != nil {
		return ""
	}
	return fmt.Sprintf("APP_REF %q also exists as a local path; treating it as a blueprint shorthand. Use %s or %s for the local path.", value, shellQuoteArg("./"+value), shellQuoteArg("file:"+value))
}

func printWarnings(output io.Writer, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(output, "reploy warning: %s\n", warning)
	}
}

func hasPackRefScheme(value string) bool {
	body, _, _ := strings.Cut(value, "?")
	return strings.Contains(body, ":")
}

type packIndex struct {
	SchemaVersion int                       `json:"schema_version"`
	Blueprints    map[string]packIndexEntry `json:"blueprints"`
}

type packIndexEntry struct {
	Ref string `json:"ref"`
}

func expandPackShorthand(value string) (string, bool, error) {
	index, err := loadPackIndex(packIndexURL())
	if err != nil {
		return "", false, fmt.Errorf("load Reploy blueprint index: %w", err)
	}
	return expandPackShorthandFromIndex(value, index)
}

func expandPackShorthandFromIndex(value string, index packIndex) (string, bool, error) {
	body, rawQuery, hasQuery := strings.Cut(value, "?")
	name, version, hasVersion := strings.Cut(body, "==")
	if strings.TrimSpace(name) == "" {
		return "", false, fmt.Errorf("blueprint shorthand must not be empty")
	}
	if hasVersion && strings.TrimSpace(version) == "" {
		return "", false, fmt.Errorf("blueprint shorthand %q has an empty version", name)
	}
	entry, found := index.Blueprints[name]
	if !found {
		return "", false, nil
	}
	template := strings.TrimSpace(entry.Ref)
	if template == "" {
		return "", false, fmt.Errorf("blueprint shorthand %q in Reploy blueprint index is missing ref", name)
	}
	if strings.Contains(template, "{version}") {
		return "", false, fmt.Errorf("ref for blueprint shorthand %q must not use the removed {version} placeholder", name)
	}
	if hasVersion {
		var err error
		template, err = appendPackShorthandVersion(name, template, version)
		if err != nil {
			return "", false, err
		}
	}
	if hasQuery {
		separator := "?"
		if strings.Contains(template, "?") {
			separator = "&"
		}
		template += separator + rawQuery
	}
	return template, true, nil
}

func appendPackShorthandVersion(name string, ref string, version string) (string, error) {
	parsed, err := deploy.ParsePackRef(ref)
	if err != nil {
		return "", fmt.Errorf("parse ref for blueprint shorthand %q: %w", name, err)
	}
	parameter := ""
	switch parsed.Scheme {
	case "pypi":
		parameter = "version"
	case "git":
		parameter = "ref"
	default:
		return "", fmt.Errorf("blueprint shorthand %q does not support version pins for %s refs", name, parsed.Scheme)
	}
	if packShorthandRefHasParameter(ref, parameter) || parsed.Scheme == "pypi" && strings.Contains(parsed.Source, "==") {
		return "", fmt.Errorf("ref for blueprint shorthand %q already declares %s and cannot also use ==VERSION", name, parameter)
	}
	separator := "?"
	if strings.Contains(ref, "?") {
		separator = "&"
	}
	return ref + separator + parameter + "=" + url.QueryEscape(version), nil
}

func packShorthandRefHasParameter(ref string, parameter string) bool {
	_, rawQuery, hasQuery := strings.Cut(ref, "?")
	if !hasQuery {
		return false
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return false
	}
	_, exists := query[parameter]
	return exists
}

func packShorthandName(value string) string {
	body, _, _ := strings.Cut(value, "?")
	name, _, _ := strings.Cut(body, "==")
	return name
}

func packIndexURL() string {
	if value := strings.TrimSpace(os.Getenv(packIndexURLEnv)); value != "" {
		return value
	}
	return defaultPackIndexURL
}

func loadPackIndex(indexURL string) (packIndex, error) {
	index, _, refreshErr := refreshPackIndex(indexURL)
	if refreshErr == nil {
		return index, nil
	}
	cached, cacheErr := os.ReadFile(packIndexCachePath(indexURL))
	if cacheErr == nil {
		return parsePackIndex(cached)
	}
	return packIndex{}, refreshErr
}

func refreshPackIndex(indexURL string) (packIndex, string, error) {
	if strings.HasPrefix(indexURL, "file:") {
		index, err := readPackIndexFile(strings.TrimPrefix(indexURL, "file:"))
		return index, "", err
	}
	if !strings.HasPrefix(indexURL, "http://") && !strings.HasPrefix(indexURL, "https://") {
		return packIndex{}, "", fmt.Errorf("unsupported index URL %q", indexURL)
	}
	content, err := fetchPackIndex(indexURL)
	if err != nil {
		return packIndex{}, "", err
	}
	index, err := parsePackIndex(content)
	if err != nil {
		return packIndex{}, "", err
	}
	path := packIndexCachePath(indexURL)
	if err := writePackIndexCachePath(path, content); err != nil {
		return packIndex{}, "", err
	}
	return index, path, nil
}

func readPackIndexFile(path string) (packIndex, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return packIndex{}, err
	}
	return parsePackIndex(content)
}

func fetchPackIndex(indexURL string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(indexURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", indexURL, response.Status)
	}
	return io.ReadAll(response.Body)
}

func parsePackIndex(content []byte) (packIndex, error) {
	var index packIndex
	if err := json.Unmarshal(content, &index); err != nil {
		return packIndex{}, err
	}
	if index.SchemaVersion != 1 {
		return packIndex{}, fmt.Errorf("unsupported schema_version %d", index.SchemaVersion)
	}
	if index.Blueprints == nil {
		index.Blueprints = map[string]packIndexEntry{}
	}
	return index, nil
}

func writePackIndexCachePath(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func packIndexCachePath(indexURL string) string {
	sum := sha256.Sum256([]byte(indexURL))
	return filepath.Join(reployCLICacheDir(), "blueprint-index", hex.EncodeToString(sum[:])+".json")
}

func reployCLICacheDir() string {
	if value := strings.TrimSpace(os.Getenv("REPLOY_CACHE_DIR")); value != "" {
		return value
	}
	if value, err := os.UserCacheDir(); err == nil && value != "" {
		return filepath.Join(value, "reploy")
	}
	return filepath.Join(os.TempDir(), "reploy-cache")
}

func optionValue(args []string, index *int) (string, bool) {
	if *index+1 >= len(args) || strings.HasPrefix(args[*index+1], "--") {
		return "", false
	}
	*index = *index + 1
	return args[*index], true
}

func startSpinner(output io.Writer, label string) func(bool) {
	stop, _ := startProgressSpinner(output, label)
	return stop
}

func startProgressSpinner(output io.Writer, label string) (func(bool), io.Writer) {
	stop, progress, _ := startProgressSpinnerWithLogs(output, label)
	return stop, progress
}

func startProgressSpinnerWithLogs(output io.Writer, label string) (func(bool), io.Writer, io.Writer) {
	if output == nil {
		return func(bool) {}, io.Discard, io.Discard
	}
	if !terminalAnimationsEnabled() {
		fmt.Fprintf(output, "%s...\n", label)
		progress := progressWriter{write: func(message string) {
			fmt.Fprintf(output, "%s: %s\n", label, message)
		}}
		return func(ok bool) {
			suffix := "... failed"
			if ok {
				suffix = "... done"
			}
			fmt.Fprintf(output, "%s%s\n", label, suffix)
		}, progress, output
	}
	done := make(chan bool, 1)
	updates := make(chan string, 16)
	logs := make(chan string, 16)
	finished := make(chan struct{})
	go func() {
		const hideCursor = "\x1b[?25l"
		const showCursor = "\x1b[?25h"
		frames := []string{"|", "/", "-", "\\"}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		index := 0
		currentLabel := label
		lastLen := 0
		fmt.Fprint(output, hideCursor)
		render := func(text string) {
			line := fmt.Sprintf("\r%s %s", text, frames[index])
			if len(line) < lastLen {
				line += strings.Repeat(" ", lastLen-len(line))
			}
			fmt.Fprint(output, line)
			lastLen = len(line)
		}
		clear := func() {
			if lastLen > 0 {
				fmt.Fprintf(output, "\r%s\r", strings.Repeat(" ", lastLen))
				lastLen = 0
			}
		}
		render(currentLabel)
		for {
			select {
			case ok := <-done:
				for {
					select {
					case line := <-logs:
						clear()
						fmt.Fprintln(output, line)
					default:
						goto finish
					}
				}
			finish:
				suffix := "... failed"
				if ok {
					suffix = "... done"
				}
				line := "\r" + label + suffix
				if len(line) < lastLen {
					line += strings.Repeat(" ", lastLen-len(line))
				}
				fmt.Fprintln(output, line+showCursor)
				close(finished)
				return
			case line := <-logs:
				clear()
				fmt.Fprintln(output, line)
				render(currentLabel)
			case update := <-updates:
				currentLabel = label + ": " + update
				render(currentLabel)
			case <-ticker.C:
				index = (index + 1) % len(frames)
				render(currentLabel)
			}
		}
	}()
	progress := progressWriter{write: func(message string) {
		updates <- message
	}}
	logOutput := &spinnerLogWriter{write: func(line string) {
		logs <- line
	}, terminal: output}
	return func(ok bool) {
		logOutput.Flush()
		done <- ok
		<-finished
	}, progress, logOutput
}

type progressWriter struct {
	write func(string)
}

func (writer progressWriter) Write(content []byte) (int, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		writer.write(line)
	}
	return len(content), nil
}

type spinnerLogWriter struct {
	buffer   strings.Builder
	write    func(string)
	terminal io.Writer
}

func (writer *spinnerLogWriter) TerminalOutput() io.Writer {
	return writer.terminal
}

func (writer *spinnerLogWriter) Write(content []byte) (int, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	for _, char := range text {
		if char == '\n' {
			writer.write(writer.buffer.String())
			writer.buffer.Reset()
			continue
		}
		writer.buffer.WriteRune(char)
	}
	return len(content), nil
}

func (writer *spinnerLogWriter) Flush() {
	if writer.buffer.Len() == 0 {
		return
	}
	writer.write(writer.buffer.String())
	writer.buffer.Reset()
}

func terminalAnimationsEnabled() bool {
	if envBool("CI") {
		return false
	}
	return strings.TrimSpace(os.Getenv("TERM")) != "dumb"
}

func envBool(name string) bool {
	value, ok := os.LookupEnv(name)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func printShortUsage(output io.Writer) {
	fmt.Fprintf(output, "reploy %s\n\n", reploy.DisplayVersion())
	fmt.Fprintln(output, "Usage: reploy COMMAND")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Next steps:")
	fmt.Fprintln(output, "  reploy stage APP_REF")
	fmt.Fprintln(output, "  reploy install APP_REF --scope user|system")
	fmt.Fprintln(output, "  reploy index search QUERY")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Run 'reploy --help' for all commands.")
}

func printHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND

Commands:
  stage        Create a staging directory
  info         Show staging state and bundle contents
  app          Run a blueprint-declared app command inside staging
  shell        Open /bin/sh in a transient staging container
  bundle       Manage staging bundle contents
  up           Start or update the staging Compose service
  restart      Recreate the staging Compose service
  down         Stop and remove the staging Compose service
  ps           Show staging Compose service status
  status       Show staging Compose service status
  logs         Show staging Compose logs with timestamps
  test         Probe the staging app health endpoint
  doctor       Check staging files and generated-file drift
  install      Install or update a deployed host service
  uninstall    Remove an installed host service and Docker resources
  services     List Reploy-managed services
  index        Manage the cached blueprint shorthand index
  version      Print version information

Bundle:
  list         List selected installation artifact roots
    all        List root and transitive built installation artifacts
  list-options List blueprint-declared bundle options
  add          Add installation artifact roots
  remove       Remove installation artifact roots
  check        Build if needed and validate installation artifacts
  build        Explicitly build and validate installation bundle artifacts
  clean        Remove built installation artifacts
  upgrade      Upgrade package roots and rebuild installation bundle artifacts

Target options:
  --docker     Use the Docker deployment target, default
  --docker-timeout DURATION
              Docker daemon responsiveness timeout, default 5s
  --aws        Reserved for a future AWS deployment target

App refs:
  APP_REF     App blueprint reference for stage.
              Indexed shorthand: arbiter-server or arbiter-server==VERSION.
              Local development refs such as ./PATH, /ABS/PATH, or file:PATH are also accepted.
              Python provider refs use pypi://PACKAGE/PATH/APP.blueprint.yaml.
              Git provider refs use github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF.

Staging options:
  --dir DIR    Staging directory, default current staging dir or reploy-staging
  --extra ROOT Add/remove an explicit bundle root; accepts comma-separated roots
  --force      With stage --update, overwrite generated files
  --preinstall Run install-readiness doctor checks
  --quiet      Suppress passing doctor checks
  --to DIR     Install target directory
  --scope user|system
              Required install scope; also applies to doctor --preinstall
  --from DIR   Installed service directory to uninstall
  --service NAME
               Installed service identity, default app id
  --service-name NAME
               Linux/systemd service name for uninstall when --from is gone
  --port PORT  Installed host port override for single-port apps
  --port NAME=PORT
              Installed host port override for a named blueprint port; repeat
              for multiple ports
  --replace PATH
              Replace a preserved managed path during install/update;
              use --replace all to replace every managed path
  --clean     Equivalent to replacing all managed paths
  --in-place  Direct install into the target path instead of a temporary
              staging-like workspace
  --dry-run    Print the install/uninstall plan without changing the host
  --remove-dir Remove the installed target directory during uninstall
  --start      Start after install, default
  --no-start   Install without starting the service
  --verbose    Show bundle check/build command output
  --follow     Follow logs instead of exiting after current output
  --tail N     Show only the last N log lines
  --timeout DURATION
              With test, readiness timeout for running services

Python provider options:
  --requirement REQ
              Exact Python package pin or absolute container path for requirements.txt

Options:
  -h, --help   Show help
  --version    Print version information
`, "\n"))
}

func printPackIndexShortUsage(commandName string, output io.Writer) {
	fmt.Fprintf(output, "Usage: reploy %s COMMAND\n", commandName)
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Next steps:")
	fmt.Fprintf(output, "  reploy %s update\n", commandName)
	fmt.Fprintf(output, "  reploy %s search QUERY\n", commandName)
	fmt.Fprintf(output, "  reploy %s show NAME[==PIN]\n", commandName)
	fmt.Fprintln(output)
	fmt.Fprintf(output, "Run 'reploy %s --help' for blueprint index help.\n", commandName)
}

func printPackIndexHelp(commandName string, output io.Writer) {
	fmt.Fprintf(output, "Usage: reploy %s COMMAND\n\n", commandName)
	fmt.Fprint(output, strings.TrimLeft(`

Commands:
  update       Download, validate, and cache the blueprint shorthand index
  search       Search cached or remote blueprint shorthands
  show         Show one blueprint shorthand, optionally resolved with NAME==PIN

Options:
  --url URL    Index URL, default from REPLOY_BLUEPRINT_INDEX_URL or built-in default
  -h, --help   Show blueprint index help
`, "\n"))
}

func printBundleShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy [--docker-timeout DURATION] bundle COMMAND")
	fmt.Fprintln(output, "Run 'reploy bundle --help' for bundle help.")
	fmt.Fprintln(output)
	fmt.Fprint(output, bundleCommandSummary())
}

func printBundleHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] bundle COMMAND

`, "\n"))
	fmt.Fprint(output, bundleCommandSummary())
	fmt.Fprint(output, strings.TrimLeft(`

Options:
  --dir DIR                  Staging directory, default current staging dir or reploy-staging
  --extra ROOT               Add/remove an explicit bundle root; accepts comma-separated roots
  --dry-run                  Print build/check commands without changing staging
  --pypi-only                Build or upgrade using only PyPI package roots
  --wheelhouse-backend NAME  Wheelhouse backend for build/check: reploy (default) or pip
  --build-backend NAME       Local source wheel build backend for reploy wheelhouse: uv (default) or pip
  --verbose                  Show bundle check/build command output
  -h, --help                 Show bundle help
`, "\n"))
}

func bundleCommandSummary() string {
	return strings.TrimLeft(`
Commands:
  list         List selected installation artifact roots
    all        List root and transitive built installation artifacts
  list-options List blueprint-declared bundle options
  add          Add installation artifact roots
  remove       Remove installation artifact roots
  check        Build if needed and validate installation artifacts
  build        Explicitly build and validate installation bundle artifacts
  clean        Remove built installation artifacts
  upgrade      Upgrade package roots and rebuild installation bundle artifacts
`, "\n")
}

func printAppShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy [--docker-timeout DURATION] app COMMAND")
	fmt.Fprintln(output, "Run 'reploy app --help' for app command help.")
	fmt.Fprintln(output)
	fmt.Fprint(output, appCommandSummary())
}

func printAppHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] app COMMAND

Run a blueprint-declared app command inside staging. App commands use the
application installed in the staging bundle, not a host executable from PATH.

`, "\n"))
	fmt.Fprint(output, appCommandSummary())
	fmt.Fprint(output, strings.TrimLeft(`

Options:
  --dir DIR    Staging directory, default current staging dir or reploy-staging
  -h, --help   Show app command help
`, "\n"))
}

func appCommandSummary() string {
	return strings.TrimLeft(`
Show this staging directory's app subcommands with:
  reploy app

Run an app subcommand with:
  reploy app COMMAND
`, "\n")
}

func printServicesShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy services COMMAND")
	fmt.Fprintln(output, "Run 'reploy services --help' for services help.")
	fmt.Fprintln(output)
	fmt.Fprint(output, servicesCommandSummary())
}

func printServicesHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy services COMMAND

Commands:
  list         List Reploy-managed Linux/systemd services

Options:
  -h, --help   Show services help
`, "\n"))
}

func servicesCommandSummary() string {
	return strings.TrimLeft(`
Commands:
  list         List Reploy-managed Linux/systemd services
`, "\n")
}

func printDockerShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND")
	fmt.Fprintln(output, "Run 'reploy --help' for help.")
}

func printDockerHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND

Commands:
  stage        Create a staging directory
  info         Show staging state and bundle contents
  app          Run a blueprint-declared app command inside staging
  shell        Open /bin/sh in a transient staging container
  bundle       Manage staging bundle contents
  services     List Reploy-managed services
  up           Start or update the staging Compose service
  restart      Recreate the staging Compose service
  down         Stop and remove the staging Compose service
  ps           Show staging Compose service status
  status       Show staging Compose service status
  logs         Show staging Compose logs with timestamps
  test         Probe the staging app health endpoint
  doctor       Check staging files and generated-file drift
  install      Install or update a deployed host service
  uninstall    Remove an installed host service and Docker resources

Bundle:
  list         List selected installation artifact roots
    all        List root and transitive built installation artifacts
  list-options List blueprint-declared bundle options
  add          Add installation artifact roots
  remove       Remove installation artifact roots
  check        Build if needed and validate installation artifacts
  build        Explicitly build and validate installation bundle artifacts
  clean        Remove built installation artifacts
  upgrade      Upgrade package roots and rebuild installation bundle artifacts

App refs:
  APP_REF     App blueprint reference for stage.
              Indexed shorthand: arbiter-server or arbiter-server==VERSION.
              Local development refs such as ./PATH, /ABS/PATH, or file:PATH are also accepted.
              Python provider refs use pypi://PACKAGE/PATH/APP.blueprint.yaml.
              Git provider refs use github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF.

Options:
  --docker-timeout DURATION
              Docker daemon responsiveness timeout, default 5s
  --dir DIR    Staging directory, default current staging dir or reploy-staging
  --extra ROOT Add/remove an explicit bundle root; accepts comma-separated roots
  --force      With stage --update, overwrite generated files
  --preinstall Run install-readiness doctor checks
  --quiet      Suppress passing doctor checks
  --to DIR     Install target directory
  --scope user|system
              Required install scope; also applies to doctor --preinstall
  --from DIR   Installed service directory to uninstall
  --service NAME
               Installed service identity, default app id
  --service-name NAME
               Linux/systemd service name for uninstall when --from is gone
  --port PORT  Installed host port override for single-port apps
  --port NAME=PORT
              Installed host port override for a named blueprint port; repeat
              for multiple ports
  --replace PATH
              Replace a preserved managed path during install/update;
              use --replace all to replace every managed path
  --clean     Equivalent to replacing all managed paths
  --in-place  Direct install into the target path instead of a temporary
              staging-like workspace
  --dry-run    Print the install/uninstall plan without changing the host
  --remove-dir Remove the installed target directory during uninstall
  --start      Start after install, default
  --no-start   Install without starting the service
  --verbose    Show bundle check/build command output
  --follow     Follow logs instead of exiting after current output
  --tail N     Show only the last N log lines
  --timeout DURATION
              With test, readiness timeout for running services

Python provider options:
  --requirement REQ
              Exact Python package pin or absolute container path for requirements.txt
  -h, --help   Show help
`, "\n"))
}

func printDockerCommandHelp(command string, output io.Writer) {
	switch command {
	case "stage":
		printDockerStageHelp(output)
	case "logs":
		printDockerLogsHelp(output)
	default:
		printDockerHelp(output)
	}
}

func printDockerStageHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker] [--docker-timeout DURATION] stage APP_REF [OPTIONS]
       reploy [--docker] [--docker-timeout DURATION] stage --update [APP_REF] [OPTIONS]

Create a staging directory from an app blueprint reference.
Use --update to refresh an existing staging directory, optionally from a new ref.

APP_REF:
  Indexed shorthand from the Reploy blueprint index:
    arbiter-server
    arbiter-server==0.4.2

  Local filesystem refs:
    ./PATH
    /ABS/PATH
    file:PATH

  Python provider refs:
    pypi://PACKAGE/PATH/APP.blueprint.yaml
    pypi://PACKAGE/PATH/APP.blueprint.yaml?version=VERSION

  Git provider refs:
    github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF
    github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF&transport=ssh

  Local paths without file: must start with . or /.
  PyPI paths must point to the blueprint file inside the package.
  GitHub paths must point to the blueprint file inside the repository.

Options:
  --dir DIR    Staging directory to create, default reploy-staging
  --update     Update an existing staging directory instead of creating one
  --force      With --update, overwrite locally edited generated files
  --verbose    Show generated file update details

Python provider options:
  --requirement REQ
              Exact Python package pin or absolute container path for requirements.txt
  -h, --help   Show stage help
`, "\n"))
}

func printDockerLogsHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker] logs [OPTIONS]

Show staging Compose logs with timestamps.

Options:
  --dir DIR    Staging directory, default current staging dir or reploy-staging
  --tail N     Show only the last N log lines
  --follow, -f
              Follow logs instead of exiting after current output
  -h, --help   Show logs help
`, "\n"))
}
