package dockerdeploy

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// Run explicitly on the store/daemon being qualified. This test pulls the pinned
// index and selected child; it never deletes shared images or changes the store.
func TestDockerBasePlatformIntegration(t *testing.T) {
	if os.Getenv("REPLOY_BASE_PLATFORM_INTEGRATION") != "1" {
		t.Skip("set REPLOY_BASE_PLATFORM_INTEGRATION=1 to qualify base-image inspection with Docker")
	}
	ctx := t.Context()
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "python:3.14-slim-bookworm@sha256:516b51cb9db2ef061e465c803e50f64361b82cefd91e525b889b967b83e83527"
	child := "python@sha256:47d41ea4947601fd5bc7ed6c00a7b9c1fbb936a7684873908a3c3b4e68ce3c86"
	run, _, err := bindDockerOutputV1(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := run(ctx, "info", "--format", "{{.ServerVersion}} {{.Architecture}} {{.Driver}} {{json .DriverStatus}}")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("daemon: %s", info)
	if version := os.Getenv("REPLOY_EXPECTED_DOCKER_VERSION"); version != "" && !strings.HasPrefix(info, version+" ") {
		t.Fatalf("required Docker %s; actual daemon: %s", version, info)
	}
	if expected := os.Getenv("REPLOY_EXPECTED_IMAGE_STORE"); expected != "" {
		containerd := strings.Contains(info, "io.containerd.snapshotter.v1")
		if expected != "classic" && expected != "containerd" {
			t.Fatalf("invalid required image store %q", expected)
		}
		if containerd != (expected == "containerd") {
			t.Fatalf("required %s image store; actual daemon: %s", expected, info)
		}
	}
	if _, err := run(ctx, "pull", "--platform", platform.Canonical, index); err != nil {
		t.Fatal(err)
	}
	first, firstConfig, err := resolveBase(ctx, index, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	again, againConfig, err := resolveBase(ctx, index, platform, run)
	if err != nil || !reflect.DeepEqual(first, again) || !reflect.DeepEqual(firstConfig, againConfig) {
		t.Fatalf("pre-pulled reuse changed identity/config: %v", err)
	}
	cached, cachedConfig, found, err := inspectCachedBase(ctx, index, platform, run, &first)
	if err != nil || !found || !reflect.DeepEqual(first, cached) || !reflect.DeepEqual(firstConfig, cachedConfig) {
		t.Fatalf("cache reuse found=%v err=%v", found, err)
	}
	// ResolveBase registers a descriptor-backed selected child before returning.
	// Validate every unchanged consumer before an explicit test-side child pull;
	// otherwise the pull would hide the shared-resolution regression.
	workspace := buildIntegrationProbeWorkspace(t, platform)
	validate := func(t *testing.T, image deploy.ImageDescriptor) {
		t.Helper()
		response, err := RunImageProbe(ctx, image, workspace, probe.RequestV1{
			Schema: probe.RequestSchemaV1, Inspections: []probe.ExecutableInspectionV1{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if response.Schema != probe.ResponseSchemaV1 || response.Observations == nil {
			t.Fatalf("invalid actual probe response: %#v", response)
		}
	}
	if first.ImmutableReference != "python@sha256:516b51cb9db2ef061e465c803e50f64361b82cefd91e525b889b967b83e83527" {
		t.Fatalf("index lost its locked repository reference: %s", first.ImmutableReference)
	}
	t.Run("index validation", func(t *testing.T) { validate(t, first) })

	store, err := providerstore.NewStore(dockerIntegrationSharedTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.Remove() })
	pythonArtifacts, cleanupPythonArtifacts, err := PreparePythonResolverArtifacts(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupPythonArtifacts)
	pythonSession, err := OpenPythonResolverSession(ctx, first, workspace, pythonArtifacts)
	if err != nil {
		t.Fatal(err)
	}
	if err := pythonSession.Close(ctx); err != nil {
		t.Fatal(err)
	}
	aptWorkspace, cleanupAPTWorkspace, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupAPTWorkspace)
	aptSession, err := OpenAPTResolverSession(ctx, first, workspace, aptWorkspace, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := aptSession.Close(ctx); err != nil {
		t.Fatal(err)
	}
	rootFSSubject, err := deploy.RootFSSubject(first.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	baseImage := providers.RealizedImageV1{
		Digest: first.ConfigDigest, ConfigDigest: first.ConfigDigest, RootFSSubject: rootFSSubject,
	}
	tagWorkspace, err := store.NewWorkspace("base-tag-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tagWorkspace) })
	temporaryReference, cleanupTemporaryReference, err := prepareTemporaryBuildBaseReference(ctx, store.Root(), tagWorkspace, baseImage, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupTemporaryReference(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("shared selected child is runnable for Python/APT sessions and temporary tag: %s", temporaryReference)
	if _, err := run(ctx, "pull", "--platform", platform.Canonical, child); err != nil {
		t.Fatal(err)
	}
	selectedChild, childConfig, err := resolveBase(ctx, child, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigDigest != selectedChild.ConfigDigest || !reflect.DeepEqual(first.RootFSDiffIDs, selectedChild.RootFSDiffIDs) || !reflect.DeepEqual(firstConfig, childConfig) {
		t.Fatal("index inspection returned config/layers different from the selected amd64 child")
	}
	t.Run("child validation", func(t *testing.T) { validate(t, selectedChild) })
	t.Logf("selected platform=%s inspected ID=%s layers=%v immutable=%s", first.Platform.Canonical, first.ConfigDigest, first.RootFSDiffIDs, first.ImmutableReference)
}
