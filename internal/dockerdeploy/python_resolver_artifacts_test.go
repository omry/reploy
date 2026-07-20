package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPreparePythonResolverArtifactsSeparatesVerifiedInputFromEmptyOutput(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Publish(context.Background(), "wheels/alpha-1-py3-none-any.whl", "wheel", strings.NewReader("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Publish(context.Background(), "wheels/beta-1-py3-none-any.whl", "wheel", strings.NewReader("beta"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(prepared.HostDir) != filepath.Join(store.Root(), "tmp") || prepared.InputContainerDir != pythonResolverInputContainerDir || prepared.OutputContainerDir != pythonResolverOutputContainerDir {
		t.Fatalf("prepared resolver artifacts = %#v", prepared)
	}
	inputInfo, err := os.Stat(prepared.InputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if inputInfo.Mode().Perm() != 0o500 {
		t.Fatalf("input mode = %o, want 500", inputInfo.Mode().Perm())
	}
	for _, artifact := range []providerstore.ArtifactDescriptor{first, second} {
		source, err := os.Stat(mustBlobPath(t, store, artifact))
		if err != nil {
			t.Fatal(err)
		}
		target, err := os.Stat(filepath.Join(prepared.InputHostDir, filepath.Base(artifact.LogicalPath)))
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(source, target) {
			t.Fatalf("resolver input %q is not the verified store object", artifact.LogicalPath)
		}
	}
	entries, err := os.ReadDir(prepared.OutputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("resolver output is not empty: %#v", entries)
	}
	cleanup()
	if _, err := os.Lstat(prepared.HostDir); !os.IsNotExist(err) {
		t.Fatalf("resolver workspace still exists: %v", err)
	}
}

func TestStagePythonResolverSourceConstraintsProtectsDeterministicInput(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wheel, err := store.Publish(context.Background(), "wheels/demo-1-py3-none-any.whl", "wheel", strings.NewReader("wheel"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{wheel})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	packageRequest, _ := pythonprovider.CanonicalPackageRequestV1("demo")
	request, _ := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{packageRequest},
	})
	source := providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV1, Component: "application", LogicalPackage: "demo",
		SourceManifestDigest: canonical.Digest("sha256:" + strings.Repeat("a", 64)), BuilderProfile: "uv-v1",
		BuildSettings:     providers.CanonicalProviderData{Schema: "source-settings-v1", Value: canonical.Object{}},
		EcosystemMetadata: providers.CanonicalProviderData{Schema: "python-source-v1", Value: canonical.Object{}},
		ArtifactDigest:    wheel.SHA256,
	}
	if err := StagePythonResolverSourceConstraints(prepared, request, []providers.ResolvedSourceInput{source}, []providerstore.ArtifactDescriptor{wheel}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(prepared.InputHostDir, filepath.Base(pythonprovider.ResolverSourceConstraintsPath)))
	if err != nil {
		t.Fatal(err)
	}
	want := "demo @ file:///.reploy-resolver/input/demo-1-py3-none-any.whl\n"
	if string(content) != want {
		t.Fatalf("constraints = %q, want %q", content, want)
	}
	info, err := os.Stat(prepared.InputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o500 {
		t.Fatalf("input mode = %o after constraints, want 500", info.Mode().Perm())
	}
}

func TestPreparePythonResolverArtifactsRejectsInvalidOrCollidingInputAndCleansScratch(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wheel, err := store.Publish(context.Background(), "wheels/demo.whl", "wheel", strings.NewReader("wheel"))
	if err != nil {
		t.Fatal(err)
	}
	collision := wheel
	collision.LogicalPath = "other/demo.whl"
	for _, reusable := range [][]providerstore.ArtifactDescriptor{
		{{LogicalPath: "packages/demo.deb", Kind: "deb", Size: wheel.Size, SHA256: wheel.SHA256}},
		{wheel, collision},
	} {
		if _, cleanup, err := PreparePythonResolverArtifacts(store, reusable); err == nil {
			cleanup()
			t.Fatal("invalid reusable resolver input was accepted")
		}
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed resolver workspaces remain: %#v", entries)
	}
}

func TestPythonResolverArtifactContentVerificationIsDeferredUntilConsumption(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wheel, err := store.Publish(context.Background(), "wheels/demo.whl", "wheel", strings.NewReader("expected"))
	if err != nil {
		t.Fatal(err)
	}
	path := mustBlobPath(t, store, wheel)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("altered!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{wheel})
	if err != nil {
		t.Fatalf("safe staging should not hash a wheel on a possible cache hit: %v", err)
	}
	defer cleanup()
	if _, err := os.Lstat(filepath.Join(prepared.InputHostDir, "demo.whl")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPythonResolverArtifacts(prepared, []providerstore.ArtifactDescriptor{wheel}); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("deferred verification error = %v", err)
	}
	verified, err := FilterVerifiedPythonResolverArtifacts(prepared, []providerstore.ArtifactDescriptor{wheel})
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 0 {
		t.Fatalf("corrupt wheel remained reusable: %#v", verified)
	}
	if _, err := os.Lstat(filepath.Join(prepared.InputHostDir, "demo.whl")); !os.IsNotExist(err) {
		t.Fatalf("corrupt staged wheel remains: %v", err)
	}
	info, err := os.Stat(prepared.InputHostDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o500 {
		t.Fatalf("input mode = %o after filtering, want 500", info.Mode().Perm())
	}
}
