package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestValidatePythonProfileObservationRunsFixedInspectionInHeldSession(t *testing.T) {
	completion, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input := completion.Validation.Final
	profile := input.Profiles[0]
	locked := profile.SelectedExecutables[0]
	version, ok := locked.Facts.Value["version"].(string)
	if !ok {
		t.Fatalf("locked interpreter facts = %#v", locked.Facts)
	}
	launcher := directExecutableObservation("shared_launcher", pythonLauncherPath)
	interpreter := directExecutableObservation("shared_interpreter", locked.InvocationPath)
	interpreter.Terminal.Size = "2"

	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	commands := []CommandSpec{}
	runImageValidationFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		_, _ = options.Stdout.Write([]byte(version + "\n"))
		return nil
	}
	session := &ImageValidationSession{descriptor: input.Image.Descriptor, containerName: "held-validation"}
	fresh, err := validatePythonProfileObservation(context.Background(), session, profile, launcher, interpreter)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Facts.Value["version"] != version || fresh.Terminal.Size != "2" {
		t.Fatalf("fresh interpreter evidence = %#v", fresh)
	}
	inspection, err := pythonprovider.InterpreterInspectionArgv(locked.InvocationPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"exec", "--user", "0:0", "--workdir", "/", "held-validation",
		pythonLauncherPath, "-i", "HOME=/tmp", "LANG=C", "LC_ALL=C",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TMPDIR=/tmp",
	}
	want = append(want, inspection...)
	if len(commands) != 1 || !reflect.DeepEqual(commands[0].Args, want) {
		t.Fatalf("inspection commands = %#v", commands)
	}
}

func TestValidatePythonProfileObservationRejectsRequestDriftAndIncompatibleVersion(t *testing.T) {
	completion, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input := completion.Validation.Final
	profile := input.Profiles[0]
	locked := profile.SelectedExecutables[0]
	version := locked.Facts.Value["version"].(string)
	launcher := directExecutableObservation("shared_launcher", pythonLauncherPath)
	interpreter := directExecutableObservation("shared_interpreter", locked.InvocationPath)
	interpreter.Terminal.Size = "2"

	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	runImageValidationFollowupCommand = func(_ CommandSpec, options RunOptions) error {
		_, _ = options.Stdout.Write([]byte(version + "\n"))
		return nil
	}
	session := &ImageValidationSession{descriptor: input.Image.Descriptor, containerName: "held-validation"}

	unchanged := profile
	unchanged.Declaration.Executables = append([]providers.ExecutableRequirement{}, profile.Declaration.Executables...)
	unchanged.Declaration.Executables[0].ValidationPolicy = providers.ValidationPolicyUnchanged
	if _, err := validatePythonProfileObservation(context.Background(), session, unchanged, launcher, interpreter); err == nil || !strings.Contains(err.Error(), "canonical request") {
		t.Fatalf("request drift error = %v", err)
	}

	runImageValidationFollowupCommand = func(_ CommandSpec, options RunOptions) error {
		_, _ = options.Stdout.Write([]byte("0.0.0\n"))
		return nil
	}
	if _, err := validatePythonProfileObservation(context.Background(), session, profile, launcher, interpreter); err == nil || !strings.Contains(err.Error(), "does not satisfy") {
		t.Fatalf("version constraint error = %v", err)
	}
}

func TestValidatePythonProfileObservationRejectsMalformedProbeBeforeExecution(t *testing.T) {
	completion, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input := completion.Validation.Final
	profile := input.Profiles[0]
	locked := profile.SelectedExecutables[0]
	launcher := directExecutableObservation("shared_launcher", pythonLauncherPath)
	interpreter := directExecutableObservation("shared_interpreter", locked.InvocationPath)
	interpreter.Terminal.Kind = "directory"

	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	runImageValidationFollowupCommand = func(CommandSpec, RunOptions) error {
		t.Fatal("malformed interpreter observation reached execution")
		return nil
	}
	session := &ImageValidationSession{descriptor: input.Image.Descriptor, containerName: "held-validation"}
	if _, err := validatePythonProfileObservation(context.Background(), session, profile, launcher, interpreter); err == nil || !strings.Contains(err.Error(), "before execution") {
		t.Fatalf("malformed interpreter error = %v", err)
	}
}
