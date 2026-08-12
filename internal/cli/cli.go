package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	reploy "github.com/omry/reploy"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/dockerdeploy"
	"github.com/omry/reploy/internal/overrideui"
)

const defaultPackIndexURL = "https://raw.githubusercontent.com/omry/reploy/main/blueprint-index.json"
const packIndexURLEnv = "REPLOY_BLUEPRINT_INDEX_URL"
const appRefUsageHint = "use an indexed shorthand such as arbiter-server or arbiter-server==VERSION, a provider ref such as pypi://PACKAGE/PATH/APP.blueprint.yaml or github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF, or a local path starting with . or /"

var dockerDirectInstall = dockerdeploy.DirectInstallProviderResultV1
var dockerInstall = dockerdeploy.InstallProviderResultV1
var dockerInstallSuccessLines = dockerdeploy.InstallSuccessLines
var dockerUninstall = dockerdeploy.UninstallProviderV1
var printReploySystemdServices = dockerdeploy.PrintReploySystemdServices
var dockerUninstallNeedsRoot = dockerdeploy.UninstallProviderNeedsRootV1
var dockerRuntime = dockerdeploy.Runtime
var dockerTestServer = dockerdeploy.TestServer
var dockerShell = dockerdeploy.Shell
var dockerStageLoadedPackDesiredState = dockerdeploy.StageLoadedPackDesiredStateV1
var dockerRestageCurrentDesiredPlatform = dockerdeploy.RestageCurrentDesiredPlatformV1
var dockerForceRestageCurrentDesiredPlatform = dockerdeploy.ForceRestageCurrentDesiredPlatformV1
var dockerRemoveStagedDeployment = dockerdeploy.RemoveStagedDeploymentV1
var dockerProviderBuild = dockerdeploy.RunProviderBuildV1
var dockerVerifyCurrentBuild = dockerdeploy.VerifyCurrentBuildV1
var dockerProviderStoreClean = dockerdeploy.CleanProviderStoreV1
var dockerProviderBuildRuntime = dockerdeploy.CurrentStagedProviderBuildRuntimeV1
var dockerAppCommand = dockerdeploy.AppCommand
var runOverrideEditor = overrideui.RunWithResult
var runBuildProgress = overrideui.RunBuildProgress
var inspectStagedOverrideValidation = dockerdeploy.InspectStagedOverrideValidation
var runControlledSessionBroker = controlledsession.RunControllerBrokerV1
var runControlledSessionAttachment = controlledsession.RunTerminalAttachmentV1

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
		return runNoCommand(stdout, stderr)
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
	case "_service-container":
		return runEmbeddedServiceContainer(args[1:], stdout, stderr, globalOptions)
	case "index":
		return runPackIndex(args[0], args[1:], stdout, stderr)
	case "validate":
		return runBlueprintValidate(args[1:], stdout, stderr)
	case "services":
		return runServices(args[1:], stdout, stderr)
	case "controlled-session":
		return runControlledSession(args[1:], stdout, stderr)
	default:
		if isDeploymentCommand(args[0]) {
			return runDocker(args, stdout, stderr, globalOptions)
		}
		if strings.HasPrefix(args[0], "-") {
			return printTopLevelUsageError(stderr, "unknown option: %s", args[0])
		}
		if suggestion := topLevelAppCommandSuggestion(args); suggestion != "" {
			return printTopLevelUsageError(stderr, "unknown command: %s; did you mean `%s`?", args[0], suggestion)
		}
		return printTopLevelUsageError(stderr, "unknown command: %s", args[0])
	}
}

func runControlledSession(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 1 && isHelpArg(args[0]) {
		printControlledSessionHelp(stdout)
		return 0
	}
	if len(args) >= 1 && args[0] == "client" {
		if len(args) == 2 && isHelpArg(args[1]) {
			printControlledSessionClientHelp(stdout)
			return 0
		}
		if len(args) != 1 {
			fmt.Fprintln(stderr, "reploy controlled-session client usage error: unexpected argument")
			printControlledSessionClientShortUsage(stderr)
			return 2
		}
		return runControlledSessionClient(stdout, stderr)
	}
	if len(args) >= 1 && args[0] == "attach" {
		if len(args) == 2 && isHelpArg(args[1]) {
			printControlledSessionAttachHelp(stdout)
			return 0
		}
		socket, err := parseControlledSessionAttachSocket(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy controlled-session attach usage error: %v\n", err)
			printControlledSessionAttachShortUsage(stderr)
			return 2
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		if err := runControlledSessionAttachment(ctx, controlledsession.TerminalAttachmentOptionsV1{
			SocketPath: socket,
			Input:      os.Stdin,
			Output:     stdout,
		}); err != nil {
			fmt.Fprintf(stderr, "reploy controlled-session attach error: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stderr, "reploy controlled-session usage error: expected client or attach")
	printControlledSessionShortUsage(stderr)
	return 2
}

func runControlledSessionClient(stdout io.Writer, stderr io.Writer) int {
	socket := os.Getenv("REPLOY_SESSION_SOCKET")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := runControlledSessionBroker(ctx, controlledsession.ControllerBrokerOptionsV1{
		SessionSocket: socket,
		TemporaryHome: controlledsession.ControllerTemporaryHomeV1,
		Input:         os.Stdin,
		Output:        stdout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "reploy controlled-session client error: %v\n", err)
		return 1
	}
	return 0
}

func parseControlledSessionAttachSocket(args []string) (string, error) {
	// The socket is deliberately the only public option. The broker chooses
	// and reports it; the attachment does not discover or create a socket.
	if len(args) == 1 && strings.HasPrefix(args[0], "--socket=") {
		socket := strings.TrimPrefix(args[0], "--socket=")
		if socket == "" {
			return "", fmt.Errorf("--socket requires a value")
		}
		return socket, nil
	}
	if len(args) == 2 && args[0] == "--socket" && args[1] != "" {
		return args[1], nil
	}
	if len(args) == 1 && args[0] == "--socket" {
		return "", fmt.Errorf("--socket requires a value")
	}
	if len(args) == 0 {
		return "", fmt.Errorf("--socket is required")
	}
	return "", fmt.Errorf("expected exactly --socket PATH")
}

func runEmbeddedServiceContainer(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	dir := ""
	dockerPath := ""
	action := ""
	for len(args) > 0 {
		switch args[0] {
		case "--dir":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				fmt.Fprintln(stderr, "reploy service-container usage error: --dir requires a value")
				return 2
			}
			dir = args[1]
			args = args[2:]
		case "--docker":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				fmt.Fprintln(stderr, "reploy service-container usage error: --docker requires a value")
				return 2
			}
			dockerPath = args[1]
			args = args[2:]
		default:
			if strings.HasPrefix(args[0], "--dir=") {
				dir = strings.TrimPrefix(args[0], "--dir=")
				args = args[1:]
				continue
			}
			if strings.HasPrefix(args[0], "--docker=") {
				dockerPath = strings.TrimPrefix(args[0], "--docker=")
				args = args[1:]
				continue
			}
			if action == "" {
				action = args[0]
				args = args[1:]
				continue
			}
			fmt.Fprintln(stderr, "reploy service-container usage error: unexpected argument")
			return 2
		}
	}
	if dir == "" || dockerPath == "" || action != "run" {
		fmt.Fprintln(stderr, "reploy service-container usage error: expected --dir DIR --docker PATH run")
		return 2
	}
	timeout := time.Duration(0)
	if globalOptions.DockerTimeoutSet {
		timeout = globalOptions.DockerTimeout
	}
	if err := dockerdeploy.RunInstalledServiceContainerV1(context.Background(), dir, action, dockerPath, dockerdeploy.RunOptions{
		Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: timeout,
	}); err != nil {
		fmt.Fprintf(stderr, "reploy service-container error: %v\n", err)
		return 1
	}
	return 0
}

func runBlueprintValidate(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 1 && isHelpArg(args[0]) {
		printBlueprintValidateHelp(stdout)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "reploy validate usage error: BLUEPRINT_REF is required")
		printBlueprintValidateShortUsage(stderr)
		return 2
	}
	if len(args) > 1 {
		fmt.Fprintln(stderr, "reploy validate usage error: BLUEPRINT_REF may only be provided once")
		printBlueprintValidateShortUsage(stderr)
		return 2
	}
	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(stderr, "reploy validate usage error: unknown option: %s\n", args[0])
		printBlueprintValidateShortUsage(stderr)
		return 2
	}
	ref, warning, err := parsePackRefArgumentWithWarning(args[0])
	if err != nil {
		var localPathError likelyLocalBlueprintRefError
		if errors.As(err, &localPathError) {
			fmt.Fprintf(stderr, "fail: %v\n", err)
			return 2
		}
		fmt.Fprintf(stderr, "reploy validate usage error: %v\n", err)
		printBlueprintValidateShortUsage(stderr)
		return 2
	}
	if warning != "" {
		printWarnings(stderr, []string{warning})
	}
	loaded, err := deploy.LoadBlueprint(ref)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", validationStatusText(stderr, "fail", "31"), err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s (syntax and semantics)\n", validationStatusText(stdout, "pass", "32"), loaded.Document.Environment.ID)
	return 0
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

func runNoCommand(stdout io.Writer, stderr io.Writer) int {
	if implicitDeploymentStateExists(dockerdeploy.DefaultDeploymentDir, false) {
		return runDockerDeploymentSummary(stdout, stderr)
	}
	printShortUsage(stdout)
	return 0
}

func runDockerDeploymentSummary(stdout io.Writer, stderr io.Writer) int {
	dir := resolveImplicitDeploymentDir(dockerdeploy.DefaultDeploymentDir, false, io.Discard)
	content, err := os.ReadFile(filepath.Join(dir, dockerdeploy.StateFileName))
	if err != nil {
		fmt.Fprintf(stderr, "reploy error: %v\n", err)
		return 1
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		fmt.Fprintf(stderr, "reploy error: %v\n", err)
		return 1
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		fmt.Fprintf(stderr, "reploy error: %v\n", err)
		return 1
	}
	summary := deploymentSummaryStateV1{AppID: document.Environment.ID, Installed: state.Deployment != nil}
	stdout, stderr, err = dockerdeploy.DeploymentOutputWriters(dir, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy error: %v\n", err)
		return 1
	}
	result, err := dockerdeploy.AppCommandList(dockerdeploy.AppCommandListOptions{Dir: dir, DeployedOnly: summary.Installed})
	if err != nil {
		fmt.Fprintf(stderr, "reploy error: %v\n", err)
		return 1
	}
	printDeploymentSummary(stdout, dir, summary, result.Commands)
	return 0
}

type deploymentSummaryStateV1 struct {
	AppID     string
	Installed bool
}

func printDeploymentSummary(output io.Writer, dir string, state deploymentSummaryStateV1, appCommands []dockerdeploy.AppCommandListEntry) {
	appID := strings.TrimSpace(state.AppID)
	if appID == "" {
		appID = "unknown"
	}
	fmt.Fprintf(output, "app: %s\n", appID)
	fmt.Fprintf(output, "reploy: %s\n", reploy.DisplayVersion())
	fmt.Fprintf(output, "context: %s deployment\n", deploymentSummaryContext(state.Installed))
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

func deploymentSummaryContext(installed bool) string {
	if installed {
		return "installed"
	}
	return "staged"
}

func deploymentSummaryDir(dir string) string {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return absolute
}

func deploymentSummaryCommands(state deploymentSummaryStateV1) []string {
	if state.Installed {
		return []string{
			"reploy up|down|status",
			"reploy logs --tail 100",
			"reploy restart",
			"reploy uninstall --from .",
		}
	}
	return []string{
		"reploy info",
		"reploy bundle list",
		"reploy up|down|status",
		"reploy logs --tail 50",
		"reploy install --scope user --to DIR",
	}
}

func deploymentSummaryAppCommandExamples(state deploymentSummaryStateV1, commands []dockerdeploy.AppCommandListEntry) []string {
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

func deploymentSummaryAppCommandListCommand(state deploymentSummaryStateV1) string {
	if state.Installed {
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
	DockerTimeout    time.Duration
	DockerTimeoutSet bool
}

func parseGlobalDeploymentOptions(args []string) (globalDeploymentOptions, []string, error) {
	options := globalDeploymentOptions{}
	for len(args) > 0 {
		arg := args[0]
		switch arg {
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
	case "stage", "overrides", "build", "verify", "info", "app", "shell", "runs", "bundle", "up", "start", "restart", "down", "stop", "ps", "status", "logs", "test", "doctor", "install", "uninstall":
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
	if args[0] == "runs" {
		return runDockerRuns(args[1:], stdout, stderr, globalOptions)
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
		return runDockerStage(args[1:], stdout, stderr, globalOptions)
	case "overrides":
		return runPackageOverrides(args[1:], stdout, stderr, globalOptions)
	case "build":
		return runDockerBuild(args[1:], stdout, stderr, globalOptions)
	case "verify":
		return runDockerVerify(args[1:], stdout, stderr)
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
	case "up", "start", "restart", "down", "stop", "ps", "status", "logs":
		return runDockerRuntime(args[0], args[1:], stdout, stderr, globalOptions)
	case "shell":
		options, err := parseDockerShellOptions(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			return 2
		}
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy shell error: %v\n", err)
			return 1
		}
		if err := dockerShell(dockerdeploy.ShellOptions{Dir: options.Dir, Wait: options.Wait, ReadOnly: options.ReadOnly, Stdin: os.Stdin, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: globalOptions.DockerTimeout}); err != nil {
			if errors.Is(err, context.Canceled) {
				return 130
			}
			if errors.Is(err, dockerdeploy.ErrLiveRunStoppedV1) {
				wrappedStdout, _, wrapErr := dockerdeploy.DeploymentOutputWriters(options.Dir, stdout, stderr)
				if wrapErr != nil {
					fmt.Fprintf(stderr, "reploy shell error: %v\n", wrapErr)
					return 1
				}
				fmt.Fprintln(wrappedStdout, "shell stopped by `reploy runs stop`.")
				return 130
			}
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

func runPackageOverrides(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerCommandOptions(args, false)
	if err != nil {
		fmt.Fprintf(stderr, "reploy overrides usage error: %v\n", err)
		printPackageOverridesHelp(stderr)
		return 2
	}
	options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "reploy overrides error: %v\n", err)
		return 1
	}
	config, err := packageOverrideEditorConfig(
		options.Dir, globalOptions, false,
	)
	if err != nil {
		fmt.Fprintf(stderr, "reploy overrides error: %v\n", err)
		return 1
	}
	config.Input = os.Stdin
	config.Output = stdout
	if _, err := runOverrideEditor(config); err != nil {
		fmt.Fprintf(stderr, "reploy overrides error: %v\n", err)
		return 1
	}
	return 0
}

func packageOverrideEditorConfig(
	deploymentDir string,
	globalOptions globalDeploymentOptions,
	noCache bool,
) (overrideui.Config, error) {
	operation, err := deploy.AcquireExistingOperationLock(context.Background(), deploymentDir)
	if err != nil {
		return overrideui.Config{}, err
	}
	state, found, readErr := operation.ReadStateV1()
	unlockErr := operation.Unlock()
	if err := errors.Join(readErr, unlockErr); err != nil {
		return overrideui.Config{}, err
	}
	if !found || state.Staging == nil {
		return overrideui.Config{}, fmt.Errorf("package overrides require a staged deployment")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return overrideui.Config{}, err
	}
	validation, err := inspectStagedOverrideValidation(context.Background(), deploymentDir)
	if err != nil {
		return overrideui.Config{}, fmt.Errorf("inspect validation status: %w", err)
	}
	discovered := make([]overrideui.DiscoveredPackage, 0, len(validation.Packages))
	for _, item := range validation.Packages {
		discovered = append(discovered, overrideui.DiscoveredPackage{Provider: item.Provider, Package: item.Package})
	}
	unused := make([]overrideui.DiscoveredPackage, 0, len(validation.Unused))
	for _, item := range validation.Unused {
		unused = append(unused, overrideui.DiscoveredPackage{Provider: item.Provider, Package: item.Package})
	}
	return overrideui.Config{
		Context: context.Background(), DeploymentDir: deploymentDir, Document: document, Overlay: state.Overlay,
		Platform: state.Platform, Validated: validation.Validated, Discovered: discovered, Unused: unused,
		Validate: func(ctx context.Context, progress io.Writer) (overrideui.ValidationResult, error) {
			runtime, err := dockerProviderBuildRuntime()
			if err != nil {
				return overrideui.ValidationResult{}, err
			}
			var childOutput synchronizedBuffer
			_, err = dockerProviderBuild(ctx, dockerdeploy.ProviderBuildRunInputV1{
				DeploymentDir: deploymentDir, Runtime: runtime, ValidateChoices: true,
				NoCache: noCache, Progress: progress,
				RunOptions: dockerdeploy.RunOptions{
					Stdout: &childOutput, Stderr: &childOutput,
					DockerPreflightTimeout: globalOptions.DockerTimeout,
				},
			})
			if err != nil {
				return overrideui.ValidationResult{}, errors.New(
					buildFailureDiagnostic(err, childOutput.String()),
				)
			}
			status, err := inspectStagedOverrideValidation(ctx, deploymentDir)
			if err != nil {
				return overrideui.ValidationResult{}, err
			}
			if !status.Validated {
				return overrideui.ValidationResult{}, fmt.Errorf(
					"trial build completed without a matching validated result; reopen the editor and try again",
				)
			}
			result := overrideui.ValidationResult{Packages: make([]overrideui.DiscoveredPackage, 0, len(status.Packages))}
			for _, item := range status.Packages {
				result.Packages = append(result.Packages, overrideui.DiscoveredPackage{Provider: item.Provider, Package: item.Package})
			}
			for _, item := range status.Unused {
				result.Unused = append(result.Unused, overrideui.DiscoveredPackage{Provider: item.Provider, Package: item.Package})
			}
			return result, nil
		},
	}, nil
}

func runDockerStage(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerCommandOptions(args, true, dockerCommandParseConfig{
		AllowUpdate:   true,
		AllowVerbose:  true,
		AllowPlatform: true,
		AllowForce:    true,
		AllowRemove:   true,
	})
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		var localPathError likelyLocalBlueprintRefError
		if !errors.As(err, &localPathError) {
			printDockerStageHelp(stderr)
		}
		return 2
	}
	printWarnings(stderr, options.Warnings)

	if options.Remove {
		options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, io.Discard)
		controlMode := dockerdeploy.ControlAdmissionImmediateV1
		if options.Force {
			controlMode = dockerdeploy.ControlAdmissionForceV1
		}
		result, removeErr := dockerRemoveStagedDeployment(
			context.Background(),
			dockerdeploy.StagedDeploymentRemoveInputV1{
				DeploymentDir: options.Dir,
				ControlMode:   controlMode,
				RunOptions: dockerdeploy.RunOptions{
					Stdout: stdout, Stderr: stderr,
					DockerPreflightTimeout: globalOptions.DockerTimeout,
				},
			},
		)
		if removeErr != nil {
			fmt.Fprintf(stderr, "reploy stage --remove error: %v\n", removeErr)
			return 1
		}
		fmt.Fprintf(stdout, "removed staging directory: %s\n", result.DeploymentDir)
		if options.Verbose {
			fmt.Fprintf(stdout, "removed environment: %s\n", result.Environment)
		}
		return 0
	}

	if options.Update {
		options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, io.Discard)
		if options.Pack.Raw != "" {
			loaded, loadErr := deploy.LoadBlueprint(options.Pack)
			if loadErr != nil {
				fmt.Fprintf(stderr, "reploy stage --update error: %v\n", loadErr)
				return 1
			}
			result, stageErr := dockerStageLoadedPackDesiredState(context.Background(), dockerdeploy.LoadedPackDesiredStateStageInputV1{
				DeploymentDir: options.Dir, Blueprint: loaded, ExplicitPlatform: options.Platform,
				Force: options.Force, RunOptions: dockerdeploy.RunOptions{DockerPreflightTimeout: globalOptions.DockerTimeout},
			})
			if stageErr != nil {
				fmt.Fprintf(stderr, "reploy stage --update error: %v\n", stageErr)
				return 1
			}
			printDesiredStateStageResult(stdout, options.Dir, result.DesiredState, options.Verbose)
			return 0
		}
		if options.Force {
			result, stageErr := dockerForceRestageCurrentDesiredPlatform(
				context.Background(),
				options.Dir,
				options.Platform,
				dockerdeploy.RunOptions{
					DockerPreflightTimeout: globalOptions.DockerTimeout,
				},
			)
			if stageErr != nil {
				fmt.Fprintf(stderr, "reploy stage --update error: %v\n", stageErr)
				return 1
			}
			printDesiredStateStageResult(stdout, options.Dir, result, options.Verbose)
			return 0
		}
		result, stageErr := dockerRestageCurrentDesiredPlatform(
			context.Background(),
			options.Dir,
			options.Platform,
		)
		if stageErr != nil {
			fmt.Fprintf(stderr, "reploy stage --update error: %v\n", stageErr)
			return 1
		}
		printDesiredStateStageResult(stdout, options.Dir, result, options.Verbose)
		return 0
	}

	loaded, err := deploy.LoadBlueprint(options.Pack)
	if err != nil {
		fmt.Fprintf(stderr, "reploy stage error: %v\n", err)
		return 1
	}
	result, stageErr := dockerStageLoadedPackDesiredState(context.Background(), dockerdeploy.LoadedPackDesiredStateStageInputV1{
		DeploymentDir: options.Dir, Blueprint: loaded, ExplicitPlatform: options.Platform,
		Create: true,
	})
	if stageErr != nil {
		if errors.Is(stageErr, deploy.ErrDesiredStateAlreadyExists) {
			fmt.Fprintf(stderr, "reploy stage error: staging directory already exists at %s. use --update to update it\n", options.Dir)
			return 1
		}
		fmt.Fprintf(stderr, "reploy stage error: %v\n", stageErr)
		return 1
	}
	fmt.Fprintf(stdout, "created staging directory for %s: %s\n", result.AppID, options.Dir)
	if options.Verbose {
		fmt.Fprintf(stdout, "selected platform: %s\n", result.DesiredState.State.Platform.Canonical)
	}
	return 0
}

func printDesiredStateStageResult(output io.Writer, dir string, result deploy.DesiredStateUpdateResult, verbose bool) {
	if result.Changed {
		fmt.Fprintf(output, "updated staging directory: %s\n", dir)
	} else {
		fmt.Fprintf(output, "staging directory is up to date: %s\n", dir)
	}
	if verbose {
		fmt.Fprintf(output, "selected platform: %s\n", result.State.Platform.Canonical)
	}
}

type dockerBuildOptions struct {
	Dir         string
	DirExplicit bool
	NoCache     bool
	Verify      bool
	Profile     bool
	Verbose     bool
}

func parseDockerBuildOptions(args []string) (dockerBuildOptions, error) {
	options := dockerBuildOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBuildOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--no-cache":
			options.NoCache = true
		case "--verify":
			options.Verify = true
		case "--profile":
			options.Profile = true
		case "--verbose":
			options.Verbose = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			return dockerBuildOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerBuildOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if options.NoCache && options.Verify {
		return dockerBuildOptions{}, fmt.Errorf("--no-cache and --verify are mutually exclusive")
	}
	return options, nil
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
		if options.OutputDir != "" || options.OutputFile != "" {
			fmt.Fprintln(stderr, "reploy usage error: output options require an app command")
			printAppShortUsage(stderr)
			return 2
		}
		if options.Wait {
			fmt.Fprintln(stderr, "reploy usage error: --wait requires an app command")
			printAppShortUsage(stderr)
			return 2
		}
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
		if options.OutputDir != "" || options.OutputFile != "" {
			fmt.Fprintln(stderr, "reploy usage error: output options require an app command")
			printAppShortUsage(stderr)
			return 2
		}
		if options.Wait {
			fmt.Fprintln(stderr, "reploy usage error: --wait requires an app command")
			printAppShortUsage(stderr)
			return 2
		}
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
	if err := dockerAppCommand(dockerdeploy.AppCommandOptions{
		Dir:                    options.Dir,
		CommandArgs:            options.CommandArgs,
		DeployedOnly:           options.DeployedOnly,
		OutputDir:              options.OutputDir,
		OutputFile:             options.OutputFile,
		Wait:                   options.Wait,
		Stdout:                 stdout,
		Stderr:                 stderr,
		DockerPreflightTimeout: globalOptions.DockerTimeout,
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
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
	OutputDir    string
	OutputFile   string
	Wait         bool
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
	afterSeparator := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if afterSeparator {
			options.CommandArgs = append(options.CommandArgs, arg)
			continue
		}
		if arg == "--" {
			afterSeparator = true
			options.CommandArgs = append(options.CommandArgs, arg)
			continue
		}
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
		case "--wait":
			options.Wait = true
		case "--format":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerAppOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Format = value
		case "--output-dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerAppOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.OutputDir = value
		case "--output-file":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerAppOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.OutputFile = value
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
			if strings.HasPrefix(arg, "--output-dir=") {
				options.OutputDir = strings.TrimPrefix(arg, "--output-dir=")
				continue
			}
			if strings.HasPrefix(arg, "--output-file=") {
				options.OutputFile = strings.TrimPrefix(arg, "--output-file=")
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
	if options.OutputDir != "" && options.OutputFile != "" {
		return dockerAppOptions{}, fmt.Errorf("--output-dir and --output-file are mutually exclusive")
	}
	if (options.OutputDir == "" && appOptionWasPresent(args, "--output-dir")) || (options.OutputFile == "" && appOptionWasPresent(args, "--output-file")) {
		return dockerAppOptions{}, fmt.Errorf("output path must not be empty")
	}
	return options, nil
}

func appOptionWasPresent(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
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

func runDockerBundle(args []string, stdout io.Writer, stderr io.Writer, _ globalDeploymentOptions) int {
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
	if isDockerBundleOverlayMutationCommand(action) {
		return runDockerBundleOverlayMutation(action, args[1:], stdout, stderr)
	}
	if action == "options" || action == "list" {
		return runDockerBundleOverlayInspection(action, args[1:], stdout, stderr)
	}
	if action != "clean" {
		fmt.Fprintf(stderr, "reploy usage error: unknown bundle command: %s\n", action)
		printBundleShortUsage(stderr)
		return 2
	}
	options, err := parseDockerBundleCommandOptions(args[1:], true)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printBundleShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, io.Discard)
	result, err := dockerProviderStoreClean(context.Background(), options.Dir)
	if err != nil {
		fmt.Fprintf(stderr, "reploy bundle clean error: %v\n", err)
		return 1
	}
	if options.Verbose {
		results := []dockerdeploy.UpdateResult{}
		if result.Removed {
			results = append(results, dockerdeploy.UpdateResult{
				Path: result.Path, Status: deploy.UpdateStatusRemoved, Ownership: "provider-cache",
				Reason: "removed deployment-local provider store",
			})
		}
		printUpdateResults(stdout, results)
	}
	return 0
}

func runDockerBundleOverlayInspection(action string, args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerBundleCommandOptions(args, false)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printBundleShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, io.Discard)
	if action == "options" {
		entries, err := dockerdeploy.ListRequestOverlayOptions(context.Background(), options.Dir)
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle options error: %v\n", err)
			return 1
		}
		for _, entry := range entries {
			fmt.Fprintf(stdout, "%s\t%s\n", entry.Name, entry.Description)
		}
		return 0
	}
	entries, err := dockerdeploy.ListRequestOverlay(context.Background(), options.Dir)
	if err != nil {
		fmt.Fprintf(stderr, "reploy bundle list error: %v\n", err)
		return 1
	}
	for _, entry := range entries {
		if entry.Kind == "option" {
			fmt.Fprintf(stdout, "option\t%s\n", entry.Value)
		} else {
			fmt.Fprintf(stdout, "package\t%s\t%s\n", entry.Component, entry.Value)
		}
	}
	return 0
}

type dockerBundleOverlayMutationOptions struct {
	Dir         string
	DirExplicit bool
	Component   string
	Arguments   []string
}

func isDockerBundleOverlayMutationCommand(action string) bool {
	switch action {
	case "add", "remove", "add-package", "remove-package":
		return true
	default:
		return false
	}
}

func runDockerBundleOverlayMutation(action string, args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerBundleOverlayMutationOptions(action, args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printBundleShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, io.Discard)
	var result deploy.RequestOverlayMutationResult
	switch action {
	case "add":
		result, err = dockerdeploy.AddRequestOverlayOptions(context.Background(), options.Dir, options.Arguments)
	case "remove":
		result, err = dockerdeploy.RemoveRequestOverlayOptions(context.Background(), options.Dir, options.Arguments)
	case "add-package":
		result, err = dockerdeploy.AddRequestOverlayPackages(context.Background(), options.Dir, options.Component, options.Arguments)
	case "remove-package":
		result, err = dockerdeploy.RemoveRequestOverlayPackages(context.Background(), options.Dir, options.Component, options.Arguments)
	}
	if err != nil {
		fmt.Fprintf(stderr, "reploy bundle %s error: %v\n", action, err)
		return 1
	}
	if result.Changed {
		fmt.Fprintln(stdout, "bundle overlay updated")
	} else {
		fmt.Fprintln(stdout, "bundle overlay unchanged")
	}
	return 0
}

func parseDockerBundleOverlayMutationOptions(action string, args []string) (dockerBundleOverlayMutationOptions, error) {
	options := dockerBundleOverlayMutationOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOverlayMutationOptions{}, fmt.Errorf("--dir requires a value")
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
				return dockerBundleOverlayMutationOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Arguments = append(options.Arguments, arg)
		}
	}
	if options.Dir == "" {
		return dockerBundleOverlayMutationOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if action == "add-package" || action == "remove-package" {
		if len(options.Arguments) < 2 {
			return dockerBundleOverlayMutationOptions{}, fmt.Errorf("bundle %s requires COMPONENT and at least one REQUIREMENT", action)
		}
		options.Component = options.Arguments[0]
		options.Arguments = options.Arguments[1:]
		return options, nil
	}
	if len(options.Arguments) == 0 {
		return dockerBundleOverlayMutationOptions{}, fmt.Errorf("bundle %s requires at least one COMPONENT/OPTION argument", action)
	}
	return options, nil
}

type dockerBundleCommandOptions struct {
	Dir         string
	DirExplicit bool
	Verbose     bool
}

func parseDockerBundleCommandOptions(args []string, allowVerbose bool) (dockerBundleCommandOptions, error) {
	options := dockerBundleCommandOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleCommandOptions{}, fmt.Errorf("--dir requires a value")
			}
			options.Dir = value
			options.DirExplicit = true
		case "--verbose":
			if !allowVerbose {
				return dockerBundleCommandOptions{}, fmt.Errorf("unknown option: --verbose")
			}
			options.Verbose = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			return dockerBundleCommandOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerBundleCommandOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
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

func runDockerVerify(args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerCommandOptions(args, false)
	if err != nil {
		fmt.Fprintf(stderr, "reploy verify usage error: %v\n", err)
		fmt.Fprintln(stderr, "Usage: reploy verify [OPTIONS]")
		return 2
	}
	options.Dir, err = resolveImplicitStagingDeploymentDir(
		options.Dir,
		options.DirExplicit,
		stderr,
	)
	if err != nil {
		fmt.Fprintf(stderr, "reploy verify error: %v\n", err)
		return 1
	}
	runtime, err := dockerProviderBuildRuntime()
	if err != nil {
		fmt.Fprintf(stderr, "reploy verify error: %v\n", err)
		return 1
	}
	result, err := dockerVerifyCurrentBuild(context.Background(), dockerdeploy.VerifyCurrentBuildInputV1{
		DeploymentDir: options.Dir,
		Runtime:       runtime,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		fmt.Fprintf(stderr, "reploy verify error: %v\n", err)
		var missingImage *dockerdeploy.CurrentBuildImageMissingErrorV1
		if errors.As(err, &missingImage) {
			fmt.Fprintln(
				stderr,
				"the current environment image may still run, but complete build lineage cannot be verified",
			)
			fmt.Fprintln(
				stderr,
				"next: run `reploy build --verify`; it will rebuild instead of reusing this incomplete cache",
			)
		}
		return 1
	}
	fmt.Fprintf(stdout, "verified current build: %s\n", result.Environment)
	fmt.Fprintf(stdout, "image: %s\n", result.Reference)
	fmt.Fprintf(stdout, "provider-store objects: %d\n", result.Details.StoreObjects)
	fmt.Fprintf(stdout, "images: %d\n", result.Details.Images)
	fmt.Fprintf(stdout, "commands: %d\n", result.Details.Commands)
	return 0
}

func runDockerInstall(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerInstallOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	presenter := newOperationPresenter(operationPresenterOptions{
		Name: "installing deployment", ProgressOutput: stderr, ResultOutput: stdout, Verbose: options.Verbose,
	})
	for _, warning := range options.Warnings {
		presenter.Warn(warning)
	}
	presenter.Step("preparing installation")
	childOutput := presenter.ChildOutput()
	if options.Verbose {
		childOutput = io.MultiWriter(childOutput, stderr)
	}
	var result dockerdeploy.ProviderInstallResultV1
	if options.Pack.Raw != "" {
		result, err = dockerDirectInstall(dockerdeploy.DirectInstallOptions{
			Pack:                   options.Pack,
			Target:                 options.Target,
			ControlMode:            options.ControlMode,
			Scope:                  options.Scope,
			Service:                options.Service,
			PortOverrides:          options.PortOverrides,
			Replace:                options.Replace,
			Clean:                  options.Clean,
			Start:                  options.Start,
			Stdout:                 childOutput,
			Progress:               presenter.Progress(),
			DockerPreflightTimeout: globalOptions.DockerTimeout,
		})
	} else {
		options.Dir, err = resolveImplicitStagingDeploymentDir(options.Dir, options.DirExplicit, stderr)
		if err == nil {
			result, err = dockerInstall(dockerdeploy.InstallOptions{
				Dir:                    options.Dir,
				Target:                 options.Target,
				ControlMode:            options.ControlMode,
				Scope:                  options.Scope,
				Service:                options.Service,
				PortOverrides:          options.PortOverrides,
				Replace:                options.Replace,
				Clean:                  options.Clean,
				Start:                  options.Start,
				Stdout:                 childOutput,
				Progress:               presenter.Progress(),
				DockerPreflightTimeout: globalOptions.DockerTimeout,
			})
		}
	}
	if err != nil {
		_ = presenter.Failure("reploy install error: " + installFailureDiagnostic(err, presenter.CapturedChildOutput()))
		return 1
	}
	for _, warning := range result.Warnings {
		presenter.Warn(warning)
	}
	successLines, successErr := dockerInstallSuccessLines(result.TargetDir, globalOptions.DockerTimeout)
	if successErr != nil {
		presenter.Warn("could not render blueprint completion details: " + successErr.Error())
	}
	_ = presenter.Success(func(output io.Writer) { writeInstallResult(output, result, successLines) })
	return 0
}

func runDockerUninstall(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerUninstallOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	if dockerUninstallNeedsRoot(dockerdeploy.UninstallOptions{
		From:        options.From,
		ServiceName: options.ServiceName,
		RemoveDir:   options.RemoveDir,
		ControlMode: options.ControlMode,
	}) && os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "reploy uninstall error: root privileges are required to stop systemd services and remove Docker resources")
		fmt.Fprintln(stderr, "rerun with sudo")
		return 1
	}
	uninstallDir := options.From
	if strings.TrimSpace(uninstallDir) == "" {
		uninstallDir = "."
	}
	presenter := newOperationPresenter(operationPresenterOptions{
		Name: "uninstalling deployment", ProgressOutput: stderr, ResultOutput: stdout, Verbose: options.Verbose,
	})
	presenter.Step("preparing uninstall")
	childOutput := presenter.ChildOutput()
	if options.Verbose {
		childOutput = io.MultiWriter(childOutput, stderr)
	}
	result, err := dockerUninstall(dockerdeploy.UninstallOptions{
		From:                   options.From,
		ServiceName:            options.ServiceName,
		RemoveDir:              options.RemoveDir,
		ControlMode:            options.ControlMode,
		Stdout:                 childOutput,
		Progress:               presenter.Progress(),
		DockerPreflightTimeout: globalOptions.DockerTimeout,
	})
	if err != nil {
		_ = presenter.Failure("reploy uninstall error: " + installFailureDiagnostic(err, presenter.CapturedChildOutput()))
		return 1
	}
	_ = presenter.Success(func(output io.Writer) { writeUninstallResult(output, result) })
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
	Start         bool
	Verbose       bool
	ControlMode   dockerdeploy.ControlAdmissionModeV1
}

type dockerUninstallOptions struct {
	From        string
	ServiceName string
	RemoveDir   bool
	Verbose     bool
	ControlMode dockerdeploy.ControlAdmissionModeV1
}

func parseDockerInstallOptions(args []string) (dockerInstallOptions, error) {
	options := dockerInstallOptions{Dir: dockerdeploy.DefaultDeploymentDir, Start: true}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--clean":
			options.Clean = true
		case "--verbose":
			options.Verbose = true
		case "--start":
			options.Start = true
		case "--no-start":
			options.Start = false
		case "--wait":
			if err := setInstallControlAdmissionMode(&options, dockerdeploy.ControlAdmissionWaitV1); err != nil {
				return dockerInstallOptions{}, err
			}
		case "--drain":
			if err := setInstallControlAdmissionMode(&options, dockerdeploy.ControlAdmissionDrainV1); err != nil {
				return dockerInstallOptions{}, err
			}
		case "--force":
			if err := setInstallControlAdmissionMode(&options, dockerdeploy.ControlAdmissionForceV1); err != nil {
				return dockerInstallOptions{}, err
			}
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
	return options, nil
}

func setInstallControlAdmissionMode(options *dockerInstallOptions, mode dockerdeploy.ControlAdmissionModeV1) error {
	if options.ControlMode != "" && options.ControlMode != mode {
		return fmt.Errorf("--wait, --drain, and --force are mutually exclusive")
	}
	options.ControlMode = mode
	return nil
}

func parseDockerUninstallOptions(args []string) (dockerUninstallOptions, error) {
	var options dockerUninstallOptions
	fromSet := false
	serviceNameSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--remove-dir":
			options.RemoveDir = true
		case "--verbose":
			options.Verbose = true
		case "--wait":
			if err := setUninstallControlAdmissionMode(&options, dockerdeploy.ControlAdmissionWaitV1); err != nil {
				return dockerUninstallOptions{}, err
			}
		case "--drain":
			if err := setUninstallControlAdmissionMode(&options, dockerdeploy.ControlAdmissionDrainV1); err != nil {
				return dockerUninstallOptions{}, err
			}
		case "--force":
			if err := setUninstallControlAdmissionMode(&options, dockerdeploy.ControlAdmissionForceV1); err != nil {
				return dockerUninstallOptions{}, err
			}
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

func setUninstallControlAdmissionMode(options *dockerUninstallOptions, mode dockerdeploy.ControlAdmissionModeV1) error {
	if options.ControlMode != "" && options.ControlMode != mode {
		return fmt.Errorf("--wait, --drain, and --force are mutually exclusive")
	}
	options.ControlMode = mode
	return nil
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
	if options.Preinstall && options.Scope == "" {
		return dockerDoctorOptions{}, fmt.Errorf("--preinstall requires --scope user|system")
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
	if options.Timestamps && action != "logs" {
		fmt.Fprintln(stderr, "reploy usage error: --timestamps is only supported with logs")
		printDockerShortUsage(stderr)
		return 2
	}
	if options.ControlMode != "" && !runtimeActionUsesControlAdmission(action) {
		fmt.Fprintf(stderr, "reploy usage error: %s is only supported with up, down, or restart (start and stop are aliases)\n", controlAdmissionModeFlag(options.ControlMode))
		printDockerShortUsage(stderr)
		return 2
	}
	if (runtimeActionStops(action) || action == "restart") && (options.ControlMode == dockerdeploy.ControlAdmissionDrainV1 || options.ControlMode == dockerdeploy.ControlAdmissionForceV1) {
		flag := controlAdmissionModeFlag(options.ControlMode)
		fmt.Fprintf(stderr, "reploy usage error: %s is not supported with %s; %s already stops outstanding jobs by default, or use --wait to let active jobs finish\n", flag, action, action)
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
	runtimeStderr := stderr
	if runtimeActionShowsSpinner(action, options.Verbose) {
		label, err := runtimeSpinnerLabel(options.Dir, action, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy %s error: %v\n", action, err)
			return 1
		}
		var logOutput io.Writer
		stopSpinner, progress, logOutput = startTimedProgressSpinnerWithLogs(stderr, label)
		runtimeStderr = logOutput
	}
	runtimeAction := canonicalRuntimeAction(action)
	runtimeStdout := stdout
	var timestampWriters []*runtimeTimestampWriter
	if action == "logs" && options.Timestamps {
		stdoutTimestamps := newRuntimeTimestampWriter(stdout, runtimeLogColorEnabled(stdout, nil))
		stderrTimestamps := newRuntimeTimestampWriter(runtimeStderr, runtimeLogColorEnabled(runtimeStderr, nil))
		timestampWriters = append(timestampWriters, stdoutTimestamps, stderrTimestamps)
		runtimeStdout = stdoutTimestamps
		runtimeStderr = stderrTimestamps
	}
	runtimeErr := dockerRuntime(dockerdeploy.RuntimeOptions{
		Dir:                    options.Dir,
		Action:                 runtimeAction,
		ControlMode:            options.ControlMode,
		Follow:                 options.Follow,
		Tail:                   options.Tail,
		Timestamps:             options.Timestamps,
		Verbose:                options.Verbose,
		Stdout:                 runtimeStdout,
		Stderr:                 runtimeStderr,
		Progress:               progress,
		DockerPreflightTimeout: globalOptions.DockerTimeout,
	})
	for _, writer := range timestampWriters {
		runtimeErr = errors.Join(runtimeErr, writer.Flush())
	}
	if runtimeErr != nil {
		stopSpinner(false)
		fmt.Fprintf(errorStderr, "reploy %s error: %v\n", action, runtimeErr)
		return 1
	}
	stopSpinner(true)
	return 0
}

func runtimeActionShowsSpinner(action string, verbose bool) bool {
	if verbose {
		return false
	}
	return action == "up" || action == "start" || action == "restart" || runtimeActionStops(action)
}

func runtimeActionUsesControlAdmission(action string) bool {
	return action == "up" || action == "start" || runtimeActionStops(action) || action == "restart"
}

func runtimeActionStops(action string) bool {
	return action == "down" || action == "stop"
}

func canonicalRuntimeAction(action string) string {
	switch action {
	case "start":
		return "up"
	case "down", "stop":
		return "down"
	case "ps":
		return "status"
	default:
		return action
	}
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
	Timestamps  bool
	Verbose     bool
	Wait        bool
	ReadOnly    bool
	ControlMode dockerdeploy.ControlAdmissionModeV1
}

func parseDockerRuntimeOptions(args []string) (dockerRuntimeOptions, error) {
	return parseDockerRuntimeOptionsV1(args, true, false, false)
}

func parseDockerShellOptions(args []string) (dockerRuntimeOptions, error) {
	return parseDockerRuntimeOptionsV1(args, false, true, true)
}

func parseDockerObservationOptions(args []string) (dockerRuntimeOptions, error) {
	return parseDockerRuntimeOptionsV1(args, false, false, false)
}

func parseDockerRuntimeOptionsV1(args []string, allowControl bool, allowLiveWait bool, allowReadOnly bool) (dockerRuntimeOptions, error) {
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
		case "--timestamps":
			options.Timestamps = true
		case "--wait":
			if allowControl {
				if err := setControlAdmissionMode(&options, dockerdeploy.ControlAdmissionWaitV1); err != nil {
					return dockerRuntimeOptions{}, err
				}
				continue
			}
			if !allowLiveWait {
				return dockerRuntimeOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Wait = true
		case "--read-only":
			if !allowReadOnly {
				return dockerRuntimeOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.ReadOnly = true
		case "--drain":
			if !allowControl {
				return dockerRuntimeOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			if err := setControlAdmissionMode(&options, dockerdeploy.ControlAdmissionDrainV1); err != nil {
				return dockerRuntimeOptions{}, err
			}
		case "--force":
			if !allowControl {
				return dockerRuntimeOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			if err := setControlAdmissionMode(&options, dockerdeploy.ControlAdmissionForceV1); err != nil {
				return dockerRuntimeOptions{}, err
			}
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

func setControlAdmissionMode(options *dockerRuntimeOptions, mode dockerdeploy.ControlAdmissionModeV1) error {
	if options.ControlMode != "" && options.ControlMode != mode {
		return fmt.Errorf("--wait, --drain, and --force are mutually exclusive")
	}
	options.ControlMode = mode
	return nil
}

func controlAdmissionModeFlag(mode dockerdeploy.ControlAdmissionModeV1) string {
	return "--" + string(mode)
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
	Dir         string
	DirExplicit bool
	Pack        deploy.PackRef
	Warnings    []string
	Update      bool
	Verbose     bool
	Platform    string
	Force       bool
	Remove      bool
}

type dockerCommandParseConfig struct {
	AllowUpdate   bool
	AllowVerbose  bool
	AllowPlatform bool
	AllowForce    bool
	AllowRemove   bool
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
		case "--force":
			if !config.AllowForce {
				return dockerCommandOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Force = true
		case "--remove":
			if !config.AllowRemove {
				return dockerCommandOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Remove = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--platform":
			if !config.AllowPlatform {
				return dockerCommandOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			if strings.TrimSpace(value) == "" {
				return dockerCommandOptions{}, fmt.Errorf("--platform must not be empty")
			}
			options.Platform = value
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--platform=") {
				if !config.AllowPlatform {
					return dockerCommandOptions{}, fmt.Errorf("unknown option: --platform")
				}
				options.Platform = strings.TrimPrefix(arg, "--platform=")
				if strings.TrimSpace(options.Platform) == "" {
					return dockerCommandOptions{}, fmt.Errorf("--platform must not be empty")
				}
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
	if options.Remove && options.Update {
		return dockerCommandOptions{}, fmt.Errorf("--remove cannot be combined with --update")
	}
	if options.Remove && options.Pack.Raw != "" {
		return dockerCommandOptions{}, fmt.Errorf("APP_REF cannot be combined with --remove")
	}
	if options.Remove && options.Platform != "" {
		return dockerCommandOptions{}, fmt.Errorf("--platform cannot be combined with --remove")
	}
	if options.Force && !options.Update && !options.Remove {
		return dockerCommandOptions{}, fmt.Errorf("--force requires --update or --remove")
	}
	if requirePack && !options.Update && !options.Remove && options.Pack.Raw == "" {
		return dockerCommandOptions{}, fmt.Errorf("APP_REF is required; %s", appRefUsageHint)
	}
	return options, nil
}

func packDisplayName(ref deploy.PackRef) string {
	if ref.Scheme == "file" {
		if loaded, err := deploy.LoadBlueprint(ref); err == nil && strings.TrimSpace(loaded.Document.Environment.ID) != "" {
			return loaded.Document.Environment.ID
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
	} else if strings.HasPrefix(original, "file://") {
		expanded = "file:" + strings.TrimPrefix(original, "file://")
	} else if !hasPackRefScheme(original) {
		if looksLikeLocalBlueprintPath(original) {
			return deploy.PackRef{}, "", likelyLocalBlueprintRefError{Value: original}
		}
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

type likelyLocalBlueprintRefError struct{ Value string }

func (problem likelyLocalBlueprintRefError) Error() string {
	explicit := problem.Value
	if !strings.HasPrefix(explicit, ".") {
		explicit = "./" + explicit
	}
	return fmt.Sprintf(
		"%q looks like a local blueprint path, not a blueprint index shorthand; use %s or %s",
		problem.Value,
		shellQuoteArg(explicit),
		shellQuoteArg("file://"+strings.TrimPrefix(problem.Value, "./")),
	)
}

func looksLikeLocalBlueprintPath(value string) bool {
	body, _, _ := strings.Cut(value, "?")
	name, _, _ := strings.Cut(body, "==")
	return strings.ContainsAny(name, `/\`) || strings.HasSuffix(strings.ToLower(name), ".blueprint.yaml")
}

func validationStatusText(output io.Writer, status string, colorCode string) string {
	if !cliStatusColorEnabled(output) {
		return status
	}
	return "\x1b[" + colorCode + "m" + status + "\x1b[0m"
}

func cliStatusColorEnabled(output io.Writer) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REPLOY_COLOR"))) {
	case "always":
		return true
	case "never":
		return false
	}
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" || strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	return operationOutputIsInteractive(output)
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
	cachePath := packIndexCachePath(indexURL)
	cached, cacheErr := readPackIndexPath(cachePath)
	if cacheErr == nil {
		index, parseErr := parsePackIndex(cached)
		if parseErr == nil {
			return index, nil
		}
		return packIndex{}, fmt.Errorf(
			"refresh blueprint index: %v; cached blueprint index %s is invalid: %w",
			refreshErr,
			cachePath,
			parseErr,
		)
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
	content, err := readPackIndexPath(path)
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
	return startProgressSpinnerWithLogsOptions(output, label, false)
}

func startTimedProgressSpinnerWithLogs(output io.Writer, label string) (func(bool), io.Writer, io.Writer) {
	return startProgressSpinnerWithLogsOptions(output, label, true)
}

var spinnerOutputIsInteractive = operationOutputIsInteractive

func startProgressSpinnerWithLogsOptions(
	output io.Writer,
	label string,
	showElapsed bool,
) (func(bool), io.Writer, io.Writer) {
	if output == nil {
		return func(bool) {}, io.Discard, io.Discard
	}
	started := time.Now()
	completionSuffix := func(ok bool) string {
		suffix := "... failed"
		if ok {
			suffix = "... done"
		}
		if showElapsed {
			suffix += " [" + formatOperationElapsed(time.Since(started)) + "]"
		}
		return suffix
	}
	if !terminalAnimationsEnabled(output) {
		fmt.Fprintf(output, "%s...\n", label)
		progress := progressWriter{write: func(message string) {
			fmt.Fprintf(output, "%s: %s\n", label, message)
		}}
		return func(ok bool) {
			fmt.Fprintf(output, "%s%s\n", label, completionSuffix(ok))
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
				suffix := completionSuffix(ok)
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

func formatOperationElapsed(elapsed time.Duration) string {
	if elapsed < time.Second {
		return elapsed.Round(10 * time.Millisecond).String()
	}
	return elapsed.Round(time.Second).String()
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

func terminalAnimationsEnabled(output io.Writer) bool {
	if !spinnerOutputIsInteractive(output) {
		return false
	}
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

func printBlueprintValidateShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy validate BLUEPRINT_REF")
	fmt.Fprintln(output, "Run 'reploy validate --help' for validation help.")
}

func printBlueprintValidateHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy validate BLUEPRINT_REF

Validate blueprint syntax and semantics. This command does not create staging
state, contact Docker, resolve provider packages, or build an image. Remote
blueprint references may be downloaded into Reploy's source cache.

BLUEPRINT_REF may be an indexed shorthand, a local path, a PyPI blueprint URL,
or a GitHub blueprint URL.

Options:
  -h, --help   Show validation help
`, "\n"))
}

func printHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] COMMAND

Commands:
  validate     Validate blueprint syntax and semantics
  stage        Create a staging directory
  overrides    Edit staged development overrides
  build        Build and validate the staged environment image
  verify       Audit the current staged build without changing it
  info         Show staging state and bundle contents
  app          Run a staged app command from the current build
  shell        Open /bin/sh in a transient staging container
  runs         List or stop outstanding app commands and shell sessions
  bundle       Manage staging bundle contents
  up           Build if needed, then start the staging Compose service
  down         Stop and remove the staging Compose service
  restart      Build if needed, then recreate the staging Compose service
  start        Alias for up
  stop         Alias for down
  ps           Show staging environment status
  status       Show staging environment status
  logs         Show staging application logs
  test         Probe the staging app health endpoint
  doctor       Check deployment state, runtime-file drift, and install readiness
  install      Install or update a deployed host service
  uninstall    Remove an installed host service and Docker resources
  services     List Reploy-managed services
  controlled-session
               Run controller-side controlled-session integration commands
  index        Manage the cached blueprint shorthand index
  version      Print version information

Staged up and restart may build with Docker and download packages.

Bundle:
  options      List component-qualified blueprint options
  list         List the current request overlay
  add          Select component-qualified blueprint options
  remove       Deselect component-qualified blueprint options
  add-package  Add direct package requests to a component
  remove-package
               Remove direct package requests from a component
  clean        Remove the deployment-local provider cache

Runtime options:
  --docker-timeout DURATION
              Docker daemon responsiveness timeout, default 5s
  --aws        Reserved for a future AWS deployment target

Blueprint refs:
  APP_REF     App blueprint reference for stage.
              Indexed shorthand: arbiter-server or arbiter-server==VERSION.
              Local development refs such as ./PATH, /ABS/PATH, or file:PATH are also accepted.
              Python provider refs use pypi://PACKAGE/PATH/APP.blueprint.yaml.
              Git provider refs use github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF.

Options:
  -h, --help   Show help
  --version    Print version information

Run 'reploy COMMAND --help' for command-specific options.
`, "\n"))
}

func printControlledSessionShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy controlled-session {client | attach --socket PATH}")
}

func printControlledSessionHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy controlled-session COMMAND

Commands:
  client                 Run the structured controller session broker
  attach --socket PATH   Attach terminal bytes to a running broker

Run 'reploy controlled-session COMMAND --help' for command-specific help.
`, "\n"))
}

func printControlledSessionClientShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy controlled-session client")
}

func printControlledSessionClientHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy controlled-session client

Run the controller-side controlled-session broker. The command consumes the
controller-private REPLOY_SESSION_SOCKET environment variable and exchanges
versioned JSON Lines with the controller orchestrator on stdin and stdout.
Human-readable diagnostics are written only to stderr.
`, "\n"))
}

func printControlledSessionAttachShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy controlled-session attach --socket PATH")
}

func printControlledSessionAttachHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy controlled-session attach --socket PATH

Attach stdin, stdout, and terminal resize events to the private terminal socket
reported by 'reploy controlled-session client'. Terminal bytes are forwarded
unchanged; human-readable diagnostics are written only to stderr.
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
  --dir DIR                  Deployment directory, default current deployment or reploy-staging
  --verbose                  Show bundle clean results
  -h, --help                 Show bundle help
`, "\n"))
}

func bundleCommandSummary() string {
	return strings.TrimLeft(`
Commands:
  options      List component-qualified blueprint options
  list         List the current request overlay
  add          Select component-qualified blueprint options
  remove       Deselect component-qualified blueprint options
  add-package  Add direct package requests to a component
  remove-package
               Remove direct package requests from a component
  clean        Remove the deployment-local provider cache
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

Run a blueprint-declared app command. By default this operates on staging. App
commands use the application in the current staged build, not a host executable
from PATH. If that build is missing or stale, the command fails and tells you
to run reploy build; app commands do not resolve packages or rebuild it.

Installed control scripts use --deployed-only to require an installed
deployment and select commands explicitly published for deployed use.

`, "\n"))
	fmt.Fprint(output, appCommandSummary())
	fmt.Fprint(output, strings.TrimLeft(`

Options:
  --dir DIR          Staging or installed deployment directory
  --deployed-only    Require an installed deployment and expose only deployed commands
  --output-dir DIR   Mount a persistent result directory at REPLOY_OUTPUT_DIR
  --output-file FILE Publish one complete result file from REPLOY_OUTPUT_FILE
  --wait             Queue behind conflicting app commands or shell sessions
  -h, --help         Show app command help
`, "\n"))
}

func printShellHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] shell [OPTIONS]

Open /bin/sh in a transient container for the current staging environment.

Options:
  --dir DIR     Staging directory, default current staging dir or reploy-staging
  --wait        Queue behind conflicting app commands or shell sessions
  --read-only   Mount shared environment paths read-only; temporary home stays writable
  -h, --help    Show shell help
`, "\n"))
}

func appCommandSummary() string {
	return strings.TrimLeft(`
Show this staging directory's app subcommands with:
  reploy app

Run an app subcommand with:
  reploy app COMMAND

For an installed deployment, add --deployed-only.
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
	fmt.Fprintln(output, "Usage: reploy [--docker-timeout DURATION] COMMAND")
	fmt.Fprintln(output, "Run 'reploy --help' for help.")
}

func printDockerHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] COMMAND

Commands:
  validate     Validate blueprint syntax and semantics
  stage        Create a staging directory
  overrides    Edit staged development overrides
  build        Build and validate the staged environment image
  verify       Audit the current staged build without changing it
  info         Show staging state and bundle contents
  app          Run a staged app command from the current build
  shell        Open /bin/sh in a transient staging container
  runs         List or stop outstanding app commands and shell sessions
  bundle       Manage staging bundle contents
  services     List Reploy-managed services
  up           Build if needed, then start the staging Compose service
  down         Stop and remove the staging Compose service
  restart      Build if needed, then recreate the staging Compose service
  start        Alias for up
  stop         Alias for down
  ps           Show staging environment status
  status       Show staging environment status
  logs         Show staging application logs
  test         Probe the staging app health endpoint
  doctor       Check deployment state, runtime-file drift, and install readiness
  install      Install or update a deployed host service
  uninstall    Remove an installed host service and Docker resources

Staged up and restart may build with Docker and download packages.

Bundle:
  options      List component-qualified blueprint options
  list         List the current request overlay
  add          Select component-qualified blueprint options
  remove       Deselect component-qualified blueprint options
  add-package  Add direct package requests to a component
  remove-package
               Remove direct package requests from a component
  clean        Remove the deployment-local provider cache

Blueprint refs:
  APP_REF     App blueprint reference for stage.
              Indexed shorthand: arbiter-server or arbiter-server==VERSION.
              Local development refs such as ./PATH, /ABS/PATH, or file:PATH are also accepted.
              Python provider refs use pypi://PACKAGE/PATH/APP.blueprint.yaml.
              Git provider refs use github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF.

Options:
  --docker-timeout DURATION
              Docker daemon responsiveness timeout, default 5s
  -h, --help   Show help

Run 'reploy COMMAND --help' for command-specific options.
`, "\n"))
}

func printDockerCommandHelp(command string, output io.Writer) {
	switch command {
	case "stage":
		printDockerStageHelp(output)
	case "overrides":
		printPackageOverridesHelp(output)
	case "build":
		printDockerBuildHelp(output)
	case "verify":
		printDockerVerifyHelp(output)
	case "install":
		printDockerInstallHelp(output)
	case "uninstall":
		printDockerUninstallHelp(output)
	case "logs":
		printDockerLogsHelp(output)
	case "up", "start", "down", "stop", "restart":
		printDockerLifecycleHelp(command, output)
	case "shell":
		printShellHelp(output)
	case "runs":
		printRunsHelp(output)
	case "doctor":
		printDockerDoctorHelp(output)
	default:
		printDockerHelp(output)
	}
}

func printPackageOverridesHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy overrides [OPTIONS]

Open the interactive editor for a staged deployment's development overrides.
Existing overrides.yaml content is loaded automatically. Keep the base image
from the blueprint or enter an exact image name.
Press A and enter os:PACKAGE to add an exact native package through the base
image's supported OS provider, or enter a Python package name to override its source.
The workspace root is optional and unset by default. Configure it inside the
editor to store paths beneath it relative to that root; other paths are absolute.
Press V in the editor to opt into a trial build that validates and caches the
saved choices without changing the current staged build.

Options:
  --dir DIR    Staging directory, default current staging dir or reploy-staging
  -h, --help   Show development override editor help
`, "\n"))
}

func printDockerDoctorHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] doctor [OPTIONS]

Check deployment state and verify that current generated runtime files match
the recorded build. With --preinstall, also check the selected install scope's
Docker access, host tools, privileges, and system account readiness without
changing the host.

Options:
  --dir DIR          Staging directory, default current staging dir or reploy-staging
  --preinstall       Also check readiness for installation
  --scope user|system
                     Required with --preinstall
  --quiet            Suppress successful checks
  -h, --help         Show doctor help
`, "\n"))
}

func printDockerVerifyHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy verify [OPTIONS]

Audit the current staged build without changing it. Verification fully hashes
the reachable provider-store objects, re-inspects the base, provider layers,
and final image, reruns cumulative provider checks in temporary network-disabled
containers, validates final-image evidence, and proves that declared runtime
commands resolve against the locked output catalog.

This command does not resolve packages, build or repair images, publish records,
update deployment state, or execute application commands. Temporary verification
containers and workspaces are removed on every exit.

Options:
  --dir DIR    Staging directory, default current staging dir or reploy-staging
  -h, --help   Show verification help
`, "\n"))
}

func printDockerLifecycleHelp(command string, output io.Writer) {
	fmt.Fprintf(output, "Usage: reploy %s [OPTIONS]\n\n", command)
	if runtimeActionStops(command) || command == "restart" {
		fmt.Fprintln(output, "By default, the command stops active jobs and cancels queued jobs. When jobs")
		fmt.Fprintln(output, "are outstanding, Reploy logs a warning and waits three seconds so Ctrl-C can")
		fmt.Fprintln(output, "abort before anything is stopped or canceled.")
	} else {
		fmt.Fprintln(output, "Without a queue option, the command fails when another run is active or queued.")
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Options:")
	fmt.Fprintln(output, "  --dir DIR    Deployment directory")
	if runtimeActionStops(command) || command == "restart" {
		fmt.Fprintln(output, "  --wait       Let active jobs finish, cancel queued jobs, then continue")
	} else {
		fmt.Fprintln(output, "  --wait       Wait in FIFO order for active and earlier queued runs")
		fmt.Fprintln(output, "  --drain      Let active runs finish, cancel queued runs, then continue")
		fmt.Fprintln(output, "  --force      Stop active runs, cancel queued runs, then continue")
	}
	fmt.Fprintln(output, "  --verbose    Show detailed command output")
	fmt.Fprintln(output, "  -h, --help   Show command help")
}

func printDockerInstallHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] install [APP_REF] --scope user|system [OPTIONS]

Install or update a deployed host service. With APP_REF, Reploy resolves and
installs that blueprint directly. Without APP_REF, it installs the staging
deployment selected by --dir. Installation may build the environment image and
may require Docker and package-network access.

Options:
  --dir DIR          Existing staging directory, default current staging dir or reploy-staging
  --to DIR           Install target; defaults from the blueprint for direct install
  --scope user|system
                     Required install scope
  --service NAME     Installed service identity, default environment id
  --port PORT        Host port override for a single-endpoint blueprint
  --port NAME=PORT   Host port override for a named endpoint; repeat as needed
  --replace PATH     Replace a preserved managed path or .env; use all for every preserved path
  --clean            Replace all managed paths and .env
  --start            Start after install, default
  --no-start         Install without starting the service
  --wait             Wait in FIFO order for active and earlier queued runs
  --drain            Let active runs finish, cancel queued runs, then install
  --force            Stop active runs, cancel queued runs, then install
  --verbose          Show backend output in addition to Reploy progress
  -h, --help         Show install help
`, "\n"))
}

func printDockerUninstallHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] uninstall [OPTIONS]

Remove an installed service and its Docker resources. The installation
directory is retained unless --remove-dir is specified.

Options:
  --from DIR         Installed service directory, default current directory
  --service-name NAME
                     Require this service name; on Linux, recover a deleted
                     installed directory from its remaining Reploy systemd unit
  --remove-dir       Remove the installed directory after successful cleanup
  --wait             Wait in FIFO order for active and earlier queued runs
  --drain            Let active runs finish, cancel queued runs, then uninstall
  --force            Stop active runs, cancel queued runs, then uninstall
  --verbose          Show backend output in addition to Reploy progress
  -h, --help         Show uninstall help
`, "\n"))
}

func printDockerBuildHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] build [OPTIONS]

Build and validate the current staged environment image without installing it.
Every newly created component layer is validated against its cumulative provider
requirements, and the resulting image is fully validated before publication.
Interactive terminals print fast results directly. Longer builds show an inline
progress panel that exits automatically before printing the result. Dumb or
redirected terminals print durable progress lines and the result directly.

Options:
  --dir DIR          Staging directory, default current staging dir or reploy-staging
  --no-cache         Rerun resolvers and image construction instead of reusing the current build
  --verify           Fully verify a reusable current build; rebuild if verification fails
  --profile          Show hierarchical timings for build decisions and backend work
  --verbose          Show durable Reploy-level build steps without backend transcripts
  -h, --help         Show build help
`, "\n"))
}

func printDockerStageHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] stage APP_REF [OPTIONS]
       reploy [--docker-timeout DURATION] stage --update [APP_REF] [OPTIONS]
       reploy [--docker-timeout DURATION] stage --remove [OPTIONS]

Create a staging directory from an app blueprint reference.
Use --update to refresh an existing staging directory, optionally from a new ref.
Use --remove to stop and remove a staging deployment and its directory.
Stage records desired state and generates the app-named control command without building.
Build explicitly or let staged up/restart build on demand.
A new stage from a local blueprint imports overrides.yaml beside that blueprint.

APP_REF:
  Indexed shorthand from the Reploy blueprint index:
    arbiter-server
    arbiter-server==0.4.2

  Local filesystem refs:
    ./PATH
    /ABS/PATH
    file:PATH
    file://PATH

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
  --dir DIR    Staging directory to create, update, or remove;
               default current staging directory or reploy-staging
  --update     Update an existing staging directory instead of creating one
  --remove     Remove an existing staging deployment and its directory
  --platform OCI
              Select an environment blueprint target, for example linux/amd64
  --force      Replace a staging directory that belongs to another blueprint,
               recover incompatible state, or stop active work during removal
  --verbose    Show additional staging details
  -h, --help   Show stage help
`, "\n"))
}

func printDockerLogsHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy logs [OPTIONS]

Show staging application logs.

Options:
  --dir DIR    Staging directory, default current staging dir or reploy-staging
  --tail N     Show only the last N log lines
  --follow, -f
              Follow logs instead of exiting after current output
  --timestamps
              Include the runtime capture timestamp
  -h, --help   Show logs help
`, "\n"))
}
