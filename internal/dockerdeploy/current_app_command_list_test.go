package dockerdeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestCurrentAppCommandListV1FiltersSortsAndDescribesForwarding(t *testing.T) {
	document := commandTestDocument()
	document.Environment.ID = "demo"
	document.Environment.Commands["hidden"] = blueprint.Command{
		Executable: "application.server", Trigger: []string{"hidden"}, Argv: []string{"hidden"}, Order: blueprint.DefaultArgumentOrder,
	}
	result := currentAppCommandListV1(document, false)
	want := []AppCommandListEntry{
		{Trigger: []string{"config", "show"}, Name: "special", ForwardArgs: true},
		{Trigger: []string{"serve"}, Name: "serve", ForwardArgs: true, ForwardFlags: []string{"--verbose"}},
	}
	if result.AppID != "demo" || !reflect.DeepEqual(result.Commands, want) {
		t.Fatalf("command list = %#v, want %#v", result, want)
	}
	deployed := currentAppCommandListV1(document, true)
	if len(deployed.Commands) != 1 || deployed.Commands[0].Name != "serve" {
		t.Fatalf("deployed command list = %#v", deployed)
	}
}

func TestAppCommandListStateV1ReadsBlueprintWithoutBuildAndRejectsUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	content, err := deploy.EncodeStateV1(current.State)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".reploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, StateFileName)
	if err := os.WriteFile(statePath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	result, err := AppCommandList(AppCommandListOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result.AppID != document.Environment.ID {
		t.Fatalf("app ID = %q, want %q", result.AppID, document.Environment.ID)
	}

	if err := os.WriteFile(statePath, []byte(`{"schema":"state-v2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = AppCommandList(AppCommandListOptions{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown state schema error = %v", err)
	}
}
