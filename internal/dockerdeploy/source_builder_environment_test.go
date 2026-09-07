package dockerdeploy

import (
	"context"
	"errors"
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

type sourceBuilderMaterializationStub struct {
	order        []string
	acquisitions []providerstore.AcquisitionRequest
	archives     []providerstore.ArchiveMaterializationRequest
	acquireErr   error
	executables  []string
}

func stubSourceBuilderMaterialization(t *testing.T, stub *sourceBuilderMaterializationStub) {
	t.Helper()
	previousAcquire, previousMaterialize := acquireSourceBuilderArtifactV1, materializeSourceBuilderArchiveV1
	t.Cleanup(func() {
		acquireSourceBuilderArtifactV1, materializeSourceBuilderArchiveV1 = previousAcquire, previousMaterialize
	})
	acquireSourceBuilderArtifactV1 = func(_ context.Context, _ providerstore.Store, request providerstore.AcquisitionRequest) (providerstore.AcquisitionResult, error) {
		stub.order = append(stub.order, "acquire")
		stub.acquisitions = append(stub.acquisitions, request)
		if stub.acquireErr != nil {
			return providerstore.AcquisitionResult{}, stub.acquireErr
		}
		return providerstore.AcquisitionResult{
			Artifact: request.Artifact,
			Provenance: providerstore.AcquisitionProvenance{
				OperationID: "test-operation", Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: request.Source.ID,
			},
		}, nil
	}
	materializeSourceBuilderArchiveV1 = func(_ context.Context, _ providerstore.Store, request providerstore.ArchiveMaterializationRequest) (providerstore.ArchiveMaterializationResult, error) {
		stub.order = append(stub.order, "materialize")
		stub.archives = append(stub.archives, request)
		executables := stub.executables
		if executables == nil {
			executables = request.ExecutablePaths
		}
		for _, executable := range executables {
			relative := strings.TrimPrefix(executable, request.ArchiveRoot+"/")
			target := filepath.Join(request.DestinationRoot, request.InstallDirectory, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return providerstore.ArchiveMaterializationResult{}, err
			}
			if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
				return providerstore.ArchiveMaterializationResult{}, err
			}
		}
		return providerstore.ArchiveMaterializationResult{FinalPath: filepath.Join(request.DestinationRoot, request.InstallDirectory)}, nil
	}
}

func materializeSourceBuilderJavaForTest(t *testing.T, stub *sourceBuilderMaterializationStub, distributions ...string) (*SourceBuilderPortableToolsV1, providerstore.Store) {
	t.Helper()
	plan, input := planSourceBuilderJavaForTest(t, distributions...)
	stubSourceBuilderMaterialization(t, stub)
	tools, err := MaterializeSourceBuilderPortableToolsV1(context.Background(), input.Store, plan)
	if err != nil {
		t.Fatal(err)
	}
	return tools, input.Store
}

func TestMaterializeSourceBuilderPortableToolsV1AcquiresThenMaterializesOfflineAndLocks(t *testing.T) {
	stub := &sourceBuilderMaterializationStub{}
	tools, _ := materializeSourceBuilderJavaForTest(t, stub, "alpha", "beta")
	defer tools.Cleanup()
	if !reflect.DeepEqual(stub.order, []string{"acquire", "acquire", "materialize"}) {
		t.Fatalf("order = %#v, want every acquisition before one materialization of the shared payload", stub.order)
	}
	wantSHA := canonical.Digest("sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370")
	for _, request := range stub.acquisitions {
		if request.Artifact.SHA256 != wantSHA || request.Source.SHA256 != wantSHA || len(request.Source.Mirrors) != 1 ||
			request.Source.ID != "tool:java/releases/21/revisions/1/sources/jdk-linux-amd64" || request.Policy != providerstore.DefaultAcquisitionPolicy() {
			t.Fatalf("acquisition request = %#v", request)
		}
	}
	for _, request := range stub.archives {
		if request.Format != providerstore.ArchiveFormatTarGz || request.InstallDirectory != "jdk-21.0.12+8" || request.ArchiveRoot != "jdk-21.0.12+8" ||
			request.ExpectedEntryCount != "542" || request.ExpectedUnpackedSize != "361144464" || request.Artifact.SHA256 != wantSHA ||
			!reflect.DeepEqual(request.ExecutablePaths, []string{"jdk-21.0.12+8/bin/java", "jdk-21.0.12+8/bin/javac"}) ||
			!strings.HasSuffix(request.DestinationRoot, filepath.FromSlash("/context/rootfs/opt/reploy/tools/java")) {
			t.Fatalf("archive request = %#v", request)
		}
	}
	if err := providers.ValidatePortableToolLockV1(tools.Lock); err != nil {
		t.Fatal(err)
	}
	if len(tools.Lock.Acquisitions) != 2 || tools.Lock.Acquisitions[0].Outcome.Kind != providerstore.AcquisitionOutcomeCacheHit ||
		tools.Lock.Acquisitions[0].Outcome.SuccessfulDeclaredLocator != "" || len(tools.Lock.Releases) != 2 ||
		!reflect.DeepEqual(tools.Lock.Plan, tools.Plan.DAG) {
		t.Fatalf("lock = %#v", tools.Lock)
	}
	if len(tools.Schedule.Entries) != 2 || tools.Schedule.Entries[0].Scope != "source-builder:alpha" ||
		tools.Schedule.Entries[0].Profile.Reference.ID != "tool:java/releases/21/validation/profiles/default" || tools.Schedule.Entries[0].Runtime != nil {
		t.Fatalf("schedule = %#v", tools.Schedule)
	}
	if len(tools.Selections) != 2 || tools.Selections[1].Scope != "source-builder:beta" || tools.Selections[1].Tool != "java" ||
		tools.Selections[1].SelectedClosureDigest != tools.Plan.Plan.Tools[1].SelectedClosureDigest {
		t.Fatalf("selections = %#v", tools.Selections)
	}
	if len(tools.Exports) != 2 || tools.Exports[0].Name != "java" || tools.Exports[1].Name != "javac" {
		t.Fatalf("exports = %#v", tools.Exports)
	}
	rootfs := filepath.Join(tools.contextDir, "rootfs")
	for _, export := range tools.Exports {
		link := filepath.Join(rootfs, "opt", "reploy", "exports", export.Name)
		target, err := os.Readlink(link)
		if err != nil || target != export.Path {
			t.Fatalf("export link %s -> %q, %v; want %s", link, target, err, export.Path)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(rootfs, "opt", "reploy", "exports")); err != nil || len(entries) != 2 {
		t.Fatalf("exports directory exposes %d entries, %v; want exactly the selected exports", len(entries), err)
	}
	wantCopies := []sourceBuilderCopyV1{
		{Source: "rootfs/opt/reploy/tools/java/jdk-21.0.12+8", Destination: "/opt/reploy/tools/java/jdk-21.0.12+8"},
		{Source: "rootfs/opt/reploy/exports", Destination: "/opt/reploy/exports"},
	}
	if !reflect.DeepEqual(tools.copies, wantCopies) {
		t.Fatalf("copies = %#v, want one copy per destination even though two scopes selected the payload", tools.copies)
	}
	if _, err := sourceBuilderDockerfileV1(tools.copies); err != nil {
		t.Fatalf("shared closure copies do not render: %v", err)
	}
	workspace := tools.workspace
	if err := tools.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace remained after cleanup: %v", err)
	}
	if err := tools.Cleanup(); err != nil {
		t.Fatalf("second cleanup = %v", err)
	}
}

func TestMaterializeSourceBuilderPortableToolsV1FailsClosedWhenAnExportWasNotMaterialized(t *testing.T) {
	plan, input := planSourceBuilderJavaForTest(t, "alpha")
	stub := &sourceBuilderMaterializationStub{executables: []string{"jdk-21.0.12+8/bin/java"}}
	stubSourceBuilderMaterialization(t, stub)
	before := workspaceEntries(t, input.Store)
	_, err := MaterializeSourceBuilderPortableToolsV1(context.Background(), input.Store, plan)
	if err == nil || !strings.Contains(err.Error(), `export "javac"`) {
		t.Fatalf("error = %v", err)
	}
	if after := workspaceEntries(t, input.Store); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed materialization left workspace entries: %#v", after)
	}
}

func TestMaterializeSourceBuilderPortableToolsV1DoesNotMaterializeAfterAcquisitionFailure(t *testing.T) {
	plan, input := planSourceBuilderJavaForTest(t, "alpha")
	stub := &sourceBuilderMaterializationStub{acquireErr: errors.New("mirror exhausted")}
	stubSourceBuilderMaterialization(t, stub)
	_, err := MaterializeSourceBuilderPortableToolsV1(context.Background(), input.Store, plan)
	if err == nil || !strings.Contains(err.Error(), "mirror exhausted") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(stub.order, []string{"acquire"}) {
		t.Fatalf("order = %#v, want no materialization after a failed acquisition", stub.order)
	}
}

func workspaceEntries(t *testing.T, store providerstore.Store) []string {
	t.Helper()
	return workspaceEntriesWithPrefix(t, store, "source-builder-")
}

func TestSourceBuilderInstallRootV1DerivesOneRootFromExports(t *testing.T) {
	payloads := []toolcatalog.SelectedPayloadRecordV1{{Record: toolcatalog.PayloadRecordV1{
		InstallDirectory: "jdk-21.0.12+8", ArchiveRoot: "jdk-21.0.12+8", Executables: []string{"jdk-21.0.12+8/bin/java", "jdk-21.0.12+8/bin/javac"},
	}}}
	root, err := sourceBuilderInstallRootV1([]providers.PortableToolExportV1{
		{Name: "java", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/java"},
		{Name: "javac", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/javac"},
	}, payloads)
	if err != nil || root != "/opt/reploy/tools/java" {
		t.Fatalf("root = %q, err = %v", root, err)
	}
	if _, err := sourceBuilderInstallRootV1([]providers.PortableToolExportV1{
		{Name: "java", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/java"},
		{Name: "javac", Path: "/other/jdk-21.0.12+8/bin/javac"},
	}, payloads); err == nil || !strings.Contains(err.Error(), "conflicting install roots") {
		t.Fatalf("conflict error = %v", err)
	}
	if _, err := sourceBuilderInstallRootV1([]providers.PortableToolExportV1{{Name: "jar", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/jar"}}, payloads); err == nil || !strings.Contains(err.Error(), "does not name a selected payload executable") {
		t.Fatalf("unknown executable error = %v", err)
	}
	if _, err := sourceBuilderInstallRootV1(nil, payloads); err == nil {
		t.Fatal("closure without exports derived an install root")
	}
}

func TestSourceBuilderDockerfileV1IsCopyOnly(t *testing.T) {
	dockerfile, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{
		{Source: "rootfs/opt/reploy/tools/java/jdk-21.0.12+8", Destination: "/opt/reploy/tools/java/jdk-21.0.12+8"},
		{Source: "rootfs/opt/reploy/exports", Destination: "/opt/reploy/exports"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "# syntax=" + MaterializationDockerfileSyntax + "\n" +
		"ARG REPLOY_BASE_IMAGE=scratch\n" +
		"FROM ${REPLOY_BASE_IMAGE}\n" +
		`COPY --chown=0:0 ["rootfs/opt/reploy/tools/java/jdk-21.0.12+8","/opt/reploy/tools/java/jdk-21.0.12+8"]` + "\n" +
		`COPY --chown=0:0 ["rootfs/opt/reploy/exports","/opt/reploy/exports"]` + "\n"
	if string(dockerfile) != want {
		t.Fatalf("dockerfile = %q, want %q", dockerfile, want)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(dockerfile)), "\n") {
		if strings.HasPrefix(line, "RUN") || strings.HasPrefix(line, "USER") || strings.HasPrefix(line, "ENV") {
			t.Fatalf("source-builder layer runs commands or changes configuration: %q", line)
		}
	}
	if _, err := sourceBuilderDockerfileV1(nil); err == nil {
		t.Fatal("empty layer rendered")
	}
	if _, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{{Source: "../escape", Destination: "/opt/x"}}); err == nil {
		t.Fatal("escaping copy source rendered")
	}
	if _, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{{Source: "rootfs/a", Destination: "/a"}, {Source: "rootfs/b", Destination: "/a"}}); err == nil {
		t.Fatal("repeated destination rendered")
	}
}

type sourceBuilderEnvironmentStub struct {
	checked         []string
	checkedUpstream deploy.ImageDescriptor
	collisionErr    error
	order           []string
	dockerfile      []byte
	contextDir      string
	upstream        deploy.ImageDescriptor
	inspected       InspectedImageCandidate
	validateErr     error
	schedule        providers.PortableToolValidationScheduleV1
	removed         []BuiltImageCandidate
}

func stubSourceBuilderEnvironment(t *testing.T, stub *sourceBuilderEnvironmentStub, builder deploy.ImageDescriptor) {
	t.Helper()
	previousBuild, previousInspect, previousValidate, previousRemove, previousAbsent := buildSourceBuilderLayerV1, inspectSourceBuilderLayerV1, validateSourceBuilderMaterializationV1, removeSourceBuilderLayerV1, requireSourceBuilderDestinationsAbsentV1
	t.Cleanup(func() {
		buildSourceBuilderLayerV1, inspectSourceBuilderLayerV1, validateSourceBuilderMaterializationV1, removeSourceBuilderLayerV1, requireSourceBuilderDestinationsAbsentV1 = previousBuild, previousInspect, previousValidate, previousRemove, previousAbsent
	})
	candidate := BuiltImageCandidate{ImageID: builder.ConfigDigest, TemporaryReference: "reploy-build:test", Workspace: t.TempDir()}
	requireSourceBuilderDestinationsAbsentV1 = func(_ context.Context, _ providerstore.Store, upstream deploy.ImageDescriptor, destinations []string) error {
		stub.order = append(stub.order, "collision-check")
		stub.checked = append([]string{}, destinations...)
		stub.checkedUpstream = upstream
		return stub.collisionErr
	}
	buildSourceBuilderLayerV1 = func(_ context.Context, _ providerstore.Store, upstream deploy.ImageDescriptor, contextDir string, dockerfile []byte, _ RunOptions) (BuiltImageCandidate, error) {
		stub.order = append(stub.order, "build")
		stub.upstream, stub.contextDir, stub.dockerfile = upstream, contextDir, dockerfile
		return candidate, nil
	}
	inspectSourceBuilderLayerV1 = func(_ context.Context, got BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
		stub.order = append(stub.order, "inspect")
		if got != candidate || platform != builder.Platform {
			t.Fatalf("inspected %#v on %s", got, platform.Canonical)
		}
		rootFSSubject, err := deploy.RootFSSubject(builder.RootFSDiffIDs)
		if err != nil {
			t.Fatal(err)
		}
		stub.inspected = InspectedImageCandidate{Descriptor: builder, Image: providers.RealizedImageV1{
			Digest: builder.ConfigDigest, ConfigDigest: builder.ConfigDigest, RootFSSubject: rootFSSubject,
		}}
		return stub.inspected, nil
	}
	validateSourceBuilderMaterializationV1 = func(_ context.Context, _ providerstore.Store, input PortableToolMaterializationValidationInputV1) ([]providers.ValidationEvidence, error) {
		stub.order = append(stub.order, "validate")
		stub.schedule = input.Schedule
		if !reflect.DeepEqual(input.Image, stub.inspected) {
			t.Fatalf("validated image %#v, want the inspected builder image", input.Image)
		}
		if stub.validateErr != nil {
			return nil, stub.validateErr
		}
		return []providers.ValidationEvidence{{Schema: "test-evidence", SubjectRootFS: input.Image.Image.RootFSSubject, ProfileDigest: rendererDigest("1")}}, nil
	}
	removeSourceBuilderLayerV1 = func(_ context.Context, got BuiltImageCandidate) error {
		stub.order = append(stub.order, "remove")
		stub.removed = append(stub.removed, got)
		return nil
	}
}

func sourceBuilderTestImageDescriptor(t *testing.T, char string) deploy.ImageDescriptor {
	t.Helper()
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	digest := canonical.Digest("sha256:" + strings.Repeat(char, 64))
	descriptor.ConfigDigest, descriptor.AuthorReference, descriptor.ImmutableReference = digest, string(digest), string(digest)
	return descriptor
}

func TestPrepareSourceBuilderEnvironmentV1BuildsInspectsValidatesThenCleansUp(t *testing.T) {
	stub := &sourceBuilderMaterializationStub{}
	tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
	defer tools.Cleanup()
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	builder := sourceBuilderTestImageDescriptor(t, "2")
	environmentStub := &sourceBuilderEnvironmentStub{}
	stubSourceBuilderEnvironment(t, environmentStub, builder)

	environment, err := PrepareSourceBuilderEnvironmentV1(context.Background(), store, tools, upstream, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(environmentStub.order, []string{"collision-check", "build", "inspect", "validate"}) {
		t.Fatalf("order = %#v", environmentStub.order)
	}
	wantDestinations := []string{"/opt/reploy/tools/java/jdk-21.0.12+8", "/opt/reploy/exports"}
	if !reflect.DeepEqual(environmentStub.checked, wantDestinations) || !reflect.DeepEqual(environmentStub.checkedUpstream, upstream) {
		t.Fatalf("collision check inspected %#v on %#v, want %#v on the upstream image", environmentStub.checked, environmentStub.checkedUpstream, wantDestinations)
	}
	wantDockerfile, err := sourceBuilderDockerfileV1(tools.copies)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(environmentStub.upstream, upstream) || environmentStub.contextDir != tools.contextDir || string(environmentStub.dockerfile) != string(wantDockerfile) {
		t.Fatalf("layer build input = %#v / %s / %s", environmentStub.upstream, environmentStub.contextDir, environmentStub.dockerfile)
	}
	if !reflect.DeepEqual(environmentStub.schedule, tools.Schedule) || len(environment.Evidence) != 1 {
		t.Fatalf("validated schedule/evidence = %#v / %#v", environmentStub.schedule, environment.Evidence)
	}
	if !reflect.DeepEqual(environment.Descriptor, builder) || !reflect.DeepEqual(environment.Upstream, upstream) ||
		!reflect.DeepEqual(environment.Image, environmentStub.inspected) || environment.ExportsDirectory != SourceBuilderExportsDirectoryV1 ||
		!reflect.DeepEqual(environment.Exports, tools.Exports) || !reflect.DeepEqual(environment.Selections, tools.Selections) ||
		!reflect.DeepEqual(environment.Recipes, tools.Plan.Recipes) {
		t.Fatalf("environment = %#v", environment)
	}
	if err := environment.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := environment.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(environmentStub.removed) != 1 || environmentStub.removed[0].ImageID != builder.ConfigDigest {
		t.Fatalf("removed = %#v, want the builder image removed exactly once", environmentStub.removed)
	}
}

func TestPrepareSourceBuilderEnvironmentV1RemovesTheImageWhenValidationFails(t *testing.T) {
	stub := &sourceBuilderMaterializationStub{}
	tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
	defer tools.Cleanup()
	builder := sourceBuilderTestImageDescriptor(t, "2")
	environmentStub := &sourceBuilderEnvironmentStub{validateErr: errors.New("java -version exited 1")}
	stubSourceBuilderEnvironment(t, environmentStub, builder)

	environment, err := PrepareSourceBuilderEnvironmentV1(context.Background(), store, tools, sourceBuilderTestImageDescriptor(t, "1"), RunOptions{})
	if err == nil || environment != nil || !strings.Contains(err.Error(), "java -version exited 1") {
		t.Fatalf("environment = %#v, err = %v", environment, err)
	}
	if !reflect.DeepEqual(environmentStub.order, []string{"collision-check", "build", "inspect", "validate", "remove"}) {
		t.Fatalf("order = %#v", environmentStub.order)
	}
}

func TestPrepareSourceBuilderEnvironmentV1DoesNotBuildOverAnOccupiedDestination(t *testing.T) {
	stub := &sourceBuilderMaterializationStub{}
	tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
	defer tools.Cleanup()
	builder := sourceBuilderTestImageDescriptor(t, "2")
	environmentStub := &sourceBuilderEnvironmentStub{collisionErr: errors.New("/opt/reploy/exports already exists")}
	stubSourceBuilderEnvironment(t, environmentStub, builder)
	environment, err := PrepareSourceBuilderEnvironmentV1(context.Background(), store, tools, sourceBuilderTestImageDescriptor(t, "1"), RunOptions{})
	if err == nil || environment != nil || !strings.Contains(err.Error(), "collision check") || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("environment = %#v, err = %v", environment, err)
	}
	if !reflect.DeepEqual(environmentStub.order, []string{"collision-check"}) {
		t.Fatalf("order = %#v, want no layer build after a collision", environmentStub.order)
	}
}

func TestRequireSourceBuilderDestinationsAbsentUsesOneFixedValidationSession(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	previousProbe, previousOpen := prepareSourceBuilderProbeWorkspaceV1, openSourceBuilderCollisionSessionV1
	t.Cleanup(func() {
		prepareSourceBuilderProbeWorkspaceV1, openSourceBuilderCollisionSessionV1 = previousProbe, previousOpen
	})
	order := []string{}
	workspace := testPreparedProbeWorkspace(t, upstream.Platform, t.TempDir())
	prepareSourceBuilderProbeWorkspaceV1 = func(_ context.Context, _ providerstore.Store, platform blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		order = append(order, "workspace")
		return workspace, func() error { order = append(order, "cleanup-workspace"); return nil }, nil
	}
	var execArgs []string
	execErr := error(nil)
	session := &ImageValidationSession{descriptor: upstream, containerName: "collision-test", runDocker: func(spec CommandSpec, options RunOptions) error {
		order = append(order, "docker:"+spec.Args[0])
		if spec.Args[0] == "exec" {
			execArgs = append([]string{}, spec.Args...)
			if execErr != nil {
				_, _ = options.Stderr.Write([]byte("exists\n"))
			}
			return execErr
		}
		return nil
	}}
	openSourceBuilderCollisionSessionV1 = func(_ context.Context, got deploy.ImageDescriptor, gotWorkspace PreparedProbeWorkspace) (*ImageValidationSession, error) {
		order = append(order, "open")
		if !reflect.DeepEqual(got, upstream) || !reflect.DeepEqual(gotWorkspace, workspace) {
			t.Fatalf("session opened with %#v / %#v", got, gotWorkspace)
		}
		return session, nil
	}
	destinations := []string{"/opt/reploy/tools/java/jdk-21.0.12+8", "/opt/reploy/exports"}
	if err := requireSourceBuilderDestinationsAbsent(context.Background(), store, upstream, destinations); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"workspace", "open", "docker:exec", "docker:rm", "cleanup-workspace"}) {
		t.Fatalf("order = %#v", order)
	}
	if !containsInOrder(execArgs, []string{"/bin/sh", "-c", `for candidate in "$@"; do test ! -e "$candidate" && test ! -L "$candidate" || exit 1; done`, "reploy-validation", destinations[0], destinations[1]}) {
		t.Fatalf("absence check argv = %#v", execArgs)
	}
	for _, argument := range execArgs {
		if strings.Contains(argument, "$@") && argument != `for candidate in "$@"; do test ! -e "$candidate" && test ! -L "$candidate" || exit 1; done` {
			t.Fatalf("destination leaked into the shell body: %q", argument)
		}
	}
	order = nil
	session.closed = false
	execErr = errors.New("exit status 1")
	err = requireSourceBuilderDestinationsAbsent(context.Background(), store, upstream, destinations)
	if err == nil || !strings.Contains(err.Error(), "to be absent") {
		t.Fatalf("occupied destination error = %v", err)
	}
	if !reflect.DeepEqual(order, []string{"workspace", "open", "docker:exec", "docker:rm", "cleanup-workspace"}) {
		t.Fatalf("failure order = %#v, want the session closed and workspace removed", order)
	}
	if err := requireSourceBuilderDestinationsAbsent(context.Background(), store, upstream, nil); err == nil {
		t.Fatal("empty destination set accepted")
	}
}

func TestSourceBuilderClaimDestinationV1DeduplicatesIdenticalArtifactsAndRejectsConflicts(t *testing.T) {
	claims := map[string]canonical.Digest{}
	first, err := sourceBuilderClaimDestinationV1(claims, "/opt/reploy/tools/java/jdk-21.0.12+8", rendererDigest("1"))
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	again, err := sourceBuilderClaimDestinationV1(claims, "/opt/reploy/tools/java/jdk-21.0.12+8", rendererDigest("1"))
	if err != nil || again {
		t.Fatalf("identical repeat claim = %v, %v; want deduplicated", again, err)
	}
	if _, err := sourceBuilderClaimDestinationV1(claims, "/opt/reploy/tools/java/jdk-21.0.12+8", rendererDigest("2")); err == nil || !strings.Contains(err.Error(), "selected for both") {
		t.Fatalf("conflicting claim error = %v", err)
	}
	if _, err := sourceBuilderClaimDestinationV1(claims, "relative/path", rendererDigest("1")); err == nil {
		t.Fatal("relative destination accepted")
	}
	if _, err := sourceBuilderClaimDestinationV1(claims, "/opt/other", canonical.Digest("bad")); err == nil {
		t.Fatal("invalid artifact digest accepted")
	}
}

func TestPrepareSourceBuilderEnvironmentV1RequiresMaterializedTools(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareSourceBuilderEnvironmentV1(context.Background(), store, &SourceBuilderPortableToolsV1{}, sourceBuilderTestImageDescriptor(t, "1"), RunOptions{}); err == nil || !strings.Contains(err.Error(), "materialized portable tools") {
		t.Fatalf("error = %v", err)
	}
}

func TestSourceBuilderArchiveFormatV1AcceptsOnlyPortableArchives(t *testing.T) {
	for logicalPath, want := range map[string]providerstore.ArchiveFormat{
		"tools/java/jdk.tar.gz": providerstore.ArchiveFormatTarGz, "tools/java/jdk.tgz": providerstore.ArchiveFormatTarGz, "tools/demo/chromium.zip": providerstore.ArchiveFormatZip,
	} {
		got, err := sourceBuilderArchiveFormatV1(logicalPath)
		if err != nil || got != want {
			t.Fatalf("format(%q) = %q, %v; want %q", logicalPath, got, err, want)
		}
	}
	if _, err := sourceBuilderArchiveFormatV1("tools/asciinema/asciinema-x86_64"); err == nil {
		t.Fatal("raw executable accepted as a portable archive")
	}
}

func TestBuildSourceBuilderLayerBuildsFromTheSharedContextAndReportsTheImageID(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	stubMaterializationBuildBaseReference(t, upstream.ConfigDigest)
	original := runMaterializationBuildCommand
	t.Cleanup(func() { runMaterializationBuildCommand = original })
	contextDir := t.TempDir()
	dockerfile, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{{Source: "rootfs/opt/reploy/exports", Destination: "/opt/reploy/exports"}})
	if err != nil {
		t.Fatal(err)
	}
	before := workspaceEntriesWithPrefix(t, store, "build-")
	var workspace string
	runMaterializationBuildCommand = func(spec CommandSpec, options RunOptions) error {
		if spec.Name != "docker" || len(spec.Args) < 2 || spec.Args[0] != "build" || spec.Args[1] != "--no-cache" || options.Context == nil {
			t.Fatalf("command = %#v", spec)
		}
		if base := commandOption(t, spec.Args, "--build-arg"); !strings.HasPrefix(base, "REPLOY_BASE_IMAGE="+temporaryBuildReferencePrefix) {
			t.Fatalf("base build argument = %q", base)
		}
		if spec.Args[len(spec.Args)-1] != contextDir {
			t.Fatalf("build context = %q, want the shared materialized context %q", spec.Args[len(spec.Args)-1], contextDir)
		}
		iidPath := commandOption(t, spec.Args, "--iidfile")
		workspace = filepath.Dir(iidPath)
		content, err := os.ReadFile(commandOption(t, spec.Args, "--file"))
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != string(dockerfile) {
			t.Fatalf("Dockerfile = %s", content)
		}
		return os.WriteFile(iidPath, []byte(string(rendererDigest("f"))+"\n"), 0o600)
	}
	candidate, err := buildSourceBuilderLayer(context.Background(), store, upstream, contextDir, dockerfile, RunOptions{Context: context.Background(), NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ImageID != rendererDigest("f") || candidate.TemporaryReference == "" || candidate.Workspace != workspace {
		t.Fatalf("candidate = %#v", candidate)
	}
	if err := removeBuiltImageCandidate(t.Context(), candidate, func(context.Context, ...string) (string, error) { return "", nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("candidate workspace remains after cleanup: %v", err)
	}
	runMaterializationBuildCommand = func(CommandSpec, RunOptions) error { return errors.New("docker build failed") }
	if _, err := buildSourceBuilderLayer(context.Background(), store, upstream, contextDir, dockerfile, RunOptions{Context: context.Background()}); err == nil || !strings.Contains(err.Error(), "docker build failed") {
		t.Fatalf("failure = %v", err)
	}
	if after := workspaceEntriesWithPrefix(t, store, "build-"); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed build left workspaces: %#v", after)
	}
}

func workspaceEntriesWithPrefix(t *testing.T, store providerstore.Store, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			names = append(names, entry.Name())
		}
	}
	return names
}
