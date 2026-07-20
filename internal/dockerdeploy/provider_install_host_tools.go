package dockerdeploy

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

type providerInstallHostToolsV1 struct {
	DockerPath        string
	SystemctlPath     string
	IncludeDockerUnit bool
}

type providerInstallHostToolBackendV1 struct {
	lookPath func(string) (string, error)
	run      commandRunner
}

func inspectProviderInstallHostToolsV1(ctx context.Context, backend installBackend) (providerInstallHostToolsV1, error) {
	return inspectProviderInstallHostToolsWithV1(ctx, backend, providerInstallHostToolBackendV1{
		lookPath: exec.LookPath,
		run:      runCommandWithoutDockerPreflight,
	})
}

func inspectProviderInstallHostToolsWithV1(ctx context.Context, backend installBackend, tools providerInstallHostToolBackendV1) (providerInstallHostToolsV1, error) {
	if ctx == nil {
		return providerInstallHostToolsV1{}, fmt.Errorf("inspect install host tools requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providerInstallHostToolsV1{}, err
	}
	if tools.lookPath == nil || tools.run == nil {
		return providerInstallHostToolsV1{}, fmt.Errorf("inspect install host tools requires a complete backend")
	}
	dockerPath, err := providerInstallAbsoluteToolPathV1(tools.lookPath, "docker")
	if err != nil {
		return providerInstallHostToolsV1{}, fmt.Errorf("Docker command not found: %w", err)
	}
	result := providerInstallHostToolsV1{DockerPath: dockerPath}
	switch backend {
	case installBackendLinuxSystemd:
		systemctlPath, err := providerInstallAbsoluteToolPathV1(tools.lookPath, "systemctl")
		if err != nil {
			return providerInstallHostToolsV1{}, fmt.Errorf("systemctl command not found: %w", err)
		}
		result.SystemctlPath = systemctlPath
		result.IncludeDockerUnit = tools.run(
			CommandSpec{Name: systemctlPath, Args: []string{"cat", "docker.service"}},
			RunOptions{Context: ctx},
		) == nil
	case installBackendDockerDesktop, installBackendDockerManaged:
	default:
		return providerInstallHostToolsV1{}, fmt.Errorf("install backend %q has no supported host tools", backend)
	}
	return result, nil
}

func providerInstallAbsoluteToolPathV1(lookPath func(string) (string, error), name string) (string, error) {
	path, err := lookPath(name)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := validateProviderInstallHostToolV1(absolute, name); err != nil {
		return "", err
	}
	return absolute, nil
}
