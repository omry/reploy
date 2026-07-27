package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func newProviderUninstallRunBackendV1() providerUninstallRunBackendV1 {
	return providerUninstallRunBackendV1{
		acquire: deploy.AcquireOperationLock,
		release: func(operation *deploy.OperationLock) error {
			return operation.Unlock()
		},
		plan:             planProviderUninstallV1,
		admit:            AdmitControlOperationV1,
		complete:         CompleteControlAdmissionV1,
		execute:          executeProviderUninstallV1,
		removeDeployment: removeProviderUninstallDeploymentV1,
	}
}

func executeProviderUninstallV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	plan providerUninstallPlanV1,
	options RunOptions,
) error {
	return executeProviderUninstallWithV1(ctx, operation, plan, options, executeProviderUninstallHostV1)
}

func executeProviderUninstallWithV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	plan providerUninstallPlanV1,
	options RunOptions,
	cleanupHost func(context.Context, providerUninstallPlanV1, RunOptions) error,
) error {
	if ctx == nil {
		return fmt.Errorf("execute provider uninstall requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if operation == nil {
		return fmt.Errorf("execute provider uninstall requires the operation lock")
	}
	if err := operation.RequireHeld(); err != nil {
		return err
	}
	if cleanupHost == nil {
		return fmt.Errorf("execute provider uninstall requires host cleanup")
	}
	if err := cleanupHost(ctx, plan, options); err != nil {
		return err
	}
	if !plan.RemoveDir {
		state, changed, err := operation.ClearInstallationStateV1(plan.Installation)
		if err != nil {
			return fmt.Errorf("mark provider deployment staged after uninstall: %w", err)
		}
		if !changed || state.Deployment != nil {
			return fmt.Errorf("provider uninstall did not clear installed state")
		}
		document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
		if err != nil {
			return fmt.Errorf("decode retained staged blueprint after uninstall: %w", err)
		}
		if _, err := syncStagedControlSurfaceV1(plan.Installation.TargetDir, document); err != nil {
			return fmt.Errorf("generate retained staged control surface after uninstall: %w", err)
		}
		if options.Stdout != nil {
			fmt.Fprintf(options.Stdout, "uninstalled service: %s\n", plan.Installation.Service)
		}
	}
	return nil
}
