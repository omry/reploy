package dockerdeploy

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestPlanCurrentRuntimeV1ReconstructsStagedPlanWithoutSystemLookup(t *testing.T) {
	current, _ := runtimeCurrentBuildFixture(t)
	dir := t.TempDir()
	lookups := 0
	result, err := planCurrentRuntimeV1(CurrentRuntimePlanInputV1{
		DeploymentDir: dir, Current: current,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1000, GID: 1001},
	}, currentRuntimePlanBackendV1{resolveSystemOwner: func(map[string]string) (resolvedInstallOwner, error) {
		lookups++
		return resolvedInstallOwner{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if lookups != 0 || result.Docker.Phase != blueprint.PhaseStaged || result.Docker.Scope != nil {
		t.Fatalf("staged plan = %#v, lookups=%d", result.Docker, lookups)
	}
	if result.Docker.Image != current.Generation.Reference || result.Docker.RuntimeUser.DockerUser != "1000:1001" {
		t.Fatalf("staged image/user = %q/%q", result.Docker.Image, result.Docker.RuntimeUser.DockerUser)
	}
}

func TestPlanCurrentRuntimeV1ReconstructsInstalledUserPlan(t *testing.T) {
	current, _ := runtimeCurrentBuildFixture(t)
	dir := t.TempDir()
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	scope := blueprint.InstallScopeUser
	expected, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir: dir, InstallTarget: dir, Phase: blueprint.PhaseInstalled, Scope: &scope,
		GeneratedImage: current.Generation.Reference, Host: blueprint.HostLinux, UID: 501, GID: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	installation := deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
		TargetDir: filepath.Clean(dir), Scope: string(scope), Service: "demo", InstanceID: "demo-instance",
		ComposeProject: expected.NetworkName, ContainerName: expected.ContainerName, NetworkName: expected.NetworkName,
		Ports: []deploy.InstallationPortBindingV1{},
	}
	current.State.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: installation}
	result, err := PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{
		DeploymentDir: dir, Current: current,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 501, GID: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Docker.Scope == nil || *result.Docker.Scope != scope || !reflect.DeepEqual(result.Docker, expected) {
		t.Fatalf("installed plan = %#v, want %#v", result.Docker, expected)
	}
	current.State.Deployment.Installation.Status = deploy.InstallationStatusConfiguring
	result, err = PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{
		DeploymentDir: dir, Current: current, AllowConfiguringRepair: true,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 501, GID: 20},
	})
	if err != nil || !reflect.DeepEqual(result.Docker, expected) {
		t.Fatalf("configuring repair plan = %#v, %v", result.Docker, err)
	}
}

func TestPlanCurrentRuntimeV1RejectsConfiguringAndDriftedInstall(t *testing.T) {
	current, _ := runtimeCurrentBuildFixture(t)
	dir := t.TempDir()
	current.State.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusConfiguring,
		TargetDir: filepath.Clean(dir), Scope: string(blueprint.InstallScopeUser), Service: "demo", InstanceID: "demo-instance",
		ComposeProject: "demo", ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
	}}
	input := CurrentRuntimePlanInputV1{
		DeploymentDir: dir, Current: current,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 501, GID: 20},
	}
	if _, err := PlanCurrentRuntimeV1(input); err == nil || !strings.Contains(err.Error(), "rerun `reploy install`") {
		t.Fatalf("configuring install error = %v", err)
	}
	input.Current.State.Deployment.Installation.Status = deploy.InstallationStatusReady
	if _, err := PlanCurrentRuntimeV1(input); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("drifted install error = %v", err)
	}
}

func TestPlanCurrentRuntimeV1ReportsSystemAccountLookupFailure(t *testing.T) {
	current, input := runtimeCurrentBuildFixture(t)
	dir := t.TempDir()
	document := input.Document
	document.Environment.Install.System.RunAs = blueprint.RunAs{User: "service", Group: "service", OnMissing: "fail"}
	var err error
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	current.State.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
		TargetDir: filepath.Clean(dir), Scope: string(blueprint.InstallScopeSystem), Service: "demo", InstanceID: "demo-instance",
		ComposeProject: "demo", ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
	}}
	want := errors.New("account disappeared")
	_, err = planCurrentRuntimeV1(CurrentRuntimePlanInputV1{
		DeploymentDir: dir, Current: current,
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux},
	}, currentRuntimePlanBackendV1{resolveSystemOwner: func(values map[string]string) (resolvedInstallOwner, error) {
		if values[reployInstallOwnerEnv] != "service:service" {
			t.Fatalf("owner values = %v", values)
		}
		return resolvedInstallOwner{}, want
	}})
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "resolve installed runtime account") {
		t.Fatalf("system account error = %v", err)
	}
}
