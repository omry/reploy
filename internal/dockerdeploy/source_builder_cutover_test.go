package dockerdeploy

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func TestJavaSourceBuilderCutoverRemainsContainedToDisposableBuilder(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	providerPlanBefore, err := canonical.Marshal(fixture.request.Plan)
	if err != nil {
		t.Fatal(err)
	}
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	builder := sourceBuilderTestImageDescriptor(t, "2")
	stubSourceBuilderTarget(t, sourceBuilderJavaTarget("debian", "12"), nil)
	materialization := &sourceBuilderMaterializationStub{}
	stubSourceBuilderMaterialization(t, materialization)
	environmentCalls := &sourceBuilderEnvironmentStub{}
	stubSourceBuilderEnvironment(t, environmentCalls, builder)
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
		return PreparedProbeWorkspace{}, func() error {
			workspaceCleaned = true
			return nil
		}, nil
	}
	var commands []CommandSpec
	var opened *PythonResolverSession
	openPythonSourceBuilderSessionV1 = func(_ context.Context, descriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace, artifacts PreparedPythonResolverArtifacts) (*PythonResolverSession, error) {
		if !reflect.DeepEqual(descriptor, builder) {
			t.Fatalf("opened source-build consumer from %#v, want builder %#v", descriptor, builder)
		}
		opened = &PythonResolverSession{
			descriptor: descriptor, upstream: descriptor, artifacts: artifacts, containerName: "java-source-builder",
			runDocker: func(spec CommandSpec, _ RunOptions) error {
				commands = append(commands, spec)
				return nil
			},
		}
		return opened, nil
	}
	launcher := providers.ValidatedExecutableInput{Evidence: providers.ExecutableEvidence{InvocationPath: "/usr/bin/env"}}
	validatePythonSourceBuilderConsumerV1 = func(_ context.Context, session *PythonResolverSession, _ providers.ImageConfigPolicy) (providers.GraphConsumerValidation, error) {
		if session != opened || session.sourceBuilder == nil {
			t.Fatal("source-build consumer was validated before its prepared environment was bound")
		}
		return providers.GraphConsumerValidation{EnvironmentLauncher: launcher}, nil
	}
	interpreter := sourceBuilderTestInterpreter()
	selectPythonSourceBuilderInterpreterV1 = func(_ context.Context, session *PythonResolverSession, _ providers.ValidatedExecutableInput, _ providers.ExecutableRequirement, _ []providers.RealizedOutput) (providers.ExecutableEvidence, error) {
		if session != opened {
			t.Fatal("selected the source-build interpreter in the wrong consumer")
		}
		return interpreter, nil
	}

	recipeDir := writeSourceBuilderTestRecipe(t, "demo-server", "[tool:java==21]")
	recipe, err := ReadPythonLocalSourceRecipeV1(recipeDir, "demo-server")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewSourceBuilderCoordinatorV1(SourceBuilderCoordinatorInputV1{
		Store:           fixture.store,
		Base:            upstream,
		ProviderPlan:    fixture.request.Plan,
		BlueprintDigest: rendererDigest("b"),
		ReployVersion:   "0.7.0.dev1",
	})
	resolver := &PythonResolverSession{descriptor: upstream, upstream: upstream}
	operations := PreparedPythonNodeOperations{Store: fixture.store, SourceBuilder: coordinator}
	buildSession, gotLauncher, gotInterpreter, cleanup, err := operations.openSourceBuilderSession(
		t.Context(), resolver, providers.ExecutableRequirement{}, interpreter,
		[]SourceBuilderSelectedRecipeV1{{Distribution: "demo-server", Recipe: recipe}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if buildSession != opened || !reflect.DeepEqual(gotLauncher, launcher) || !reflect.DeepEqual(gotInterpreter, interpreter) ||
		buildSession.sourceBuilder == nil || resolver.sourceBuilder != nil || !reflect.DeepEqual(resolver.descriptor, upstream) {
		t.Fatalf("resolver/build consumers = %#v / %#v", resolver, buildSession)
	}
	environment := buildSession.sourceBuilder
	if len(coordinator.tools) != 1 {
		t.Fatalf("materialized tool sets = %d, want 1", len(coordinator.tools))
	}
	workspace := coordinator.tools[0].workspace
	t.Cleanup(func() {
		_ = cleanup()
		_ = coordinator.Cleanup()
	})

	wantExports := []providers.PortableToolExportV1{
		{Name: "java", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/java"},
		{Name: "javac", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/javac"},
	}
	if !reflect.DeepEqual(environment.Exports, wantExports) || environment.ExportsDirectory != SourceBuilderExportsDirectoryV1 {
		t.Fatalf("builder exports = %#v at %q", environment.Exports, environment.ExportsDirectory)
	}
	schedule, err := providers.PortableToolValidationScheduleFromLockV1(coordinator.tools[0].Lock)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule.Entries) != 1 ||
		schedule.Entries[0].Scope != "source-builder:demo-server" ||
		schedule.Entries[0].Tool != "java" ||
		schedule.Entries[0].Profile.Reference.ID != "tool:java/releases/21/validation/profiles/default" ||
		schedule.Entries[0].Runtime != nil {
		t.Fatalf("builder validation schedule = %#v", schedule)
	}
	profile, err := toolcatalog.DecodePortableToolValidationProfileV1(
		schedule.Entries[0].Profile.Reference,
		schedule.Entries[0].Profile.Record,
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Tool != "java" || profile.Version != "21" || !reflect.DeepEqual(profile.Probes, []toolcatalog.RecordProbeV1{{
		Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/java", Args: []string{"-version"},
	}}) {
		t.Fatalf("builder validation profile = %#v", profile)
	}
	if !reflect.DeepEqual(environment.Upstream, upstream) || !reflect.DeepEqual(environment.Descriptor, builder) {
		t.Fatalf("builder boundary = upstream %#v, descriptor %#v", environment.Upstream, environment.Descriptor)
	}

	if strings.Contains(resolver.sourceBuildPath(), SourceBuilderExportsDirectoryV1) || resolver.sourceBuilder != nil {
		t.Fatalf("resolver consumer inherited source-builder tools: %#v", resolver)
	}
	for _, command := range [][]string{{"java", "-version"}, {"javac", "-version"}} {
		if err := buildSession.runWheelEnvironmentCommand(t.Context(), launcher, "/usr/bin/python3", "portable tool containment proof", command); err != nil {
			t.Fatal(err)
		}
	}
	if len(commands) != 2 {
		t.Fatalf("builder commands = %#v", commands)
	}
	wantPath := "PATH=" + SourceBuilderExportsDirectoryV1 + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	for index, command := range commands {
		wantCommand := [][]string{{"java", "-version"}, {"javac", "-version"}}[index]
		if !containsInOrder(command.Args, []string{wantPath}) || !reflect.DeepEqual(command.Args[len(command.Args)-2:], wantCommand) {
			t.Fatalf("builder command %d = %#v", index, command.Args)
		}
	}
	providerPlanAfter, err := canonical.Marshal(fixture.request.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if string(providerPlanAfter) != string(providerPlanBefore) || !reflect.DeepEqual(resolver.descriptor, upstream) || resolver.sourceBuilder != nil {
		t.Fatal("source-builder preparation changed the provider graph or resolver consumer")
	}

	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if !workspaceCleaned || !buildSession.closed {
		t.Fatalf("source-build consumer cleanup: workspace=%t session=%t", workspaceCleaned, buildSession.closed)
	}
	if len(environmentCalls.removed) != 1 || environmentCalls.removed[0].ImageID != builder.ConfigDigest {
		t.Fatalf("removed builder images = %#v", environmentCalls.removed)
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("host tool workspace was removed with the builder image: %v", err)
	}
	if err := coordinator.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("host tool workspace remained after coordinator cleanup: %v", err)
	}
}

func TestJavaSourceBuilderCutoverHasNoLegacyPackageOrExecutableSwitches(t *testing.T) {
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(packageDir) != "dockerdeploy" {
		t.Fatalf("test package directory = %q, want dockerdeploy", packageDir)
	}
	internalDir := filepath.Dir(packageDir)
	productionDirs := []string{
		filepath.Join(internalDir, "dockerdeploy"),
		filepath.Join(internalDir, "providers", "python"),
		filepath.Join(internalDir, "toolcatalog"),
		filepath.Join(internalDir, "toolrequest"),
	}
	legacySwitches := []string{"default-jre-headless", "/usr/bin/java"}
	for _, dir := range productionDirs {
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, legacy := range legacySwitches {
				if strings.Contains(string(content), legacy) {
					t.Errorf("legacy Java switch %q remains in production source %s", legacy, path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
