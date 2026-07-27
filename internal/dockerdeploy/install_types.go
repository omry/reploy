package dockerdeploy

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type InstallOptions struct {
	Dir                    string
	Target                 string
	ControlMode            ControlAdmissionModeV1
	Scope                  InstallScope
	Service                string
	PortOverrides          []PortOverride
	Replace                []string
	Clean                  bool
	Start                  bool
	DryRun                 bool
	Stdout                 io.Writer
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
}

type DirectInstallOptions struct {
	Pack                   deploy.PackRef
	Target                 string
	ControlMode            ControlAdmissionModeV1
	Scope                  InstallScope
	Service                string
	PortOverrides          []PortOverride
	Replace                []string
	Clean                  bool
	Start                  bool
	DryRun                 bool
	Stdout                 io.Writer
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
}

const defaultSystemdUnitDir = "/etc/systemd/system"

var installSystemdUnitDir = defaultSystemdUnitDir

type InstallScope string

const (
	InstallScopeUser   InstallScope = "user"
	InstallScopeSystem InstallScope = "system"
)

func ParseInstallScope(value string) (InstallScope, error) {
	switch InstallScope(strings.TrimSpace(value)) {
	case InstallScopeUser:
		return InstallScopeUser, nil
	case InstallScopeSystem:
		return InstallScopeSystem, nil
	case "":
		return "", fmt.Errorf("--scope is required and must be user or system")
	default:
		return "", fmt.Errorf("--scope must be user or system: %s", value)
	}
}

func validateInstallScopeForBackend(scope InstallScope, backend installBackend, platform hostPlatform) error {
	switch scope {
	case InstallScopeUser:
		switch backend {
		case installBackendDockerDesktop, installBackendDockerManaged:
			return nil
		case installBackendLinuxSystemd:
			return fmt.Errorf("--scope user requires a Docker-managed backend on Linux")
		default:
			return platform.unsupportedPersistentInstallError("install")
		}
	case InstallScopeSystem:
		switch backend {
		case installBackendLinuxSystemd:
			return nil
		case installBackendDockerDesktop:
			return fmt.Errorf("--scope system is not supported on %s with Docker Desktop; no native system service backend is available", installTargetHostKey(platform.GOOS))
		default:
			return platform.unsupportedPersistentInstallError("install")
		}
	default:
		_, err := ParseInstallScope(string(scope))
		return err
	}
}
