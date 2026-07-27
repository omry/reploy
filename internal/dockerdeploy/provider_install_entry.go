package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
)

type ProviderInstallInputV1 struct {
	SourceDeploymentDir      string
	DestinationDeploymentDir string
	Runtime                  StagedProviderBuildRuntimeV1
	ControlMode              ControlAdmissionModeV1
	Scope                    InstallScope
	Service                  string
	PortOverrides            []PortOverride
	Replace                  []string
	Clean                    bool
	Start                    bool
	RunOptions               RunOptions
}

type providerInstallEntryBackendV1 struct {
	run func(context.Context, providerInstallRunInputV1, providerInstallRunBackend) (deploy.StateV1, error)
}

func RunProviderInstallV1(ctx context.Context, input ProviderInstallInputV1) (deploy.StateV1, error) {
	return runProviderInstallEntryV1(ctx, input, providerInstallEntryBackendV1{run: runProviderInstallV1})
}

func runProviderInstallEntryV1(
	ctx context.Context,
	input ProviderInstallInputV1,
	backend providerInstallEntryBackendV1,
) (deploy.StateV1, error) {
	if ctx == nil {
		return deploy.StateV1{}, fmt.Errorf("provider install requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.StateV1{}, err
	}
	if backend.run == nil {
		return deploy.StateV1{}, fmt.Errorf("provider install requires a complete backend")
	}
	scope, err := ParseInstallScope(string(input.Scope))
	if err != nil {
		return deploy.StateV1{}, err
	}
	platform, err := installHostPlatformV1(input.Runtime.Host)
	if err != nil {
		return deploy.StateV1{}, err
	}
	if err := validateInstallScopeForBackend(scope, platform.installBackendForScope(scope), platform); err != nil {
		return deploy.StateV1{}, err
	}
	if input.Service != "" && !validServiceName(input.Service) {
		return deploy.StateV1{}, fmt.Errorf("--service contains unsupported characters: %s", input.Service)
	}
	return backend.run(ctx, providerInstallRunInputV1{
		SourceDeploymentDir:      input.SourceDeploymentDir,
		DestinationDeploymentDir: input.DestinationDeploymentDir,
		Runtime:                  input.Runtime,
		ControlMode:              input.ControlMode,
		Install: providerInstallOptionsV1{
			Scope:         scope,
			Service:       input.Service,
			PortOverrides: append([]PortOverride(nil), input.PortOverrides...),
			Replace:       append([]string(nil), input.Replace...),
			Clean:         input.Clean,
			Start:         input.Start,
		},
		RunOptions: input.RunOptions,
	}, newProviderInstallRunBackendV1())
}
