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

func TestCurrentBuildMatchesIgnoresDeploymentLocalState(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	current.State.Deployment = &deploy.DeploymentStateV1{
		Schema: deploy.DeploymentStateSchemaV1,
		Installation: deploy.InstallationStateV1{
			Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
			TargetDir: "/opt/demo", Scope: "system", Service: "demo",
			UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo-1",
			ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
		},
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
				Component: "application", Package: providers.CanonicalPackageRequest{Schema: "test-package-v1", Value: canonical.Object{}},
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
			input.DockerPlan.TemporaryHome = "/tmp/alternate-home"
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
		Component: "application", Package: providers.CanonicalPackageRequest{Schema: "test-package-v1", Value: canonical.Object{}},
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
	dockerPlan := DockerExecutionPlan{}
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
