package blueprint

import (
	"strings"
	"testing"
)

const minimalBlueprint = `
blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=NEXT"
  compatibility:
    platforms: [linux/amd64, linux/arm64]
environment:
  id: demo
  base:
    image: python:3.13-slim
  applications:
    application:
      packages:
        python:
          requirements: [demo-server]
      executables:
        server:
          source: python
          binary: demo-server
  mounts:
    data:
      target: /data
      writable: true
      update_policy: preserve
  commands:
    serve:
      executable: application.server
      argv: [serve]
  workload:
    command: serve
    endpoints:
      http:
        scheme: http
        port: 8080
docker:
  mounts:
    data:
      extends: environment.mounts.data
      mode: managed-bind
      source: data
  workload:
    endpoints:
      http:
        extends: environment.workload.endpoints.http
        bind: {address: 0.0.0.0}
        publish: {address: 127.0.0.1, staging: 18080, deployed: 8080}
`

func TestDecodeAcceptsEnvironmentSchema(t *testing.T) {
	source, err := Decode([]byte(minimalBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	if source.Environment.ID != "demo" || source.Environment.Base.Image != "python:3.13-slim" || len(source.Blueprint.Compatibility.Platforms) != 2 {
		t.Fatalf("decoded source = %#v", source)
	}
}

func TestDecodeUsesSystemAccountTerminology(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  base:\n", "  install:\n    system:\n      account:\n        user: demo\n        group: demo\n        on_missing: create\n  base:\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	if source.Environment.Install.System.Account != (SystemAccountSyntax{User: "demo", Group: "demo", OnMissing: "create"}) {
		t.Fatalf("system account = %#v", source.Environment.Install.System.Account)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if document.Environment.Install.System.Account != (SystemAccount{User: "demo", Group: "demo", OnMissing: "create"}) {
		t.Fatalf("resolved system account = %#v", document.Environment.Install.System.Account)
	}

	legacy := strings.Replace(value, "      account:\n", "      run_as:\n", 1)
	if _, err := Decode([]byte(legacy)); err == nil || !strings.Contains(err.Error(), "field run_as not found") {
		t.Fatalf("legacy system run_as error = %v", err)
	}
}

func TestDecodeRejectsRemovedWorkspaceNode(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  base:\n", "  workspace:\n    root: ..\n    packages:\n      python:\n        demo-server: server\n  base:\n", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field workspace not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsTranslationsAlias(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  base:\n", "  translations: {}\n  base:\n", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field translations not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsEnvironmentExecutablesAlias(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  commands:\n", "  executables: {}\n  commands:\n", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field executables not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsCommandMountAuthorityExpansion(t *testing.T) {
	value := strings.Replace(minimalBlueprint,
		"    serve:\n      executable: application.server\n      argv: [serve]\n",
		"    serve:\n      executable: application.server\n      argv: [serve]\n      mounts:\n        data:\n          writable: true\n          target: /other\n", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field target not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsExecutableComponentField(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "          binary: demo-server", "          component: application\n          binary: demo-server", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field component not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsUnreleasedComponentsShape(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  applications:\n", "  components: {}\n  applications:\n", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field components not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsLegacyEnvironmentMountFields(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "paths", old: "  mounts:\n", new: "  paths:\n", want: "field paths not found"},
		{name: "container", old: "      target: /data\n", new: "      container: /data\n", want: "field container not found"},
		{name: "update", old: "      update_policy: preserve\n", new: "      update: preserve\n", want: "field update not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode([]byte(strings.Replace(minimalBlueprint, tt.old, tt.new, 1)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestDecodeRejectsRemovedAdditionalDockerMountRoots(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "docker:\n", "docker:\n  additional_mount_roots: [/srv/demo]\n", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field additional_mount_roots not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	_, err := Decode([]byte(strings.Replace(minimalBlueprint, "  id: demo\n", "  id: demo\n  surprise: true\n", 1)))
	if err == nil || !strings.Contains(err.Error(), "field surprise not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsDockerImage(t *testing.T) {
	_, err := Decode([]byte(strings.Replace(minimalBlueprint, "docker:\n", "docker:\n  image: python:3.13-slim\n", 1)))
	if err == nil || !strings.Contains(err.Error(), "field image not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsLegacyAppShape(t *testing.T) {
	_, err := Decode([]byte(minimalBlueprint + "app:\n  id: legacy\n"))
	if err == nil || !strings.Contains(err.Error(), "field app not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsUnsupportedSchema(t *testing.T) {
	_, err := Decode([]byte(strings.Replace(minimalBlueprint, "schema: 1", "schema: 2", 1)))
	if err == nil || !strings.Contains(err.Error(), "blueprint.schema must be 1") {
		t.Fatalf("error = %v", err)
	}
}
