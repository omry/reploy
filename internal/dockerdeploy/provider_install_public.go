package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type providerInstallPublicBackendV1 struct {
	runtime func() (StagedProviderBuildRuntimeV1, error)
	install func(context.Context, ProviderInstallInputV1) (deploy.StateV1, error)
	direct  func(context.Context, DirectProviderInstallInputV1) (DirectProviderInstallResultV1, error)
}

// InstallProviderV1 installs a state-v1 staging deployment through the
// provider build and publication pipeline.
func InstallProviderV1(options InstallOptions) error {
	return installProviderV1(options, providerInstallPublicBackendV1{
		runtime: CurrentStagedProviderBuildRuntimeV1,
		install: RunProviderInstallV1,
	})
}

// InstallProviderResultV1 installs a staged deployment and returns the facts
// needed for user-facing completion output.
func InstallProviderResultV1(options InstallOptions) (ProviderInstallResultV1, error) {
	if options.DryRun {
		return ProviderInstallResultV1{}, fmt.Errorf("provider install does not support dry-run; use reploy validate for blueprint validation")
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		return ProviderInstallResultV1{}, err
	}
	return RunProviderInstallResultV1(context.Background(), ProviderInstallInputV1{
		SourceDeploymentDir: options.Dir, DestinationDeploymentDir: options.Target,
		Runtime: runtime, ControlMode: options.ControlMode, Scope: options.Scope, Service: options.Service,
		PortOverrides: append([]PortOverride(nil), options.PortOverrides...),
		Replace:       append([]string(nil), options.Replace...), Clean: options.Clean, Start: options.Start,
		RunOptions: providerInstallRunOptionsV1(options.Stdout, options.Progress, options.DockerPreflightTimeout),
	})
}

func installProviderV1(options InstallOptions, backend providerInstallPublicBackendV1) error {
	if options.DryRun {
		return fmt.Errorf("provider install does not support dry-run; use reploy validate for blueprint validation")
	}
	if backend.runtime == nil || backend.install == nil {
		return fmt.Errorf("provider install requires a complete public backend")
	}
	runtime, err := backend.runtime()
	if err != nil {
		return err
	}
	_, err = backend.install(context.Background(), ProviderInstallInputV1{
		SourceDeploymentDir:      options.Dir,
		DestinationDeploymentDir: options.Target,
		Runtime:                  runtime,
		ControlMode:              options.ControlMode,
		Scope:                    options.Scope,
		Service:                  options.Service,
		PortOverrides:            append([]PortOverride(nil), options.PortOverrides...),
		Replace:                  append([]string(nil), options.Replace...),
		Clean:                    options.Clean,
		Start:                    options.Start,
		RunOptions:               providerInstallRunOptionsV1(options.Stdout, options.Progress, options.DockerPreflightTimeout),
	})
	return err
}

// DirectInstallProviderV1 resolves a blueprint into a private state-v1 source
// workspace, installs it, and returns the resolved destination.
func DirectInstallProviderV1(options DirectInstallOptions) (string, error) {
	return directInstallProviderV1(options, providerInstallPublicBackendV1{
		runtime: CurrentStagedProviderBuildRuntimeV1,
		direct:  RunDirectProviderInstallV1,
	})
}

// DirectInstallProviderResultV1 resolves, builds, and installs a blueprint and
// returns the facts needed for user-facing completion output.
func DirectInstallProviderResultV1(options DirectInstallOptions) (ProviderInstallResultV1, error) {
	if options.DryRun {
		return ProviderInstallResultV1{}, fmt.Errorf("provider install does not support dry-run; use reploy validate for blueprint validation")
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		return ProviderInstallResultV1{}, err
	}
	result, err := RunDirectProviderInstallV1(context.Background(), DirectProviderInstallInputV1{
		Pack: options.Pack, Target: options.Target, Runtime: runtime,
		ControlMode: options.ControlMode, Scope: options.Scope, Service: options.Service,
		PortOverrides: append([]PortOverride(nil), options.PortOverrides...),
		Replace:       append([]string(nil), options.Replace...), Clean: options.Clean, Start: options.Start,
		RunOptions: providerInstallRunOptionsV1(options.Stdout, options.Progress, options.DockerPreflightTimeout),
	})
	return result.Install, err
}

func directInstallProviderV1(options DirectInstallOptions, backend providerInstallPublicBackendV1) (string, error) {
	if options.DryRun {
		return "", fmt.Errorf("provider install does not support dry-run; use reploy validate for blueprint validation")
	}
	if backend.runtime == nil || backend.direct == nil {
		return "", fmt.Errorf("direct provider install requires a complete public backend")
	}
	runtime, err := backend.runtime()
	if err != nil {
		return "", err
	}
	result, err := backend.direct(context.Background(), DirectProviderInstallInputV1{
		Pack:          options.Pack,
		Target:        options.Target,
		Runtime:       runtime,
		ControlMode:   options.ControlMode,
		Scope:         options.Scope,
		Service:       options.Service,
		PortOverrides: append([]PortOverride(nil), options.PortOverrides...),
		Replace:       append([]string(nil), options.Replace...),
		Clean:         options.Clean,
		Start:         options.Start,
		RunOptions:    providerInstallRunOptionsV1(options.Stdout, options.Progress, options.DockerPreflightTimeout),
	})
	if err != nil {
		return "", err
	}
	return result.Target, nil
}

func providerInstallRunOptionsV1(output io.Writer, progress io.Writer, dockerPreflightTimeout time.Duration) RunOptions {
	return RunOptions{
		Context:                context.Background(),
		Stdout:                 output,
		Stderr:                 output,
		Progress:               progress,
		DockerPreflightTimeout: dockerPreflightTimeout,
	}
}
