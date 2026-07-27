package dockerdeploy

import (
	"context"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/probe"
)

func TestValidateAPTProfileObservationReusesCombinedProbeAndAcceptsCompatibleTools(t *testing.T) {
	input := fullValidationInput(t, "7")
	profile := testAPTFullValidationProfile(t, input)
	input.Profiles = append(input.Profiles, profile)
	plan, err := planFullImageValidationProbe(input)
	if err != nil {
		t.Fatal(err)
	}
	observations := fullValidationDirectObservations(plan.Request)
	profileOutputs := [][]byte{
		[]byte("ID\x00debian\x00VERSION_ID\x0013.1\x00"),
		[]byte("apt 4.0.0 (amd64)\n"),
		[]byte("Debian 'dpkg' package management program version 1.23.0 (amd64).\n"),
		[]byte("Debian 'dpkg-deb' package archive backend version 1.23.0 (amd64).\n"),
		[]byte("Debian dpkg-query package management program query tool version 1.23.0 (amd64).\n"),
		[]byte("sha256sum (GNU coreutils) 10.0\n"),
		[]byte("amd64\n"),
		{},
	}
	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	commands := []CommandSpec{}
	outputIndex := 0
	runImageValidationFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		if outputIndex >= len(profileOutputs) {
			t.Fatalf("unexpected APT profile command: %#v", spec.Args)
		}
		_, _ = options.Stdout.Write(profileOutputs[outputIndex])
		outputIndex++
		return nil
	}
	workspace := PreparedAPTResolverWorkspace{}
	session := &ImageValidationSession{
		descriptor: input.Image.Descriptor, containerName: "held-validation", aptWorkspace: &workspace,
	}
	fresh, err := validateAPTProfileObservation(context.Background(), session, profile, plan.APT.ToolInspection, observations)
	if err != nil {
		t.Fatal(err)
	}
	aptVersion := ""
	for _, tool := range fresh.Profile.Tools {
		if tool.Name == "apt_get" {
			aptVersion = tool.Version
		}
	}
	if aptVersion != "apt 4.0.0 (amd64)" || fresh.Profile.OSRelease[1].Value != "13.1" || len(commands) != 8 {
		t.Fatalf("fresh APT profile = %#v, commands = %#v", fresh.Profile, commands)
	}
	for _, command := range commands {
		if len(command.Args) < 2 || command.Args[0] != "exec" || strings.Contains(strings.Join(command.Args, " "), "--network") {
			t.Fatalf("APT profile command escaped held session: %#v", command.Args)
		}
	}
}

func TestValidateAPTProfileObservationRejectsMissingBatchEvidenceBeforeCommands(t *testing.T) {
	input := fullValidationInput(t, "7")
	profile := testAPTFullValidationProfile(t, input)
	input.Profiles = append(input.Profiles, profile)
	plan, err := planFullImageValidationProbe(input)
	if err != nil {
		t.Fatal(err)
	}
	observations := fullValidationDirectObservations(plan.Request)
	delete(observations, plan.APT.ToolInspection["apt_get"])
	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	runImageValidationFollowupCommand = func(CommandSpec, RunOptions) error {
		t.Fatal("missing combined APT observation reached a profile command")
		return nil
	}
	workspace := PreparedAPTResolverWorkspace{}
	session := &ImageValidationSession{
		descriptor: input.Image.Descriptor, containerName: "held-validation", aptWorkspace: &workspace,
	}
	if _, err := validateAPTProfileObservation(context.Background(), session, profile, plan.APT.ToolInspection, observations); err == nil || !strings.Contains(err.Error(), "no combined observation") {
		t.Fatalf("missing observation error = %v", err)
	}
}

func fullValidationDirectObservations(request probe.RequestV1) map[string]probe.ExecutableObservationV1 {
	result := make(map[string]probe.ExecutableObservationV1, len(request.Inspections))
	for _, inspection := range request.Inspections {
		result[inspection.ID] = directExecutableObservation(inspection.ID, inspection.InvocationPath)
	}
	return result
}
