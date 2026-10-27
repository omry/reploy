package dockerdeploy

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestIdentityRolesV1ProviderViewsPreserveAssignmentAndCommands(t *testing.T) {
	source, err := blueprint.Decode([]byte(`
blueprint: {schema: 1, version: 0.1.0, compatibility: {platforms: [linux/amd64]}}
environment:
  id: roles
  base: {image: python:3.13-slim}
  runtime:
    identity_policy:
      schema: 1
      users: {worker: 200}
      groups: {worker: 200}
      roles: {app: {user: worker}, background: {user: worker}}
  applications:
    application:
      packages: {python: {requirements: [demo-server]}}
      executables:
        server: {source: python, binary: demo-server, role: app}
        unused: {source: python, binary: demo-server, role: background}
  commands:
    serve: {executable: application.server, argv: [serve]}
    inspect: {executable: application.unused, argv: [inspect]}
`))
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	before, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	views := map[string]func(blueprint.Document) (blueprint.Document, error){
		"ordinary provider view": providerDocumentWithoutApplicationToolsV1,
		"package addition reconstruction": func(d blueprint.Document) (blueprint.Document, error) {
			intent := deploy.EmptyPackageOverrideIntentV1(d.Environment.ID)
			intent.Additions = []deploy.PackageAdditionIntentV1{{Provider: "os", Requirement: "curl"}}
			return documentWithPackageAdditionsV1(d, intent)
		},
	}
	for name, view := range views {
		t.Run(name, func(t *testing.T) {
			// Start every view from its own canonical copy, as deployment does.
			d, err := blueprint.DecodeResolvedDocumentV1(before)
			if err != nil {
				t.Fatal(err)
			}
			d, err = view(d)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d.Environment.Runtime.IdentityPolicy, document.Environment.Runtime.IdentityPolicy) || !reflect.DeepEqual(d.Environment.Commands, document.Environment.Commands) {
				t.Fatal("provider view changed portable policy or command inventory")
			}
			for _, command := range d.Environment.Commands {
				owner, profile, ok := d.Environment.ResolveExecutableProfile(command.Executable)
				_, original, _ := document.Environment.ResolveExecutableProfile(command.Executable)
				if !ok || profile.Role != original.Role || !reflect.DeepEqual(profile, d.Environment.Components[owner].Executables[command.Executable[len("application."):]]) {
					t.Fatalf("view lost executable role: %q %#v", command.Executable, profile)
				}
			}
			if _, err := blueprint.EncodeResolvedDocumentV1(d); err != nil {
				t.Fatal(err)
			}
		})
	}
	after, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil || after != before {
		t.Fatalf("provider view mutated original authority: %v", err)
	}
}
