package deploy

import (
	"strings"
	"testing"
)

func TestParseDeploymentStateNormalizesAbsentOverlay(t *testing.T) {
	state, err := ParseDeploymentState([]byte(`{"schema_version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if state.Overlay.Schema != RequestOverlaySchemaV1 || state.Overlay.SelectedOptions == nil || state.Overlay.DirectPackages == nil {
		t.Fatalf("overlay = %#v", state.Overlay)
	}
}

func TestMarshalDeploymentStateAlwaysWritesCanonicalOverlay(t *testing.T) {
	content, err := MarshalDeploymentState(DeploymentState{SchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"overlay": {`, `"schema": "overlay-v1"`, `"selected_options": []`, `"direct_packages": []`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("state does not contain %s:\n%s", want, content)
		}
	}
	parsed, err := ParseDeploymentState(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Overlay.Schema != RequestOverlaySchemaV1 {
		t.Fatalf("parsed overlay = %#v", parsed.Overlay)
	}
}

func TestParseDeploymentStateRejectsNoncanonicalOverlay(t *testing.T) {
	content := []byte(`{
  "schema_version": 1,
  "overlay": {
    "schema": "overlay-v1",
    "selected_options": null,
    "direct_packages": []
  }
}`)
	if _, err := ParseDeploymentState(content); err == nil || !strings.Contains(err.Error(), "must be arrays") {
		t.Fatalf("error = %v", err)
	}
}
