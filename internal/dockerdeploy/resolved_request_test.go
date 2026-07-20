package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
)

func registryDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func TestBuildResolvedRequestV1CombinesBlueprintOptionsAndDirectPackages(t *testing.T) {
	document := resolvedRequestTestDocument()
	pythonDirect, err := pythonprovider.CanonicalPackageRequestV1("extra==2")
	if err != nil {
		t.Fatal(err)
	}
	aptDirect, err := apt.CanonicalPackageRequestV1(blueprint.APTPackageRequest{Name: "curl", Exports: map[string]blueprint.ExecutableExport{}})
	if err != nil {
		t.Fatal(err)
	}
	overlay := deploy.RequestOverlayV1{
		Schema: deploy.RequestOverlaySchemaV1,
		SelectedOptions: []deploy.QualifiedOption{
			{Component: "system", Option: "tools"},
			{Component: "application", Option: "debug"},
		},
		DirectPackages: []deploy.DirectPackageRequest{
			{Component: "system", Package: aptDirect},
			{Component: "application", Package: pythonDirect},
		},
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	request, err := BuildResolvedRequestV1(document, overlay, platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, component := range request.Components {
		names = append(names, component.Component)
	}
	if !reflect.DeepEqual(names, []string{"application", "base", "system"}) {
		t.Fatalf("components = %#v", request.Components)
	}
	pythonRequest := request.Components[0].Request
	if got := len(pythonRequest.Value["requirements"].([]any)); got != 3 {
		t.Fatalf("Python requirements = %#v", pythonRequest.Value["requirements"])
	}
	aptRequest := request.Components[2].Request
	aptComponents := aptRequest.Value["components"].([]any)
	aptPackages := aptComponents[0].(canonical.Object)["packages"].([]any)
	if len(aptPackages) != 3 {
		t.Fatalf("APT packages = %#v", aptPackages)
	}
	plan, err := registry.Plan(providers.PlanInput{Components: request.Components, Platform: request.Platform})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 3 || len(plan.Edges) != 1 || plan.Edges[0].Supplier != "apt" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestBuildResolvedRequestV1NormalizesOverlayBeforeIdentity(t *testing.T) {
	document := resolvedRequestTestDocument()
	packageRequest, err := pythonprovider.CanonicalPackageRequestV1("extra==2")
	if err != nil {
		t.Fatal(err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	duplicate := deploy.RequestOverlayV1{
		Schema: deploy.RequestOverlaySchemaV1,
		SelectedOptions: []deploy.QualifiedOption{
			{Component: "application", Option: "debug"},
			{Component: "application", Option: "debug"},
		},
		DirectPackages: []deploy.DirectPackageRequest{
			{Component: "application", Package: packageRequest},
			{Component: "application", Package: packageRequest},
		},
	}
	canonicalOverlay := deploy.RequestOverlayV1{
		Schema:          deploy.RequestOverlaySchemaV1,
		SelectedOptions: []deploy.QualifiedOption{{Component: "application", Option: "debug"}},
		DirectPackages:  []deploy.DirectPackageRequest{{Component: "application", Package: packageRequest}},
	}
	first, err := BuildResolvedRequestV1(document, duplicate, platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildResolvedRequestV1(document, canonicalOverlay, platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := providers.ResolvedRequestDigest(first, registry.ValidateResolvedRequestOwnersV1)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := providers.ResolvedRequestDigest(second, registry.ValidateResolvedRequestOwnersV1)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("resolved request digests differ: %s != %s", firstDigest, secondDigest)
	}
}

func TestBuildResolvedRequestV1ExcludesUnusedTranslationCandidatesFromIdentity(t *testing.T) {
	firstDocument := resolvedRequestTestDocument()
	firstDocument.Environment.Translations = map[string]blueprint.Translation{
		"workspace": {
			Type: blueprint.ComponentTypePython, Scope: blueprint.TranslationScopeDevelopment,
			Root: "/checkout/one", Mappings: map[string]string{"unused": "package"},
		},
	}
	secondDocument := resolvedRequestTestDocument()
	secondDocument.Environment.Translations = map[string]blueprint.Translation{
		"other": {
			Type: blueprint.ComponentTypePython, Scope: blueprint.TranslationScopeDevelopment,
			Root: "/checkout/two", Mappings: map[string]string{"also-unused": "src"},
		},
	}
	platform := firstDocument.Blueprint.Compatibility.Platforms[0]
	first, err := BuildResolvedRequestV1(firstDocument, deploy.EmptyRequestOverlayV1(), platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildResolvedRequestV1(secondDocument, deploy.EmptyRequestOverlayV1(), platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := providers.ResolvedRequestDigest(first, registry.ValidateResolvedRequestOwnersV1)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := providers.ResolvedRequestDigest(second, registry.ValidateResolvedRequestOwnersV1)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("unused translations changed provider request identity: %s != %s", firstDigest, secondDigest)
	}
	firstDocumentDigest, err := blueprint.DocumentDigestV1(firstDocument)
	if err != nil {
		t.Fatal(err)
	}
	secondDocumentDigest, err := blueprint.DocumentDigestV1(secondDocument)
	if err != nil {
		t.Fatal(err)
	}
	if firstDocumentDigest == secondDocumentDigest {
		t.Fatal("complete blueprint provenance did not retain translation changes")
	}
}

func TestFinalizeResolvedRequestV1UsesOnlyExactSelectedCandidates(t *testing.T) {
	document := resolvedRequestTestDocument()
	platform := document.Blueprint.Compatibility.Platforms[0]
	source := func(name string, manifest string, artifact string) providers.ResolvedSourceInput {
		return providers.ResolvedSourceInput{
			Schema: providers.ResolvedSourceInputSchemaV1, Component: "application", LogicalPackage: name,
			SourceManifestDigest: registryDigest(manifest), BuilderProfile: "python-wheel-v1",
			BuildSettings:     providers.CanonicalProviderData{Schema: "python-source-build-settings-v1", Value: canonical.Object{}},
			EcosystemMetadata: providers.CanonicalProviderData{Schema: "python-source-metadata-v1", Value: canonical.Object{}},
			ArtifactDigest:    registryDigest(artifact),
		}
	}
	selected := source("demo", "1", "2")
	unused := source("unused", "3", "4")
	candidateRequest, err := BuildResolvedRequestV1(
		document, deploy.EmptyRequestOverlayV1(), platform,
		[]providers.ResolvedSourceInput{selected, unused},
	)
	if err != nil {
		t.Fatal(err)
	}
	finalized, err := finalizeResolvedRequestV1(
		document, deploy.EmptyRequestOverlayV1(), candidateRequest,
		[]providers.ResolvedSourceInput{selected},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(finalized.Sources) != 1 || !reflect.DeepEqual(finalized.Sources[0], selected) {
		t.Fatalf("finalized sources = %#v", finalized.Sources)
	}
	changed := selected
	changed.ArtifactDigest = registryDigest("5")
	if _, err := finalizeResolvedRequestV1(document, deploy.EmptyRequestOverlayV1(), candidateRequest, []providers.ResolvedSourceInput{changed}); err == nil || !strings.Contains(err.Error(), "not an exact source candidate") {
		t.Fatalf("changed selected source error = %v", err)
	}
	locked, exact, err := resolvedRequestForLockedSourcesV1(
		document, deploy.EmptyRequestOverlayV1(), candidateRequest,
		[]providers.ResolvedSourceInput{selected},
	)
	if err != nil || !exact || len(locked.Sources) != 1 || !reflect.DeepEqual(locked.Sources[0], selected) {
		t.Fatalf("locked request = %#v, exact = %v, error = %v", locked, exact, err)
	}
	if _, exact, err := resolvedRequestForLockedSourcesV1(document, deploy.EmptyRequestOverlayV1(), candidateRequest, []providers.ResolvedSourceInput{changed}); err != nil || exact {
		t.Fatalf("changed locked source exact = %v, error = %v", exact, err)
	}
}

func TestResolvedRequestOwnersBindOuterComponentAndSourcesTargetActiveNodes(t *testing.T) {
	document := resolvedRequestTestDocument()
	document.Environment.Components["optional"] = blueprint.Component{
		Type: blueprint.ComponentTypePython,
		Python: &blueprint.PythonComponent{
			Interpreter:  blueprint.CommandRequirement{Command: "python", Supplier: "base"},
			Requirements: []string{},
		},
		Options: map[string]blueprint.ComponentOption{},
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	source := providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV1, Component: "optional", LogicalPackage: "demo",
		SourceManifestDigest: registryDigest("1"), BuilderProfile: "python-wheel-v1",
		BuildSettings:     providers.CanonicalProviderData{Schema: "python-source-build-settings-v1", Value: canonical.Object{}},
		EcosystemMetadata: providers.CanonicalProviderData{Schema: "python-source-metadata-v1", Value: canonical.Object{}},
		ArtifactDigest:    registryDigest("2"),
	}
	if _, err := BuildResolvedRequestV1(document, deploy.EmptyRequestOverlayV1(), platform, []providers.ResolvedSourceInput{source}); err == nil || !strings.Contains(err.Error(), "missing or unsupported") {
		t.Fatalf("inactive source error = %v", err)
	}
	request, err := BuildResolvedRequestV1(document, deploy.EmptyRequestOverlayV1(), platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	for index := range request.Components {
		if request.Components[index].Provider == blueprint.ComponentTypePython {
			request.Components[index].Request.Value["component"] = "other"
			break
		}
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("component binding error = %v", err)
	}
}

func TestBuildResolvedRequestV1RequiresBaseRoot(t *testing.T) {
	document := resolvedRequestTestDocument()
	delete(document.Environment.Components, "base")
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildResolvedRequestV1(document, deploy.EmptyRequestOverlayV1(), platform, []providers.ResolvedSourceInput{}); err == nil || !strings.Contains(err.Error(), "exactly one base") {
		t.Fatalf("missing base error = %v", err)
	}
}

func TestBuildResolvedRequestV1RejectsUndeclaredPlatform(t *testing.T) {
	document := resolvedRequestTestDocument()
	platform, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildResolvedRequestV1(
		document, deploy.EmptyRequestOverlayV1(), platform, []providers.ResolvedSourceInput{},
	)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared platform error = %v", err)
	}
}

func resolvedRequestTestDocument() blueprint.Document {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		panic(err)
	}
	return blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{platform}}},
		Environment: blueprint.Environment{Components: map[string]blueprint.Component{
			"base": {
				Type:    blueprint.ComponentTypeBase,
				Base:    &blueprint.BaseComponent{Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{}},
				Options: map[string]blueprint.ComponentOption{},
			},
			"application": {
				Type: blueprint.ComponentTypePython,
				Python: &blueprint.PythonComponent{
					Interpreter:  blueprint.CommandRequirement{Command: "python", Supplier: "system"},
					Requirements: []string{"demo==1"},
				},
				Options: map[string]blueprint.ComponentOption{
					"debug": {PythonRequirements: []string{"debugpy==1"}},
				},
			},
			"system": {
				Type: blueprint.ComponentTypeAPT,
				APT:  &blueprint.APTComponent{Packages: []blueprint.APTPackageRequest{{Name: "python3", Exports: map[string]blueprint.ExecutableExport{}}}},
				Options: map[string]blueprint.ComponentOption{
					"tools": {APTPackages: []blueprint.APTPackageRequest{{Name: "jq", Exports: map[string]blueprint.ExecutableExport{}}}},
				},
			},
		}}}
}
