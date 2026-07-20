package dockerdeploy

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func TestPlanProviderInstallationV1UsesLockedBlueprintAndDestinationReference(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Schema: 1, Version: "1.0.0"},
		Environment: blueprint.Environment{
			ID: "demo", ControlScript: "democtl",
			Components: map[string]blueprint.Component{"application": {Type: blueprint.ComponentTypePython}},
			Executables: map[string]blueprint.Executable{"server": {
				Component: "application", Binary: "demo", Order: blueprint.DefaultArgumentOrder,
			}},
			Commands: map[string]blueprint.Command{"serve": {
				Executable: "server", Argv: []string{"serve"}, Order: blueprint.DefaultArgumentOrder,
			}},
			Workload: &blueprint.Workload{Command: "serve", Endpoints: map[string]blueprint.Endpoint{
				"http": {Scheme: "http", Port: 8080},
			}},
		},
		Docker: blueprint.Docker{Mounts: map[string]blueprint.DockerMount{}, Workload: &blueprint.DockerWorkload{
			Endpoints: map[string]blueprint.DockerEndpoint{
				"http": {
					Bind: blueprint.Bind{Address: "0.0.0.0"}, Publish: blueprint.Publication{Address: "127.0.0.1", Deployed: 18080},
					Endpoint: blueprint.Endpoint{Scheme: "http", Port: 8080},
				},
			},
		}},
	}
	references := fixedPublicationReferences(t, destinationDir, 0x91)
	plan, err := planProviderInstallationV1(t.Context(), providerInstallPlanningV1{
		SourceBuild: CurrentBuild{
			State: deploy.StateV1{Blueprint: testResolvedBlueprintV1(t, document)},
			Lock: deploy.BuildLockV1{Catalog: []providers.RealizedOutput{{
				SupplierComponent: "application", SupplierNode: "python/application", Name: "demo",
				Candidate: providers.ExecutableCandidate{InvocationPath: "/opt/demo"},
				Evidence:  providers.ExecutableEvidence{InvocationPath: "/opt/demo"},
			}}},
		},
		References: references,
		Input: providerInstallRunInputV1{
			SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
			Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1000, GID: 1000},
			Install: providerInstallOptionsV1{
				Scope: InstallScopeSystem, Service: "demo-service",
				SystemUser: "demo", SystemGroup: "demo", SystemUID: 991, SystemGID: 992,
				PortOverrides: []PortOverride{{Name: "http", HostPort: "19090"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := pathIdentityHash(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	resourceName := "demo-" + hash
	instanceID := "demo-service-" + hash
	wantInstallation := deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
		TargetDir: destinationDir, Scope: "system", Service: "demo-service",
		UnitPath: systemdPath(installSystemdUnitDir, "demo-service.service"), InstanceID: instanceID,
		ComposeProject: resourceName, ContainerName: resourceName, NetworkName: resourceName,
		Ports: []deploy.InstallationPortBindingV1{{Name: "http", HostBind: "127.0.0.1", HostPort: "19090", ContainerPort: "8080"}},
	}
	if !reflect.DeepEqual(plan.Installation, wantInstallation) {
		t.Fatalf("installation = %#v, want %#v", plan.Installation, wantInstallation)
	}
	if plan.Backend != installBackendLinuxSystemd || plan.Docker.Image != references.Generation || plan.Docker.RuntimeUser.DockerUser != "991:992" {
		t.Fatalf("provider installation plan = %#v", plan)
	}
	if !reflect.DeepEqual(plan.Docker.Workload.Argv, []string{"/opt/demo", "serve"}) {
		t.Fatalf("installed workload argv = %#v", plan.Docker.Workload.Argv)
	}
	if plan.Rendered.Environment["REPLOY_IMAGE"] != references.Generation || len(plan.Rendered.Compose) == 0 {
		t.Fatalf("rendered inputs = %#v", plan.Rendered)
	}
}

func TestPlanProviderInstallationV1RejectsReferenceForAnotherDestination(t *testing.T) {
	document := blueprint.Document{Blueprint: blueprint.Metadata{Schema: 1}, Environment: blueprint.Environment{ID: "demo", ControlScript: "democtl"}}
	_, err := planProviderInstallationV1(t.Context(), providerInstallPlanningV1{
		SourceBuild: CurrentBuild{State: deploy.StateV1{Blueprint: testResolvedBlueprintV1(t, document)}},
		References:  fixedPublicationReferences(t, t.TempDir(), 0x92),
		Input: providerInstallRunInputV1{
			SourceDeploymentDir: t.TempDir(), DestinationDeploymentDir: t.TempDir(),
			Runtime: blueprintRuntimeFixtureV1(), Install: providerInstallOptionsV1{Scope: InstallScopeUser},
		},
	})
	if err == nil {
		t.Fatal("expected destination-owned reference rejection")
	}
}

func blueprintRuntimeFixtureV1() StagedProviderBuildRuntimeV1 {
	return StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1000, GID: 1000}
}
