package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type hostPlatform struct {
	GOOS string
}

type installBackend string

const (
	installBackendLinuxSystemd       installBackend = "linux-systemd"
	installBackendDockerDesktop      installBackend = "docker-desktop"
	installBackendUnsupported        installBackend = "unsupported"
	dockerRuntimeUnknown             dockerRuntime  = "unknown"
	dockerRuntimeLinuxEngine         dockerRuntime  = "linux-engine"
	dockerRuntimeDockerDesktop       dockerRuntime  = "docker-desktop"
	dockerDesktopSecurityWarningText                = "warning: Docker-managed permanent installs on macOS and Windows provide weaker isolation than Linux/systemd OS service installs"
)

type dockerRuntime string

type dockerRuntimeInfo struct {
	Runtime         dockerRuntime
	OperatingSystem string
	ServerVersion   string
}

var detectHostPlatform = func() hostPlatform {
	return hostPlatform{GOOS: runtime.GOOS}
}

var detectDockerRuntimeForDoctor = detectDockerRuntime

func currentHostPlatform() hostPlatform {
	return detectHostPlatform()
}

func (platform hostPlatform) installBackend() installBackend {
	switch platform.GOOS {
	case "linux":
		return installBackendLinuxSystemd
	case "darwin":
		return installBackendDockerDesktop
	default:
		return installBackendUnsupported
	}
}

func (platform hostPlatform) unsupportedPersistentInstallError(action string) error {
	if platform.GOOS == "windows" {
		return fmt.Errorf("Windows persistent %s is not supported by this build", action)
	}
	return fmt.Errorf("%s is not supported on %s", action, platform.GOOS)
}

func dockerDesktopSecurityWarning() string {
	return dockerDesktopSecurityWarningText
}

func detectDockerRuntime(ctx context.Context, spec CommandSpec, timeout time.Duration) (dockerRuntimeInfo, error) {
	if spec.Name == "" {
		spec.Name = "docker"
	}
	probeCtx, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(timeout))
	defer cancel()

	command := exec.CommandContext(probeCtx, spec.Name, "info", "--format", "{{json .}}")
	command.Dir = spec.Dir
	if len(spec.Env) > 0 {
		command.Env = append(os.Environ(), spec.Env...)
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if probeCtx.Err() == context.DeadlineExceeded {
			return dockerRuntimeInfo{}, fmt.Errorf("docker info did not respond within %s", effectiveDockerPreflightTimeout(timeout))
		}
		if text := trimmedCommandOutput(output.String()); text != "" {
			return dockerRuntimeInfo{}, fmt.Errorf("docker runtime check failed: %w\ncommand output:\n%s", err, text)
		}
		return dockerRuntimeInfo{}, fmt.Errorf("docker runtime check failed: %w", err)
	}

	var info struct {
		OperatingSystem string
		ServerVersion   string
	}
	if err := json.Unmarshal(output.Bytes(), &info); err != nil {
		return dockerRuntimeInfo{}, fmt.Errorf("parse docker runtime info: %w", err)
	}
	runtimeKind := dockerRuntimeLinuxEngine
	if strings.Contains(strings.ToLower(info.OperatingSystem), "docker desktop") {
		runtimeKind = dockerRuntimeDockerDesktop
	}
	if strings.TrimSpace(info.OperatingSystem) == "" {
		runtimeKind = dockerRuntimeUnknown
	}
	return dockerRuntimeInfo{
		Runtime:         runtimeKind,
		OperatingSystem: info.OperatingSystem,
		ServerVersion:   info.ServerVersion,
	}, nil
}
