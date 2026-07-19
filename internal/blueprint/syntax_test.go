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
environment:
  id: demo
  components:
    application:
      type: python
      requirements: [demo-server]
  paths:
    data:
      container: /data
      writable: true
      update: preserve
  executables:
    server:
      component: application
      binary: demo-server
  commands:
    serve:
      executable: server
      argv: [serve]
  workload:
    command: serve
    endpoints:
      http:
        scheme: http
        port: 8080
docker:
  image: python:3.13-slim
  mounts:
    data:
      extends: environment.paths.data
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
	if source.Environment.ID != "demo" || source.Docker.Image != "python:3.13-slim" {
		t.Fatalf("decoded source = %#v", source)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	_, err := Decode([]byte(strings.Replace(minimalBlueprint, "  id: demo\n", "  id: demo\n  surprise: true\n", 1)))
	if err == nil || !strings.Contains(err.Error(), "field surprise not found") {
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
