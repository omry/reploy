package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/buildprofile"
)

type DockerEngineKind string

const (
	DockerEngineLinux   DockerEngineKind = "linux-engine"
	DockerEngineDesktop DockerEngineKind = "docker-desktop"
)

type BuildKitCapabilities struct {
	ServerVersion   string
	ServerOS        string
	OperatingSystem string
	Context         string
	Engine          DockerEngineKind
}

type dockerOutputRunner func(context.Context, ...string) (string, error)
type dockerOutputBinder func(context.Context) (dockerOutputRunner, string, error)

var runDockerOutput dockerOutputRunner = executeDockerOutput
var bindBuildKitDockerOutput dockerOutputBinder = bindDockerOutputV1

// ProbeBuildKitCapabilities verifies the common Linux daemon contract used on
// native Linux and by Docker Desktop. The generated-build smoke test remains
// the final proof that the daemon's BuildKit frontend supports RUN mounts.
func ProbeBuildKitCapabilities(ctx context.Context) (BuildKitCapabilities, error) {
	run, contextName, err := bindBuildKitDockerOutput(ctx)
	if err != nil {
		return BuildKitCapabilities{}, fmt.Errorf("probe Docker daemon for generated images: %w", err)
	}
	return probeBuildKitCapabilitiesForContext(ctx, run, contextName)
}

func probeBuildKitCapabilities(ctx context.Context, run dockerOutputRunner) (BuildKitCapabilities, error) {
	return probeBuildKitCapabilitiesForContext(ctx, run, "")
}

func probeBuildKitCapabilitiesForContext(ctx context.Context, run dockerOutputRunner, contextName string) (BuildKitCapabilities, error) {
	output, err := run(ctx, "info", "--format", "{{.ServerVersion}}\t{{.OSType}}\t{{.OperatingSystem}}")
	if err != nil {
		return BuildKitCapabilities{}, fmt.Errorf("probe Docker daemon for generated images: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(output), "\t")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return BuildKitCapabilities{}, fmt.Errorf("probe Docker daemon returned unexpected output %q", strings.TrimSpace(output))
	}
	if contextName == "" {
		contextOutput, err := run(ctx, "context", "show")
		if err != nil {
			return BuildKitCapabilities{}, fmt.Errorf("probe Docker context: %w", err)
		}
		contextName = strings.TrimSpace(contextOutput)
	}
	capabilities := BuildKitCapabilities{
		ServerVersion: parts[0], ServerOS: parts[1], OperatingSystem: parts[2], Context: contextName,
		Engine: DockerEngineLinux,
	}
	if strings.Contains(strings.ToLower(capabilities.OperatingSystem), "docker desktop") {
		capabilities.Engine = DockerEngineDesktop
	}
	if capabilities.ServerOS != "linux" {
		return BuildKitCapabilities{}, fmt.Errorf("generated images require a Linux Docker daemon; context %q reports %q", capabilities.Context, capabilities.ServerOS)
	}
	if !minimumDockerVersion(capabilities.ServerVersion, 24, 0) {
		return BuildKitCapabilities{}, fmt.Errorf("generated images require Docker Engine 24.0 or newer; daemon reports %s", capabilities.ServerVersion)
	}
	return capabilities, nil
}

func bindDockerOutputV1(ctx context.Context) (dockerOutputRunner, string, error) {
	spec := CommandSpec{Name: "docker"}
	target, err := verifiedLocalDockerTargetV1(ctx, spec, defaultDockerPreflightTimeout)
	if err != nil {
		return nil, "", err
	}
	return func(runCtx context.Context, args ...string) (string, error) {
		return executeDockerOutputAtEndpoint(runCtx, target.Endpoint, args...)
	}, target.Context, nil
}

func minimumDockerVersion(value string, minimumMajor int, minimumMinor int) bool {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return major > minimumMajor || major == minimumMajor && minor >= minimumMinor
}

func executeDockerOutput(ctx context.Context, args ...string) (string, error) {
	spec := CommandSpec{Name: "docker", Args: args}
	endpoint, err := verifiedLocalDockerEndpointV1(ctx, spec, defaultDockerPreflightTimeout)
	if err != nil {
		return "", err
	}
	return executeDockerOutputAtEndpoint(ctx, endpoint, args...)
}

func executeDockerOutputAtEndpoint(ctx context.Context, endpoint string, args ...string) (string, error) {
	spec := CommandSpec{Name: "docker", Args: args}
	spec = pinDockerEndpointV1(spec, endpoint)
	ctx, end := buildprofile.Start(ctx, dockerProfileOperation(args))
	command := exec.CommandContext(ctx, spec.Name, spec.Args...)
	command.Env = append(os.Environ(), spec.Env...)
	output, err := command.CombinedOutput()
	// Docker output probes often use a non-zero exit to represent ordinary
	// absence. Their semantic caller records a failure only when it propagates.
	end(nil)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, message)
		}
		return "", fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return string(output), nil
}
