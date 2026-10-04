package dockerdeploy

import (
	"context"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestTerminalGuardRejectsCallerHeldDockerWritersV1(t *testing.T) {
	f := newCompanionDockerFixtureV1(t)
	document, platform := testSelectedPlatformDocumentV1(t)
	state := deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document), Platform: platform, Overlay: deploy.EmptyRequestOverlayV1(), BlueprintSource: "fixture", Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}}
	if err := f.operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishCurrentRuntimeInputsV1(f.operation, f.dir, currentRuntimeFilePlanV1()); err != nil {
		t.Fatal(err)
	}
	if err := f.operation.BeginTerminalRemovalV1(state); err != nil {
		t.Fatal(err)
	}
	if err := f.create(t.Context()); err == nil || len(f.calls) != 0 {
		t.Fatalf("guarded companion calls=%v err=%v", f.calls, err)
	}

	for _, publish := range []func() error{
		func() error {
			_, err := PublishBuild(t.Context(), f.operation, providerstore.Store{}, BuildPublicationInput{NoCache: true})
			return err
		},
		func() error {
			_, err := PublishValidatedBuild(t.Context(), f.operation, providerstore.Store{}, "demo", f.dir, deploy.BuildLockV1{}, ValidatedBuildInputsV1{})
			return err
		},
	} {
		if err := publish(); err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Fatalf("caller-held publication: %v", err)
		}
	}
	if err := RequireCurrentRuntimeInputsV1(f.operation, f.dir, currentRuntimeFilePlanV1()); err != nil {
		t.Fatalf("guarded diagnostic: %v", err)
	}
	if _, err := PublishCurrentRuntimeInputsV1(f.operation, f.dir, currentRuntimeFilePlanV1()); err == nil {
		t.Fatal("guarded runtime publication accepted")
	}
	if err := RunPublishedRuntimeContainerV1(t.Context(), PublishedRuntimeContainerInput{Operation: f.operation, Invocation: RuntimeInvocationV1{PlanID: runtimeShellPlanID}}, func(context.Context, CurrentBuild) error {
		t.Fatal("guarded runtime created a container")
		return nil
	}); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("guarded runtime execution: %v", err)
	}
	// Explicit retirement still works on the original retained owner.
	if err := f.remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("retirement calls=%v", f.calls)
	}
}

func TestTerminalGuardRejectsNoCachePreparationBeforeBackendV1(t *testing.T) {
	input, loaded, current, selected, prepared := providerBuildPreparationFixture(t)
	input.NoCache = true
	state, _, err := input.Operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "fixture"
	if err := input.Operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := input.Operation.BeginTerminalRemovalV1(state); err != nil {
		t.Fatal(err)
	}
	var order []string
	backend := providerBuildPreparationTestBackend(t, loaded, current, selected, prepared, &order)
	if _, err := prepareLockedProviderBuildV1(t.Context(), input, backend); err == nil || !strings.Contains(err.Error(), "terminal") || len(order) != 0 {
		t.Fatalf("no-cache backend order=%v err=%v", order, err)
	}
	if _, err := PrepareLockedProviderBuildV1(t.Context(), input); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("public no-cache entry: %v", err)
	}
}
