package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/dockerdeploy"
	"github.com/omry/reploy/internal/overrideui"
)

var dockerBuildWarningPattern = regexp.MustCompile(`^-\s*([A-Za-z][A-Za-z0-9]+):\s*(.+)$`)
var dockerExitStatusPattern = regexp.MustCompile(`(?i)(?::\s*)?docker failed: exit status [0-9]+`)

var buildOverrideUIEnabled = func(input io.Reader, output io.Writer) bool {
	inputFile, inputOK := input.(*os.File)
	return buildOverrideUIAllowed(
		inputOK && term.IsTerminal(inputFile.Fd()),
		operationOutputIsInteractive(output),
	)
}

func buildOverrideUIAllowed(inputTerminal bool, outputTerminal bool) bool {
	return inputTerminal &&
		outputTerminal &&
		!envBool("CI") &&
		!strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb")
}

func runDockerBuild(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerBuildOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy build usage error: %v\n", err)
		printDockerBuildHelp(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, io.Discard)
	started := time.Now()
	if buildOverrideUIEnabled(os.Stdin, stderr) {
		config, configErr := packageOverrideEditorConfig(
			options.Dir, globalOptions, options.NoCache, options.ValidateLayers,
		)
		if configErr != nil {
			fmt.Fprintf(stderr, "reploy build error: %v\n", configErr)
			return 1
		}
		config.Input = os.Stdin
		config.Output = stderr
		config.AutoValidate = true
		config.BuildMode = true
		config.Validate = buildModeValidationRunner(
			config.Validate, options.Dir, globalOptions, started,
		)
		editorResult, editorErr := runOverrideEditor(config)
		if editorErr != nil {
			fmt.Fprintf(stderr, "reploy build error: package validation UI: %v\n", editorErr)
			return 1
		}
		if !editorResult.Validated {
			if editorResult.Canceled {
				fmt.Fprintln(stderr, "reploy build canceled")
				return 130
			}
			fmt.Fprintln(stderr, "reploy build stopped before package choices were validated")
			return 1
		}
		return 0
	}
	presenter := newOperationPresenter(operationPresenterOptions{
		Name: "building environment", ProgressOutput: stderr, ResultOutput: stdout, Verbose: options.Verbose,
	})
	presenter.Step("preparing staged environment")

	runtimeInput, err := dockerProviderBuildRuntime()
	if err != nil {
		_ = presenter.Failure("reploy build error: " + buildFailureDiagnostic(err, ""))
		return 1
	}
	result, err := dockerProviderBuild(context.Background(), dockerdeploy.ProviderBuildRunInputV1{
		DeploymentDir:  options.Dir,
		Runtime:        runtimeInput,
		NoCache:        options.NoCache,
		ValidateLayers: options.ValidateLayers,
		Progress:       presenter.Progress(),
		RunOptions: dockerdeploy.RunOptions{
			Stdout: presenter.ChildOutput(), Stderr: presenter.ChildOutput(),
			DockerPreflightTimeout: globalOptions.DockerTimeout,
		},
	})
	childOutput := presenter.CapturedChildOutput()
	for _, warning := range buildWarnings(childOutput, err == nil) {
		presenter.Warn(warning)
	}
	if err != nil {
		_ = presenter.Failure("reploy build error: " + buildFailureDiagnostic(err, childOutput))
		return 1
	}
	summary, err := summarizeProviderBuild(result)
	if err != nil {
		_ = presenter.Failure("reploy build error: summarize completed build: " + err.Error())
		return 1
	}
	elapsed := time.Since(started)
	_ = presenter.Success(func(output io.Writer) {
		fmt.Fprintf(output, "image: %s\n", summary.Image)
		if elapsed >= time.Second {
			fmt.Fprintf(output, "elapsed: %s\n", elapsed.Round(100*time.Millisecond))
		}
		if result.Reused {
			fmt.Fprintf(output, "environment already current: %s\n", summary.Environment)
			return
		}
		fmt.Fprintf(output, "built environment: %s\n", summary.Environment)
	})
	return 0
}

func buildModeValidationRunner(
	validate overrideui.ValidationRunner,
	deploymentDir string,
	globalOptions globalDeploymentOptions,
	started time.Time,
) overrideui.ValidationRunner {
	return func(ctx context.Context, progress io.Writer) (overrideui.ValidationResult, error) {
		result, err := validate(ctx, progress)
		if err != nil {
			return overrideui.ValidationResult{}, err
		}
		runtimeInput, err := dockerProviderBuildRuntime()
		if err != nil {
			return overrideui.ValidationResult{}, err
		}
		var childOutput synchronizedBuffer
		build, err := dockerProviderBuild(ctx, dockerdeploy.ProviderBuildRunInputV1{
			DeploymentDir: deploymentDir,
			Runtime:       runtimeInput,
			Progress:      progress,
			RunOptions: dockerdeploy.RunOptions{
				Stdout: &childOutput, Stderr: &childOutput,
				DockerPreflightTimeout: globalOptions.DockerTimeout,
			},
		})
		if err != nil {
			return overrideui.ValidationResult{}, fmt.Errorf(
				"publish validated build: %s",
				buildFailureDiagnostic(err, childOutput.String()),
			)
		}
		summary, err := summarizeProviderBuild(build)
		if err != nil {
			return overrideui.ValidationResult{}, fmt.Errorf("summarize completed build: %w", err)
		}
		result.Build = &overrideui.BuildOutcome{
			Environment: summary.Environment,
			Image:       summary.Image,
			Elapsed:     time.Since(started),
			Reused:      build.Reused,
		}
		return result, nil
	}
}

type providerBuildSummary struct {
	Environment string
	Image       string
}

func summarizeProviderBuild(result dockerdeploy.LockedProviderBuildExecutionResultV1) (providerBuildSummary, error) {
	document, err := blueprint.DecodeResolvedDocumentV1(result.State.Blueprint)
	if err != nil {
		return providerBuildSummary{}, err
	}
	if result.State.Current == nil {
		return providerBuildSummary{}, fmt.Errorf("completed build has no current generation")
	}
	image := strings.TrimSpace(string(result.State.Current.ImageDigest))
	if image == "" {
		image = strings.TrimSpace(string(result.Lock.FinalImage.Digest))
	}
	if image == "" {
		return providerBuildSummary{}, fmt.Errorf("completed build has no image identity")
	}
	return providerBuildSummary{Environment: document.Environment.ID, Image: image}, nil
}

func buildWarnings(output string, successful bool) []string {
	warnings := []string{}
	seen := map[string]struct{}{}
	for _, rawLine := range strings.Split(stripBuildTerminalControls(output), "\n") {
		line := strings.TrimSpace(rawLine)
		match := dockerBuildWarningPattern.FindStringSubmatch(line)
		if len(match) == 3 {
			if match[1] == "SecretsUsedInArgOrEnv" {
				continue
			}
			addBuildWarning(&warnings, seen, translateDockerBuildWarning(match[1], match[2]))
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "warning:") {
			message := strings.TrimSpace(line[len("warning:"):])
			if message != "" && !strings.Contains(strings.ToLower(message), "use docker --debug") {
				addBuildWarning(&warnings, seen, "Build backend: "+message)
			}
			continue
		}
		if strings.HasPrefix(line, "W:") {
			message := strings.TrimSpace(strings.TrimPrefix(line, "W:"))
			if message != "" && !(successful && expectedSuccessfulAPTResolverWarning(message)) {
				addBuildWarning(&warnings, seen, "APT: "+message)
			}
		}
	}
	return warnings
}

func expectedSuccessfulAPTResolverWarning(message string) bool {
	switch message {
	case "Unable to read /etc/apt/apt.conf.d/ - DirectoryExists (2: No such file or directory)",
		"Could not open lock file /var/lib/dpkg/lock-frontend - open (13: Permission denied)",
		"Could not open lock file /var/lib/dpkg/lock - open (13: Permission denied)":
		return true
	}
	return strings.HasPrefix(
		message,
		"Download is performed unsandboxed as root as file '/tmp/reploy-apt-resolve/",
	) && strings.HasSuffix(
		message,
		"couldn't be accessed by user '_apt'. - pkgAcquire::Run (13: Permission denied)",
	)
}

func addBuildWarning(warnings *[]string, seen map[string]struct{}, warning string) {
	warning = strings.TrimSpace(warning)
	if warning == "" {
		return
	}
	if _, found := seen[warning]; found {
		return
	}
	seen[warning] = struct{}{}
	*warnings = append(*warnings, warning)
}

func translateDockerBuildWarning(code string, message string) string {
	switch code {
	case "InvalidDefaultArgInFrom":
		return "Docker check InvalidDefaultArgInFrom: Reploy generated an invalid base-image default; this is an internal Reploy warning"
	default:
		message = strings.TrimSpace(message)
		if index := strings.Index(strings.ToLower(message), "(use docker "); index >= 0 {
			message = strings.TrimSpace(message[:index])
		}
		return fmt.Sprintf("Docker check %s: %s", code, message)
	}
}

func buildFailureDiagnostic(err error, childOutput string) string {
	if diagnostic := buildOutputFailureDiagnostic(childOutput); diagnostic != "" {
		return diagnostic
	}
	if diagnostic := buildOutputFailureDiagnostic(err.Error()); diagnostic != "" {
		return diagnostic
	}
	message := strings.TrimSpace(stripBuildTerminalControls(err.Error()))
	if diagnostic := buildCommandFailureDiagnostic(message); diagnostic != "" {
		return diagnostic
	}
	if strings.Contains(strings.ToLower(message), "docker failed: exit status") {
		return "environment image construction failed; the build backend did not provide a usable diagnostic"
	}
	return message
}

func buildCommandFailureDiagnostic(message string) string {
	const marker = "\ncommand output:\n"
	index := strings.LastIndex(message, marker)
	if index < 0 {
		return ""
	}
	context := strings.TrimSpace(dockerExitStatusPattern.ReplaceAllString(message[:index], ""))
	output := strings.TrimSpace(message[index+len(marker):])
	if context == "" || output == "" {
		return ""
	}
	context = strings.TrimSuffix(context, ":")
	return context + "\ncommand output:\n" + output
}

func buildOutputFailureDiagnostic(output string) string {
	lines := strings.Split(stripBuildTerminalControls(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "error: failed to solve:") {
			detail := strings.TrimSpace(line[len("ERROR: failed to solve:"):])
			if detail != "" {
				return "environment image construction failed: " + detail
			}
		}
		switch {
		case strings.HasPrefix(line, "E:"):
			return "APT package operation failed: " + strings.TrimSpace(strings.TrimPrefix(line, "E:"))
		case strings.Contains(lower, "temporary failure resolving"),
			strings.Contains(lower, "could not resolve"),
			strings.Contains(lower, "network is unreachable"),
			strings.Contains(lower, "connection timed out"):
			return "network access failed: " + line
		case strings.Contains(lower, "failed to fetch"):
			return "package download failed: " + line
		case strings.Contains(lower, "no matching distribution found"),
			strings.Contains(lower, "could not find a version that satisfies"):
			return "Python package resolution failed: " + trimBuildErrorPrefix(line)
		case strings.Contains(lower, "signatures couldn't be verified"),
			strings.Contains(lower, "public key is not available"):
			return "APT repository trust check failed: " + line
		}
		if strings.HasPrefix(lower, "error:") {
			detail := trimBuildErrorPrefix(line)
			if detail != "" {
				return "environment build failed: " + detail
			}
		}
	}
	return ""
}

func trimBuildErrorPrefix(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(strings.ToLower(line), "error:") {
		return strings.TrimSpace(line[len("ERROR:"):])
	}
	return line
}

func stripBuildTerminalControls(value string) string {
	value = ansi.Strip(strings.ReplaceAll(value, "\r", "\n"))
	return strings.Map(func(char rune) rune {
		switch char {
		case '\n':
			return char
		case '\t':
			return ' '
		}
		if unicode.IsControl(char) || unicode.In(char, unicode.Cf) {
			return -1
		}
		return char
	}, value)
}
