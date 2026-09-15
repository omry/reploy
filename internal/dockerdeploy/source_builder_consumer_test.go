package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func sourceBuilderTestEnvironment(t *testing.T, builder deploy.ImageDescriptor, upstream deploy.ImageDescriptor) *SourceBuilderEnvironmentV1 {
	t.Helper()
	return &SourceBuilderEnvironmentV1{
		Descriptor: builder, Upstream: upstream, ExportsDirectory: SourceBuilderExportsDirectoryV1,
		Exports: []providers.PortableToolExportV1{{Name: "java", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/java"}},
		Selections: []SourceBuilderPortableToolSelectionV1{{
			Scope: "source-builder:demo", Tool: "java", Version: "21", Revision: "1",
			ManifestDigest: rendererDigest("a"), SelectedClosureDigest: rendererDigest("c"),
		}},
		Recipes:   map[string]SourceBuilderRecipeIdentityV1{},
		candidate: BuiltImageCandidate{ImageID: builder.ConfigDigest},
	}
}

func sourceBuilderTestInterpreter() providers.ExecutableEvidence {
	return providers.ExecutableEvidence{
		InvocationPath: "/usr/bin/python3",
		Facts:          pythonInterpreterFactsV2ForTest("3.13.2"),
	}
}

func TestPythonResolverSessionSourceBuildEnvironmentIdentityBindsExactBuilderAndSelections(t *testing.T) {
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	plain := &PythonResolverSession{descriptor: upstream, upstream: upstream, inspected: map[string]pythonprovider.InterpreterInspectionFactsV2{"/usr/bin/python3": pythonInspectionFactsV2ForTest("3.13.2", nil, nil)}}
	interpreter := sourceBuilderTestInterpreter()
	plainDigest, err := plain.SourceBuildEnvironmentDigest(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	firstBuilder := sourceBuilderTestImageDescriptor(t, "2")
	first := &PythonResolverSession{descriptor: firstBuilder, upstream: firstBuilder, containerName: "first", inspected: map[string]pythonprovider.InterpreterInspectionFactsV2{"/usr/bin/python3": pythonInspectionFactsV2ForTest("3.13.2", nil, nil)}}
	if err := first.BindSourceBuilder(sourceBuilderTestEnvironment(t, firstBuilder, upstream)); err != nil {
		t.Fatal(err)
	}
	firstDigest, err := first.SourceBuildEnvironmentDigest(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	secondBuilder := sourceBuilderTestImageDescriptor(t, "3")
	second := &PythonResolverSession{descriptor: secondBuilder, upstream: secondBuilder, containerName: "second", inspected: map[string]pythonprovider.InterpreterInspectionFactsV2{"/usr/bin/python3": pythonInspectionFactsV2ForTest("3.13.2", nil, nil)}}
	if err := second.BindSourceBuilder(sourceBuilderTestEnvironment(t, secondBuilder, upstream)); err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.SourceBuildEnvironmentDigest(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == plainDigest {
		t.Fatal("portable-tool selections did not change the source-build environment identity")
	}
	if firstDigest == secondDigest {
		t.Fatal("different prepared builder images did not change the source-build environment identity")
	}
	changed := sourceBuilderTestEnvironment(t, secondBuilder, upstream)
	changed.Selections[0].SelectedClosureDigest = rendererDigest("d")
	if err := second.BindSourceBuilder(changed); err != nil {
		t.Fatal(err)
	}
	changedDigest, err := second.SourceBuildEnvironmentDigest(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == secondDigest {
		t.Fatal("a changed selected closure did not change the source-build environment identity")
	}
	mismatched := &PythonResolverSession{descriptor: upstream, upstream: upstream, containerName: "other"}
	if err := mismatched.BindSourceBuilder(sourceBuilderTestEnvironment(t, firstBuilder, upstream)); err == nil || !strings.Contains(err.Error(), "not created from the prepared source-builder image") {
		t.Fatalf("mismatched bind error = %v", err)
	}
}

func TestPythonResolverSessionRejectsConflictingFactsForInspectedInterpreterPath(t *testing.T) {
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	inspected := pythonInspectionFactsV2ForTest("3.13.2", []string{"py3-none-any"}, []string{"py3-none-any"})
	session := &PythonResolverSession{
		descriptor: upstream,
		upstream:   upstream,
		inspected: map[string]pythonprovider.InterpreterInspectionFactsV2{
			"/usr/bin/python3": inspected,
		},
	}
	interpreter := sourceBuilderTestInterpreter()
	interpreter.Facts = pythonprovider.CanonicalInterpreterFactsV2(
		pythonInspectionFactsV2ForTest("3.13.2", []string{"py3-none-any"}, []string{}),
	)
	if _, err := session.SourceBuildEnvironmentDigest(interpreter); err == nil ||
		!strings.Contains(err.Error(), "interpreter was not inspected in this container") {
		t.Fatalf("conflicting interpreter facts error = %v", err)
	}
}

func TestPythonResolverSessionExposesSelectedExportsOnlyToSourceBuilds(t *testing.T) {
	builder := sourceBuilderTestImageDescriptor(t, "2")
	commands := []CommandSpec{}
	session := &PythonResolverSession{
		descriptor: builder, upstream: builder, containerName: "builder-session",
		runDocker: func(spec CommandSpec, _ RunOptions) error {
			commands = append(commands, spec)
			return nil
		},
	}
	launcher := providers.ValidatedExecutableInput{Evidence: providers.ExecutableEvidence{InvocationPath: "/usr/bin/env"}}
	if err := session.runWheelEnvironmentCommand(context.Background(), launcher, "/usr/bin/python3", "probe", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if err := session.BindSourceBuilder(sourceBuilderTestEnvironment(t, builder, sourceBuilderTestImageDescriptor(t, "1"))); err != nil {
		t.Fatal(err)
	}
	if err := session.runWheelEnvironmentCommand(context.Background(), launcher, "/usr/bin/python3", "probe", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %#v", commands)
	}
	if !containsInOrder(commands[0].Args, []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}) {
		t.Fatalf("unbound source-build PATH = %#v", commands[0].Args)
	}
	if !containsInOrder(commands[1].Args, []string{"PATH=" + SourceBuilderExportsDirectoryV1 + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}) {
		t.Fatalf("bound source-build PATH = %#v", commands[1].Args)
	}
}

func TestExecutePreparedPythonGraphPassesSourceBuilderToEveryPythonNode(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	coordinator := NewSourceBuilderCoordinatorV1(SourceBuilderCoordinatorInputV1{})
	previous := preparePythonGraphExecutionBackend
	t.Cleanup(func() { preparePythonGraphExecutionBackend = previous })
	var gotConfigs map[providers.NodeID]PreparedPythonNodeConfig
	preparePythonGraphExecutionBackend = func(_ context.Context, _ providerstore.Store, _ providers.ProviderPlanV1, _ deploy.ImageDescriptor, _ providers.ImageConfigPolicy, configs map[providers.NodeID]PreparedPythonNodeConfig, _ map[providers.NodeID]PreparedAPTNodeConfig, _ RunOptions) (PreparedPythonGraphBackend, func() error, error) {
		gotConfigs = configs
		return PreparedPythonGraphBackend{}, func() error { return nil }, errors.New("stop before the graph executes")
	}
	_, err := ExecutePreparedPythonGraph(context.Background(), PreparedPythonGraphExecutionInput{
		Store: fixture.store, Plan: fixture.request.Plan, BaseDescriptor: testProbeImageDescriptor(t, "linux/amd64"),
		BaseCatalog: []providers.RealizedOutput{}, Sources: []providers.ResolvedSourceInput{},
		SourceWheels: []providerstore.ArtifactDescriptor{}, LocalOverrides: []PythonLocalOverrideV1{},
		SourceBuilder: coordinator, FinalImageConfig: pythonConsumerTestImageConfig(),
	})
	if err == nil || !strings.Contains(err.Error(), "stop before the graph executes") {
		t.Fatalf("error = %v", err)
	}
	if len(gotConfigs) == 0 {
		t.Fatal("no Python node configs were prepared")
	}
	for id, config := range gotConfigs {
		if config.SourceBuilder != coordinator {
			t.Fatalf("node %s did not receive the source-builder coordinator", id)
		}
	}
}

func TestPreparedPythonNodeOperationsOpensDistinctBuilderConsumerAfterSelection(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts, cleanupArtifacts, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupArtifacts()
	planned := [][]SourceBuilderSelectedRecipeV1{}
	stubSourceBuilderCoordinator(t, &planned)
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	builder := sourceBuilderTestImageDescriptor(t, "2")
	prepareSelectedSourceBuilderEnvironmentV1 = func(_ context.Context, _ providerstore.Store, tools *SourceBuilderPortableToolsV1, got deploy.ImageDescriptor, _ RunOptions) (*SourceBuilderEnvironmentV1, error) {
		if !reflect.DeepEqual(got, upstream) {
			t.Fatalf("builder upstream = %#v", got)
		}
		return &SourceBuilderEnvironmentV1{
			Descriptor: builder, Upstream: upstream, ExportsDirectory: SourceBuilderExportsDirectoryV1,
			Recipes: tools.Plan.Recipes, removed: true,
		}, nil
	}
	previousWorkspace := preparePythonSourceBuilderWorkspaceV1
	previousOpen := openPythonSourceBuilderSessionV1
	previousValidate := validatePythonSourceBuilderConsumerV1
	previousSelect := selectPythonSourceBuilderInterpreterV1
	t.Cleanup(func() {
		preparePythonSourceBuilderWorkspaceV1 = previousWorkspace
		openPythonSourceBuilderSessionV1 = previousOpen
		validatePythonSourceBuilderConsumerV1 = previousValidate
		selectPythonSourceBuilderInterpreterV1 = previousSelect
	})
	workspaceCleaned := false
	preparePythonSourceBuilderWorkspaceV1 = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return PreparedProbeWorkspace{}, func() error { workspaceCleaned = true; return nil }, nil
	}
	var opened *PythonResolverSession
	openPythonSourceBuilderSessionV1 = func(_ context.Context, got deploy.ImageDescriptor, _ PreparedProbeWorkspace, gotArtifacts PreparedPythonResolverArtifacts) (*PythonResolverSession, error) {
		if !reflect.DeepEqual(got, builder) || !reflect.DeepEqual(gotArtifacts, artifacts) {
			t.Fatalf("source-build consumer input = %#v / %#v", got, gotArtifacts)
		}
		opened = &PythonResolverSession{
			descriptor: builder, upstream: builder, artifacts: artifacts, containerName: "selected-source-builder",
			runDocker: func(CommandSpec, RunOptions) error { return nil },
		}
		return opened, nil
	}
	launcher := providers.ValidatedExecutableInput{}
	validatePythonSourceBuilderConsumerV1 = func(_ context.Context, got *PythonResolverSession, _ providers.ImageConfigPolicy) (providers.GraphConsumerValidation, error) {
		if got != opened || got.sourceBuilder == nil {
			t.Fatal("source-build consumer was validated before binding its environment")
		}
		return providers.GraphConsumerValidation{EnvironmentLauncher: launcher}, nil
	}
	interpreter := sourceBuilderTestInterpreter()
	selectPythonSourceBuilderInterpreterV1 = func(_ context.Context, got *PythonResolverSession, _ providers.ValidatedExecutableInput, _ providers.ExecutableRequirement, candidates []providers.RealizedOutput) (providers.ExecutableEvidence, error) {
		if got != opened || len(candidates) != 1 || candidates[0].Candidate.InvocationPath != interpreter.InvocationPath {
			t.Fatalf("source-build interpreter candidates = %#v", candidates)
		}
		return interpreter, nil
	}
	dir := writeSourceBuilderTestRecipe(t, "demo", "[tool:java==21]")
	recipe, err := ReadPythonLocalSourceRecipeV1(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewSourceBuilderCoordinatorV1(SourceBuilderCoordinatorInputV1{Store: store})
	defer coordinator.Cleanup()
	resolver := &PythonResolverSession{descriptor: upstream, upstream: upstream, artifacts: artifacts}
	operations := PreparedPythonNodeOperations{Store: store, Artifacts: artifacts, SourceBuilder: coordinator}
	buildSession, gotLauncher, gotInterpreter, cleanup, err := operations.openSourceBuilderSession(
		t.Context(), resolver, providers.ExecutableRequirement{}, interpreter,
		[]SourceBuilderSelectedRecipeV1{{Distribution: "demo", Recipe: recipe}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if buildSession != opened || !reflect.DeepEqual(gotLauncher, launcher) || !reflect.DeepEqual(gotInterpreter, interpreter) ||
		buildSession.sourceBuilder == nil || !reflect.DeepEqual(resolver.descriptor, upstream) || resolver.sourceBuilder != nil {
		t.Fatalf("resolver/build sessions = %#v / %#v", resolver, buildSession)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if !workspaceCleaned || !opened.closed {
		t.Fatalf("cleanup state: workspace=%t session=%t", workspaceCleaned, opened.closed)
	}
}

func TestPythonResolverSessionBindSourceBuilderRejectsIncompleteEnvironments(t *testing.T) {
	builder := sourceBuilderTestImageDescriptor(t, "2")
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	closed := &PythonResolverSession{descriptor: builder, upstream: builder, closed: true}
	if err := closed.BindSourceBuilder(sourceBuilderTestEnvironment(t, builder, upstream)); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("closed session error = %v", err)
	}
	session := &PythonResolverSession{descriptor: builder, upstream: builder, containerName: "builder-session"}
	if err := session.BindSourceBuilder(nil); err == nil || !strings.Contains(err.Error(), "requires a prepared source-builder environment") {
		t.Fatalf("nil environment error = %v", err)
	}
	badUpstream := sourceBuilderTestEnvironment(t, builder, deploy.ImageDescriptor{})
	if err := session.BindSourceBuilder(badUpstream); err == nil || !strings.Contains(err.Error(), "upstream descriptor") {
		t.Fatalf("invalid upstream error = %v", err)
	}
	badExports := sourceBuilderTestEnvironment(t, builder, upstream)
	badExports.ExportsDirectory = "relative/exports"
	if err := session.BindSourceBuilder(badExports); err == nil || !strings.Contains(err.Error(), "exports directory") {
		t.Fatalf("relative exports error = %v", err)
	}
	if session.sourceBuilder != nil || !reflect.DeepEqual(session.upstream, builder) {
		t.Fatal("rejected environments changed the session")
	}
}
