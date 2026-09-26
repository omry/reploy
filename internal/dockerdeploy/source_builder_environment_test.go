package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func sourceBuilderExportArchiveHeadersForTest(t *testing.T, archivePath string) []tar.Header {
	t.Helper()
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	reader := tar.NewReader(archive)
	var headers []tar.Header
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return headers
		}
		if err != nil {
			t.Fatal(err)
		}
		headers = append(headers, *header)
	}
}

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
	if !reflect.DeepEqual(stub.order, []string{"acquire", "materialize"}) {
		t.Fatalf("order = %#v, want one acquisition before one materialization of the shared payload", stub.order)
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
			request.SymbolicLinkPolicy != providerstore.ArchiveSymbolicLinkPolicyMaterializeRegularTarget ||
			!reflect.DeepEqual(request.ExecutablePaths, []string{"jdk-21.0.12+8/bin/java", "jdk-21.0.12+8/bin/javac"}) ||
			!strings.HasSuffix(request.DestinationRoot, filepath.FromSlash("/context/rootfs/opt/reploy/tools/java")) {
			t.Fatalf("archive request = %#v", request)
		}
	}
	if err := providers.ValidatePortableToolLockV1(tools.Lock); err != nil {
		t.Fatal(err)
	}
	lockedPayload := tools.Lock.Plan.PortableToolPlan.Tools[0].Responsibilities.Payloads[0]
	if lockedPayload.Record.Value["symbolic_link_policy"] != toolcatalog.PayloadSymbolicLinkPolicyMaterializeRegularTargetV1 ||
		lockedPayload.Reference != tools.Plan.Closures[0].Records.Payloads[0].Reference {
		t.Fatalf("locked payload materialization identity = %#v", lockedPayload)
	}
	if len(tools.Lock.Acquisitions) != 2 || tools.Lock.Acquisitions[0].Outcome.Kind != providerstore.AcquisitionOutcomeCacheHit ||
		tools.Lock.Acquisitions[0].Outcome.SuccessfulDeclaredLocator != "" || len(tools.Lock.Releases) != 2 ||
		!reflect.DeepEqual(tools.Lock.Plan, tools.Plan.DAG) {
		t.Fatalf("lock = %#v", tools.Lock)
	}
	schedule, err := providers.PortableToolValidationScheduleFromLockV1(tools.Lock)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule.Entries) != 2 || schedule.Entries[0].Scope != "source-builder:alpha" ||
		schedule.Entries[0].Profile.Reference.ID != "tool:java/releases/21/validation/profiles/default" || schedule.Entries[0].Runtime != nil {
		t.Fatalf("schedule = %#v", schedule)
	}
	if len(tools.Selections) != 2 || tools.Selections[1].Scope != "source-builder:beta" || tools.Selections[1].Tool != "java" ||
		tools.Selections[1].SelectedClosureDigest != tools.Plan.Plan.Tools[1].SelectedClosureDigest {
		t.Fatalf("selections = %#v", tools.Selections)
	}
	if len(tools.Exports) != 2 || tools.Exports[0].Name != "java" || tools.Exports[1].Name != "javac" {
		t.Fatalf("exports = %#v", tools.Exports)
	}
	if tools.exportsArchive != sourceBuilderExportsArchiveV1 {
		t.Fatalf("exports archive = %q", tools.exportsArchive)
	}
	headers := sourceBuilderExportArchiveHeadersForTest(t, filepath.Join(tools.contextDir, tools.exportsArchive))
	if len(headers) != 3 || headers[0].Name != "opt/reploy/exports/" || headers[0].Typeflag != tar.TypeDir ||
		headers[1].Name != "opt/reploy/exports/java" || headers[1].Typeflag != tar.TypeSymlink || headers[1].Linkname != tools.Exports[0].Path ||
		headers[2].Name != "opt/reploy/exports/javac" || headers[2].Typeflag != tar.TypeSymlink || headers[2].Linkname != tools.Exports[1].Path {
		t.Fatalf("export archive headers = %#v", headers)
	}
	wantCopies := []sourceBuilderCopyV1{
		{Source: "rootfs/opt/reploy/tools/java/jdk-21.0.12+8", Destination: "/opt/reploy/tools/java/jdk-21.0.12+8"},
	}
	if !reflect.DeepEqual(tools.copies, wantCopies) {
		t.Fatalf("copies = %#v, want one copy per destination even though two scopes selected the payload", tools.copies)
	}
	if _, err := sourceBuilderDockerfileV1(tools.copies, tools.exportsArchive); err != nil {
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

func TestAcquireSourceBuilderPortableToolArtifactsV1BindingWheel(t *testing.T) {
	previous := acquireSourceBuilderArtifactV1
	t.Cleanup(func() { acquireSourceBuilderArtifactV1 = previous })
	const filename = "demo-1.0-py3-none-any.whl"
	digest := canonical.Digest("sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370")
	artifact := providers.PortableToolRecordReferenceV1{ID: "tool:demo/releases/1.0/bindings/python/artifacts/linux-amd64", Digest: digest}
	descriptor := providerstore.ArtifactDescriptor{LogicalPath: "wheels/" + filename, Kind: "wheel", Size: "123", SHA256: digest}
	source := providers.PortableToolSelectedRecordV1{Reference: providers.PortableToolRecordReferenceV1{ID: "tool:demo/releases/1.0/sources/wheel", Digest: digest}}
	records := toolcatalog.EmbeddedPortableToolLockRecordSetV1{Artifacts: []toolcatalog.EmbeddedPortableToolArtifactSourceV1{{
		Scope: "application:demo", Tool: "demo", Artifact: artifact, Descriptor: descriptor, Source: source,
		Mirrors: []string{"https://example.org/demo.whl"},
	}}}
	calls := 0
	acquireSourceBuilderArtifactV1 = func(_ context.Context, _ providerstore.Store, request providerstore.AcquisitionRequest) (providerstore.AcquisitionResult, error) {
		calls++
		if request.Artifact != descriptor || request.Source.ID != source.Reference.ID || request.Source.SHA256 != digest ||
			!reflect.DeepEqual(request.Source.Mirrors, records.Artifacts[0].Mirrors) || request.Policy != providerstore.DefaultAcquisitionPolicy() {
			t.Fatalf("binding acquisition request = %#v", request)
		}
		return providerstore.AcquisitionResult{Artifact: request.Artifact, Provenance: providerstore.AcquisitionProvenance{
			Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: request.Source.ID,
		}}, nil
	}
	acquired, inputs, err := acquireSourceBuilderPortableToolArtifactsV1(context.Background(), providerstore.Store{}, records)
	if err != nil || calls != 1 || acquired[artifact] != descriptor || len(inputs) != 1 || inputs[0].Descriptor != descriptor ||
		inputs[0].Source.Reference != source.Reference || inputs[0].Provenance.Outcome != providerstore.AcquisitionOutcomeCacheHit {
		t.Fatalf("acquired = %#v, inputs = %#v, calls = %d, err = %v", acquired, inputs, calls, err)
	}
}

func TestAcquireSourceBuilderPortableToolArtifactsV1RejectsBindingDescriptorMismatch(t *testing.T) {
	previous := acquireSourceBuilderArtifactV1
	t.Cleanup(func() { acquireSourceBuilderArtifactV1 = previous })
	digest := canonical.Digest("sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370")
	descriptor := providerstore.ArtifactDescriptor{LogicalPath: "wheels/demo-1.0-py3-none-any.whl", Kind: "wheel", Size: "123", SHA256: digest}
	records := toolcatalog.EmbeddedPortableToolLockRecordSetV1{Artifacts: []toolcatalog.EmbeddedPortableToolArtifactSourceV1{{
		Artifact: providers.PortableToolRecordReferenceV1{ID: "binding-wheel", Digest: digest}, Descriptor: descriptor,
	}}}
	acquireSourceBuilderArtifactV1 = func(_ context.Context, _ providerstore.Store, request providerstore.AcquisitionRequest) (providerstore.AcquisitionResult, error) {
		wrong := request.Artifact
		wrong.Size = "124"
		return providerstore.AcquisitionResult{Artifact: wrong}, nil
	}
	acquired, inputs, err := acquireSourceBuilderPortableToolArtifactsV1(context.Background(), providerstore.Store{}, records)
	if err == nil || !strings.Contains(err.Error(), "does not match its selected descriptor") || acquired != nil || inputs != nil {
		t.Fatalf("acquired = %#v, inputs = %#v, err = %v", acquired, inputs, err)
	}
}

func TestAcquireSourceBuilderPortableToolArtifactsV1DeduplicatesSharedBindingWheel(t *testing.T) {
	previous := acquireSourceBuilderArtifactV1
	t.Cleanup(func() { acquireSourceBuilderArtifactV1 = previous })
	digest := canonical.Digest("sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370")
	artifact := providers.PortableToolRecordReferenceV1{ID: "binding-wheel", Digest: digest}
	descriptor := providerstore.ArtifactDescriptor{LogicalPath: "wheels/demo-1.0-py3-none-any.whl", Kind: "wheel", Size: "123", SHA256: digest}
	source := providers.PortableToolSelectedRecordV1{Reference: providers.PortableToolRecordReferenceV1{ID: "wheel-source", Digest: digest}}
	first := toolcatalog.EmbeddedPortableToolArtifactSourceV1{Scope: "application:first", Tool: "demo", Artifact: artifact, Descriptor: descriptor, Source: source, Mirrors: []string{"https://example.org/demo.whl"}}
	second := first
	second.Scope = "application:second"
	records := toolcatalog.EmbeddedPortableToolLockRecordSetV1{Artifacts: []toolcatalog.EmbeddedPortableToolArtifactSourceV1{first, second}}
	calls := 0
	acquireSourceBuilderArtifactV1 = func(_ context.Context, _ providerstore.Store, request providerstore.AcquisitionRequest) (providerstore.AcquisitionResult, error) {
		calls++
		return providerstore.AcquisitionResult{Artifact: request.Artifact, Provenance: providerstore.AcquisitionProvenance{
			OperationID: "one-operation", Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: request.Source.ID,
		}}, nil
	}
	acquired, inputs, err := acquireSourceBuilderPortableToolArtifactsV1(context.Background(), providerstore.Store{}, records)
	if err != nil || calls != 1 || len(acquired) != 1 || len(inputs) != 2 ||
		inputs[0].Scope != first.Scope || inputs[1].Scope != second.Scope ||
		inputs[0].Provenance.OperationID != "one-operation" || inputs[1].Provenance.OperationID != "one-operation" {
		t.Fatalf("acquired = %#v, inputs = %#v, calls = %d, err = %v", acquired, inputs, calls, err)
	}
	conflicting := second
	conflicting.Mirrors = []string{"https://example.org/other.whl"}
	records.Artifacts[1] = conflicting
	calls = 0
	if _, _, err := acquireSourceBuilderPortableToolArtifactsV1(context.Background(), providerstore.Store{}, records); err == nil ||
		!strings.Contains(err.Error(), "conflicting descriptors or sources") || calls != 0 {
		t.Fatalf("conflicting source error = %v, calls = %d; want pre-acquisition rejection", err, calls)
	}
}

func TestAcquireSourceBuilderPortableToolArtifactsV1BindingWheelVerifiedCacheHit(t *testing.T) {
	content := []byte("cached binding wheel bytes")
	digest := canonical.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(content)))
	descriptor := providerstore.ArtifactDescriptor{
		LogicalPath: "wheels/demo-1.0-py3-none-any.whl", Kind: "wheel", Size: fmt.Sprint(len(content)), SHA256: digest,
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	records := toolcatalog.EmbeddedPortableToolLockRecordSetV1{Artifacts: []toolcatalog.EmbeddedPortableToolArtifactSourceV1{{
		Scope: "application:demo", Tool: "demo",
		Artifact: providers.PortableToolRecordReferenceV1{ID: "binding-wheel", Digest: digest}, Descriptor: descriptor,
		Source:  providers.PortableToolSelectedRecordV1{Reference: providers.PortableToolRecordReferenceV1{ID: "wheel-source", Digest: digest}},
		Mirrors: []string{"https://127.0.0.1:1/unreachable.whl"},
	}}}
	acquired, inputs, err := acquireSourceBuilderPortableToolArtifactsV1(context.Background(), store, records)
	if err != nil {
		t.Fatal(err)
	}
	if acquired[records.Artifacts[0].Artifact] != descriptor || len(inputs) != 1 ||
		inputs[0].Provenance.Outcome != providerstore.AcquisitionOutcomeCacheHit ||
		inputs[0].Provenance.SourceID != "wheel-source" || len(inputs[0].Provenance.Attempts) != 0 {
		t.Fatalf("verified cache acquisition = %#v, inputs = %#v", acquired, inputs)
	}
}

func TestMaterializeSourceBuilderPortableToolsV1ExactTemurinArtifactCompatibility(t *testing.T) {
	archivePath := os.Getenv("REPLOY_TEST_TEMURIN_ARCHIVE")
	if archivePath == "" {
		t.Skip("set REPLOY_TEST_TEMURIN_ARCHIVE to the pinned Temurin tar.gz")
	}
	plan, input := planSourceBuilderJavaForTest(t, "alpha")
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	descriptor, err := input.Store.Publish(
		context.Background(),
		"tools/java/21.0.12+8/OpenJDK21U-jdk_x64_linux_hotspot_21.0.12_8.tar.gz",
		"jdk-archive",
		archive,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantSHA := canonical.Digest("sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370")
	if descriptor.Size != "207486543" || descriptor.SHA256 != wantSHA {
		t.Fatalf("Temurin descriptor = %#v", descriptor)
	}
	previousAcquire := acquireSourceBuilderArtifactV1
	t.Cleanup(func() { acquireSourceBuilderArtifactV1 = previousAcquire })
	acquireSourceBuilderArtifactV1 = func(_ context.Context, _ providerstore.Store, request providerstore.AcquisitionRequest) (providerstore.AcquisitionResult, error) {
		return providerstore.AcquisitionResult{
			Artifact: request.Artifact,
			Provenance: providerstore.AcquisitionProvenance{
				OperationID: "exact-temurin-test", Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: request.Source.ID,
			},
		}, nil
	}
	tools, err := MaterializeSourceBuilderPortableToolsV1(context.Background(), input.Store, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tools.Cleanup() })
	workspace := tools.workspace
	installRoot := filepath.Join(tools.contextDir, "rootfs", "opt", "reploy", "tools", "java", "jdk-21.0.12+8")
	for _, executable := range []string{"java", "javac"} {
		info, err := os.Lstat(filepath.Join(installRoot, "bin", executable))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("Temurin %s mode = %v, %v", executable, info, err)
		}
	}
	baseLicense, err := os.ReadFile(filepath.Join(installRoot, "legal", "java.base", "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	linkedLicensePath := filepath.Join(installRoot, "legal", "jdk.compiler", "LICENSE")
	linkedLicense, err := os.ReadFile(linkedLicensePath)
	if err != nil || !reflect.DeepEqual(linkedLicense, baseLicense) {
		t.Fatalf("Temurin materialized linked license matches target = %v, %v", reflect.DeepEqual(linkedLicense, baseLicense), err)
	}
	if info, err := os.Lstat(linkedLicensePath); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("Temurin materialized linked license mode = %v, %v", info, err)
	}
	if err := tools.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("Temurin workspace remained after cleanup: %v", err)
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

func TestSourceBuilderPortableToolsV1CleanupRemovesReadOnlyMaterializedTreeWithoutFollowingLinks(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	readOnlyDirectory := filepath.Join(workspace, "context", "rootfs", "opt", "tool")
	if err := os.MkdirAll(readOnlyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readOnlyDirectory, "payload"), []byte("payload"), 0o400); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(readOnlyDirectory, "outside-link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("ordinary Windows users cannot create the test symlink")
		}
		t.Fatal(err)
	}
	if err := os.Chmod(readOnlyDirectory, 0o555); err != nil {
		t.Fatal(err)
	}
	tools := &SourceBuilderPortableToolsV1{workspace: workspace}
	if err := tools.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if tools.workspace != "" {
		t.Fatalf("workspace path retained after cleanup: %q", tools.workspace)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace remained after cleanup: %v", err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "keep" {
		t.Fatalf("cleanup followed outside link: %q, %v", content, err)
	}
}

func TestSourceBuilderExposeExportsV1WritesHostNeutralLinuxLinks(t *testing.T) {
	contextDir := t.TempDir()
	rootfs := filepath.Join(contextDir, "rootfs")
	target := filepath.Join(rootfs, "opt", "reploy", "tools", "java", "bin", "java")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("portable-java"), 0o600); err != nil {
		t.Fatal(err)
	}
	exports, archiveName, err := sourceBuilderExposeExportsV1(rootfs, contextDir, map[string]providers.PortableToolExportV1{
		"java": {Name: "java", Path: "/opt/reploy/tools/java/bin/java"},
	})
	if err != nil || len(exports) != 1 {
		t.Fatalf("exports = %#v, err = %v", exports, err)
	}
	if archiveName != sourceBuilderExportsArchiveV1 {
		t.Fatalf("archive = %q", archiveName)
	}
	if _, err := os.Lstat(filepath.Join(rootfs, "opt", "reploy", "exports", "java")); !os.IsNotExist(err) {
		t.Fatalf("host export link exists: %v", err)
	}
	headers := sourceBuilderExportArchiveHeadersForTest(t, filepath.Join(contextDir, archiveName))
	if len(headers) != 2 || headers[1].Name != "opt/reploy/exports/java" || headers[1].Typeflag != tar.TypeSymlink || headers[1].Linkname != "/opt/reploy/tools/java/bin/java" {
		t.Fatalf("host-neutral export archive headers = %#v", headers)
	}
	secondContext := t.TempDir()
	secondRootfs := filepath.Join(secondContext, "rootfs")
	secondTarget := filepath.Join(secondRootfs, "opt", "reploy", "tools", "java", "bin", "java")
	if err := os.MkdirAll(filepath.Dir(secondTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondTarget, []byte("different host bytes do not enter the link layer"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, secondArchiveName, err := sourceBuilderExposeExportsV1(secondRootfs, secondContext, map[string]providers.PortableToolExportV1{
		"java": {Name: "java", Path: "/opt/reploy/tools/java/bin/java"},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstArchive, err := os.ReadFile(filepath.Join(contextDir, archiveName))
	if err != nil {
		t.Fatal(err)
	}
	secondArchive, err := os.ReadFile(filepath.Join(secondContext, secondArchiveName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstArchive, secondArchive) {
		t.Fatal("Linux export-link layer depends on host paths, bytes, modes, or timestamps")
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

func TestSourceBuilderSymbolicLinkPolicyV1UsesOnlyCanonicalPayloadData(t *testing.T) {
	for _, test := range []struct {
		policy string
		want   providerstore.ArchiveSymbolicLinkPolicy
	}{
		{policy: toolcatalog.PayloadSymbolicLinkPolicyRejectV1, want: providerstore.ArchiveSymbolicLinkPolicyReject},
		{policy: toolcatalog.PayloadSymbolicLinkPolicyMaterializeRegularTargetV1, want: providerstore.ArchiveSymbolicLinkPolicyMaterializeRegularTarget},
	} {
		got, err := sourceBuilderSymbolicLinkPolicyV1(test.policy)
		if err != nil || got != test.want {
			t.Fatalf("policy %q = %q, %v; want %q", test.policy, got, err, test.want)
		}
	}
	if _, err := sourceBuilderSymbolicLinkPolicyV1(""); err == nil {
		t.Fatal("missing canonical payload policy was inferred from archive format")
	}
}

func TestSourceBuilderDockerfileV1IsCommandFree(t *testing.T) {
	dockerfile, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{
		{Source: "rootfs/opt/reploy/tools/java/jdk-21.0.12+8", Destination: "/opt/reploy/tools/java/jdk-21.0.12+8"},
	}, sourceBuilderExportsArchiveV1)
	if err != nil {
		t.Fatal(err)
	}
	want := "# syntax=" + MaterializationDockerfileSyntax + "\n" +
		"ARG REPLOY_BASE_IMAGE=scratch\n" +
		"FROM ${REPLOY_BASE_IMAGE}\n" +
		`COPY --chown=0:0 ["rootfs/opt/reploy/tools/java/jdk-21.0.12+8","/opt/reploy/tools/java/jdk-21.0.12+8"]` + "\n" +
		`ADD --chown=0:0 ["reploy-source-builder-exports.tar","/"]` + "\n"
	if string(dockerfile) != want {
		t.Fatalf("dockerfile = %q, want %q", dockerfile, want)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(dockerfile)), "\n") {
		if strings.HasPrefix(line, "RUN") || strings.HasPrefix(line, "USER") || strings.HasPrefix(line, "ENV") {
			t.Fatalf("source-builder layer runs commands or changes configuration: %q", line)
		}
	}
	if _, err := sourceBuilderDockerfileV1(nil, ""); err == nil {
		t.Fatal("empty layer rendered")
	}
	if _, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{{Source: "../escape", Destination: "/opt/x"}}, ""); err == nil {
		t.Fatal("escaping copy source rendered")
	}
	if _, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{{Source: "rootfs/a", Destination: "/a"}, {Source: "rootfs/b", Destination: "/a"}}, ""); err == nil {
		t.Fatal("repeated destination rendered")
	}
	if _, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{{Source: "rootfs/a", Destination: "/a"}}, "other.tar"); err == nil {
		t.Fatal("untrusted exports archive rendered")
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
	removeErr       error
	evidenceMode    string
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
		stub.schedule = providers.PortableToolValidationScheduleV1{
			Schema:  providers.PortableToolValidationScheduleSchemaV1,
			Entries: make([]providers.PortableToolScheduledValidationV1, len(input.selected)),
		}
		for index, selected := range input.selected {
			stub.schedule.Entries[index] = selected.entry
		}
		if !reflect.DeepEqual(input.Image, stub.inspected) {
			t.Fatalf("validated image %#v, want the inspected builder image", input.Image)
		}
		if stub.validateErr != nil {
			return nil, stub.validateErr
		}
		var evidence []providers.ValidationEvidence
		for _, selected := range input.selected {
			item, err := providers.NewPortableToolValidationEvidence(input.Image.Image.RootFSSubject, selected.entry.Profile.Reference, selected.entry.Runtime)
			if err != nil {
				return nil, err
			}
			evidence = append(evidence, item)
		}
		switch stub.evidenceMode {
		case "missing":
			return nil, nil
		case "wrong image":
			evidence[0].SubjectRootFS = canonical.Digest("sha256:" + strings.Repeat("f", 64))
		}
		return evidence, nil
	}
	removeSourceBuilderLayerV1 = func(_ context.Context, got BuiltImageCandidate) error {
		stub.order = append(stub.order, "remove")
		stub.removed = append(stub.removed, got)
		return stub.removeErr
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
	wantDockerfile, err := sourceBuilderDockerfileV1(tools.copies, tools.exportsArchive)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(environmentStub.upstream, upstream) || environmentStub.contextDir != tools.contextDir || string(environmentStub.dockerfile) != string(wantDockerfile) {
		t.Fatalf("layer build input = %#v / %s / %s", environmentStub.upstream, environmentStub.contextDir, environmentStub.dockerfile)
	}
	wantSchedule, err := providers.PortableToolValidationScheduleFromLockV1(tools.Lock)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(environmentStub.schedule, wantSchedule) || len(environment.Evidence) != 1 {
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

func buildIntegrationCaseForSourceBuilderTest(t *testing.T) toolcatalog.IntegrationCaseV1 {
	t.Helper()
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	for _, caseV1 := range cases {
		if caseV1.Support.Context == "build" && caseV1.Fixture.Target == sourceBuilderJavaTarget("debian", "12") {
			return caseV1
		}
	}
	t.Fatal("embedded catalog has no representative Debian build case")
	return toolcatalog.IntegrationCaseV1{}
}

func sourceBuilderCaseUpstreamForTest(t *testing.T, caseV1 toolcatalog.IntegrationCaseV1) deploy.ImageDescriptor {
	t.Helper()
	upstream := sourceBuilderTestImageDescriptor(t, "1")
	upstream.AuthorReference = caseV1.Fixture.BaseImage
	upstream.ImmutableReference = "docker.io/library/debian@" + string(caseV1.Fixture.BaseImageDigest)
	upstream.ManifestDigest = caseV1.Fixture.BaseImageDigest
	if err := upstream.Validate(); err != nil {
		t.Fatal(err)
	}
	return upstream
}

func TestValidateSourceBuilderBuildCaseV1UsesExactImageAndScope(t *testing.T) {
	stub := &sourceBuilderMaterializationStub{}
	tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha", "beta")
	defer tools.Cleanup()
	caseV1 := buildIntegrationCaseForSourceBuilderTest(t)
	scope := tools.Plan.Recipes["alpha"].Scope
	wholeSchedule, err := providers.PortableToolValidationScheduleFromLockV1(tools.Lock)
	if err != nil || len(wholeSchedule.Entries) != 2 {
		t.Fatalf("representative locked build needs two scopes, got %#v, %v", wholeSchedule, err)
	}
	image := sourceBuilderTestImageDescriptor(t, "2")
	environmentStub := &sourceBuilderEnvironmentStub{}
	stubSourceBuilderEnvironment(t, environmentStub, image)
	evidence, err := ValidateSourceBuilderBuildCaseV1(
		context.Background(), store, tools, sourceBuilderCaseUpstreamForTest(t, caseV1), RunOptions{}, caseV1, scope,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(environmentStub.order, []string{"collision-check", "build", "inspect", "validate", "remove"}) ||
		len(environmentStub.schedule.Entries) != 1 ||
		environmentStub.schedule.Entries[0].Scope != scope ||
		environmentStub.schedule.Entries[0].Profile.Reference != caseV1.Fixture.ValidationProfiles[0] || len(evidence) != 1 ||
		evidence[0].SubjectRootFS != environmentStub.inspected.Image.RootFSSubject {
		t.Fatalf("build case did not validate the exact inspected image and selected scope: order=%v schedule=%#v evidence=%#v", environmentStub.order, environmentStub.schedule, evidence)
	}
	if len(environmentStub.removed) != 1 {
		t.Fatalf("builder image removed %d times, want once", len(environmentStub.removed))
	}
}

func TestValidateSourceBuilderBuildCaseV1FailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*toolcatalog.IntegrationCaseV1, *string)
		want   string
	}{
		{"wrong scope", func(_ *toolcatalog.IntegrationCaseV1, scope *string) { *scope = "source-builder:other" }, "exact selected profile set"},
		{"wrong release", func(caseV1 *toolcatalog.IntegrationCaseV1, _ *string) {
			caseV1.ManifestReference.Digest = canonical.Digest("sha256:" + strings.Repeat("f", 64))
		}, "catalog-derived"},
		{"wrong profile", func(caseV1 *toolcatalog.IntegrationCaseV1, _ *string) {
			caseV1.Fixture.ValidationProfiles[0].Digest = canonical.Digest("sha256:" + strings.Repeat("f", 64))
		}, "catalog-derived"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &sourceBuilderMaterializationStub{}
			tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
			defer tools.Cleanup()
			caseV1 := buildIntegrationCaseForSourceBuilderTest(t)
			scope := tools.Plan.Recipes["alpha"].Scope
			test.change(&caseV1, &scope)
			environmentStub := &sourceBuilderEnvironmentStub{}
			stubSourceBuilderEnvironment(t, environmentStub, sourceBuilderTestImageDescriptor(t, "2"))
			evidence, err := ValidateSourceBuilderBuildCaseV1(
				context.Background(), store, tools, sourceBuilderCaseUpstreamForTest(t, caseV1), RunOptions{}, caseV1, scope,
			)
			wantOrder := []string{"collision-check", "build", "inspect", "remove"}
			if test.name != "wrong scope" {
				wantOrder = nil
			}
			if err == nil || evidence != nil || !strings.Contains(err.Error(), test.want) ||
				!reflect.DeepEqual(environmentStub.order, wantOrder) {
				t.Fatalf("invalid case yielded evidence=%#v, error=%v, order=%v", evidence, err, environmentStub.order)
			}
		})
	}
}

func TestValidateSourceBuilderBuildCaseV1RejectsWrongTargetBeforeImageBuild(t *testing.T) {
	stub := &sourceBuilderMaterializationStub{}
	tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
	defer tools.Cleanup()
	caseV1 := buildIntegrationCaseForSourceBuilderTest(t)
	caseV1.Fixture.Target.VersionID = "13"
	environmentStub := &sourceBuilderEnvironmentStub{}
	stubSourceBuilderEnvironment(t, environmentStub, sourceBuilderTestImageDescriptor(t, "2"))
	evidence, err := ValidateSourceBuilderBuildCaseV1(
		context.Background(), store, tools, sourceBuilderCaseUpstreamForTest(t, caseV1), RunOptions{},
		caseV1, tools.Plan.Recipes["alpha"].Scope,
	)
	if err == nil || evidence != nil || !strings.Contains(err.Error(), "target does not match") || len(environmentStub.order) != 0 {
		t.Fatalf("wrong target yielded evidence=%#v, error=%v, order=%v", evidence, err, environmentStub.order)
	}
}

func TestValidateSourceBuilderBuildCaseV1RejectsWrongFixtureBaseBeforeImageBuild(t *testing.T) {
	stub := &sourceBuilderMaterializationStub{}
	tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
	defer tools.Cleanup()
	caseV1 := buildIntegrationCaseForSourceBuilderTest(t)
	upstream := sourceBuilderCaseUpstreamForTest(t, caseV1)
	otherDigest := canonical.Digest("sha256:" + strings.Repeat("f", 64))
	upstream.ImmutableReference = "docker.io/library/debian@" + string(otherDigest)
	upstream.ManifestDigest = otherDigest
	if err := upstream.Validate(); err != nil {
		t.Fatal(err)
	}
	environmentStub := &sourceBuilderEnvironmentStub{}
	stubSourceBuilderEnvironment(t, environmentStub, sourceBuilderTestImageDescriptor(t, "2"))
	evidence, err := ValidateSourceBuilderBuildCaseV1(
		context.Background(), store, tools, upstream, RunOptions{},
		caseV1, tools.Plan.Recipes["alpha"].Scope,
	)
	if err == nil || evidence != nil || !strings.Contains(err.Error(), "fixture base image digest") || len(environmentStub.order) != 0 {
		t.Fatalf("substituted fixture base yielded evidence=%#v, error=%v, order=%v", evidence, err, environmentStub.order)
	}
}

func TestValidateSourceBuilderBuildCaseV1RejectsSubstitutedCaseFixtureBeforeImageBuild(t *testing.T) {
	stub := &sourceBuilderMaterializationStub{}
	tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
	defer tools.Cleanup()
	caseV1 := buildIntegrationCaseForSourceBuilderTest(t)
	caseV1.Fixture.BaseImageDigest = canonical.Digest("sha256:" + strings.Repeat("f", 64))
	upstream := sourceBuilderCaseUpstreamForTest(t, caseV1)
	environmentStub := &sourceBuilderEnvironmentStub{}
	stubSourceBuilderEnvironment(t, environmentStub, sourceBuilderTestImageDescriptor(t, "2"))
	evidence, err := ValidateSourceBuilderBuildCaseV1(
		context.Background(), store, tools, upstream, RunOptions{},
		caseV1, tools.Plan.Recipes["alpha"].Scope,
	)
	if err == nil || evidence != nil || !strings.Contains(err.Error(), "catalog-derived") || len(environmentStub.order) != 0 {
		t.Fatalf("substituted case fixture yielded evidence=%#v, error=%v, order=%v", evidence, err, environmentStub.order)
	}
}

func TestValidateSourceBuilderBuildCaseV1RejectsDifferentSelectedTuple(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*toolcatalog.SelectedClosureV1)
	}{
		{"bindings", func(closure *toolcatalog.SelectedClosureV1) { closure.Contract.Bindings = []string{"other"} }},
		{"selections", func(closure *toolcatalog.SelectedClosureV1) {
			closure.Contract.Selections = map[string][]string{"variant": {"other"}}
		}},
		{"fixture", func(closure *toolcatalog.SelectedClosureV1) {
			closure.Fixture.BaseImageDigest = canonical.Digest("sha256:" + strings.Repeat("f", 64))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &sourceBuilderMaterializationStub{}
			tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
			defer tools.Cleanup()
			caseV1 := buildIntegrationCaseForSourceBuilderTest(t)
			test.change(&tools.Plan.Closures[0])
			environmentStub := &sourceBuilderEnvironmentStub{}
			stubSourceBuilderEnvironment(t, environmentStub, sourceBuilderTestImageDescriptor(t, "2"))
			evidence, err := ValidateSourceBuilderBuildCaseV1(
				context.Background(), store, tools, sourceBuilderCaseUpstreamForTest(t, caseV1), RunOptions{},
				caseV1, tools.Plan.Recipes["alpha"].Scope,
			)
			if err == nil || evidence != nil || !strings.Contains(err.Error(), "different support tuple or closure") ||
				!reflect.DeepEqual(environmentStub.order, []string{"collision-check", "build", "inspect", "remove"}) {
				t.Fatalf("different %s yielded evidence=%#v, error=%v, order=%v", test.name, evidence, err, environmentStub.order)
			}
		})
	}
}

func TestValidateSourceBuilderBuildCaseV1RejectsMissingWrongAndUncleanObservations(t *testing.T) {
	for _, test := range []struct {
		name string
		stub sourceBuilderEnvironmentStub
		want string
	}{
		{"missing callback evidence", sourceBuilderEnvironmentStub{evidenceMode: "missing"}, "returned 0 observations"},
		{"wrong image evidence", sourceBuilderEnvironmentStub{evidenceMode: "wrong image"}, "different image or profile"},
		{"failed probe", sourceBuilderEnvironmentStub{validateErr: errors.New("probe failed")}, "probe failed"},
		{"failed cleanup", sourceBuilderEnvironmentStub{removeErr: errors.New("remove failed")}, "remove failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &sourceBuilderMaterializationStub{}
			tools, store := materializeSourceBuilderJavaForTest(t, stub, "alpha")
			defer tools.Cleanup()
			caseV1 := buildIntegrationCaseForSourceBuilderTest(t)
			environmentStub := &test.stub
			stubSourceBuilderEnvironment(t, environmentStub, sourceBuilderTestImageDescriptor(t, "2"))
			evidence, err := ValidateSourceBuilderBuildCaseV1(
				context.Background(), store, tools, sourceBuilderCaseUpstreamForTest(t, caseV1), RunOptions{},
				caseV1, tools.Plan.Recipes["alpha"].Scope,
			)
			if err == nil || evidence != nil || !strings.Contains(err.Error(), test.want) || len(environmentStub.removed) != 1 {
				t.Fatalf("failed case yielded evidence=%#v, error=%v, removed=%v", evidence, err, environmentStub.removed)
			}
		})
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
	dockerfile, err := sourceBuilderDockerfileV1([]sourceBuilderCopyV1{{Source: "rootfs/opt/reploy/tools/demo", Destination: "/opt/reploy/tools/demo"}}, "")
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
