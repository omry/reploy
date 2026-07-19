package dockerdeploy

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

var runDockerOutput dockerOutputRunner = executeDockerOutput

// ProbeBuildKitCapabilities verifies the common Linux daemon contract used on
// native Linux and by Docker Desktop. The generated-build smoke test remains
// the final proof that the daemon's BuildKit frontend supports RUN mounts.
func ProbeBuildKitCapabilities(ctx context.Context) (BuildKitCapabilities, error) {
	return probeBuildKitCapabilities(ctx, runDockerOutput)
}

func probeBuildKitCapabilities(ctx context.Context, run dockerOutputRunner) (BuildKitCapabilities, error) {
	output, err := run(ctx, "info", "--format", "{{.ServerVersion}}\t{{.OSType}}\t{{.OperatingSystem}}")
	if err != nil {
		return BuildKitCapabilities{}, fmt.Errorf("probe Docker daemon for generated images: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(output), "\t")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return BuildKitCapabilities{}, fmt.Errorf("probe Docker daemon returned unexpected output %q", strings.TrimSpace(output))
	}
	contextName, err := run(ctx, "context", "show")
	if err != nil {
		return BuildKitCapabilities{}, fmt.Errorf("probe Docker context: %w", err)
	}
	capabilities := BuildKitCapabilities{
		ServerVersion: parts[0], ServerOS: parts[1], OperatingSystem: parts[2], Context: strings.TrimSpace(contextName),
		Engine: DockerEngineLinux,
	}
	if strings.Contains(strings.ToLower(capabilities.OperatingSystem), "docker desktop") {
		capabilities.Engine = DockerEngineDesktop
	}
	if capabilities.ServerOS != "linux" {
		return BuildKitCapabilities{}, fmt.Errorf("generated images require a Linux Docker daemon; context %q reports %q", capabilities.Context, capabilities.ServerOS)
	}
	if !minimumDockerVersion(capabilities.ServerVersion, 20, 10) {
		return BuildKitCapabilities{}, fmt.Errorf("generated images require Docker 20.10 or newer; daemon reports %s", capabilities.ServerVersion)
	}
	return capabilities, nil
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
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, message)
		}
		return "", fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return string(output), nil
}
