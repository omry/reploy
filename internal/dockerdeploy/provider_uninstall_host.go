package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

type providerUninstallHostPlanV1 struct {
	TargetDir              string
	ServiceName            string
	UnitPath               string
	ComposeProject         string
	Backend                installBackend
	DockerPreflightTimeout time.Duration
}

type providerUninstallHostBackendV1 struct {
	apply func(providerUninstallHostPlanV1, io.Writer) error
}

func executeProviderUninstallHostV1(ctx context.Context, plan providerUninstallPlanV1, options RunOptions) error {
	return executeProviderUninstallHostWithV1(ctx, plan, options, providerUninstallHostBackendV1{apply: applyProviderUninstallHostPlanV1})
}

func executeProviderUninstallHostWithV1(
	ctx context.Context,
	plan providerUninstallPlanV1,
	options RunOptions,
	backend providerUninstallHostBackendV1,
) error {
	if ctx == nil {
		return fmt.Errorf("execute provider uninstall host cleanup requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend.apply == nil {
		return fmt.Errorf("execute provider uninstall host cleanup requires a complete backend")
	}
	installation := plan.Installation
	hostPlan := providerUninstallHostPlanV1{
		TargetDir:   installation.TargetDir,
		ServiceName: installation.Service, UnitPath: installation.UnitPath,
		ComposeProject: installation.ComposeProject, Backend: plan.Backend,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	}
	if err := backend.apply(hostPlan, options.Stdout); err != nil {
		return fmt.Errorf("provider uninstall host cleanup: %w", err)
	}
	return nil
}

func applyProviderUninstallHostPlanV1(plan providerUninstallHostPlanV1, _ io.Writer) error {
	if isDockerManagedInstallBackend(plan.Backend) {
		return runProviderUninstallComposeCleanupV1(plan)
	}
	if plan.Backend != installBackendLinuxSystemd {
		return currentHostPlatform().unsupportedPersistentInstallError("uninstall")
	}
	systemctlBin, err := uninstallLookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl command not found: %w", err)
	}
	_, unitErr := os.Lstat(plan.UnitPath)
	unitMissing := os.IsNotExist(unitErr)
	if unitErr != nil && !unitMissing {
		return fmt.Errorf("inspect systemd unit: %w", unitErr)
	}
	if unitMissing {
		if err := runProviderUninstallComposeCleanupV1(plan); err != nil {
			return err
		}
		if err := uninstallRunCommand(systemctlBin, "daemon-reload"); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w", err)
		}
		return nil
	}
	if err := uninstallRunCommand(systemctlBin, "stop", plan.ServiceName+".service"); err != nil {
		return fmt.Errorf("systemctl stop %s.service: %w", plan.ServiceName, err)
	}
	if err := runProviderUninstallComposeCleanupV1(plan); err != nil {
		return err
	}
	if err := uninstallRunCommand(systemctlBin, "disable", plan.ServiceName+".service"); err != nil {
		return fmt.Errorf("systemctl disable %s.service: %w", plan.ServiceName, err)
	}
	if err := uninstallRemove(plan.UnitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove systemd unit: %w", err)
	}
	if err := uninstallRunCommand(systemctlBin, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	return nil
}

func runProviderUninstallComposeCleanupV1(plan providerUninstallHostPlanV1) error {
	spec := composeCommandWithProject(plan.TargetDir, plan.ComposeProject, "down", "--remove-orphans")
	if err := uninstallRunDockerCommand(spec, plan.DockerPreflightTimeout); err != nil {
		return fmt.Errorf("Docker Compose cleanup: %w", err)
	}
	return nil
}
