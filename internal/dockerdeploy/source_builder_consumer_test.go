package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolrequest"
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
		Facts:          providers.CanonicalProviderData{Schema: pythonprovider.InterpreterFactsSchemaV1, Value: canonical.Object{"version": "3.13.2"}},
	}
}

func TestPythonResolverSessionSourceBuildEnvironmentIdentityBindsUpstreamAndSelections(t *testing.T) {
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	plain := &PythonResolverSession{descriptor: upstream, upstream: upstream, inspected: map[string]string{"/usr/bin/python3": "3.13.2"}}
	interpreter := sourceBuilderTestInterpreter()
	plainDigest, err := plain.SourceBuildEnvironmentDigest(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	firstBuilder := sourceBuilderTestImageDescriptor(t, "2")
	first := &PythonResolverSession{descriptor: firstBuilder, upstream: firstBuilder, containerName: "first", inspected: map[string]string{"/usr/bin/python3": "3.13.2"}}
	if err := first.BindSourceBuilder(sourceBuilderTestEnvironment(t, firstBuilder, upstream)); err != nil {
		t.Fatal(err)
	}
	firstDigest, err := first.SourceBuildEnvironmentDigest(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	secondBuilder := sourceBuilderTestImageDescriptor(t, "3")
	second := &PythonResolverSession{descriptor: secondBuilder, upstream: secondBuilder, containerName: "second", inspected: map[string]string{"/usr/bin/python3": "3.13.2"}}
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
	if firstDigest != secondDigest {
		t.Fatal("different disposable builder image IDs changed the source-build environment identity")
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

func TestPythonResolverSessionRequiresThePlannedRecipeForToolRequirements(t *testing.T) {
	recipe := PythonLocalSourceRecipeV1{
		Found: true, Project: "demo", Build: PythonBuildTypeSetuptoolsLegacy, Digest: rendererDigest("5"),
		Requirements: []toolrequest.CanonicalRequirementGroupV1{{Scope: "source-builder:demo", Tool: "java", Context: "build"}},
	}
	unbound := &PythonResolverSession{}
	if err := unbound.requireSourceBuilderRecipe("demo", recipe); err == nil || !strings.Contains(err.Error(), "requires a portable source-builder environment before Python resolution") {
		t.Fatalf("unbound error = %v", err)
	}
	builder := sourceBuilderTestImageDescriptor(t, "2")
	environment := sourceBuilderTestEnvironment(t, builder, sourceBuilderTestImageDescriptor(t, "1"))
	session := &PythonResolverSession{descriptor: builder, upstream: builder, containerName: "builder-session"}
	if err := session.BindSourceBuilder(environment); err != nil {
		t.Fatal(err)
	}
	if err := session.requireSourceBuilderRecipe("demo", recipe); err == nil || !strings.Contains(err.Error(), "did not resolve") {
		t.Fatalf("unplanned recipe error = %v", err)
	}
	environment.Recipes["demo"] = SourceBuilderRecipeIdentityV1{Scope: "source-builder:demo", Digest: rendererDigest("6"), Groups: recipe.Requirements}
	if err := session.requireSourceBuilderRecipe("demo", recipe); err == nil || !strings.Contains(err.Error(), "changed after its source-builder environment was planned") {
		t.Fatalf("drifted recipe error = %v", err)
	}
	environment.Recipes["demo"] = SourceBuilderRecipeIdentityV1{Scope: "source-builder:demo", Digest: recipe.Digest, Groups: recipe.Requirements}
	if err := session.requireSourceBuilderRecipe("demo", recipe); err != nil {
		t.Fatalf("planned recipe rejected: %v", err)
	}
}

func TestPreparedPythonNodeOperationsBuildWithPreparedSourceBuilderAfterRecipeCheck(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	sourceDir := writeSourceBuilderTestRecipe(t, "demo", "[tool:java==21]")
	recipe, err := ReadPythonLocalSourceRecipeV1(sourceDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	builder := sourceBuilderTestImageDescriptor(t, "2")
	environment := sourceBuilderTestEnvironment(t, builder, sourceBuilderTestImageDescriptor(t, "1"))
	environment.Recipes["demo"] = SourceBuilderRecipeIdentityV1{Scope: "source-builder:demo", Digest: recipe.Digest, Groups: recipe.Requirements}
	buildAttempted := false
	session := &PythonResolverSession{
		descriptor: builder, upstream: builder, artifacts: artifacts, containerName: "portable-tool-test",
		runDocker: func(spec CommandSpec, options RunOptions) error {
			if containsInOrder(spec.Args, []string{"-m", "uv", "build"}) {
				buildAttempted = true
				if !containsInOrder(spec.Args, []string{"PATH=" + SourceBuilderExportsDirectoryV1 + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}) {
					t.Fatalf("source build without exports on PATH: %#v", spec.Args)
				}
				_, _ = options.Stderr.Write([]byte("javac: build backend stopped for the test\n"))
				return errors.New("exit status 7")
			}
			return nil
		},
	}
	if err := session.BindSourceBuilder(environment); err != nil {
		t.Fatal(err)
	}
	operations := PreparedPythonNodeOperations{Store: store, Artifacts: artifacts}
	_, _, err = operations.materializeLocalOverrides(
		context.Background(), session,
		providers.ValidatedExecutableInput{}, providers.ExecutableRequirement{},
		providers.ExecutableEvidence{}, rendererDigest("a"), "application",
		[]PythonLocalOverrideV1{{Distribution: "demo", HostDir: sourceDir}},
		[]providers.ResolvedSourceInput{}, []providerstore.ArtifactDescriptor{},
	)
	if err == nil || strings.Contains(err.Error(), "requires a portable source-builder environment") || !strings.Contains(err.Error(), "environment launcher") {
		t.Fatalf("error = %v, want the recipe gate to pass and the source build to reach its launcher check", err)
	}
	if buildAttempted {
		t.Fatal("source build ran without a validated environment launcher")
	}
}

func TestPythonNodePreparerPreparesTheSourceBuilderBeforeOpeningTheSession(t *testing.T) {
	upstream := testProbeImageDescriptor(t, "linux/amd64")
	builder := sourceBuilderTestImageDescriptor(t, "2")
	builder.RootFSDiffIDs = upstream.RootFSDiffIDs
	workspace := testPreparedProbeWorkspace(t, upstream.Platform, t.TempDir())
	_, probeResponse := pythonResolverProbeExchange()
	commands, _ := stubPythonResolverCommands(t, mustCanonicalProbeResponse(t, probeResponse), nil, nil)
	order := []string{}
	previousRemove := removeSourceBuilderLayerV1
	t.Cleanup(func() { removeSourceBuilderLayerV1 = previousRemove })
	removeSourceBuilderLayerV1 = func(context.Context, BuiltImageCandidate) error {
		order = append(order, "remove-builder")
		return nil
	}
	environment := sourceBuilderTestEnvironment(t, builder, upstream)
	preparer := PythonNodePreparer{
		Descriptor: upstream, Workspace: workspace, Artifacts: testPreparedPythonResolverArtifacts(t),
		PrepareBuilder: func(_ context.Context, got deploy.ImageDescriptor) (*SourceBuilderEnvironmentV1, error) {
			order = append(order, "prepare-builder")
			if !reflect.DeepEqual(got, upstream) || len(*commands) != 0 {
				t.Fatalf("builder prepared from %#v after %d Docker commands", got, len(*commands))
			}
			return environment, nil
		},
		ValidateCached: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest, providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			return providers.GraphConsumerValidation{}, nil
		},
		ResolveFresh: func(_ context.Context, session *PythonResolverSession, _ providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			order = append(order, "resolve")
			if session.sourceBuilder != environment || !reflect.DeepEqual(session.descriptor, builder) || !reflect.DeepEqual(session.upstream, upstream) {
				t.Fatalf("session was not opened on the prepared source-builder image: %#v", session.descriptor)
			}
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, nil
		},
	}
	if _, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{Resolve: pythonNodePreparationRequest(t, upstream)}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"prepare-builder", "resolve", "remove-builder"}) {
		t.Fatalf("order = %#v", order)
	}
	created := false
	for _, command := range *commands {
		if len(command.Args) != 0 && command.Args[0] == "create" && containsInOrder(command.Args, []string{string(builder.ConfigDigest)}) {
			created = true
		}
	}
	if !created {
		t.Fatalf("resolver container was not created from the builder image: %#v", *commands)
	}
	removeIndex, closeIndex := -1, -1
	for index, entry := range order {
		if entry == "remove-builder" {
			removeIndex = index
		}
	}
	for index, command := range *commands {
		if len(command.Args) != 0 && command.Args[0] == "rm" {
			closeIndex = index
		}
	}
	if removeIndex < 0 || closeIndex < 0 {
		t.Fatalf("session close (%d) and builder removal (%d) both must happen", closeIndex, removeIndex)
	}
}

func TestPythonNodePreparerFailsBeforeDockerWhenTheSourceBuilderCannotBePrepared(t *testing.T) {
	upstream := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, upstream.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	want := errors.New("acquisition exhausted")
	preparer := PythonNodePreparer{
		Descriptor: upstream, Workspace: workspace, Artifacts: testPreparedPythonResolverArtifacts(t),
		PrepareBuilder: func(context.Context, deploy.ImageDescriptor) (*SourceBuilderEnvironmentV1, error) { return nil, want },
		ValidateCached: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest, providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			return providers.GraphConsumerValidation{}, nil
		},
		ResolveFresh: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			t.Fatal("resolved without a source-builder environment")
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, nil
		},
	}
	if _, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{Resolve: pythonNodePreparationRequest(t, upstream)}); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	if len(*commands) != 0 {
		t.Fatalf("failed builder preparation reached Docker: %#v", *commands)
	}
}

func TestPreparedPythonNodeOperationsWireSourceBuilderIntoThePreparer(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	plain := PreparedPythonNodeOperations{}.Preparer(descriptor, workspace)
	if plain.PrepareBuilder != nil {
		t.Fatal("node without portable tools prepares a source builder")
	}
	tools := &SourceBuilderPortableToolsV1{Plan: &SourceBuilderPortableToolPlanV1{}, workspace: t.TempDir(), contextDir: t.TempDir()}
	prepared := PreparedPythonNodeOperations{SourceBuilder: tools}.Preparer(descriptor, workspace)
	if prepared.PrepareBuilder == nil {
		t.Fatal("node with portable tools does not prepare a source builder")
	}
	previous := prepareSourceBuilderEnvironmentV1
	t.Cleanup(func() { prepareSourceBuilderEnvironmentV1 = previous })
	var gotTools *SourceBuilderPortableToolsV1
	var gotUpstream deploy.ImageDescriptor
	prepareSourceBuilderEnvironmentV1 = func(_ context.Context, _ providerstore.Store, got *SourceBuilderPortableToolsV1, upstream deploy.ImageDescriptor, _ RunOptions) (*SourceBuilderEnvironmentV1, error) {
		gotTools, gotUpstream = got, upstream
		return &SourceBuilderEnvironmentV1{}, nil
	}
	if _, err := prepared.PrepareBuilder(context.Background(), descriptor); err != nil {
		t.Fatal(err)
	}
	if gotTools != tools || !reflect.DeepEqual(gotUpstream, descriptor) {
		t.Fatalf("builder prepared with %#v from %#v", gotTools, gotUpstream)
	}
}

func TestExecutePreparedPythonGraphPassesSourceBuilderToEveryPythonNode(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	tools := &SourceBuilderPortableToolsV1{Plan: &SourceBuilderPortableToolPlanV1{}}
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
		SourceBuilder: tools, FinalImageConfig: pythonConsumerTestImageConfig(),
	})
	if err == nil || !strings.Contains(err.Error(), "stop before the graph executes") {
		t.Fatalf("error = %v", err)
	}
	if len(gotConfigs) == 0 {
		t.Fatal("no Python node configs were prepared")
	}
	for id, config := range gotConfigs {
		if config.SourceBuilder != tools {
			t.Fatalf("node %s did not receive the source-builder tools", id)
		}
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
