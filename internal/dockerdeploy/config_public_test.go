package dockerdeploy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestAppCommandEnsuresStagedProviderBuildBeforeExecution(t *testing.T) {
	dir := appCommandStateDir(t)
	originalBuild := runAppCommandProviderBuild
	originalCommand := runCurrentAppCommand
	t.Cleanup(func() {
		runAppCommandProviderBuild = originalBuild
		runCurrentAppCommand = originalCommand
	})

	var order []string
	var progress bytes.Buffer
	var builds []ProviderBuildRunInputV1
	runAppCommandProviderBuild = func(ctx context.Context, input ProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
		if ctx == nil {
			t.Fatal("build context is nil")
		}
		builds = append(builds, input)
		order = append(order, "build")
		return LockedProviderBuildExecutionResultV1{}, nil
	}
	var commands []CurrentAppCommandRunInputV1
	runCurrentAppCommand = func(ctx context.Context, input CurrentAppCommandRunInputV1) error {
		if ctx == nil {
			t.Fatal("command context is nil")
		}
		commands = append(commands, input)
		order = append(order, "command")
		return nil
	}

	if err := AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"serve"}, Progress: &progress}); err != nil {
		t.Fatal(err)
	}
	if err := AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"serve"}, DeployedOnly: true}); err != nil {
		t.Fatal(err)
	}

	if len(builds) != 1 || builds[0].DeploymentDir != dir || !builds[0].Automatic || builds[0].NoCache || builds[0].ValidateLayers {
		t.Fatalf("implicit builds = %#v", builds)
	}
	if len(commands) != 2 || commands[0].DeployedOnly || !commands[1].DeployedOnly {
		t.Fatalf("commands = %#v", commands)
	}
	if got, want := order, []string{"build", "command", "command"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatch order = %#v, want %#v", got, want)
	}
	if progress.String() != "prepare current build\n" {
		t.Fatalf("automatic build progress = %q", progress.String())
	}
}

func TestAppCommandRejectsInvalidRequestBeforeImplicitBuild(t *testing.T) {
	dir := appCommandStateDir(t)
	originalBuild := runAppCommandProviderBuild
	t.Cleanup(func() { runAppCommandProviderBuild = originalBuild })
	runAppCommandProviderBuild = func(context.Context, ProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
		t.Fatal("invalid app command attempted an implicit build")
		return LockedProviderBuildExecutionResultV1{}, nil
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

	originalBuild := runAppCommandProviderBuild
	originalCommand := runCurrentAppCommand
	t.Cleanup(func() {
		runAppCommandProviderBuild = originalBuild
		runCurrentAppCommand = originalCommand
	})
	runAppCommandProviderBuild = func(context.Context, ProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
		t.Fatal("installed app command attempted an implicit build")
		return LockedProviderBuildExecutionResultV1{}, nil
	}
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
