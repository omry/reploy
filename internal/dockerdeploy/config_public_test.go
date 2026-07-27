package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestAppCommandUsesCurrentGenerationWithoutProviderBuild(t *testing.T) {
	dir := appCommandStateDir(t)
	originalCommand := runCurrentAppCommand
	t.Cleanup(func() {
		runCurrentAppCommand = originalCommand
	})

	var order []string
	var commands []CurrentAppCommandRunInputV1
	runCurrentAppCommand = func(ctx context.Context, input CurrentAppCommandRunInputV1) error {
		if ctx == nil {
			t.Fatal("command context is nil")
		}
		commands = append(commands, input)
		order = append(order, "command")
		return nil
	}

	if err := AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"serve"}}); err != nil {
		t.Fatal(err)
	}
	if err := AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"serve"}, DeployedOnly: true}); err != nil {
		t.Fatal(err)
	}

	if len(commands) != 2 || commands[0].DeployedOnly || !commands[1].DeployedOnly {
		t.Fatalf("commands = %#v", commands)
	}
	if got, want := order, []string{"command", "command"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatch order = %#v, want %#v", got, want)
	}
}

func TestAppCommandRejectsInvalidRequestBeforeRuntimeExecution(t *testing.T) {
	dir := appCommandStateDir(t)
	originalCommand := runCurrentAppCommand
	t.Cleanup(func() { runCurrentAppCommand = originalCommand })
	runCurrentAppCommand = func(context.Context, CurrentAppCommandRunInputV1) error {
		t.Fatal("invalid app command reached runtime execution")
		return nil
	}
	if err := AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"unknown"}}); err == nil {
		t.Fatal("invalid command was accepted")
	}
}

func TestAppCommandNeverBuildsInstalledState(t *testing.T) {
	dir := appCommandStateDir(t)
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	state.Deployment = &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1, Installation: installedBuildPublicationInstallation(dir),
	}
	content, err = deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), content, 0o600); err != nil {
		t.Fatal(err)
	}

	originalCommand := runCurrentAppCommand
	t.Cleanup(func() {
		runCurrentAppCommand = originalCommand
	})
	runCurrentAppCommand = func(context.Context, CurrentAppCommandRunInputV1) error { return nil }

	if err := AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"serve"}}); err != nil {
		t.Fatal(err)
	}
}

func appCommandStateDir(t *testing.T) string {
	t.Helper()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	commands := commandTestDocument()
	document.Environment.ID = "demo"
	document.Environment.Commands = commands.Environment.Commands
	document.Environment.Components["application"] = commands.Environment.Components["application"]
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	current.State.Blueprint = resolved
	content, err := deploy.EncodeStateV1(current.State)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, StateFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
