package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestPlanProviderUninstallUsesLockedInstalledState(t *testing.T) {
	dir := t.TempDir()
	operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, dir)
	installation := installedBuildPublicationInstallation(dir)
	if _, _, err := operation.SetInstallationStateV1(installation); err != nil {
		t.Fatal(err)
	}

	plan, err := planProviderUninstallV1(providerUninstallPlanningInputV1{
		Operation: operation, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
		Service: "demo", RemoveDir: true,
	})
	if err != nil {
		t.Fatalf("plan uninstall: %v", err)
	}
	if plan.Environment != "demo" || plan.GenerationReference != current.Generation.Reference || plan.Backend != installBackendLinuxSystemd || !plan.RemoveDir {
		t.Fatalf("plan identity = %#v", plan)
	}
	if !reflect.DeepEqual(plan.Installation, installation) {
		t.Fatalf("installation = %#v, want %#v", plan.Installation, installation)
	}
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || !reflect.DeepEqual(state, plan.State) {
		t.Fatalf("planning changed state: found=%v err=%v state=%#v", found, err, state)
	}
}

func TestPlanProviderUninstallAcceptsConfiguringAndUserBackend(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	installation := installedBuildPublicationInstallation(dir)
	installation.Status = deploy.InstallationStatusConfiguring
	installation.Scope = string(InstallScopeUser)
	installation.UnitPath = ""
	if _, _, err := operation.SetInstallationStateV1(installation); err != nil {
		t.Fatal(err)
	}
	plan, err := planProviderUninstallV1(providerUninstallPlanningInputV1{
		Operation: operation, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	})
	if err != nil {
		t.Fatalf("plan configuring uninstall: %v", err)
	}
	if plan.Backend != installBackendDockerManaged || plan.Installation.Status != deploy.InstallationStatusConfiguring {
		t.Fatalf("configuring plan = %#v", plan)
	}
}

func TestPlanProviderUninstallRejectsNonInstalledAndMismatchedService(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	if _, err := planProviderUninstallV1(providerUninstallPlanningInputV1{
		Operation: operation, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	}); err == nil || !strings.Contains(err.Error(), "installed state-v1") {
		t.Fatalf("non-installed error = %v", err)
	}
	if _, _, err := operation.SetInstallationStateV1(installedBuildPublicationInstallation(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := planProviderUninstallV1(providerUninstallPlanningInputV1{
		Operation: operation, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, Service: "other",
	}); err == nil || !strings.Contains(err.Error(), "does not match installed service") {
		t.Fatalf("service mismatch error = %v", err)
	}
}

func TestPlanProviderUninstallRejectsStateClaimingAnotherTarget(t *testing.T) {
	dir := t.TempDir()
	operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, dir)
	state := current.State
	state.Deployment = &deploy.DeploymentStateV1{
		Schema:       deploy.DeploymentStateSchemaV1,
		Installation: installedBuildPublicationInstallation(t.TempDir()),
	}
	expected := *current.State.Current
	if err := operation.CommitStateV1(&expected, state); err != nil {
		t.Fatal(err)
	}
	if _, err := planProviderUninstallV1(providerUninstallPlanningInputV1{
		Operation: operation, DeploymentDir: dir,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	}); err == nil || !strings.Contains(err.Error(), "does not match locked deployment") {
		t.Fatalf("target mismatch error = %v", err)
	}
}
