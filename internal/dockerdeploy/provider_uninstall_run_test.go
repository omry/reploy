package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestRunProviderUninstallAdmitsBeforeExecutionAndCompletes(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	if _, _, err := operation.SetInstallationStateV1(installedBuildPublicationInstallation(dir)); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	lease := new(deploy.ControlLeaseV1)
	backend := providerUninstallRunBackendV1{
		acquire: deploy.AcquireOperationLock,
		release: func(operation *deploy.OperationLock) error {
			order = append(order, "release")
			return operation.Unlock()
		},
		plan: func(input providerUninstallPlanningInputV1) (providerUninstallPlanV1, error) {
			order = append(order, "plan")
			return planProviderUninstallV1(input)
		},
		admit: func(_ context.Context, _ string, operation *deploy.OperationLock, input ControlAdmissionInputV1) (AdmittedControlV1, error) {
			order = append(order, "admit")
			if input.Operation != deploy.ControlOperationUninstallV1 || input.Mode != ControlAdmissionDrainV1 || input.GenerationReference == "" {
				t.Fatalf("admission input = %#v", input)
			}
			return AdmittedControlV1{Operation: operation, Marker: deploy.ControlMarkerV1{ID: "control-0000000000000001"}, Lease: lease}, nil
		},
		complete: func(operation *deploy.OperationLock, markerID string, gotLease *deploy.ControlLeaseV1) error {
			order = append(order, "complete")
			if markerID != "control-0000000000000001" || gotLease != lease {
				t.Fatalf("completion identity: marker=%q lease=%p", markerID, gotLease)
			}
			return operation.Unlock()
		},
		execute: func(_ context.Context, _ *deploy.OperationLock, plan providerUninstallPlanV1, _ RunOptions) error {
			order = append(order, "execute")
			if plan.Installation.Service != "demo" {
				t.Fatalf("uninstall plan = %#v", plan)
			}
			return nil
		},
	}
	err := runProviderUninstallV1(t.Context(), ProviderUninstallInputV1{
		DeploymentDir: dir, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
		ControlMode: ControlAdmissionDrainV1,
	}, backend)
	if err != nil {
		t.Fatalf("run uninstall: %v", err)
	}
	if got := strings.Join(order, ","); got != "plan,admit,execute,complete" {
		t.Fatalf("order = %s", got)
	}
}

func TestRunProviderUninstallRevalidatesStateAfterWaiting(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	installation := installedBuildPublicationInstallation(dir)
	if _, _, err := operation.SetInstallationStateV1(installation); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	executed := false
	lease := new(deploy.ControlLeaseV1)
	backend := providerUninstallRunBackendV1{
		acquire: deploy.AcquireOperationLock,
		release: func(operation *deploy.OperationLock) error { return operation.Unlock() },
		plan:    planProviderUninstallV1,
		admit: func(ctx context.Context, dir string, operation *deploy.OperationLock, _ ControlAdmissionInputV1) (AdmittedControlV1, error) {
			if err := operation.Unlock(); err != nil {
				return AdmittedControlV1{}, err
			}
			reacquired, err := deploy.AcquireOperationLock(ctx, dir)
			if err != nil {
				return AdmittedControlV1{}, err
			}
			installation.Status = deploy.InstallationStatusConfiguring
			if _, _, err := reacquired.SetInstallationStateV1(installation); err != nil {
				_ = reacquired.Unlock()
				return AdmittedControlV1{}, err
			}
			return AdmittedControlV1{Operation: reacquired, Marker: deploy.ControlMarkerV1{ID: "control-0000000000000002"}, Lease: lease}, nil
		},
		complete: func(operation *deploy.OperationLock, _ string, gotLease *deploy.ControlLeaseV1) error {
			if gotLease != lease {
				t.Fatalf("completed uninstall with lease %p, want %p", gotLease, lease)
			}
			return operation.Unlock()
		},
		execute: func(context.Context, *deploy.OperationLock, providerUninstallPlanV1, RunOptions) error {
			executed = true
			return nil
		},
	}
	err := runProviderUninstallV1(t.Context(), ProviderUninstallInputV1{
		DeploymentDir: dir, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, ControlMode: ControlAdmissionWaitV1,
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "state changed while uninstall was waiting") {
		t.Fatalf("state change error = %v", err)
	}
	if executed {
		t.Fatal("state change reached uninstall execution")
	}
}

func TestRunProviderUninstallConflictRecommendsWait(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	if _, _, err := operation.SetInstallationStateV1(installedBuildPublicationInstallation(dir)); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	backend := providerUninstallRunBackendV1{
		acquire: deploy.AcquireOperationLock,
		release: func(operation *deploy.OperationLock) error { return operation.Unlock() },
		plan:    planProviderUninstallV1,
		admit: func(_ context.Context, _ string, operation *deploy.OperationLock, _ ControlAdmissionInputV1) (AdmittedControlV1, error) {
			if err := operation.Unlock(); err != nil {
				return AdmittedControlV1{}, err
			}
			return AdmittedControlV1{}, deploy.ErrLiveRunConflict
		},
		complete: func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error {
			return errors.New("unexpected complete")
		},
		execute: func(context.Context, *deploy.OperationLock, providerUninstallPlanV1, RunOptions) error {
			return errors.New("unexpected execute")
		},
	}
	err := runProviderUninstallV1(t.Context(), ProviderUninstallInputV1{
		DeploymentDir: dir, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	}, backend)
	if !errors.Is(err, deploy.ErrLiveRunConflict) || !strings.Contains(err.Error(), "--wait") {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestRunProviderUninstallRemoveDirTransfersAdmissionOwnership(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	if _, _, err := operation.SetInstallationStateV1(installedBuildPublicationInstallation(dir)); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	completed := false
	removed := false
	lease := new(deploy.ControlLeaseV1)
	backend := providerUninstallRunBackendV1{
		acquire: deploy.AcquireOperationLock,
		release: func(operation *deploy.OperationLock) error { return operation.Unlock() },
		plan:    planProviderUninstallV1,
		admit: func(_ context.Context, _ string, operation *deploy.OperationLock, input ControlAdmissionInputV1) (AdmittedControlV1, error) {
			return AdmittedControlV1{Operation: operation, Marker: deploy.ControlMarkerV1{ID: "control-0000000000000003", Operation: input.Operation}, Lease: lease}, nil
		},
		complete: func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error {
			completed = true
			return errors.New("normal completion must not run")
		},
		execute: func(context.Context, *deploy.OperationLock, providerUninstallPlanV1, RunOptions) error { return nil },
		removeDeployment: func(_ context.Context, operation *deploy.OperationLock, markerID string, gotLease *deploy.ControlLeaseV1, plan providerUninstallPlanV1, _ RunOptions) error {
			removed = true
			if markerID != "control-0000000000000003" || gotLease != lease || !plan.RemoveDir {
				t.Fatalf("removal handoff: marker=%q lease=%p plan=%#v", markerID, gotLease, plan)
			}
			if err := operation.RequireHeld(); err != nil {
				t.Fatalf("removal did not own lock: %v", err)
			}
			return operation.Unlock()
		},
	}
	err := runProviderUninstallV1(t.Context(), ProviderUninstallInputV1{
		DeploymentDir: dir, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, RemoveDir: true,
	}, backend)
	if err != nil {
		t.Fatalf("remove-dir uninstall: %v", err)
	}
	if !removed || completed {
		t.Fatalf("removal handoff: removed=%v completed=%v", removed, completed)
	}
}
