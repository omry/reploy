package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPortablePythonAliasArchiveDerivesSortedDeterministicEntries(t *testing.T) {
	first := portablePythonAliasSpecV1{
		Name: "alpha", Component: "app", Destination: "/usr/local/bin/alpha",
		Target: "/opt/reploy/providers/python/app/bin/alpha", BindingIdentity: "binding-alpha",
	}
	second := portablePythonAliasSpecV1{
		Name: "con", Component: "app", Destination: "/usr/local/bin/con",
		Target: "/opt/reploy/providers/python/app/bin/con", BindingIdentity: "binding-con",
	}
	write := func(aliases []portablePythonAliasSpecV1) []byte {
		t.Helper()
		contextDir := t.TempDir()
		if err := writePortablePythonAliasArchiveV1(contextDir, aliases, []string{"/usr/local/bin", "/usr/local"}); err != nil {
			t.Fatal(err)
		}
		archive, err := os.ReadFile(filepath.Join(contextDir, portablePythonAliasArchiveV1))
		if err != nil {
			t.Fatal(err)
		}
		return archive
	}
	archive := write([]portablePythonAliasSpecV1{second, first, first})
	if other := write([]portablePythonAliasSpecV1{first, second}); !bytes.Equal(archive, other) {
		t.Fatal("alias archive changed with claim order or exact duplicate")
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	for index, expected := range []struct {
		name, target string
		kind         byte
	}{
		{"usr/local", "", tar.TypeDir},
		{"usr/local/bin", "", tar.TypeDir},
		{"usr/local/bin/alpha", first.Target, tar.TypeSymlink},
		{"usr/local/bin/con", second.Target, tar.TypeSymlink},
	} {
		header, err := reader.Next()
		if err != nil {
			t.Fatalf("archive entry %d: %v", index, err)
		}
		if header.Name != expected.name || header.Linkname != expected.target || header.Typeflag != expected.kind || !header.ModTime.Equal(time.Unix(0, 0).UTC()) {
			t.Fatalf("archive entry %d = %#v", index, header)
		}
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("archive has an extra entry: %v", err)
	}
}

func TestPortablePythonAliasArchiveSupportsLongLinuxDestinationsOnAnyHost(t *testing.T) {
	component := "app"
	name := "con"
	destination := "/usr/local/" + strings.Repeat("a", 120) + "/" + strings.Repeat("b", 120) + "/" + name
	target := "/opt/reploy/providers/python/app/bin/" + name
	alias := portablePythonAliasSpecV1{Name: name, Component: component, Destination: destination, Target: target, BindingIdentity: "binding-con"}
	contextDir := t.TempDir()
	if err := writePortablePythonAliasArchiveV1(contextDir, []portablePythonAliasSpecV1{alias}, []string{filepath.ToSlash(filepath.Dir(destination))}); err != nil {
		t.Fatal(err)
	}
	archiveFile, err := os.Open(filepath.Join(contextDir, portablePythonAliasArchiveV1))
	if err != nil {
		t.Fatal(err)
	}
	defer archiveFile.Close()
	reader := tar.NewReader(archiveFile)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			t.Fatal("long Linux alias symlink missing from archive")
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeSymlink {
			if header.Name != strings.TrimPrefix(destination, "/") || header.Linkname != target {
				t.Fatalf("long alias header = %#v", header)
			}
			break
		}
	}
}

func TestPortablePythonAliasArchiveRejectsInvalidClaimsBeforeWriting(t *testing.T) {
	valid := portablePythonAliasSpecV1{
		Name: "tool", Component: "app", Destination: "/usr/local/bin/tool",
		Target: "/opt/reploy/providers/python/app/bin/tool", BindingIdentity: "binding-tool",
	}
	for _, test := range []struct {
		name string
		edit func(*portablePythonAliasSpecV1)
	}{
		{"wrong target", func(alias *portablePythonAliasSpecV1) { alias.Target = "/wrong" }},
		{"unsafe destination", func(alias *portablePythonAliasSpecV1) { alias.Destination = "/usr/local/../tool" }},
		{"unsafe name", func(alias *portablePythonAliasSpecV1) { alias.Name = "../tool" }},
		{"missing binding", func(alias *portablePythonAliasSpecV1) { alias.BindingIdentity = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			alias := valid
			test.edit(&alias)
			contextDir := t.TempDir()
			if err := writePortablePythonAliasArchiveV1(contextDir, []portablePythonAliasSpecV1{alias}, nil); err == nil {
				t.Fatal("invalid claim produced an alias archive")
			}
			if _, err := os.Stat(filepath.Join(contextDir, portablePythonAliasArchiveV1)); !os.IsNotExist(err) {
				t.Fatalf("invalid claim left an archive: %v", err)
			}
		})
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
	other = base
	other.BindingIdentity = "binding-b"
	if _, err := normalizePortablePythonAliasSpecsV1([]portablePythonAliasSpecV1{base, other}); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("binding collision error = %v", err)
	}
	other = base
	other.Name = "child"
	other.Destination = "/usr/local/bin/tool/child"
	other.Target = "/opt/reploy/providers/python/app/bin/child"
	other.BindingIdentity = "binding-child"
	other.Output.Name = "child"
	if _, err := normalizePortablePythonAliasSpecsV1([]portablePythonAliasSpecV1{base, other}); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlapping destination error = %v", err)
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
		workspaceEntries, err := os.ReadDir(filepath.Dir(gotContext))
		if err != nil || len(workspaceEntries) != 1 || workspaceEntries[0].Name() != "context" {
			t.Fatalf("alias build workspace contains host staging entries: entries=%#v err=%v", workspaceEntries, err)
		}
		archiveFile, err := os.Open(filepath.Join(gotContext, portablePythonAliasArchiveV1))
		if err != nil {
			t.Fatal(err)
		}
		reader := tar.NewReader(archiveFile)
		header, err := reader.Next()
		_ = archiveFile.Close()
		if err != nil || header.Typeflag != tar.TypeSymlink || header.Linkname != alias.Target {
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

func TestValidatePortablePythonAliasFinalImageRejectsSubstitutedLink(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias := portablePythonAliasSpecV1{
		Name: "tool", Component: "app", Destination: "/usr/local/bin/tool",
		Target: "/opt/reploy/providers/python/app/bin/tool", BindingIdentity: "binding-tool",
	}
	previous := collectPortablePythonAliasEvidenceV1
	t.Cleanup(func() { collectPortablePythonAliasEvidenceV1 = previous })
	collectPortablePythonAliasEvidenceV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []FullImageExecutableProbe) ([]providers.ExecutableEvidence, error) {
		return []providers.ExecutableEvidence{{
			InvocationPath: alias.Destination,
			LinkChain: []providers.LinkEvidence{{
				Path: alias.Destination, Target: "/opt/reploy/providers/python/other/bin/tool",
				ResolvedPath: "/opt/reploy/providers/python/other/bin/tool",
			}},
			Terminal: providers.FileEvidence{Path: "/opt/reploy/providers/python/other/bin/tool"},
		}}, nil
	}
	err = validatePortablePythonAliasFinalImageEvidenceV1(context.Background(), store, testProbeImageDescriptor(t, "linux/amd64"), []portablePythonAliasSpecV1{alias})
	if err == nil || !strings.Contains(err.Error(), "link target") {
		t.Fatalf("substituted final-image link error = %v", err)
	}
}

func TestBuildAndValidatePortablePythonAliasLayerRejectsInterruptedBuild(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := portablePythonAliasLayerTestSource(t)
	alias := portablePythonAliasSpecV1{
		Name: "tool", Component: "app", Destination: "/usr/local/bin/tool",
		Target: "/opt/reploy/providers/python/app/bin/tool", BindingIdentity: "binding-tool",
	}
	previousBuild, previousDestinations := buildPortablePythonAliasLayerV1, inspectPortablePythonAliasDestinationsV1
	t.Cleanup(func() {
		buildPortablePythonAliasLayerV1 = previousBuild
		inspectPortablePythonAliasDestinationsV1 = previousDestinations
	})
	inspectPortablePythonAliasDestinationsV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) ([]string, error) {
		return nil, nil
	}
	buildPortablePythonAliasLayerV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, string, []byte, RunOptions) (BuiltImageCandidate, error) {
		return BuiltImageCandidate{}, errors.New("interrupted alias layer build")
	}
	candidate, inspected, err := buildAndValidatePortablePythonAliasLayerV1(context.Background(), store, source, []portablePythonAliasSpecV1{alias}, source.Descriptor.Platform, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "interrupted alias layer build") || candidate.ImageID != "" || inspected.Descriptor.ConfigDigest != "" {
		t.Fatalf("interrupted alias layer result candidate=%#v inspected=%#v err=%v", candidate, inspected, err)
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
