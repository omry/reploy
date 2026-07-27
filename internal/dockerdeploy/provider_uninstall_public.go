package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type UninstallOptions struct {
	From        string
	ServiceName string
	RemoveDir   bool
	Stdout      io.Writer
	Progress    io.Writer
	ControlMode ControlAdmissionModeV1

	DockerPreflightTimeout time.Duration
}

type ProviderUninstallResultV1 struct {
	AlreadyAbsent     bool
	DeploymentDir     string
	Environment       string
	Service           string
	RemovedDirectory  bool
	RetainedDirectory bool
}

type providerUninstallPublicBackendV1 struct {
	runtime      func() (StagedProviderBuildRuntimeV1, error)
	uninstall    func(context.Context, ProviderUninstallInputV1) error
	readState    func(string) (deploy.StateV1, bool, error)
	recover      func(context.Context, ProviderUninstallRecoveryInputV1) error
	targetAbsent func(string) (bool, error)
}

// UninstallProviderV1 removes an installed state-v1 deployment through the
// serialized provider runtime path.
func UninstallProviderV1(options UninstallOptions) (ProviderUninstallResultV1, error) {
	return uninstallProviderV1(options, providerUninstallPublicBackendV1{
		runtime:      CurrentStagedProviderBuildRuntimeV1,
		uninstall:    RunProviderUninstallV1,
		readState:    readProviderUninstallStateV1,
		recover:      RecoverMissingProviderUninstallV1,
		targetAbsent: providerUninstallTargetAbsentV1,
	})
}

func uninstallProviderV1(options UninstallOptions, backend providerUninstallPublicBackendV1) (ProviderUninstallResultV1, error) {
	if backend.runtime == nil || backend.uninstall == nil {
		return ProviderUninstallResultV1{}, fmt.Errorf("provider uninstall requires a complete public backend")
	}
	deploymentDir := strings.TrimSpace(options.From)
	if deploymentDir == "" {
		deploymentDir = "."
	}
	if strings.TrimSpace(options.From) != "" && strings.TrimSpace(options.ServiceName) == "" {
		if backend.targetAbsent == nil {
			return ProviderUninstallResultV1{}, fmt.Errorf("provider uninstall requires target inspection")
		}
		absent, err := backend.targetAbsent(deploymentDir)
		if err != nil {
			return ProviderUninstallResultV1{}, err
		}
		if absent {
			return ProviderUninstallResultV1{AlreadyAbsent: true, DeploymentDir: deploymentDir}, nil
		}
	}
	runtime, err := backend.runtime()
	if err != nil {
		return ProviderUninstallResultV1{}, err
	}
	if options.ServiceName != "" {
		if backend.readState == nil {
			return ProviderUninstallResultV1{}, fmt.Errorf("provider uninstall recovery requires a complete public backend")
		}
		_, found, err := backend.readState(deploymentDir)
		if err != nil {
			return ProviderUninstallResultV1{}, err
		}
		if !found {
			if backend.recover == nil {
				return ProviderUninstallResultV1{}, fmt.Errorf("provider uninstall recovery requires a complete public backend")
			}
			err := backend.recover(context.Background(), ProviderUninstallRecoveryInputV1{
				RequestedDir: strings.TrimSpace(options.From), Service: options.ServiceName,
				Runtime: runtime, ControlMode: options.ControlMode, RemoveDir: options.RemoveDir,
				RunOptions: providerInstallRunOptionsV1(options.Stdout, options.Progress, options.DockerPreflightTimeout),
			})
			return ProviderUninstallResultV1{
				DeploymentDir: deploymentDir, Service: options.ServiceName,
				RemovedDirectory: options.RemoveDir, RetainedDirectory: !options.RemoveDir,
			}, err
		}
	}
	result := ProviderUninstallResultV1{DeploymentDir: deploymentDir}
	err = backend.uninstall(context.Background(), ProviderUninstallInputV1{
		DeploymentDir: deploymentDir,
		Runtime:       runtime,
		ControlMode:   options.ControlMode,
		Service:       options.ServiceName,
		RemoveDir:     options.RemoveDir,
		RunOptions:    providerInstallRunOptionsV1(options.Stdout, options.Progress, options.DockerPreflightTimeout),
		result:        &result,
	})
	return result, err
}

// UninstallProviderNeedsRootV1 reports whether the recorded state-v1 install
// uses the Linux system scope. Failure to read state is conservative on Linux;
// the locked uninstall path performs the authoritative validation.
func UninstallProviderNeedsRootV1(options UninstallOptions) bool {
	return uninstallProviderNeedsRootV1(options, providerUninstallPublicBackendV1{
		runtime:      CurrentStagedProviderBuildRuntimeV1,
		readState:    readProviderUninstallStateV1,
		targetAbsent: providerUninstallTargetAbsentV1,
	})
}

func uninstallProviderNeedsRootV1(options UninstallOptions, backend providerUninstallPublicBackendV1) bool {
	if backend.runtime == nil || backend.readState == nil {
		return true
	}
	runtime, err := backend.runtime()
	if err != nil {
		return true
	}
	if runtime.Host != blueprint.HostLinux {
		return false
	}
	deploymentDir := strings.TrimSpace(options.From)
	if deploymentDir == "" {
		deploymentDir = "."
	}
	if strings.TrimSpace(options.From) != "" && strings.TrimSpace(options.ServiceName) == "" {
		if backend.targetAbsent == nil {
			return true
		}
		absent, err := backend.targetAbsent(deploymentDir)
		if err != nil {
			return true
		}
		if absent {
			return false
		}
	}
	state, found, err := backend.readState(deploymentDir)
	if err != nil || !found || state.Deployment == nil {
		return true
	}
	scope, err := ParseInstallScope(state.Deployment.Installation.Scope)
	return err != nil || scope == InstallScopeSystem
}

func providerUninstallTargetAbsentV1(deploymentDir string) (bool, error) {
	_, err := os.Lstat(deploymentDir)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect provider uninstall deployment directory: %w", err)
	}
	return false, nil
}

func readProviderUninstallStateV1(deploymentDir string) (deploy.StateV1, bool, error) {
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return deploy.StateV1{}, false, fmt.Errorf("resolve provider uninstall deployment directory: %w", err)
	}
	stateDir := filepath.Join(absoluteDir, ".reploy")
	info, err := os.Lstat(stateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return deploy.StateV1{}, false, nil
	}
	if err != nil {
		return deploy.StateV1{}, false, fmt.Errorf("inspect provider uninstall state directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return deploy.StateV1{}, false, fmt.Errorf("provider uninstall state path must be a real directory: %s", stateDir)
	}
	statePath := filepath.Join(stateDir, "state.json")
	info, err = os.Lstat(statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return deploy.StateV1{}, false, nil
	}
	if err != nil {
		return deploy.StateV1{}, false, fmt.Errorf("inspect provider uninstall state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return deploy.StateV1{}, false, fmt.Errorf("provider uninstall state path must be a regular file: %s", statePath)
	}
	content, err := os.ReadFile(statePath)
	if err != nil {
		return deploy.StateV1{}, false, fmt.Errorf("read provider uninstall state: %w", err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		return deploy.StateV1{}, false, err
	}
	return state, true, nil
}
