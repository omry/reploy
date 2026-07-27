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
  components:
    base:
      image: python:3.13-slim
    application:
      type: python
      requirements: [demo-server]
      executables:
        server:
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
	if source.Environment.ID != "demo" || source.Environment.Components["base"].Image != "python:3.13-slim" || len(source.Blueprint.Compatibility.Platforms) != 2 {
		t.Fatalf("decoded source = %#v", source)
	}
}

func TestDecodeRejectsRemovedWorkspaceNode(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  components:\n", "  workspace:\n    root: ..\n    packages:\n      python:\n        demo-server: server\n  components:\n", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field workspace not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsTranslationsAlias(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  components:\n", "  translations: {}\n  components:\n", 1)
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

func TestDecodeRejectsExecutableComponentField(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "          binary: demo-server", "          component: application\n          binary: demo-server", 1)
	_, err := Decode([]byte(value))
	if err == nil || !strings.Contains(err.Error(), "field component not found") {
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
