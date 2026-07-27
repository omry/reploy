package dockerdeploy

import (
	"context"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestDirectProviderInstallStagesPrivatelyAndResolvesBlueprintTarget(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{ID: "demo"}}
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	installed := deploy.StateV1{Schema: deploy.StateSchemaV1}
	result, err := runDirectProviderInstallEntryV1(t.Context(), DirectProviderInstallInputV1{
		Pack: deploy.PackRef{Raw: "file:/demo"}, Runtime: blueprintRuntimeFixtureV1(),
		ControlMode: ControlAdmissionWaitV1,
		Scope:       InstallScopeSystem, Service: "demo-service", Replace: []string{"conf"}, Start: true,
	}, directProviderInstallEntryBackendV1{
		withSource: func(ctx context.Context, input directProviderInstallSourceInputV1, run func(context.Context, string) error) error {
			if input.Pack.Raw != "file:/demo" {
				t.Fatalf("staged pack = %#v", input.Pack)
			}
			return run(ctx, "/private/staging")
		},
		readState: func(context.Context, string) (deploy.StateV1, error) {
			return deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: resolved}, nil
		},
		roots: func(string) (installTargetRootsV1, error) {
			return installTargetRootsV1{UserHome: "/home/user", UserData: "/home/user/.local/share", UserLocalData: "/home/user/.local/share", SystemData: "/var/lib", ReployInstallRoot: "/opt"}, nil
		},
		install: func(_ context.Context, input ProviderInstallInputV1) (deploy.StateV1, error) {
			if input.SourceDeploymentDir != "/private/staging" || input.DestinationDeploymentDir != "/opt/demo" || input.ControlMode != ControlAdmissionWaitV1 || input.Scope != InstallScopeSystem || input.Service != "demo-service" || len(input.Replace) != 1 || !input.Start {
				t.Fatalf("direct install input = %#v", input)
			}
			if input.result == nil {
				t.Fatal("direct install did not request result details")
			}
			input.result.Environment = "demo"
			input.result.TargetDir = "/opt/demo"
			return installed, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != "/opt/demo" || result.State.Schema != deploy.StateSchemaV1 ||
		result.Install.Environment != "demo" || result.Install.TargetDir != "/opt/demo" ||
		result.Install.State.Schema != deploy.StateSchemaV1 {
		t.Fatalf("direct install result = %#v", result)
	}
}
