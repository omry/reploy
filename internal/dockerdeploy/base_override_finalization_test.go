package dockerdeploy

import (
	"context"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestBaseOverrideFinalizationPreservesBlueprint(t *testing.T) {
	document := resolvedRequestTestDocument()
	before := testResolvedBlueprintDigestV1(t, document)
	overlay := deploy.EmptyRequestOverlayV1()
	overrides := deploy.EmptyPackageOverrideIntentV1(document.Environment.ID)
	platform := document.Blueprint.Compatibility.Platforms[0]
	replacement := "docker.io/library/python@" + string(registryDigest("1"))
	candidate, err := BuildResolvedRequestWithOverridesV1(document, overlay, overrides, replacement, platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Plan(providers.PlanInput{Components: candidate.Components, Platform: platform})
	if err != nil {
		t.Fatal(err)
	}
	graph := providers.GraphExecutionResult{Plan: plan, Bundles: []providers.ResolvedBundle{}, SelectedSources: []providers.ResolvedSourceInput{}}
	finalized, _, err := finalizeResolvedRequestV1(document, overlay, overrides, replacement, candidate, graph)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(finalized, candidate) || testResolvedBlueprintDigestV1(t, document) != before {
		t.Fatal("finalization changed the request or original blueprint authority")
	}
	for _, desired := range []string{"", "python:3.13-slim"} {
		if _, _, err := finalizeResolvedRequestV1(document, overlay, overrides, desired, candidate, graph); err == nil {
			t.Fatalf("candidate accepted after changing the selected override to %q", desired)
		}
	}
	changed := document
	changed.Environment.ID = "other"
	if _, _, err := finalizeResolvedRequestV1(changed, overlay, overrides, replacement, candidate, graph); err == nil {
		t.Fatal("base override authorized an unrelated blueprint change")
	}
}

func TestBaseOverrideRuntimeReadinessPreservesPublishedAuthority(t *testing.T) {
	for _, phase := range []blueprint.Phase{blueprint.PhaseStaged, blueprint.PhaseInstalled} {
		t.Run(string(phase), func(t *testing.T) {
			current, input := runtimeCurrentBuildFixture(t)
			document := input.Document
			document.Environment.Base.Image = "python:3.11-slim"
			if err := document.Environment.RebuildProviderContributions(); err != nil {
				t.Fatal(err)
			}
			current.State.Blueprint = testResolvedBlueprintV1(t, document)
			current.Lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
			request, err := BuildResolvedRequestWithOverridesV1(document, current.State.Overlay,
				current.Lock.PackageOverrides, current.Lock.Base.AuthorReference,
				current.Lock.Platform, []providers.ResolvedSourceInput{})
			if err != nil {
				t.Fatal(err)
			}
			digest, err := providers.ResolvedRequestDigest(request, registry.ValidateResolvedRequestOwnersV1)
			if err != nil || digest != current.Lock.ResolvedRequestDigest {
				t.Fatalf("fixture effective request does not match its lock: %v", err)
			}
			refreshCurrentBuildReuseGeneration(t, &current)
			input.DockerPlan.Phase = phase
			invocation, err := ShellRuntimeInvocationV1(input.DockerPlan)
			if err != nil {
				t.Fatal(err)
			}
			runs := 0
			err = runPublishedRuntimeContainerV1(t.Context(), PublishedRuntimeContainerInput{
				Operation: runtimeExecutionOperationV1(t), DockerPlan: input.DockerPlan, Invocation: invocation,
			}, func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
				return current, true, nil
			}, func(_ context.Context, got CurrentBuild) error {
				runs++
				if !reflect.DeepEqual(got, current) {
					t.Fatal("runtime replaced the published override authority")
				}
				return nil
			})
			if err != nil || runs != 1 {
				t.Fatalf("overridden runtime runs=%d err=%v", runs, err)
			}
		})
	}
}

func TestBaseOverridePreparationReusesCurrentAndValidatedBuilds(t *testing.T) {
	for _, validated := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "validated"}[validated], func(t *testing.T) {
			input, loaded, current, selected, _ := providerBuildPreparationFixture(t)
			input.BaseImage = current.Lock.Base.AuthorReference
			loaded.BaseImage = input.BaseImage
			loaded.Document.Environment.Base = *loaded.Document.Environment.Components["base"].Base
			loaded.Document.Environment.Base.Image = "python:3.11-slim"
			if err := loaded.Document.Environment.RebuildProviderContributions(); err != nil {
				t.Fatal(err)
			}
			var err error
			loaded.Request, err = BuildResolvedRequestWithOverridesV1(loaded.Document, loaded.State.Overlay,
				loaded.PackageOverrides, loaded.BaseImage, loaded.Request.Platform, loaded.Request.Sources)
			if err != nil {
				t.Fatal(err)
			}
			current.State.Blueprint = testResolvedBlueprintV1(t, loaded.Document)
			current.Lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, loaded.Document)
			refreshCurrentBuildReuseGeneration(t, &current)
			order := []string{}
			backend := providerBuildPreparationTestBackend(t, loaded, current, selected, PreparedProviderBase{}, &order)
			if validated {
				input.ValidatedCandidate = &ValidatedBuildCandidateV1{Current: current}
				backend.validateCurrent = func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
					return CurrentBuild{}, false, nil
				}
			}
			backend.matches = func(got CurrentBuild, reuse CurrentBuildReuseInput) (bool, error) {
				if !reflect.DeepEqual(got, current) || !reflect.DeepEqual(reuse.ResolvedRequest, loaded.Request) {
					t.Fatal("preparation dropped the effective base from cache comparison")
				}
				return true, nil
			}
			backend.realizeBase = func(context.Context, providerstore.Store, SelectedProviderBase) (PreparedProviderBase, error) {
				t.Fatal("exact reuse attempted base realization")
				return PreparedProviderBase{}, nil
			}
			result, err := prepareLockedProviderBuildV1(t.Context(), input, backend)
			if err != nil || !result.Reused {
				t.Fatalf("reuse=%v err=%v", result.Reused, err)
			}
		})
	}
}

func TestBaseOverrideCompletionWithPythonSources(t *testing.T) {
	input, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	// Keep the completed graph and its local Python source, but declare a
	// different author base. Only the explicit input authorizes this graph.
	input.BaseImage = input.Document.Environment.Base.Image
	input.Document.Environment.Base.Image = "python:3.11-slim"
	if err := input.Document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	before := testResolvedBlueprintDigestV1(t, input.Document)
	if len(input.ResolvedRequest.Sources) == 0 {
		t.Fatal("fixture must exercise selected local Python sources")
	}
	policy, err := providerBuildRuntimePolicyV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProviderBuildCompletionInput(input, policy); err != nil {
		t.Fatalf("effective base completion rejected: %v", err)
	}
	for _, desired := range []string{"", "python:3.13-slim"} {
		changed := input
		changed.BaseImage = desired
		if err := validateProviderBuildCompletionInput(changed, policy); err == nil {
			t.Fatalf("completion accepted changed override %q", desired)
		}
	}
	changed := input
	changed.Base.AuthorReference = input.Document.Environment.Base.Image
	if err := validateProviderBuildCompletionInput(changed, policy); err == nil {
		t.Fatal("completion accepted a descriptor for the original base")
	}
	if testResolvedBlueprintDigestV1(t, input.Document) != before {
		t.Fatal("completion changed original blueprint authority")
	}
}

func TestBaseOverrideLockedRequestReplayAndReuse(t *testing.T) {
	current, reuse := currentBuildReuseFixture(t)
	// The stored blueprint stays original; the lock and desired request name
	// the separately selected base. Reuse must preserve both authorities.
	reuse.Document.Environment.Base.Image = "python:3.11-slim"
	if err := reuse.Document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	current.State.Blueprint = testResolvedBlueprintV1(t, reuse.Document)
	current.Lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, reuse.Document)
	refreshCurrentBuildReuseGeneration(t, &current)
	for _, desired := range []string{reuse.Base.AuthorReference, "python:3.13-slim", ""} {
		t.Run(desired, func(t *testing.T) {
			loaded, err := loadBuildRequestWithInputsV1(nil, reuse.PackageOverrides, desired,
				[]providers.ResolvedSourceInput{}, current.State, reuse.Document)
			if err != nil {
				t.Fatal(err)
			}
			request, overrides, exact, err := resolvedRequestForLockedBuildV1(
				loaded.Document, loaded.State.Overlay, loaded.PackageOverrides, loaded.BaseImage,
				loaded.Request, []providers.ResolvedSourceInput{}, current.Lock, providerstore.Store{})
			if err != nil || !exact {
				t.Fatalf("locked request replay: exact=%v err=%v", exact, err)
			}
			input := reuse
			input.ResolvedRequest, input.PackageOverrides = request, overrides
			input.Base.AuthorReference, err = resolvedRequestBaseReference(request)
			if err != nil {
				t.Fatal(err)
			}
			matched, err := CurrentBuildMatches(current, input)
			if err != nil || matched != (desired == reuse.Base.AuthorReference) {
				t.Fatalf("override=%q matched=%v err=%v", desired, matched, err)
			}
			if !reflect.DeepEqual(loaded.Document, reuse.Document) {
				t.Fatal("loading changed the stored blueprint")
			}
		})
	}
}
