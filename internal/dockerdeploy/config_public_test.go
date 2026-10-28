package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestAppCommandForwardsNonterminalStdin(t *testing.T) {
	for _, name := range []string{"pipe", "redirected input", "redirected output", "empty pipe", "closed input", "execution error"} {
		t.Run(name, func(t *testing.T) {
			payload := []byte("scripted input\n")
			var input *os.File
			if name == "redirected input" {
				var err error
				input, err = os.CreateTemp(t.TempDir(), "stdin")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := input.Write(payload); err != nil {
					t.Fatal(err)
				}
				if _, err := input.Seek(0, io.SeekStart); err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				var writer *os.File
				input, writer, err = os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				if name == "empty pipe" || name == "closed input" {
					payload = nil
				}
				if _, err := writer.Write(payload); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				if name == "closed input" {
					if err := input.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			var captured, stderr bytes.Buffer
			var stdout io.Writer = &captured
			if name == "redirected output" {
				file, err := os.CreateTemp(t.TempDir(), "stdout")
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				stdout = file
			}
			originalInput, originalCommand := os.Stdin, runCurrentAppCommand
			t.Cleanup(func() {
				os.Stdin, runCurrentAppCommand = originalInput, originalCommand
				_ = input.Close()
			})
			os.Stdin = input
			called := false
			executionErr := errors.New("execution failed")
			runCurrentAppCommand = func(ctx context.Context, command CurrentAppCommandRunInputV1) error {
				called = true
				if ctx == nil || command.RunOptions.Stdin != input || command.TTY {
					t.Fatal("public app command dropped stdin or requested a nonterminal TTY")
				}
				if command.RunOptions.Stdout != stdout || command.RunOptions.Stderr != &stderr {
					t.Fatal("public app command replaced output streams")
				}
				got, err := io.ReadAll(command.RunOptions.Stdin)
				if name == "closed input" {
					if !errors.Is(err, os.ErrClosed) {
						t.Fatalf("closed reader error = %v", err)
					}
					return err
				}
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("stdin did not arrive unchanged through EOF: %v", err)
				}
				if name == "execution error" {
					return executionErr
				}
				return nil
			}
			err := AppCommand(AppCommandOptions{Dir: appCommandStateDir(t), CommandArgs: []string{"serve"}, Stdout: stdout, Stderr: &stderr})
			if !called {
				t.Fatal("public app command did not reach execution")
			}
			switch name {
			case "closed input":
				if !errors.Is(err, os.ErrClosed) {
					t.Fatalf("closed-input error was lost: %v", err)
				}
			case "execution error":
				if err != executionErr {
					t.Fatalf("execution error was replaced: %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

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
