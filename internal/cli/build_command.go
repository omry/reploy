package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprogress"
	"github.com/omry/reploy/internal/dockerdeploy"
	"github.com/omry/reploy/internal/overrideui"
)

var dockerBuildWarningPattern = regexp.MustCompile(`^-\s*([A-Za-z][A-Za-z0-9]+):\s*(.+)$`)
var dockerExitStatusPattern = regexp.MustCompile(`(?i)(?::\s*)?docker failed: exit status [0-9]+`)

var interactiveBuildDisplayDelay = 500 * time.Millisecond

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
		buildRunner := interactiveBuildRunner(
			options.Dir,
			globalOptions,
			started,
			options.NoCache,
			options.ValidateLayers,
		)
		session := startInteractiveBuildSession(buildRunner)
		defer session.cancel()
		timer := time.NewTimer(interactiveBuildDisplayDelay)
		select {
		case <-session.done:
			timer.Stop()
			result, buildErr := session.result()
			if buildErr != nil {
				fmt.Fprintln(stderr, "reploy build error: "+buildErr.Error())
				return 1
			}
			if result.Build == nil {
				fmt.Fprintln(stderr, "reploy build error: completed build did not provide an outcome")
				return 1
			}
			writeBuildOutcome(stdout, *result.Build)
			printWarnings(stderr, result.Warnings)
			return 0
		case <-timer.C:
		}
		progressResult, progressErr := runBuildProgress(overrideui.BuildProgressConfig{
			Context: context.Background(),
			Input:   os.Stdin,
			Output:  stderr,
			Run:     session.validationRunner(),
		})
		if progressErr != nil {
			fmt.Fprintf(stderr, "reploy build error: build progress UI: %v\n", progressErr)
			return 1
		}
		if progressResult.Canceled {
			fmt.Fprintln(stderr, "reploy build canceled")
			return 130
		}
		if progressResult.BuildError != nil {
			fmt.Fprintln(stderr, "reploy build error: "+progressResult.BuildError.Error())
			return 1
		}
		if progressResult.Validation.Build == nil {
			fmt.Fprintln(stderr, "reploy build error: completed build did not provide an outcome")
			return 1
		}
		writeBuildOutcome(stdout, *progressResult.Validation.Build)
		printWarnings(stderr, progressResult.Validation.Warnings)
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
		writeBuildOutcome(output, overrideui.BuildOutcome{
			Environment: summary.Environment, ImageReference: summary.ImageReference, Elapsed: elapsed,
			Reused: result.Reused, Republished: result.Republished,
		})
	})
	return 0
}

type interactiveBuildCompletion struct {
	result overrideui.ValidationResult
	err    error
}

type interactiveBuildProgress struct {
	mutex      sync.Mutex
	log        strings.Builder
	subscriber io.Writer
	event      buildprogress.Event
	eventFound bool
	reporter   buildprogress.Reporter
}

func (progress *interactiveBuildProgress) Write(content []byte) (int, error) {
	progress.mutex.Lock()
	defer progress.mutex.Unlock()
	_, _ = progress.log.Write(content)
	if progress.subscriber != nil {
		_, _ = progress.subscriber.Write(content)
	}
	return len(content), nil
}

func (progress *interactiveBuildProgress) report(event buildprogress.Event) {
	progress.mutex.Lock()
	defer progress.mutex.Unlock()
	if event.Environment == "" && progress.eventFound {
		event.Environment = progress.event.Environment
	}
	progress.event = event
	progress.eventFound = true
	if progress.reporter != nil {
		progress.reporter(event)
	}
}

func (progress *interactiveBuildProgress) attach(output io.Writer, reporter buildprogress.Reporter) func() {
	progress.mutex.Lock()
	if output != nil {
		lines := strings.Split(strings.TrimSpace(progress.log.String()), "\n")
		if len(lines) != 0 && lines[len(lines)-1] != "" {
			_, _ = fmt.Fprintln(output, lines[len(lines)-1])
		}
	}
	progress.subscriber = output
	progress.reporter = reporter
	if progress.eventFound && reporter != nil {
		reporter(progress.event)
	}
	progress.mutex.Unlock()
	return func() {
		progress.mutex.Lock()
		progress.subscriber = nil
		progress.reporter = nil
		progress.mutex.Unlock()
	}
}

type interactiveBuildSession struct {
	cancel     context.CancelFunc
	progress   interactiveBuildProgress
	done       chan struct{}
	mutex      sync.Mutex
	completion interactiveBuildCompletion
}

func startInteractiveBuildSession(runner overrideui.BuildRunner) *interactiveBuildSession {
	ctx, cancel := context.WithCancel(context.Background())
	session := &interactiveBuildSession{cancel: cancel, done: make(chan struct{})}
	go func() {
		result, err := runner(ctx, &session.progress, session.progress.report)
		session.mutex.Lock()
		session.completion = interactiveBuildCompletion{result: result, err: err}
		session.mutex.Unlock()
		close(session.done)
	}()
	return session
}

func (session *interactiveBuildSession) result() (overrideui.ValidationResult, error) {
	<-session.done
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return session.completion.result, session.completion.err
}

func (session *interactiveBuildSession) validationRunner() overrideui.BuildRunner {
	return func(
		ctx context.Context,
		progress io.Writer,
		reporter buildprogress.Reporter,
	) (overrideui.ValidationResult, error) {
		detach := session.progress.attach(progress, reporter)
		defer detach()
		select {
		case <-session.done:
			return session.result()
		case <-ctx.Done():
			session.cancel()
			return session.result()
		}
	}
}

func writeBuildOutcome(output io.Writer, outcome overrideui.BuildOutcome) {
	fmt.Fprintf(output, "image: %s\n", outcome.ImageReference)
	if outcome.Elapsed >= time.Second {
		fmt.Fprintf(output, "elapsed: %s\n", outcome.Elapsed.Round(100*time.Millisecond))
	}
	if outcome.Republished {
		fmt.Fprintf(output, "updated %s\n", outcome.Environment)
		return
	}
	if outcome.Reused {
		fmt.Fprintf(output, "%s is already up to date\n", outcome.Environment)
		return
	}
	fmt.Fprintf(output, "built %s\n", outcome.Environment)
}

func interactiveBuildRunner(
	deploymentDir string,
	globalOptions globalDeploymentOptions,
	started time.Time,
	noCache bool,
	validateLayers bool,
) overrideui.BuildRunner {
	return func(
		ctx context.Context,
		progress io.Writer,
		reporter buildprogress.Reporter,
	) (overrideui.ValidationResult, error) {
		runtimeInput, err := dockerProviderBuildRuntime()
		if err != nil {
			return overrideui.ValidationResult{}, err
		}
		var childOutput synchronizedBuffer
		build, err := dockerProviderBuild(ctx, dockerdeploy.ProviderBuildRunInputV1{
			DeploymentDir:  deploymentDir,
			Runtime:        runtimeInput,
			NoCache:        noCache,
			ValidateLayers: validateLayers,
			Progress:       progress,
			BuildProgress:  reporter,
			RunOptions: dockerdeploy.RunOptions{
				Stdout: &childOutput, Stderr: &childOutput,
				DockerPreflightTimeout: globalOptions.DockerTimeout,
			},
		})
		if err != nil {
			return overrideui.ValidationResult{}, fmt.Errorf(
				"build environment: %s",
				buildFailureDiagnostic(err, childOutput.String()),
			)
		}
		summary, err := summarizeProviderBuild(build)
		if err != nil {
			return overrideui.ValidationResult{}, fmt.Errorf("summarize completed build: %w", err)
		}
		return overrideui.ValidationResult{
			Build: &overrideui.BuildOutcome{
				Environment: summary.Environment, ImageReference: summary.ImageReference,
				Elapsed: time.Since(started), Reused: build.Reused, Republished: build.Republished,
			},
			Warnings: buildWarnings(childOutput.String(), true),
		}, nil
	}
}

type providerBuildSummary struct {
	Environment    string
	ImageReference string
}

func summarizeProviderBuild(result dockerdeploy.LockedProviderBuildExecutionResultV1) (providerBuildSummary, error) {
	document, err := blueprint.DecodeResolvedDocumentV1(result.State.Blueprint)
	if err != nil {
		return providerBuildSummary{}, err
	}
	if result.State.Current == nil {
		return providerBuildSummary{}, fmt.Errorf("completed build has no current generation")
	}
	imageReference := strings.TrimSpace(result.State.Current.Reference)
	if imageReference == "" {
		return providerBuildSummary{}, fmt.Errorf("completed build has no image reference")
	}
	return providerBuildSummary{
		Environment: document.Environment.ID, ImageReference: imageReference,
	}, nil
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
