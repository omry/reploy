package dockerdeploy

import (
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
)

type providerInstallHostCommandsV1 struct {
	Configure []CommandSpec
	Start     CommandSpec
}

func planProviderInstallHostCommandsV1(plan providerInstallationPlanV1, dockerPath string, systemctlPath string) (providerInstallHostCommandsV1, error) {
	if err := deploy.ValidateInstallationStateV1(plan.Installation); err != nil {
		return providerInstallHostCommandsV1{}, fmt.Errorf("plan install host commands: %w", err)
	}
	if plan.Installation.Status != deploy.InstallationStatusReady {
		return providerInstallHostCommandsV1{}, fmt.Errorf("plan install host commands requires a ready installation plan")
	}
	result := providerInstallHostCommandsV1{Configure: []CommandSpec{}}
	switch plan.Backend {
	case installBackendLinuxSystemd:
		if err := validateProviderInstallHostToolV1(systemctlPath, "systemctl"); err != nil {
			return providerInstallHostCommandsV1{}, err
		}
		unit := plan.Installation.Service + ".service"
		result.Configure = []CommandSpec{
			{Name: systemctlPath, Args: []string{"daemon-reload"}},
			{Name: systemctlPath, Args: []string{"enable", unit}},
		}
		result.Start = CommandSpec{Name: systemctlPath, Args: []string{"restart", unit}}
	case installBackendDockerDesktop, installBackendDockerManaged:
		if err := validateProviderInstallHostToolV1(dockerPath, "Docker"); err != nil {
			return providerInstallHostCommandsV1{}, err
		}
		result.Start = CommandSpec{
			Name: dockerPath,
			Args: []string{
				"compose",
				"--project-name", plan.Installation.ComposeProject,
				"--project-directory", plan.Installation.TargetDir,
				"--env-file", filepath.Join(plan.Installation.TargetDir, DockerEnvFileName),
				"-f", filepath.Join(plan.Installation.TargetDir, ComposeFileName),
				"up", "-d",
			},
			Dir: plan.Installation.TargetDir,
		}
	default:
		return providerInstallHostCommandsV1{}, fmt.Errorf("install backend %q cannot configure a persistent service", plan.Backend)
	}
	return result, nil
}

func validateProviderInstallHostToolV1(path string, name string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("plan install host commands requires an absolute clean %s path", name)
	}
	return nil
}
