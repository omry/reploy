package cli

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/dockerdeploy"
)

var dockerBuildWarningPattern = regexp.MustCompile(`^-\s*([A-Za-z][A-Za-z0-9]+):\s*(.+)$`)

func runDockerBuild(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	options, err := parseDockerBuildOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy build usage error: %v\n", err)
		printDockerBuildHelp(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, io.Discard)
	started := time.Now()
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
	for _, warning := range buildWarnings(childOutput) {
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

func buildWarnings(output string) []string {
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
			if message != "" {
				addBuildWarning(&warnings, seen, "APT: "+message)
			}
		}
	}
	return warnings
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
	lines := strings.Split(stripBuildTerminalControls(childOutput), "\n")
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
		if strings.HasPrefix(lower, "error:") {
			detail := strings.TrimSpace(line[len("ERROR:"):])
			if detail != "" {
				return "environment build failed: " + detail
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
			return "Python package resolution failed: " + line
		case strings.Contains(lower, "signatures couldn't be verified"),
			strings.Contains(lower, "public key is not available"):
			return "APT repository trust check failed: " + line
		}
	}
	message := strings.TrimSpace(err.Error())
	if strings.Contains(strings.ToLower(message), "docker failed: exit status") {
		return "environment image construction failed; the build backend did not provide a usable diagnostic"
	}
	return message
}

func stripBuildTerminalControls(value string) string {
	return ansi.Strip(strings.ReplaceAll(value, "\r", "\n"))
}
