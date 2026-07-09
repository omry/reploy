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
type platformCommand string
type platformSupportStatus string

const (
	installBackendLinuxSystemd       installBackend = "linux-systemd"
	installBackendDockerDesktop      installBackend = "docker-desktop"
	installBackendDockerManaged      installBackend = "docker-managed"
	installBackendUnsupported        installBackend = "unsupported"
	dockerRuntimeUnknown             dockerRuntime  = "unknown"
	dockerRuntimeLinuxEngine         dockerRuntime  = "linux-engine"
	dockerRuntimeDockerDesktop       dockerRuntime  = "docker-desktop"
	dockerDesktopSecurityWarningText                = "warning: Docker-managed permanent installs on macOS and Windows provide weaker isolation than Linux/systemd OS service installs"
)

const (
	platformSupportSupported   platformSupportStatus = "supported"
	platformSupportPlanned     platformSupportStatus = "planned"
	platformSupportDeferred    platformSupportStatus = "deferred"
	platformSupportUnsupported platformSupportStatus = "unsupported"
)

const (
	platformCommandHelp                 platformCommand = "help"
	platformCommandIndex                platformCommand = "index"
	platformCommandStage                platformCommand = "stage"
	platformCommandStageUpdate          platformCommand = "stage-update"
	platformCommandInfo                 platformCommand = "info"
	platformCommandBundleMetadata       platformCommand = "bundle-metadata"
	platformCommandBundleMutation       platformCommand = "bundle-mutation"
	platformCommandBundleDocker         platformCommand = "bundle-docker"
	platformCommandAppSummary           platformCommand = "app-summary"
	platformCommandAppCommand           platformCommand = "app-command"
	platformCommandStagingRuntime       platformCommand = "staging-runtime"
	platformCommandTest                 platformCommand = "test"
	platformCommandDoctorStaging        platformCommand = "doctor-staging"
	platformCommandDoctorPreinstall     platformCommand = "doctor-preinstall"
	platformCommandInstall              platformCommand = "install"
	platformCommandUninstallFrom        platformCommand = "uninstall-from"
	platformCommandUninstallServiceName platformCommand = "uninstall-service-name"
	platformCommandUninstallList        platformCommand = "uninstall-list"
	platformCommandInstalledPowerShell  platformCommand = "installed-powershell-control"
	platformCommandInstalledPOSIX       platformCommand = "installed-posix-control"
	platformCommandWindowsService       platformCommand = "windows-service"
)

type dockerRuntime string

type dockerRuntimeInfo struct {
	Runtime         dockerRuntime
	OperatingSystem string
	ServerVersion   string
}

type platformCommandSupport struct {
	Status                platformSupportStatus
	RequiresDockerDesktop bool
	Reason                string
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
	case "darwin", "windows":
		return installBackendDockerDesktop
	default:
		return installBackendUnsupported
	}
}

func (platform hostPlatform) installBackendForScope(scope InstallScope) installBackend {
	if (platform.GOOS == "linux" || platform.GOOS == "darwin") && scope == InstallScopeUser {
		return installBackendDockerManaged
	}
	return platform.installBackend()
}

func isDockerManagedInstallBackend(backend installBackend) bool {
	switch backend {
	case installBackendDockerDesktop, installBackendDockerManaged:
		return true
	default:
		return false
	}
}

func (platform hostPlatform) commandSupport(command platformCommand) platformCommandSupport {
	switch platform.GOOS {
	case "linux":
		return linuxCommandSupport(command)
	case "darwin":
		return dockerManagedPOSIXCommandSupport(command)
	case "windows":
		return windowsCommandSupport(command)
	default:
		return unsupportedCommandSupport("unsupported host platform")
	}
}

func linuxCommandSupport(command platformCommand) platformCommandSupport {
	switch command {
	case platformCommandHelp,
		platformCommandIndex,
		platformCommandStage,
		platformCommandStageUpdate,
		platformCommandInfo,
		platformCommandBundleMetadata,
		platformCommandBundleMutation,
		platformCommandBundleDocker,
		platformCommandAppSummary,
		platformCommandAppCommand,
		platformCommandStagingRuntime,
		platformCommandTest,
		platformCommandDoctorStaging,
		platformCommandDoctorPreinstall,
		platformCommandInstall,
		platformCommandUninstallFrom,
		platformCommandUninstallServiceName,
		platformCommandUninstallList:
		return supportedCommandSupport(false)
	case platformCommandInstalledPOSIX:
		return supportedCommandSupport(false)
	case platformCommandInstalledPowerShell,
		platformCommandWindowsService:
		return unsupportedCommandSupport("not part of the Linux support path")
	default:
		return unsupportedCommandSupport("unknown command surface")
	}
}

func dockerManagedPOSIXCommandSupport(command platformCommand) platformCommandSupport {
	switch command {
	case platformCommandHelp,
		platformCommandIndex,
		platformCommandStage,
		platformCommandStageUpdate,
		platformCommandInfo,
		platformCommandBundleMetadata,
		platformCommandBundleMutation,
		platformCommandAppSummary,
		platformCommandDoctorStaging,
		platformCommandUninstallFrom,
		platformCommandInstalledPOSIX:
		return supportedCommandSupport(false)
	case platformCommandBundleDocker,
		platformCommandAppCommand,
		platformCommandStagingRuntime,
		platformCommandTest,
		platformCommandDoctorPreinstall,
		platformCommandInstall:
		return supportedCommandSupport(false)
	case platformCommandUninstallServiceName,
		platformCommandUninstallList:
		return unsupportedCommandSupport("Linux/systemd service discovery is not part of Docker-managed install")
	case platformCommandInstalledPowerShell,
		platformCommandWindowsService:
		return unsupportedCommandSupport("not part of the macOS Docker-managed support path")
	default:
		return unsupportedCommandSupport("unknown command surface")
	}
}

func windowsCommandSupport(command platformCommand) platformCommandSupport {
	switch command {
	case platformCommandHelp,
		platformCommandIndex,
		platformCommandStage,
		platformCommandStageUpdate,
		platformCommandInfo,
		platformCommandBundleMetadata,
		platformCommandBundleMutation,
		platformCommandAppSummary,
		platformCommandDoctorStaging:
		return supportedCommandSupport(false)
	case platformCommandBundleDocker,
		platformCommandAppCommand,
		platformCommandStagingRuntime,
		platformCommandTest,
		platformCommandDoctorPreinstall:
		return supportedCommandSupport(true)
	case platformCommandInstall,
		platformCommandUninstallFrom,
		platformCommandInstalledPowerShell:
		return supportedCommandSupport(true)
	case platformCommandInstalledPOSIX:
		return platformCommandSupport{
			Status:                platformSupportDeferred,
			RequiresDockerDesktop: true,
			Reason:                "optional WSL/Linux-like access to a native Windows install",
		}
	case platformCommandUninstallServiceName:
		return platformCommandSupport{
			Status: platformSupportDeferred,
			Reason: "requires mapping to recorded Docker-managed installed state; must not imply Windows Service semantics",
		}
	case platformCommandUninstallList:
		return unsupportedCommandSupport("Linux/systemd service discovery is not part of native Windows support")
	case platformCommandWindowsService:
		return unsupportedCommandSupport("Windows Service install is a future design topic")
	default:
		return unsupportedCommandSupport("unknown command surface")
	}
}

func supportedCommandSupport(requiresDockerDesktop bool) platformCommandSupport {
	return platformCommandSupport{
		Status:                platformSupportSupported,
		RequiresDockerDesktop: requiresDockerDesktop,
	}
}

func plannedCommandSupport(requiresDockerDesktop bool, reason string) platformCommandSupport {
	return platformCommandSupport{
		Status:                platformSupportPlanned,
		RequiresDockerDesktop: requiresDockerDesktop,
		Reason:                reason,
	}
}

func unsupportedCommandSupport(reason string) platformCommandSupport {
	return platformCommandSupport{
		Status: platformSupportUnsupported,
		Reason: reason,
	}
}

func (platform hostPlatform) unsupportedPersistentInstallError(action string) error {
	if platform.GOOS == "windows" {
		return fmt.Errorf("Windows persistent %s is planned as a Docker-managed permanent install but is not supported by this build", action)
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
