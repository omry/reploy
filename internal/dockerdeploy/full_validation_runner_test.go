package dockerdeploy

import (
	"context"
	"errors"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providerstore"
)

func TestProviderFullImageValidationRunnerUsesOneSessionAndCleansBaseOnlyValidation(t *testing.T) {
	input := fullValidationInput(t, "7")
	plan, err := planFullImageValidationProbe(input)
	if err != nil {
		t.Fatal(err)
	}
	workspace := testPreparedProbeWorkspace(t, input.Image.Descriptor.Platform, t.TempDir())
	previousPrepare := prepareFullValidationProbeWorkspace
	previousOpen := openFullValidationSession
	previousRun := runImageValidationFollowupCommand
	t.Cleanup(func() {
		prepareFullValidationProbeWorkspace = previousPrepare
		openFullValidationSession = previousOpen
		runImageValidationFollowupCommand = previousRun
	})
	cleaned := false
	prepareFullValidationProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return workspace, func() error { cleaned = true; return nil }, nil
	}
	opened := 0
	openFullValidationSession = func(_ context.Context, descriptor deploy.ImageDescriptor, got PreparedProbeWorkspace) (*ImageValidationSession, error) {
		opened++
		return &ImageValidationSession{descriptor: descriptor, workspace: got, containerName: "held-validation"}, nil
	}
	commands := []CommandSpec{}
	observations := make([]probe.ExecutableObservationV1, 0, len(plan.Request.Inspections))
	for _, inspection := range plan.Request.Inspections {
		observations = append(observations, directExecutableObservation(inspection.ID, inspection.InvocationPath))
	}
	response := mustCanonicalProbeResponse(t, probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: observations})
	runImageValidationFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		if len(spec.Args) != 0 && spec.Args[len(spec.Args)-1] == workspace.ContainerExecutable {
			_, _ = options.Stdout.Write(response)
		}
		return nil
	}
	profiles, outputs, err := (ProviderFullImageValidationRunner{}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if opened != 1 || !cleaned || len(profiles) != 0 || len(outputs) != 0 || len(commands) != 3 || commands[0].Args[0] != "exec" || commands[1].Args[len(commands[1].Args)-1] != "/.reploy-build" || commands[2].Args[0] != "rm" {
		t.Fatalf("opened=%d cleaned=%t profiles=%#v outputs=%#v commands=%#v", opened, cleaned, profiles, outputs, commands)
	}
}

func TestProviderFullImageValidationRunnerCleansSessionAndWorkspaceAfterProbeFailure(t *testing.T) {
	input := fullValidationInput(t, "7")
	workspace := testPreparedProbeWorkspace(t, input.Image.Descriptor.Platform, t.TempDir())
	previousPrepare := prepareFullValidationProbeWorkspace
	previousOpen := openFullValidationSession
	previousRun := runImageValidationFollowupCommand
	t.Cleanup(func() {
		prepareFullValidationProbeWorkspace = previousPrepare
		openFullValidationSession = previousOpen
		runImageValidationFollowupCommand = previousRun
	})
	cleaned := false
	prepareFullValidationProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return workspace, func() error { cleaned = true; return nil }, nil
	}
	openFullValidationSession = func(_ context.Context, descriptor deploy.ImageDescriptor, got PreparedProbeWorkspace) (*ImageValidationSession, error) {
		return &ImageValidationSession{descriptor: descriptor, workspace: got, containerName: "held-validation"}, nil
	}
	removed := false
	runImageValidationFollowupCommand = func(spec CommandSpec, _ RunOptions) error {
		if len(spec.Args) != 0 && spec.Args[0] == "rm" {
			removed = true
			return nil
		}
		return errors.New("probe failed")
	}
	if _, _, err := (ProviderFullImageValidationRunner{}).Run(context.Background(), input); err == nil || !cleaned || !removed {
		t.Fatalf("error=%v cleaned=%t removed=%t", err, cleaned, removed)
	}
}

func TestProviderFullImageValidationRunnerRetainsWorkspaceAfterContainerRemovalFailure(t *testing.T) {
	input := fullValidationInput(t, "7")
	workspace := testPreparedProbeWorkspace(t, input.Image.Descriptor.Platform, t.TempDir())
	previousPrepare := prepareFullValidationProbeWorkspace
	previousOpen := openFullValidationSession
	previousRun := runImageValidationFollowupCommand
	t.Cleanup(func() {
		prepareFullValidationProbeWorkspace = previousPrepare
		openFullValidationSession = previousOpen
		runImageValidationFollowupCommand = previousRun
	})
	cleaned := false
	prepareFullValidationProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return workspace, func() error { cleaned = true; return nil }, nil
	}
	openFullValidationSession = func(_ context.Context, descriptor deploy.ImageDescriptor, got PreparedProbeWorkspace) (*ImageValidationSession, error) {
		return &ImageValidationSession{descriptor: descriptor, workspace: got, containerName: "held-validation"}, nil
	}
	runImageValidationFollowupCommand = func(spec CommandSpec, _ RunOptions) error {
		if len(spec.Args) != 0 && spec.Args[0] == "rm" {
			return errors.New("container removal failed")
		}
		return errors.New("probe failed")
	}
	if _, _, err := (ProviderFullImageValidationRunner{}).Run(context.Background(), input); err == nil {
		t.Fatal("validation succeeded")
	}
	if cleaned {
		t.Fatal("container removal failure deleted its bind workspace")
	}
}

func TestProviderFullImageValidationRunnerValidatesPythonProfileAndOutputFromOneProbe(t *testing.T) {
	completion, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input := completion.Validation.Final
	plan, err := planFullImageValidationProbe(input)
	if err != nil {
		t.Fatal(err)
	}
	workspace := testPreparedProbeWorkspace(t, input.Image.Descriptor.Platform, t.TempDir())
	previousPrepare := prepareFullValidationProbeWorkspace
	previousOpen := openFullValidationSession
	previousRun := runImageValidationFollowupCommand
	t.Cleanup(func() {
		prepareFullValidationProbeWorkspace = previousPrepare
		openFullValidationSession = previousOpen
		runImageValidationFollowupCommand = previousRun
	})
	prepareFullValidationProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return workspace, func() error { return nil }, nil
	}
	openFullValidationSession = func(_ context.Context, descriptor deploy.ImageDescriptor, got PreparedProbeWorkspace) (*ImageValidationSession, error) {
		return &ImageValidationSession{descriptor: descriptor, workspace: got, containerName: "held-validation"}, nil
	}
	observations := make([]probe.ExecutableObservationV1, 0, len(plan.Request.Inspections))
	for _, inspection := range plan.Request.Inspections {
		observations = append(observations, directExecutableObservation(inspection.ID, inspection.InvocationPath))
	}
	response := mustCanonicalProbeResponse(t, probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: observations})
	version := input.Profiles[0].SelectedExecutables[0].Facts.Value["version"].(string)
	commands := []CommandSpec{}
	runImageValidationFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		if len(spec.Args) != 0 && spec.Args[0] == "rm" {
			return nil
		}
		if spec.Args[len(spec.Args)-1] == workspace.ContainerExecutable {
			_, _ = options.Stdout.Write(response)
			return nil
		}
		_, _ = options.Stdout.Write([]byte(version + "\n"))
		return nil
	}
	profiles, outputs, err := (ProviderFullImageValidationRunner{}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].SubjectRootFS != input.Image.Image.RootFSSubject || len(outputs) != len(input.Outputs) || len(commands) != 4 {
		t.Fatalf("profiles=%#v outputs=%#v commands=%#v", profiles, outputs, commands)
	}
}
