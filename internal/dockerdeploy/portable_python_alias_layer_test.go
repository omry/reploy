package dockerdeploy

import (
	"archive/tar"
	"context"
	"errors"
	"io"
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
)

func TestPortablePythonAliasArchiveUsesPrimitiveStagingPayload(t *testing.T) {
	stagingRoot := t.TempDir()
	contextDir := t.TempDir()
	linkPath := portablePythonAliasStagingPathV1(stagingRoot, portablePythonAliasSpecV1{Destination: "/usr/local/bin/tool"})
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createPortablePythonAliasStagedEntryV1(linkPath, "/opt/reploy/providers/python/app/bin/tool"); err != nil {
		t.Fatal(err)
	}
	alias := portablePythonAliasSpecV1{
		Name: "tool", Destination: "/usr/local/bin/tool", Target: "/opt/reploy/providers/python/app/bin/tool",
	}
	if err := writePortablePythonAliasArchiveV1(contextDir, stagingRoot, []portablePythonAliasSpecV1{alias}, nil); err != nil {
		t.Fatal(err)
	}
	archiveFile, err := os.Open(filepath.Join(contextDir, portablePythonAliasArchiveV1))
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(archiveFile)
	var headers []tar.Header
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		headers = append(headers, *header)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	if len(headers) != 1 || headers[0].Name != "usr/local/bin/tool" || headers[0].Typeflag != tar.TypeSymlink || headers[0].Linkname != alias.Target {
		t.Fatalf("alias archive headers = %#v", headers)
	}
	missingContext := t.TempDir()
	if err := writePortablePythonAliasArchiveV1(missingContext, stagingRoot, []portablePythonAliasSpecV1{alias}, []string{"/usr/local", "/usr/local/bin"}); err != nil {
		t.Fatal(err)
	}
	missingFile, err := os.Open(filepath.Join(missingContext, portablePythonAliasArchiveV1))
	if err != nil {
		t.Fatal(err)
	}
	missingReader := tar.NewReader(missingFile)
	for _, expectedName := range []string{"usr/local", "usr/local/bin", "usr/local/bin/tool"} {
		header, err := missingReader.Next()
		if err != nil {
			t.Fatalf("missing-parent archive next %q: %v", expectedName, err)
		}
		if header.Name != expectedName {
			t.Fatalf("missing-parent archive header = %#v, want %q", header, expectedName)
		}
		if expectedName != "usr/local/bin/tool" && (header.Typeflag != tar.TypeDir || header.Mode != 0o755) {
			t.Fatalf("missing-parent archive directory header = %#v", header)
		}
	}
	if err := missingFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := createPortablePythonAliasStagedEntryV1(linkPath, "/wrong"); err != nil {
		t.Fatal(err)
	}
	if err := writePortablePythonAliasArchiveV1(t.TempDir(), stagingRoot, []portablePythonAliasSpecV1{alias}, nil); err == nil || !strings.Contains(err.Error(), "resolves") {
		t.Fatalf("staged payload mismatch error = %v", err)
	}
}

func TestPortablePythonAliasArchiveSupportsLongRuntimeAndDestinationPaths(t *testing.T) {
	stagingRoot := t.TempDir()
	contextDir := t.TempDir()
	name := "tool"
	destination := "/usr/local/" + strings.Repeat("a", 120) + "/" + strings.Repeat("b", 120) + "/" + name
	target := "/opt/reploy/providers/python/application/_" + strings.Repeat("c", 64) + "/bin/" + name
	linkPath := portablePythonAliasStagingPathV1(stagingRoot, portablePythonAliasSpecV1{Destination: destination})
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createPortablePythonAliasStagedEntryV1(linkPath, target); err != nil {
		t.Fatal(err)
	}
	alias := portablePythonAliasSpecV1{Name: name, Destination: destination, Target: target}
	if err := writePortablePythonAliasArchiveV1(contextDir, stagingRoot, []portablePythonAliasSpecV1{alias}, []string{filepath.ToSlash(filepath.Dir(destination))}); err != nil {
		t.Fatal(err)
	}
	archiveFile, err := os.Open(filepath.Join(contextDir, portablePythonAliasArchiveV1))
	if err != nil {
		t.Fatal(err)
	}
	defer archiveFile.Close()
	reader := tar.NewReader(archiveFile)
	var found bool
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeSymlink {
			found = true
			if header.Name != strings.TrimPrefix(destination, "/") || header.Linkname != target {
				t.Fatalf("long alias header = %#v", header)
			}
		}
	}
	if !found {
		t.Fatal("long alias symlink missing from archive")
	}
}

func TestPortablePythonAliasArchiveRejectsUnexpectedStagedEntry(t *testing.T) {
	stagingRoot := t.TempDir()
	contextDir := t.TempDir()
	alias := portablePythonAliasSpecV1{
		Name: "tool", Destination: "/usr/local/bin/tool", Target: "/opt/reploy/providers/python/app/bin/tool",
	}
	linkPath := portablePythonAliasStagingPathV1(stagingRoot, alias)
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createPortablePythonAliasStagedEntryV1(linkPath, alias.Target); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(filepath.Dir(linkPath), "unowned-entry")
	if err := createPortablePythonAliasStagedEntryV1(extra, alias.Target); err != nil {
		t.Fatal(err)
	}
	if err := writePortablePythonAliasArchiveV1(contextDir, stagingRoot, []portablePythonAliasSpecV1{alias}, nil); err == nil || !strings.Contains(err.Error(), "unexpected entry") {
		t.Fatalf("unexpected staged entry error = %v", err)
	}
}

func TestNormalizePortablePythonAliasSpecsRejectsSharedDestinationCollision(t *testing.T) {
	base := portablePythonAliasSpecV1{
		Name: "tool", Destination: "/usr/local/bin/tool", Target: "/opt/reploy/providers/python/app/bin/tool",
		BindingIdentity: "binding-a", Output: providers.QualifiedOutput{Component: "app", Name: "tool"},
		Facts: providers.CanonicalProviderData{Schema: "test-facts-v1", Value: canonical.Object{}},
	}
	other := base
	other.Target = "/opt/reploy/providers/python/other/bin/tool"
	other.Output.Component = "other"
	if _, err := normalizePortablePythonAliasSpecsV1([]portablePythonAliasSpecV1{base, other}); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("collision error = %v", err)
	}
}

func TestBuildAndValidatePortablePythonAliasLayerProvesFinalImageBeforeReturn(t *testing.T) {
	storeRoot := t.TempDir()
	store, err := providerstore.NewStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	source := portablePythonAliasLayerTestSource(t)
	alias := portablePythonAliasSpecV1{
		Name: "con", Destination: "/usr/local/bin/con", Target: "/opt/reploy/providers/python/app/bin/con",
		BindingIdentity: "binding-a", Output: providers.QualifiedOutput{Component: "app", Name: "con"},
		Facts: providers.CanonicalProviderData{Schema: "test-facts-v1", Value: canonical.Object{}},
	}
	previousBuild := buildPortablePythonAliasLayerV1
	previousInspect := inspectPortablePythonAliasLayerV1
	previousProbe := collectPortablePythonAliasEvidenceV1
	previousDestinations := inspectPortablePythonAliasDestinationsV1
	t.Cleanup(func() {
		buildPortablePythonAliasLayerV1 = previousBuild
		inspectPortablePythonAliasLayerV1 = previousInspect
		collectPortablePythonAliasEvidenceV1 = previousProbe
		inspectPortablePythonAliasDestinationsV1 = previousDestinations
	})
	inspectPortablePythonAliasDestinationsV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) ([]string, error) {
		return nil, nil
	}
	var events []string
	var contextDir string
	imageID := canonical.Digest("sha256:" + strings.Repeat("d", 64))
	aliasDescriptor := source.Descriptor
	aliasDescriptor.ConfigDigest = imageID
	aliasDescriptor.ImmutableReference = string(imageID)
	aliasImage, err := realizedImageFromDescriptor(aliasDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	aliasSource := source
	aliasSource.Descriptor = aliasDescriptor
	aliasSource.Image = aliasImage
	buildPortablePythonAliasLayerV1 = func(_ context.Context, _ providerstore.Store, upstream deploy.ImageDescriptor, gotContext string, dockerfile []byte, _ RunOptions) (BuiltImageCandidate, error) {
		events = append(events, "build")
		contextDir = gotContext
		if !reflect.DeepEqual(upstream, source.Descriptor) || !strings.Contains(string(dockerfile), "ADD --chown=0:0") {
			t.Fatalf("alias build inputs = %s, %s", upstream.ImmutableReference, dockerfile)
		}
		archiveFile, err := os.Open(filepath.Join(gotContext, portablePythonAliasArchiveV1))
		if err != nil {
			t.Fatal(err)
		}
		reader := tar.NewReader(archiveFile)
		header, err := reader.Next()
		_ = archiveFile.Close()
		if err != nil || header.Linkname != alias.Target {
			t.Fatalf("alias archive header = %#v, %v", header, err)
		}
		return BuiltImageCandidate{ImageID: imageID}, nil
	}
	inspectPortablePythonAliasLayerV1 = func(_ context.Context, candidate BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
		events = append(events, "inspect")
		if candidate.ImageID != imageID || platform != source.Descriptor.Platform {
			t.Fatalf("alias inspection input = %#v, %s", candidate, platform.Canonical)
		}
		return aliasSource, nil
	}
	collectPortablePythonAliasEvidenceV1 = func(_ context.Context, _ providerstore.Store, descriptor deploy.ImageDescriptor, checks []FullImageExecutableProbe) ([]providers.ExecutableEvidence, error) {
		events = append(events, "probe")
		if !reflect.DeepEqual(descriptor, aliasSource.Descriptor) || len(checks) != 1 || checks[0].ID != "python_alias_000000" || checks[0].InvocationPath != alias.Destination {
			t.Fatalf("alias final probe input = %#v, %#v", descriptor, checks)
		}
		return []providers.ExecutableEvidence{{
			InvocationPath: alias.Destination,
			LinkChain:      []providers.LinkEvidence{{Path: alias.Destination, Target: alias.Target, ResolvedPath: alias.Target}},
			Terminal:       providers.FileEvidence{Path: alias.Target},
		}}, nil
	}
	candidate, inspected, err := buildAndValidatePortablePythonAliasLayerV1(context.Background(), store, source, []portablePythonAliasSpecV1{alias}, source.Descriptor.Platform, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ImageID != imageID || !reflect.DeepEqual(inspected, aliasSource) || !reflect.DeepEqual(events, []string{"build", "inspect", "probe"}) {
		t.Fatalf("alias result candidate=%#v inspected=%#v events=%#v", candidate, inspected, events)
	}
	if _, err := os.Stat(filepath.Dir(contextDir)); !os.IsNotExist(err) {
		t.Fatalf("alias staging workspace still exists: %v", err)
	}
}

func TestInspectPortablePythonAliasDestinationsReturnsMissingParents(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upstream := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, upstream.Platform, t.TempDir())
	previousPrepare, previousOpen := prepareSourceBuilderProbeWorkspaceV1, openSourceBuilderCollisionSessionV1
	t.Cleanup(func() {
		prepareSourceBuilderProbeWorkspaceV1 = previousPrepare
		openSourceBuilderCollisionSessionV1 = previousOpen
	})
	prepareSourceBuilderProbeWorkspaceV1 = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return workspace, func() error { return nil }, nil
	}
	session := &ImageValidationSession{descriptor: upstream, containerName: "alias-source-check", runDocker: func(spec CommandSpec, options RunOptions) error {
		if spec.Args[0] == "exec" {
			if _, err := options.Stdout.Write([]byte("/usr/local\n/usr/local/bin\n")); err != nil {
				return err
			}
		}
		return nil
	}}
	openSourceBuilderCollisionSessionV1 = func(context.Context, deploy.ImageDescriptor, PreparedProbeWorkspace) (*ImageValidationSession, error) {
		return session, nil
	}
	missing, err := inspectPortablePythonAliasDestinations(context.Background(), store, upstream, []string{"/usr/local/bin/tool"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(missing, []string{"/usr/local", "/usr/local/bin"}) {
		t.Fatalf("missing parents = %#v", missing)
	}
}

func TestInspectPortablePythonAliasDestinationsKeepsWorkspaceWhenContainerCleanupFails(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upstream := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, upstream.Platform, t.TempDir())
	previousPrepare, previousOpen := prepareSourceBuilderProbeWorkspaceV1, openSourceBuilderCollisionSessionV1
	t.Cleanup(func() {
		prepareSourceBuilderProbeWorkspaceV1 = previousPrepare
		openSourceBuilderCollisionSessionV1 = previousOpen
	})
	cleanupCalled := false
	prepareSourceBuilderProbeWorkspaceV1 = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return workspace, func() error { cleanupCalled = true; return nil }, nil
	}
	cleanupErr := errors.New("injected container removal failure")
	session := &ImageValidationSession{descriptor: upstream, containerName: "alias-source-check", runDocker: func(spec CommandSpec, options RunOptions) error {
		if spec.Args[0] == "rm" {
			return cleanupErr
		}
		if spec.Args[0] == "exec" {
			_, _ = options.Stdout.Write([]byte("/usr/local\n/usr/local/bin\n"))
		}
		return nil
	}}
	openSourceBuilderCollisionSessionV1 = func(context.Context, deploy.ImageDescriptor, PreparedProbeWorkspace) (*ImageValidationSession, error) {
		return session, nil
	}
	_, err = inspectPortablePythonAliasDestinations(context.Background(), store, upstream, []string{"/usr/local/bin/tool"})
	if !errors.Is(err, cleanupErr) || cleanupCalled {
		t.Fatalf("container cleanup error = %v, workspace cleanup called = %v", err, cleanupCalled)
	}
}

func TestBuildAndValidatePortablePythonAliasLayerFailsBeforeBuildOnSourceDestination(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := portablePythonAliasLayerTestSource(t)
	alias := portablePythonAliasSpecV1{
		Name: "tool", Component: "app", Destination: "/usr/local/bin/tool", Target: "/opt/reploy/providers/python/app/bin/tool",
		BindingIdentity: "binding-a",
	}
	previousDestinations, previousBuild := inspectPortablePythonAliasDestinationsV1, buildPortablePythonAliasLayerV1
	t.Cleanup(func() {
		inspectPortablePythonAliasDestinationsV1 = previousDestinations
		buildPortablePythonAliasLayerV1 = previousBuild
	})
	buildCalled := false
	inspectPortablePythonAliasDestinationsV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) ([]string, error) {
		return nil, errors.New("destination already exists in source image")
	}
	buildPortablePythonAliasLayerV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, string, []byte, RunOptions) (BuiltImageCandidate, error) {
		buildCalled = true
		return BuiltImageCandidate{}, nil
	}
	_, _, err = buildAndValidatePortablePythonAliasLayerV1(context.Background(), store, source, []portablePythonAliasSpecV1{alias}, source.Descriptor.Platform, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "destination already exists") || buildCalled {
		t.Fatalf("source destination rejection err=%v buildCalled=%v", err, buildCalled)
	}
}

func portablePythonAliasLayerTestSource(t *testing.T) InspectedImageCandidate {
	t.Helper()
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	config := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
	}
	image, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	return InspectedImageCandidate{Descriptor: descriptor, Config: config, Image: image}
}
