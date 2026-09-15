package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestValidatePythonProfileObservationRunsFixedInspectionInHeldSession(t *testing.T) {
	completion, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input := completion.Validation.Final
	profile := input.Profiles[0]
	locked := profile.SelectedExecutables[0]
	lockedFacts, err := pythonprovider.DecodeInterpreterFactsV2(locked.Facts)
	if err != nil {
		t.Fatal(err)
	}
	launcher := directExecutableObservation("shared_launcher", pythonLauncherPath)
	interpreter := directExecutableObservation("shared_interpreter", locked.InvocationPath)
	interpreter.Terminal.Size = "2"

	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	commands := []CommandSpec{}
	runImageValidationFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		_, _ = options.Stdout.Write(pythonInspectionOutputFromFactsV2ForTest(t, locked.Facts))
		return nil
	}
	session := &ImageValidationSession{descriptor: input.Image.Descriptor, containerName: "held-validation"}
	fresh, err := validatePythonProfileObservation(context.Background(), session, profile, launcher, interpreter)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Facts.Value["version"] != lockedFacts.Version || fresh.Terminal.Size != "2" {
		t.Fatalf("fresh interpreter evidence = %#v", fresh)
	}
	targetArchitecture, err := pythonInspectionArchitectureV2(input.Image.Descriptor.Platform)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := pythonprovider.InterpreterInspectionArgv(locked.InvocationPath, lockedFacts.TestedTags, targetArchitecture)
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
	launcher := directExecutableObservation("shared_launcher", pythonLauncherPath)
	interpreter := directExecutableObservation("shared_interpreter", locked.InvocationPath)
	interpreter.Terminal.Size = "2"

	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	runImageValidationFollowupCommand = func(_ CommandSpec, options RunOptions) error {
		_, _ = options.Stdout.Write(pythonInspectionOutputFromFactsV2ForTest(t, locked.Facts))
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
		_, _ = options.Stdout.Write(pythonInspectionOutputV2ForTest("0.0.0", nil, nil))
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

func TestValidatePythonProfileObservationPreservesPortableInterpreterFacts(t *testing.T) {
	completion, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input := completion.Validation.Final
	profile := input.Profiles[0]
	profile.SelectedExecutables = append([]providers.ExecutableEvidence{}, profile.SelectedExecutables...)
	locked := profile.SelectedExecutables[0]
	lockedFacts, err := pythonprovider.DecodeInterpreterFactsV2(locked.Facts)
	if err != nil {
		t.Fatal(err)
	}
	lockedFacts.TestedTags = []string{"cp313-cp313-manylinux_2_35_x86_64", "py3-none-any"}
	lockedFacts.CompatibleTags = append([]string{}, lockedFacts.TestedTags...)
	locked.Facts = pythonprovider.CanonicalInterpreterFactsV2(lockedFacts)
	profile.SelectedExecutables[0] = locked
	launcher := directExecutableObservation("shared_launcher", pythonLauncherPath)
	interpreter := directExecutableObservation("shared_interpreter", locked.InvocationPath)
	interpreter.Terminal.Size = "2"

	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	session := &ImageValidationSession{descriptor: input.Image.Descriptor, containerName: "held-validation"}
	runImageValidationFollowupCommand = func(_ CommandSpec, options RunOptions) error {
		_, _ = options.Stdout.Write(pythonInspectionOutputFromFactsV2ForTest(t, locked.Facts))
		return nil
	}
	if _, err := validatePythonProfileObservation(context.Background(), session, profile, launcher, interpreter); err != nil {
		t.Fatalf("unchanged portable interpreter facts: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*pythonprovider.InterpreterInspectionFactsV2)
		want   string
	}{
		{name: "version", mutate: func(facts *pythonprovider.InterpreterInspectionFactsV2) { facts.Version = "3.13.3" }, want: "interpreter version changed"},
		{name: "implementation", mutate: func(facts *pythonprovider.InterpreterInspectionFactsV2) { facts.Implementation = "pypy" }, want: "interpreter implementation changed"},
		{name: "ABI", mutate: func(facts *pythonprovider.InterpreterInspectionFactsV2) { facts.ABI = "abi3" }, want: "interpreter ABI changed"},
		{name: "libc", mutate: func(facts *pythonprovider.InterpreterInspectionFactsV2) { facts.Libc = "musl" }, want: "interpreter libc changed"},
		{name: "libc major", mutate: func(facts *pythonprovider.InterpreterInspectionFactsV2) { facts.LibcMajor = "3" }, want: "interpreter libc release changed"},
		{name: "libc minor", mutate: func(facts *pythonprovider.InterpreterInspectionFactsV2) { facts.LibcMinor = "36" }, want: "interpreter libc release changed"},
		{name: "tested tags", mutate: func(facts *pythonprovider.InterpreterInspectionFactsV2) {
			facts.TestedTags = []string{"py3-none-any"}
			facts.CompatibleTags = []string{"py3-none-any"}
		}, want: "tested tags do not equal requested tags"},
		{name: "compatible tags", mutate: func(facts *pythonprovider.InterpreterInspectionFactsV2) {
			facts.CompatibleTags = []string{"py3-none-any"}
		}, want: "interpreter compatible tags changed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fresh := lockedFacts
			fresh.TestedTags = append([]string{}, lockedFacts.TestedTags...)
			fresh.CompatibleTags = append([]string{}, lockedFacts.CompatibleTags...)
			test.mutate(&fresh)
			encoded, err := canonical.Marshal(fresh)
			if err != nil {
				t.Fatal(err)
			}
			runImageValidationFollowupCommand = func(_ CommandSpec, options RunOptions) error {
				_, _ = options.Stdout.Write(encoded)
				return nil
			}
			if _, err := validatePythonProfileObservation(context.Background(), session, profile, launcher, interpreter); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("drift error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePythonProfileObservationKeepsOrdinaryCompatibleVersionPolicy(t *testing.T) {
	completion, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input := completion.Validation.Final
	profile := input.Profiles[0]
	locked := profile.SelectedExecutables[0]
	lockedFacts, err := pythonprovider.DecodeInterpreterFactsV2(locked.Facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(lockedFacts.TestedTags) != 0 {
		t.Fatalf("ordinary profile tested tags = %#v", lockedFacts.TestedTags)
	}
	launcher := directExecutableObservation("shared_launcher", pythonLauncherPath)
	interpreter := directExecutableObservation("shared_interpreter", locked.InvocationPath)
	interpreter.Terminal.Size = "2"
	lockedFacts.Version = "3.13.3"

	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	encoded, err := canonical.Marshal(lockedFacts)
	if err != nil {
		t.Fatal(err)
	}
	runImageValidationFollowupCommand = func(_ CommandSpec, options RunOptions) error {
		_, _ = options.Stdout.Write(encoded)
		return nil
	}
	session := &ImageValidationSession{descriptor: input.Image.Descriptor, containerName: "held-validation"}
	if _, err := validatePythonProfileObservation(context.Background(), session, profile, launcher, interpreter); err != nil {
		t.Fatalf("compatible ordinary version transition: %v", err)
	}
}
