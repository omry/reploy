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
			{Application: "system", Option: "tools"},
			{Application: "application", Option: "debug"},
		},
		DirectPackages: []deploy.DirectPackageRequest{
			{Contribution: "application/system/os", Package: aptDirect},
			{Contribution: "application/application/python", Package: pythonDirect},
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
	if !reflect.DeepEqual(names, []string{"application/application/python", "application/system/os", "base"}) {
		t.Fatalf("components = %#v", request.Components)
	}
	pythonRequest := request.Components[0].Request
	if got := len(pythonRequest.Value["requirements"].([]any)); got != 3 {
		t.Fatalf("Python requirements = %#v", pythonRequest.Value["requirements"])
	}
	aptRequest := request.Components[1].Request
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

func TestBuildResolvedRequestV1ExpandsOneApplicationOptionAcrossProviders(t *testing.T) {
	document := resolvedRequestTestDocument()
	application := document.Environment.Applications["application"]
	debug := application.Options["debug"]
	debug.Packages.OS = []blueprint.APTPackageRequest{{
		Name: "gdb", Exports: map[string]blueprint.ExecutableExport{},
	}}
	application.Options["debug"] = debug
	document.Environment.Applications["application"] = application
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}

	request, err := BuildResolvedRequestV1(
		document,
		deploy.RequestOverlayV1{
			Schema: deploy.RequestOverlaySchemaV1,
			SelectedOptions: []deploy.QualifiedOption{{
				Application: "application", Option: "debug",
			}},
			DirectPackages: []deploy.DirectPackageRequest{},
		},
		document.Blueprint.Compatibility.Platforms[0],
		[]providers.ResolvedSourceInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Plan(providers.PlanInput{
		Components: request.Components,
		Platform:   request.Platform,
	})
	if err != nil {
		t.Fatal(err)
	}
	var aptNode *providers.NodeSpec
	for index := range plan.Nodes {
		if plan.Nodes[index].Provider == blueprint.ComponentTypeAPT {
			aptNode = &plan.Nodes[index]
		}
	}
	if aptNode == nil || !reflect.DeepEqual(aptNode.Components, []string{
		"application/application/os",
		"application/system/os",
	}) {
		t.Fatalf("APT node contributions = %#v", aptNode)
	}
	pythonFound := false
	for _, component := range request.Components {
		if component.Component == "application/application/python" {
			pythonFound = len(component.Request.Value["requirements"].([]any)) == 2
		}
	}
	if !pythonFound {
		t.Fatalf("Python option contribution was not selected: %#v", request.Components)
	}
}

func TestBuildResolvedRequestWithOverridesReplacesOnlyBaseImage(t *testing.T) {
	document := resolvedRequestTestDocument()
	platform := document.Blueprint.Compatibility.Platforms[0]
	request, err := BuildResolvedRequestWithOverridesV1(
		document, deploy.EmptyRequestOverlayV1(),
		deploy.EmptyPackageOverrideIntentV1(document.Environment.ID),
		"python:3.13-slim", platform, []providers.ResolvedSourceInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, component := range request.Components {
		if component.Component == "base" {
			found = true
			if component.Request.Value["image"] != "python:3.13-slim" {
				t.Fatalf("resolved base request = %#v", component)
			}
		}
	}
	if !found {
		t.Fatal("resolved request has no base component")
	}
	if document.Environment.Base.Image == "python:3.13-slim" {
		t.Fatal("base override mutated the blueprint document")
	}
}

func TestBuildResolvedRequestPackageAdditionActivatesOSProviderWithoutMutatingBlueprint(t *testing.T) {
	document := resolvedRequestTestDocument()
	delete(document.Environment.Applications, "system")
	document.Environment.Base.Exports["python"] = blueprint.BaseExecutableExport{Executable: "/usr/bin/python3"}
	application := document.Environment.Applications["application"]
	application.Packages.Python.Interpreter.Supplier = "base"
	document.Environment.Applications["application"] = application
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}

	intent := deploy.EmptyPackageOverrideIntentV1(document.Environment.ID)
	intent.Additions = []deploy.PackageAdditionIntentV1{{
		Provider: "os", Requirement: "curl",
	}}
	request, err := BuildResolvedRequestWithPackageOverridesV1(
		document, deploy.EmptyRequestOverlayV1(), intent,
		document.Blueprint.Compatibility.Platforms[0], []providers.ResolvedSourceInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	var osComponent *providers.ResolvedComponentRequestV1
	for index := range request.Components {
		if request.Components[index].Provider == blueprint.ComponentTypeAPT {
			osComponent = &request.Components[index]
		}
	}
	if osComponent == nil || osComponent.Component != "environment/os" {
		t.Fatalf("OS package addition did not activate an OS component: %#v", request.Components)
	}
	packageValue := osComponent.Request.Value["components"].([]any)[0].(canonical.Object)["packages"].([]any)[0].(canonical.Object)
	value := packageValue["value"].(canonical.Object)
	if value["name"] != "curl" {
		t.Fatalf("OS package request = %#v", packageValue)
	}
	if _, exists := document.Environment.Components["environment/os"]; exists {
		t.Fatal("OS package addition mutated the blueprint document")
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
			{Application: "application", Option: "debug"},
			{Application: "application", Option: "debug"},
		},
		DirectPackages: []deploy.DirectPackageRequest{
			{Contribution: "application/application/python", Package: packageRequest},
			{Contribution: "application/application/python", Package: packageRequest},
		},
	}
	canonicalOverlay := deploy.RequestOverlayV1{
		Schema:          deploy.RequestOverlaySchemaV1,
		SelectedOptions: []deploy.QualifiedOption{{Application: "application", Option: "debug"}},
		DirectPackages:  []deploy.DirectPackageRequest{{Contribution: "application/application/python", Package: packageRequest}},
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

func TestFinalizeResolvedRequestV1RecordsGraphSelectedSources(t *testing.T) {
	document := resolvedRequestTestDocument()
	platform := document.Blueprint.Compatibility.Platforms[0]
	source := func(name string, manifest string, artifact string) providers.ResolvedSourceInput {
		return testPythonResolvedSource("application/application/python", name, "1.0", registryDigest(manifest), registryDigest(artifact))
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
	packageOverrides := deploy.EmptyPackageOverrideIntentV1(document.Environment.ID)
	plan, err := registry.Plan(providers.PlanInput{Components: candidateRequest.Components, Platform: candidateRequest.Platform})
	if err != nil {
		t.Fatal(err)
	}
	graph := providers.GraphExecutionResult{Plan: plan, Bundles: []providers.ResolvedBundle{}, SelectedSources: []providers.ResolvedSourceInput{selected}}
	finalized, relevant, err := finalizeResolvedRequestV1(
		document, deploy.EmptyRequestOverlayV1(), packageOverrides, candidateRequest,
		graph,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(relevant, packageOverrides) {
		t.Fatalf("relevant overrides = %#v", relevant)
	}
	if len(finalized.Sources) != 1 || !reflect.DeepEqual(finalized.Sources[0], selected) {
		t.Fatalf("finalized sources = %#v", finalized.Sources)
	}
	built := selected
	built.OutputArtifactDigest = registryDigest("5")
	graph.SelectedSources = []providers.ResolvedSourceInput{built}
	finalized, _, err = finalizeResolvedRequestV1(document, deploy.EmptyRequestOverlayV1(), packageOverrides, candidateRequest, graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(finalized.Sources) != 1 || !reflect.DeepEqual(finalized.Sources[0], built) {
		t.Fatalf("post-build finalized sources = %#v", finalized.Sources)
	}
	invalid := built
	invalid.Component = "missing"
	graph.SelectedSources = []providers.ResolvedSourceInput{invalid}
	if _, _, err := finalizeResolvedRequestV1(document, deploy.EmptyRequestOverlayV1(), packageOverrides, candidateRequest, graph); err == nil || !strings.Contains(err.Error(), "missing or unsupported") {
		t.Fatalf("invalid selected source error = %v", err)
	}
	changed := selected
	changed.OutputArtifactDigest = registryDigest("6")
	locked, exact, err := resolvedRequestForLockedSourcesV1(
		document, deploy.EmptyRequestOverlayV1(), packageOverrides, candidateRequest,
		[]providers.ResolvedSourceInput{selected},
	)
	if err != nil || !exact || len(locked.Sources) != 1 || !reflect.DeepEqual(locked.Sources[0], selected) {
		t.Fatalf("locked request = %#v, exact = %v, error = %v", locked, exact, err)
	}
	if _, exact, err := resolvedRequestForLockedSourcesV1(document, deploy.EmptyRequestOverlayV1(), packageOverrides, candidateRequest, []providers.ResolvedSourceInput{changed}); err != nil || exact {
		t.Fatalf("changed locked source exact = %v, error = %v", exact, err)
	}
}

func TestResolvedRequestForLockedBuildIgnoresUnrelatedPackageOverride(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{fixture.request.Platform}}},
		Environment: blueprint.Environment{
			ID: "demo",
			Base: blueprint.BaseComponent{
				Image:   fixture.lock.Base.AuthorReference,
				Exports: map[string]blueprint.BaseExecutableExport{"python": {Executable: "/usr/bin/python3"}},
			},
			Applications: map[string]blueprint.Application{
				"application": {
					Packages: blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{
						Interpreter:  blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
						Requirements: []string{"demo-server==1.0"},
					}},
					Options: map[string]blueprint.ApplicationOption{},
				},
			},
		},
	}
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	fullIntent := deploy.PackageOverrideIntentV1{
		Schema: deploy.PackageOverrideIntentSchemaV1, EnvironmentID: "demo",
		Choices: []deploy.PackageOverrideIntentChoiceV1{
			{Provider: "python", Package: "demo-server", Kind: "local"},
			{Provider: "python", Package: "unused", Kind: "version", Version: "99"},
		},
	}
	candidate, err := BuildResolvedRequestWithPackageOverridesV1(
		document, deploy.EmptyRequestOverlayV1(), fullIntent, fixture.request.Platform,
		append([]providers.ResolvedSourceInput{}, fixture.request.SourceCandidates...),
	)
	if err != nil {
		t.Fatal(err)
	}
	lockedSources, err := buildLockSelectedSourcesV1(fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	request, relevant, exact, err := resolvedRequestForLockedBuildV1(
		document, deploy.EmptyRequestOverlayV1(), fullIntent, candidate,
		lockedSources, fixture.lock, fixture.store,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exact || !reflect.DeepEqual(relevant, fixture.lock.PackageOverrides) {
		t.Fatalf("exact=%v relevant=%#v", exact, relevant)
	}
	for _, component := range request.Components {
		if component.Provider != blueprint.ComponentTypePython {
			continue
		}
		overrides := component.Request.Value["overrides"].([]any)
		if len(overrides) != 1 {
			t.Fatalf("closure-relevant Python overrides = %#v", overrides)
		}
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
	source := testPythonResolvedSource("optional", "demo", "1.0", registryDigest("1"), registryDigest("2"))
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
	document.Environment.Base = blueprint.BaseComponent{}
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

func TestBuildResolvedRequestV1RejectsUnresolvedRuntimePortableTools(t *testing.T) {
	source, err := blueprint.Decode([]byte(`
blueprint:
  schema: 1
  version: test
  compatibility: {platforms: [linux/amd64]}
environment:
  id: demo
  base: {image: docker.io/library/debian:13-slim}
  applications:
    z-tools:
      packages: {tools: [tool:asciinema==3.2.1]}
    a-tools:
      packages: {tools: [tool:playwright==1.61.0, tool:java==21]}
docker: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Environment.Applications["a-tools"].Packages.Tools) != 2 {
		t.Fatalf("runtime tool demand was not retained: %#v", document.Environment.Applications)
	}
	_, err = BuildResolvedRequestV1(
		document,
		deploy.EmptyRequestOverlayV1(),
		document.Blueprint.Compatibility.Platforms[0],
		[]providers.ResolvedSourceInput{},
	)
	if err == nil ||
		!strings.Contains(err.Error(), `application "a-tools" has unresolved runtime portable tools [java playwright]`) ||
		!strings.Contains(err.Error(), "ordinary provider-request boundary") {
		t.Fatalf("unresolved runtime tool error = %v", err)
	}

	withoutTools := resolvedRequestTestDocument()
	if _, err := BuildResolvedRequestV1(
		withoutTools,
		deploy.EmptyRequestOverlayV1(),
		withoutTools.Blueprint.Compatibility.Platforms[0],
		[]providers.ResolvedSourceInput{},
	); err != nil {
		t.Fatalf("request without runtime tool demand: %v", err)
	}
}

func resolvedRequestTestDocument() blueprint.Document {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		panic(err)
	}
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{platform}}},
		Environment: blueprint.Environment{
			ID:   "demo",
			Base: blueprint.BaseComponent{Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{}},
			Applications: map[string]blueprint.Application{
				"application": {
					Packages: blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{
						Interpreter: blueprint.CommandRequirement{
							Command: "python", Supplier: "application/system/os",
						},
						Requirements: []string{"demo==1"},
					}},
					Options: map[string]blueprint.ApplicationOption{
						"debug": {
							Packages: blueprint.ApplicationOptionPackages{
								Python: &blueprint.PythonOptionPackages{Requirements: []string{"debugpy==1"}},
							},
						},
					},
				},
				"system": {
					Packages: blueprint.ApplicationPackages{
						OS: []blueprint.APTPackageRequest{{
							Name: "python3", Exports: map[string]blueprint.ExecutableExport{},
						}},
					},
					Options: map[string]blueprint.ApplicationOption{
						"tools": {
							Packages: blueprint.ApplicationOptionPackages{
								OS: []blueprint.APTPackageRequest{{
									Name: "jq", Exports: map[string]blueprint.ExecutableExport{},
								}},
							},
						},
					},
				},
			},
		},
	}
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		panic(err)
	}
	return document
}
