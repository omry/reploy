package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/blueprint"
)

type ProviderUninstallRecoveryInputV1 struct {
	RequestedDir string
	Service      string
	Runtime      StagedProviderBuildRuntimeV1
	ControlMode  ControlAdmissionModeV1
	RemoveDir    bool
	RunOptions   RunOptions
}

type providerUninstallRecoveryPlanV1 struct {
	TargetDir          string
	Service            string
	UnitPath           string
	ComposeProject     string
	PendingUnitRemoval bool
}

type providerUninstallRecoveryBackendV1 struct {
	unitDir string
	apply   func(context.Context, providerUninstallRecoveryPlanV1, RunOptions) error
}

type providerUninstallRecoveryApplyBackendV1 struct {
	lookPath            func(string) (string, error)
	runHost             func(string, ...string) error
	removeDockerProject func(context.Context, string, time.Duration) error
	rename              func(string, string) error
	remove              func(string) error
}

// RecoverMissingProviderUninstallV1 removes a Linux system installation whose
// deployment directory is gone. The root-owned Reploy systemd unit is the only
// recovery authority; existing or corrupt deployment state never falls back to
// this path.
func RecoverMissingProviderUninstallV1(ctx context.Context, input ProviderUninstallRecoveryInputV1) error {
	return recoverMissingProviderUninstallV1(ctx, input, providerUninstallRecoveryBackendV1{
		unitDir: uninstallSystemdUnitDir,
		apply:   applyProviderUninstallRecovery,
	})
}

func recoverMissingProviderUninstallV1(ctx context.Context, input ProviderUninstallRecoveryInputV1, backend providerUninstallRecoveryBackendV1) error {
	if ctx == nil {
		return fmt.Errorf("provider uninstall recovery requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.Runtime.Host != blueprint.HostLinux {
		return fmt.Errorf("service-name recovery is supported only for Linux systemd installations")
	}
	service := strings.TrimSpace(input.Service)
	if service == "" || !validServiceName(service) {
		return fmt.Errorf("provider uninstall recovery requires a valid service name")
	}
	if backend.unitDir == "" || backend.apply == nil {
		return fmt.Errorf("provider uninstall recovery requires a complete backend")
	}
	unitPath := filepath.Join(backend.unitDir, service+".service")
	unitSourcePath := unitPath
	pendingUnitRemoval := false
	info, err := os.Lstat(unitSourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			unitSourcePath = unitPath + ".reploy-uninstall-pending"
			info, err = os.Lstat(unitSourcePath)
			if os.IsNotExist(err) {
				return fmt.Errorf("Reploy service unit not found for %q at %s; run `reploy services list`", service, unitPath)
			}
			pendingUnitRemoval = err == nil
		}
		if err != nil {
			return fmt.Errorf("inspect Reploy service unit for recovery: %w", err)
		}
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Reploy service unit must be a regular file: %s", unitSourcePath)
	}
	content, err := os.ReadFile(unitSourcePath)
	if err != nil {
		return fmt.Errorf("read Reploy service unit for recovery: %w", err)
	}
	managed := false
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "# Managed-By: reploy" {
			managed = true
			break
		}
	}
	if !managed {
		return fmt.Errorf("service unit %s is not managed by Reploy", unitSourcePath)
	}
	serviceRecord, _ := parseReploySystemdService(string(content))
	if serviceRecord.ServiceName != service {
		return fmt.Errorf("service unit %s records service %q, not %q", unitSourcePath, serviceRecord.ServiceName, service)
	}
	target := strings.TrimSpace(serviceRecord.TargetDir)
	if target == "" || !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return fmt.Errorf("Reploy service unit %s does not record a valid absolute target directory", unitSourcePath)
	}
	if input.RequestedDir != "" {
		requested, err := filepath.Abs(input.RequestedDir)
		if err != nil {
			return fmt.Errorf("resolve requested uninstall recovery directory: %w", err)
		}
		requested = filepath.Clean(requested)
		if requested != target {
			return fmt.Errorf("service unit %s records target %q, not requested directory %q", unitSourcePath, target, requested)
		}
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("installed target %s still exists; recovery by service name is only for a deleted directory", target)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect recorded target for uninstall recovery: %w", err)
	}
	project := strings.TrimSpace(serviceRecord.ComposeProject)
	if project == "" || dockerNameSlug(project, "") != project {
		return fmt.Errorf("Reploy service unit %s does not record a valid Compose project", unitSourcePath)
	}
	return backend.apply(ctx, providerUninstallRecoveryPlanV1{
		TargetDir: target, Service: service, UnitPath: unitPath, ComposeProject: project,
		PendingUnitRemoval: pendingUnitRemoval,
	}, input.RunOptions)
}

func applyProviderUninstallRecovery(ctx context.Context, plan providerUninstallRecoveryPlanV1, options RunOptions) error {
	return applyProviderUninstallRecoveryV1(ctx, plan, options, providerUninstallRecoveryApplyBackendV1{
		lookPath:            uninstallLookPath,
		runHost:             uninstallRunCommand,
		removeDockerProject: removeDockerComposeProjectByLabelV1,
		rename:              os.Rename,
		remove:              uninstallRemove,
	})
}

func applyProviderUninstallRecoveryV1(ctx context.Context, plan providerUninstallRecoveryPlanV1, options RunOptions, backend providerUninstallRecoveryApplyBackendV1) error {
	if ctx == nil {
		return fmt.Errorf("apply provider uninstall recovery requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend.lookPath == nil || backend.runHost == nil || backend.removeDockerProject == nil || backend.rename == nil || backend.remove == nil {
		return fmt.Errorf("apply provider uninstall recovery requires a complete backend")
	}
	systemctl, err := backend.lookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl command not found: %w", err)
	}
	tombstone := plan.UnitPath + ".reploy-uninstall-pending"
	if plan.PendingUnitRemoval {
		if info, err := os.Lstat(tombstone); err != nil {
			return fmt.Errorf("inspect pending recovered systemd unit removal: %w", err)
		} else if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("pending recovered systemd unit must be a regular file: %s", tombstone)
		}
		if err := backend.runHost(systemctl, "daemon-reload"); err != nil {
			restoreErr := backend.rename(tombstone, plan.UnitPath)
			return errors.Join(
				fmt.Errorf("systemctl daemon-reload: %w", err),
				providerUninstallRecoveryRestoreErrorV1(plan.UnitPath, tombstone, restoreErr),
			)
		}
		if err := backend.remove(tombstone); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf(
				"remove pending recovered systemd unit: %w; verified managed unit retained at %s; retry the same uninstall command",
				err,
				tombstone,
			)
		}
		if options.Stdout != nil {
			fmt.Fprintf(options.Stdout, "uninstalled service: %s\n", plan.Service)
		}
		return nil
	}
	if _, err := os.Lstat(tombstone); err == nil {
		return fmt.Errorf("pending recovered systemd unit removal already exists: %s", tombstone)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect pending recovered systemd unit removal: %w", err)
	}
	if err := backend.runHost(systemctl, "stop", plan.Service+".service"); err != nil {
		return fmt.Errorf("systemctl stop %s.service: %w", plan.Service, err)
	}
	if err := backend.removeDockerProject(ctx, plan.ComposeProject, options.DockerPreflightTimeout); err != nil {
		return fmt.Errorf("clean recovered Docker Compose project %q: %w", plan.ComposeProject, err)
	}
	if err := backend.runHost(systemctl, "disable", plan.Service+".service"); err != nil {
		return fmt.Errorf("systemctl disable %s.service: %w", plan.Service, err)
	}
	if err := backend.rename(plan.UnitPath, tombstone); err != nil {
		return fmt.Errorf("quarantine recovered systemd unit: %w", err)
	}
	if err := backend.runHost(systemctl, "daemon-reload"); err != nil {
		restoreErr := backend.rename(tombstone, plan.UnitPath)
		return errors.Join(
			fmt.Errorf("systemctl daemon-reload: %w", err),
			providerUninstallRecoveryRestoreErrorV1(plan.UnitPath, tombstone, restoreErr),
		)
	}
	if err := backend.remove(tombstone); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf(
			"remove recovered systemd unit: %w; verified managed unit retained at %s; retry the same uninstall command",
			err,
			tombstone,
		)
	}
	if options.Stdout != nil {
		fmt.Fprintf(options.Stdout, "uninstalled service: %s\n", plan.Service)
	}
	return nil
}

func providerUninstallRecoveryRestoreErrorV1(unitPath string, tombstone string, err error) error {
	if err == nil {
		return fmt.Errorf(
			"uninstall remains incomplete; restored verified managed unit at %s; retry the same uninstall command",
			unitPath,
		)
	}
	return fmt.Errorf(
		"uninstall remains incomplete; restore verified managed unit from %s to %s: %w",
		tombstone,
		unitPath,
		err,
	)
}
