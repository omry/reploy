package dockerdeploy

import (
	"context"
	"fmt"
	"os"

	"github.com/omry/reploy/internal/blueprint"
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
	result                   *ProviderInstallResultV1
}

type providerInstallEntryBackendV1 struct {
	run          func(context.Context, providerInstallRunInputV1, providerInstallRunBackend) (deploy.StateV1, error)
	effectiveUID func() int
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
	scope, err := validateProviderInstallEntryV1(input.Scope, input.Runtime.Host, input.Service, backend.effectiveUID)
	if err != nil {
		return deploy.StateV1{}, err
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
		result:     input.result,
	}, newProviderInstallRunBackendV1())
}

// Privilege comes from the executing process, never the application runtime
// account or a sudo/login environment variable. Direct install uses this same
// gate before fetching or staging its source.
func validateProviderInstallEntryV1(scopeInput InstallScope, host blueprint.HostOS, service string, effectiveUID func() int) (InstallScope, error) {
	scope, err := ParseInstallScope(string(scopeInput))
	if err != nil {
		return "", err
	}
	platform, err := installHostPlatformV1(host)
	if err != nil {
		return "", err
	}
	if err := validateInstallScopeForBackend(scope, platform.installBackendForScope(scope), platform); err != nil {
		return "", err
	}
	if service != "" && !validServiceName(service) {
		return "", fmt.Errorf("--service contains unsupported characters: %s", service)
	}
	if effectiveUID == nil {
		effectiveUID = os.Geteuid
	}
	if scope == InstallScopeSystem && effectiveUID() != 0 {
		return "", fmt.Errorf("--scope system requires root privileges; rerun the original install command with sudo")
	}
	return scope, nil
}

// RunProviderInstallResultV1 installs one staged deployment and returns the
// facts needed to present the completed operation.
func RunProviderInstallResultV1(ctx context.Context, input ProviderInstallInputV1) (ProviderInstallResultV1, error) {
	var result ProviderInstallResultV1
	input.result = &result
	state, err := RunProviderInstallV1(ctx, input)
	result.State = state
	return result, err
}
