package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

func TestCurrentBuildMatchesExactCanonicalInputs(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	matched, err := CurrentBuildMatches(current, input)
	if err != nil || !matched {
		t.Fatalf("matched=%v error=%v", matched, err)
	}
}

func TestPortableToolSelectionMatchesCurrentBuildV1InvalidatesChangedClosure(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	current := buildLockAssemblyPortableToolsV1(
		t, fixture.store, fixture.request.Plan, fixture.request.NodeID,
	)
	requested := current
	requested.Plan.PortableToolPlan = clonePortableToolPlanForReuseTestV1(
		current.Plan.PortableToolPlan,
	)
	requested.Plan.PortableToolPlan.Tools[0].SelectedClosureDigest = rendererDigest("f")

	matched, err := portableToolSelectionMatchesCurrentBuildV1(&current, &requested)
	if err != nil || matched {
		t.Fatalf("matched=%v error=%v", matched, err)
	}
	matched, err = portableToolSelectionMatchesCurrentBuildV1(&current, &current)
	if err != nil || !matched {
		t.Fatalf("exact selection matched=%v error=%v", matched, err)
	}
	matched, err = portableToolSelectionMatchesCurrentBuildV1(nil, &requested)
	if err != nil || matched {
		t.Fatalf("missing selection matched=%v error=%v", matched, err)
	}
}

func TestPortableToolSelectionsMatchCurrentBuildV1IgnoresNonMaterializationMetadata(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	portableTools := buildLockAssemblyPortableToolsV1(
		t, fixture.store, fixture.request.Plan, fixture.request.NodeID,
	)
	current := portableTools.Plan.PortableToolPlan
	requested := buildLockAssemblyPortableToolsV1(
		t, fixture.store, fixture.request.Plan, fixture.request.NodeID,
	).Plan.PortableToolPlan
	payload := &requested.Tools[0].Responsibilities.Payloads[0]
	payload.Record.Value["entries"] = "2"
	digest, err := canonical.Sum("portable-tool-record", "portable-tool-record-v1", payload.Record.Value)
	if err != nil {
		t.Fatal(err)
	}
	payload.Reference.Digest = digest

	matched, err := portableToolSelectionsMatchCurrentBuildV1(current, requested)
	if err != nil || !matched {
		t.Fatalf("unchanged selected closure matched=%v error=%v", matched, err)
	}
}

func clonePortableToolPlanForReuseTestV1(plan providers.PortableToolPlanV1) providers.PortableToolPlanV1 {
	clone := plan
	clone.Tools = append([]providers.PortableToolPlanEntryV1{}, plan.Tools...)
	return clone
}

func TestRebindCurrentBuildLockV1UpdatesOnlyDesiredBlueprintIdentity(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	input.Document.Environment.ControlScript = "updated-control"
	current.State.Blueprint = testResolvedBlueprintV1(t, input.Document)

	matched, err := CurrentBuildMatches(current, input)
	if err != nil || !matched {
		t.Fatalf("matched=%v error=%v", matched, err)
	}
	rebound, err := rebindCurrentBuildLockV1(current.Lock, input.Document, current.Lock.PortableTools)
	if err != nil {
		t.Fatal(err)
	}
	want := current.Lock
	want.BlueprintDigest = testResolvedBlueprintDigestV1(t, input.Document)
	if !reflect.DeepEqual(rebound, want) {
		t.Fatalf("rebound lock changed validated build inputs:\n got: %#v\nwant: %#v", rebound, want)
	}
	if rebound.BlueprintDigest == current.Lock.BlueprintDigest {
		t.Fatal("runtime-only document update retained the obsolete blueprint digest")
	}
}

func TestRebindCurrentBuildLockV1PublishesOwnedDesiredPortableToolLock(t *testing.T) {
	current, desired := portableToolReuseBuildLocksV1(t)
	document, _ := testSelectedPlatformDocumentV1(t)
	rebound, err := rebindCurrentBuildLockV1(current, document, &desired)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.PortableTools == nil || rebound.PortableTools == &desired || rebound.PortableTools == current.PortableTools ||
		!reflect.DeepEqual(*rebound.PortableTools, desired) {
		t.Fatalf("rebound portable tools = %#v, want owned desired snapshot", rebound.PortableTools)
	}
	desired.Releases[0].Manifest.Record.Value["aliases"] = []any{"mutated-desired"}
	current.PortableTools.Releases[0].Manifest.Record.Value["aliases"] = []any{"mutated-current"}
	if !reflect.DeepEqual(rebound.PortableTools.Releases[0].Manifest.Record.Value["aliases"], []any{"stable"}) {
		t.Fatal("rebound portable tools alias caller or current lock")
	}
}

func portableToolReuseBuildLocksV1(t *testing.T) (deploy.BuildLockV1, providers.PortableToolLockV1) {
	t.Helper()
	fixture := newPreparedPythonGraphReuseFixture(t)
	current := fixture.lock
	portableTools := buildLockAssemblyPortableToolsV1(
		t, fixture.store, fixture.request.Plan, fixture.request.NodeID,
	)
	for _, node := range fixture.request.Plan.Nodes {
		if node.ID != "base" {
			continue
		}
		current.Base.AuthorReference, _ = node.Request.Value["image"].(string)
		var err error
		current.BasePlanDigest, err = providers.ProviderNodePlanDigest(node)
		if err != nil {
			t.Fatal(err)
		}
	}
	current.PortableTools = &portableTools
	if err := deploy.ValidateBuildLockV1(current, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}

	desired := providers.ClonePortableToolLockV1(portableTools)
	desired.Releases[0].Manifest.Record.Value["aliases"] = []any{"stable"}
	manifestDigest, err := canonical.Sum(
		"portable-tool-record",
		"portable-tool-record-v1",
		desired.Releases[0].Manifest.Record.Value,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired.Releases[0].Manifest.Reference.Digest = manifestDigest
	desired.Plan.PortableToolPlan.Tools[0].Provenance.ManifestDigest = manifestDigest
	if err := providers.ValidatePortableToolLockV1(desired); err != nil {
		t.Fatal(err)
	}
	return current, desired
}

func TestCurrentBuildMatchesIgnoresDeploymentLocalState(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	current.State.Deployment = &deploy.DeploymentStateV1{
		Schema:       deploy.DeploymentStateSchemaV1,
		Installation: installedBuildPublicationInstallation(t.TempDir()),
	}
	matched, err := CurrentBuildMatches(current, input)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("deployment-local installation state invalidated the environment build")
	}
}

func TestCurrentBuildMatchesInvalidatesEverySemanticBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*CurrentBuild, *CurrentBuildReuseInput)
	}{
		{name: "request", mutate: func(current *CurrentBuild, _ *CurrentBuildReuseInput) {
			current.Lock.ResolvedRequestDigest = rendererDigest("f")
			refreshCurrentBuildReuseGeneration(t, current)
		}},
		{name: "overlay", mutate: func(current *CurrentBuild, _ *CurrentBuildReuseInput) {
			current.Lock.Overlay = deploy.AddOverlayPackages(current.Lock.Overlay, []deploy.DirectPackageRequest{{
				Contribution: "application", Package: providers.CanonicalPackageRequest{Schema: "test-package-v1", Value: canonical.Object{}},
			}})
			refreshCurrentBuildReuseGeneration(t, current)
		}},
		{name: "package overrides", mutate: func(_ *CurrentBuild, input *CurrentBuildReuseInput) {
			input.PackageOverrides.Choices = []deploy.PackageOverrideIntentChoiceV1{{
				Provider: "python", Package: "demo", Kind: "version", Version: "2",
			}}
		}},
		{name: "base", mutate: func(_ *CurrentBuild, input *CurrentBuildReuseInput) {
			input.Base.ConfigDigest = rendererDigest("f")
			input.Base.ImmutableReference = string(input.Base.ConfigDigest)
		}},
		{name: "runtime policy", mutate: func(_ *CurrentBuild, input *CurrentBuildReuseInput) {
			input.DockerPlan.Mounts = []MountExecutionPlan{{
				Name: "alternate", Mode: blueprint.MountVolume, Target: "/mnt/alternate",
			}}
		}},
		{name: "startup verifier artifact", mutate: func(_ *CurrentBuild, input *CurrentBuildReuseInput) {
			input.StartupVerifier.Artifact = rendererDigest("a")
		}},
		{name: "container local account", mutate: func(_ *CurrentBuild, input *CurrentBuildReuseInput) {
			input.DockerPlan.Sandbox = testApplicationSandboxPlanV1(2000, 2000)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			current, input := currentBuildReuseFixture(t)
			test.mutate(&current, &input)
			matched, err := CurrentBuildMatches(current, input)
			if err != nil || matched {
				t.Fatalf("matched=%v error=%v", matched, err)
			}
		})
	}
}

func TestCurrentBuildMatchesRejectsOverlayRequestMismatch(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	input.ResolvedRequest.OverlayDigest = rendererDigest("f")
	matched, err := CurrentBuildMatches(current, input)
	if err == nil || !strings.Contains(err.Error(), "overlay") || matched {
		t.Fatalf("matched=%v error=%v", matched, err)
	}
}

func TestCurrentBuildMatchesTreatsChangedStateOverlayAsStale(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	current.State.Overlay = deploy.AddOverlayPackages(current.State.Overlay, []deploy.DirectPackageRequest{{
		Contribution: "application", Package: providers.CanonicalPackageRequest{Schema: "test-package-v1", Value: canonical.Object{}},
	}})
	matched, err := CurrentBuildMatches(current, input)
	if err != nil || matched {
		t.Fatalf("matched=%v error=%v", matched, err)
	}
}

func TestCurrentBuildMatchesTreatsChangedStateBlueprintAsStale(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	document, _ := testSelectedPlatformDocumentV1(t)
	document.Environment.ID = "changed"
	current.State.Blueprint = testResolvedBlueprintV1(t, document)
	matched, err := CurrentBuildMatches(current, input)
	if err != nil || matched {
		t.Fatalf("matched=%v error=%v", matched, err)
	}
}

func TestCurrentBuildMatchesTreatsChangedSelectedPlatformAsStale(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	arm64, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	document := input.Document
	document.Blueprint.Compatibility.Platforms = append(document.Blueprint.Compatibility.Platforms, arm64)
	current.State.Blueprint = testResolvedBlueprintV1(t, document)
	input.Document = document
	current.State.Platform = arm64
	matched, err := CurrentBuildMatches(current, input)
	if err != nil || matched {
		t.Fatalf("matched=%v error=%v", matched, err)
	}
}

func currentBuildReuseFixture(t *testing.T) (CurrentBuild, CurrentBuildReuseInput) {
	t.Helper()
	dir := t.TempDir()
	_, lock := publicationLockFixture(t, dir, "4", "5", "6")
	request := providerBaseResolvedRequest(t)
	overlay := deploy.EmptyRequestOverlayV1()
	overlayDigest, err := deploy.RequestOverlayDigestV1(overlay)
	if err != nil {
		t.Fatal(err)
	}
	request.OverlayDigest = overlayDigest
	lock.Overlay = overlay
	lock.Base.AuthorReference = "debian:bookworm-slim"
	document, _ := testSelectedPlatformDocumentV1(t)
	lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	requestDigest, err := providers.ResolvedRequestDigest(request, registry.ValidateResolvedRequestOwnersV1)
	if err != nil {
		t.Fatal(err)
	}
	lock.ResolvedRequestDigest = requestDigest
	dockerPlan := DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(1000, 1000)}
	plans, err := RuntimePlansV1(document, dockerPlan)
	if err != nil {
		t.Fatal(err)
	}
	lock.RuntimePolicy, err = CompileRuntimePolicyFromLockV1(document, lock, plans)
	if err != nil {
		t.Fatal(err)
	}
	input := CurrentBuildReuseInput{
		ResolvedRequest: request, Overlay: overlay, PackageOverrides: lock.PackageOverrides,
		Base: lock.Base, Document: document, DockerPlan: dockerPlan,
		StartupVerifier: lock.RuntimeLayer.Verifier,
	}
	lockDigest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	generation := deploy.EnvironmentGenerationState{
		Reference: "reploy/env/demo-deadbeef:g-current", ImageDigest: lock.FinalImage.Digest,
		RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
		Platform: request.Platform, Overlay: overlay, Current: &generation,
	}
	current := CurrentBuild{State: state, Generation: generation, Lock: lock}
	return current, input
}

func refreshCurrentBuildReuseGeneration(t *testing.T, current *CurrentBuild) {
	t.Helper()
	lockDigest, err := deploy.BuildLockDigestV1(current.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(current.Lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	current.Generation.BuildLockDigest = lockDigest
	current.Generation.RuntimePolicyDigest = policyDigest
	current.State.Current = &current.Generation
	current.State.Overlay = current.Lock.Overlay
	if !reflect.DeepEqual(*current.State.Current, current.Generation) {
		t.Fatal("failed to refresh generation")
	}
}
