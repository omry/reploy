package deploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestStateV1CanonicalRoundTrip(t *testing.T) {
	state := StateV1{
		Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t),
		Overlay: EmptyRequestOverlayV1(),
	}
	content, err := EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != StateSchemaV1 || decoded.Current != nil || decoded.Deployment != nil || decoded.Overlay.Schema != RequestOverlaySchemaV1 {
		t.Fatalf("decoded state = %#v", decoded)
	}
	if _, err := DecodeStateV1(append([]byte(" "), content...)); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("noncanonical state error = %v", err)
	}
}

func TestStateV1SeparatesCanonicalInstallationFromEnvironmentInputs(t *testing.T) {
	state := StateV1{
		Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t),
		Overlay: EmptyRequestOverlayV1(),
		Deployment: &DeploymentStateV1{
			Schema: DeploymentStateSchemaV1,
			Installation: InstallationStateV1{
				Schema: InstallationSchemaV1, Status: InstallationStatusReady,
				TargetDir: "/opt/demo", Scope: "system", Service: "demo",
				UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo-1",
				ContainerName: "demo", NetworkName: "demo", Ports: []InstallationPortBindingV1{
					{Name: "admin", HostBind: "127.0.0.1", HostPort: "19001", ContainerPort: "9001"},
					{Name: "http", HostBind: "127.0.0.1", HostPort: "19000", ContainerPort: "9000"},
				},
			},
		},
	}
	content, err := EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Deployment == nil || decoded.Deployment.Installation.TargetDir != "/opt/demo" {
		t.Fatalf("decoded deployment = %#v", decoded.Deployment)
	}

	state.Deployment.Installation.Ports[0], state.Deployment.Installation.Ports[1] = state.Deployment.Installation.Ports[1], state.Deployment.Installation.Ports[0]
	if _, err := EncodeStateV1(state); err == nil || !strings.Contains(err.Error(), "sorted by name") {
		t.Fatalf("unsorted installation ports error = %v", err)
	}
}

func stateV1TestBlueprint(t *testing.T) blueprint.ResolvedDocumentV1 {
	t.Helper()
	payload, err := blueprint.EncodeResolvedDocumentV1(blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{stateV1TestPlatform(t)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func stateV1TestPlatform(t *testing.T) blueprint.Platform {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	return platform
}

func TestDecodeStateV1RejectsPrototypeWithoutDecodingFields(t *testing.T) {
	legacy := []byte(`{"schema_version":1,"bundle":{"roots":"not-an-array"},"materialization":{"bundles":"secret"}}`)
	_, err := DecodeStateV1(legacy)
	if !errors.Is(err, ErrLegacyStateUnsupported) || !strings.Contains(err.Error(), "recreate") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("legacy state error = %v", err)
	}
}

func TestDecodeStateV1RejectsUnknownFields(t *testing.T) {
	content := []byte(`{"current":null,"extra":true,"overlay":{"direct_packages":[],"schema":"overlay-v1","selected_options":[]},"schema":"state-v1"}`)
	if _, err := DecodeStateV1(content); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestDecodeStateV1RequiresResolvedBlueprint(t *testing.T) {
	content := []byte(`{"current":null,"overlay":{"direct_packages":[],"schema":"overlay-v1","selected_options":[]},"schema":"state-v1"}`)
	if _, err := DecodeStateV1(content); err == nil || !strings.Contains(err.Error(), "blueprint") {
		t.Fatalf("missing blueprint error = %v", err)
	}
}

func TestStateV1RequiresPlatformDeclaredByResolvedBlueprint(t *testing.T) {
	state := StateV1{
		Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t),
		Overlay: EmptyRequestOverlayV1(),
	}
	state.Platform, _ = blueprint.ParsePlatform("linux/arm64")
	if _, err := EncodeStateV1(state); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared platform error = %v", err)
	}
}

func TestDecodeStateV1DistinguishesUnknownVersionedSchema(t *testing.T) {
	_, err := DecodeStateV1([]byte(`{"current":null,"overlay":{},"schema":"state-v2"}`))
	if err == nil || errors.Is(err, ErrLegacyStateUnsupported) || !strings.Contains(err.Error(), "state-v2") {
		t.Fatalf("unknown schema error = %v", err)
	}
}
