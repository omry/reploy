package dockerdeploy

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestLoadPreparedPythonGraphReuseUsesOnlyCurrentCompatibleContent(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.Sources, fixture.sourceWheels, &fixture.lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := reuse.CachedResolutions[fixture.request.NodeID]; !found {
		t.Fatalf("cached resolutions = %#v", reuse.CachedResolutions)
	}
	if got := len(reuse.NodeConfigs[fixture.request.NodeID].ReusableWheels); got != 2 {
		t.Fatalf("reusable wheels = %d, want 2", got)
	}
	if got := len(reuse.ReusableArtifacts[fixture.request.NodeID]); got != 2 {
		t.Fatalf("reusable references = %d, want 2", got)
	}

	changed := append([]providers.ResolvedSourceInput{}, fixture.request.Sources...)
	changed[0].BuilderProfile = "different-builder"
	newLocal, err := fixture.store.Publish(context.Background(), "wheels/demo_server-1.1-py3-none-any.whl", "wheel", strings.NewReader("new local wheel"))
	if err != nil {
		t.Fatal(err)
	}
	changed[0].ArtifactDigest = newLocal.SHA256
	reuse, err = LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, changed, []providerstore.ArtifactDescriptor{newLocal}, &fixture.lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := reuse.CachedResolutions[fixture.request.NodeID]; found {
		t.Fatalf("changed local source retained cached resolution")
	}
	wheels := reuse.NodeConfigs[fixture.request.NodeID].ReusableWheels
	if len(wheels) != 2 || !containsWheelDigest(wheels, newLocal.SHA256) || !containsWheelDigest(wheels, fixture.downloadedWheelDigest) || containsWheelDigest(wheels, fixture.localWheelDigest) {
		t.Fatalf("reusable wheels after source change = %#v", wheels)
	}
}

func TestLoadPreparedPythonGraphReuseRequiresCurrentSourceWheelOnFirstBuild(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.Sources, fixture.sourceWheels, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	wheels := reuse.NodeConfigs[fixture.request.NodeID].ReusableWheels
	if len(wheels) != 1 || wheels[0].SHA256 != fixture.localWheelDigest {
		t.Fatalf("first-build source wheels = %#v", wheels)
	}
	if len(reuse.CachedResolutions) != 0 {
		t.Fatalf("first-build cached resolutions = %#v", reuse.CachedResolutions)
	}
	_, err = LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.Sources, []providerstore.ArtifactDescriptor{}, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "has no wheel descriptor") {
		t.Fatalf("missing source wheel error = %v", err)
	}
}

func TestLoadPreparedPythonGraphReuseTreatsMissingBlobAsCacheMiss(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	path, err := fixture.store.BlobPath(fixture.downloadedWheelDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.Sources, fixture.sourceWheels, &fixture.lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := reuse.CachedResolutions[fixture.request.NodeID]; found {
		t.Fatalf("missing blob retained cached resolution")
	}
	wheels := reuse.NodeConfigs[fixture.request.NodeID].ReusableWheels
	if len(wheels) != 1 || wheels[0].SHA256 != fixture.localWheelDigest {
		t.Fatalf("reusable wheels after missing blob = %#v", wheels)
	}
}

func TestPreparedPythonCachedValidationHashesStagedWheelBeforeConsumerWork(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.Sources, fixture.sourceWheels, &fixture.lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	config := reuse.NodeConfigs[fixture.request.NodeID]
	artifacts, cleanup, err := PreparePythonResolverArtifacts(fixture.store, config.ReusableWheels)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	path, err := fixture.store.BlobPath(fixture.downloadedWheelDigest)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content[0] ^= 0xff
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o444); err != nil {
		t.Fatal(err)
	}
	fixture.request.ReusableArtifacts = reuse.ReusableArtifacts[fixture.request.NodeID]
	operations := PreparedPythonNodeOperations{
		Store: fixture.store,
		Validators: providers.ProviderOwnerValidators{
			Profile: pythonprovider.ValidateRequirementProfileV1,
			Bundle:  pythonprovider.ValidateResolvedBundlePayloadV1,
		},
		Artifacts:      artifacts,
		ReusableWheels: config.ReusableWheels,
	}
	_, err = operations.validateCached(
		context.Background(), nil, fixture.request, reuse.CachedResolutions[fixture.request.NodeID],
	)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("cached wheel validation error = %v", err)
	}
}

type preparedPythonGraphReuseFixture struct {
	store                 providerstore.Store
	request               providers.ResolveNodeRequest
	lock                  deploy.BuildLockV1
	localWheelDigest      canonical.Digest
	downloadedWheelDigest canonical.Digest
	sourceWheels          []providerstore.ArtifactDescriptor
}

func newPreparedPythonGraphReuseFixture(t *testing.T) preparedPythonGraphReuseFixture {
	t.Helper()
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	request := preparedPythonResolveRequest(t, descriptor)
	wheelDir := t.TempDir()
	localPath := filepath.Join(wheelDir, "demo_server-1.0-py3-none-any.whl")
	downloadedPath := filepath.Join(wheelDir, "dependency-2.0-py3-none-any.whl")
	writeReuseTestWheel(t, localPath, "demo-server", "1.0")
	writeReuseTestWheel(t, downloadedPath, "dependency", "2.0")
	localDigest := reuseTestFileDigest(t, localPath)
	downloadedDigest := reuseTestFileDigest(t, downloadedPath)
	request.Sources = []providers.ResolvedSourceInput{{
		Schema:               providers.ResolvedSourceInputSchemaV1,
		Component:            "application",
		LogicalPackage:       "demo-server",
		SourceManifestDigest: reuseTestDigest("1"),
		BuilderProfile:       "uv-wheel-v1",
		BuildSettings:        providers.CanonicalProviderData{Schema: "python-build-settings-v1", Value: canonical.Object{}},
		EcosystemMetadata:    providers.CanonicalProviderData{Schema: "python-source-metadata-v1", Value: canonical.Object{}},
		ArtifactDigest:       localDigest,
	}}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	interpreter := request.EarlierCatalog[0].Evidence
	interpreter.RequirementID = "interpreter"
	interpreter.Terminal.RequirementID = "interpreter"
	interpreter.Facts = pythonprovider.CanonicalInterpreterFactsV1("3.13.2")
	resolution, err := providers.ResolveProviderNode(
		context.Background(), request,
		pythonprovider.WheelNodeResolver{
			ResolveInterpreter: func(context.Context, providers.ExecutableRequirement, []providers.RealizedOutput, providers.RealizedImageV1, blueprint.Platform) (providers.ExecutableEvidence, error) {
				return interpreter, nil
			},
			PrepareWheels: func(context.Context, providers.ResolveInput, providers.ExecutableEvidence) (string, error) {
				return wheelDir, nil
			},
		},
		store,
		providers.ProviderOwnerValidators{
			Profile: pythonprovider.ValidateRequirementProfileV1,
			Bundle:  pythonprovider.ValidateResolvedBundlePayloadV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var localWheel providerstore.ArtifactDescriptor
	for _, artifact := range resolution.Bundle.Payload.Artifacts {
		if artifact.SHA256 == localDigest {
			localWheel = artifact
		}
	}
	if localWheel.SHA256 == "" {
		t.Fatal("resolved bundle is missing the local source wheel")
	}
	manifest, err := providers.PublishResolvedBundleManifest(
		context.Background(), store, resolution.Bundle, pythonprovider.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	planDigest, err := providers.ProviderNodePlanDigest(request.Plan.Nodes[1])
	if err != nil {
		t.Fatal(err)
	}
	resultImage := providers.RealizedImageV1{
		Digest: reuseTestDigest("8"), ConfigDigest: reuseTestDigest("9"), RootFSSubject: reuseTestDigest("a"),
	}
	lock := deploy.BuildLockV1{
		Schema: deploy.BuildLockSchemaV1, BlueprintDigest: reuseTestDigest("2"), Overlay: deploy.EmptyRequestOverlayV1(),
		ResolvedRequestDigest: reuseTestDigest("3"), Platform: request.Platform, Base: descriptor,
		Graph: deploy.ProviderGraphLockV1{Nodes: []providers.NodeID{"base", request.NodeID}, Edges: append([]providers.ProviderEdgeV1{}, request.Plan.Edges...)},
		Nodes: []deploy.NodeLockV1{{
			NodeID: request.NodeID, Provider: blueprint.ComponentTypePython, PlanDigest: planDigest,
			ResolverCacheKey: reuseTestDigest("4"), RequirementProfile: resolution.Profile,
			ValidationEvidence: resolution.Evidence, BundleManifest: manifest, TransactionDigest: reuseTestDigest("5"),
			Upstream: request.Upstream, Result: resultImage,
			GeneratedExecutables: []providers.RealizedGeneratedExecutable{}, Outputs: []providers.RealizedOutput{},
		}},
		RuntimePolicy: deploy.RuntimePolicyV1{
			Schema: deploy.RuntimePolicySchemaV1, AllowedRoots: []string{"/mnt"},
			ProtectedPaths: []deploy.ProtectedPathV1{}, Plans: []deploy.RuntimePlanV1{},
		},
		ValidationRecord: providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: reuseTestDigest("6")},
		FinalImage:       resultImage,
	}
	if err := deploy.ValidateBuildLockV1(lock, pythonprovider.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	return preparedPythonGraphReuseFixture{
		store: store, request: request, lock: lock,
		localWheelDigest: localDigest, downloadedWheelDigest: downloadedDigest,
		sourceWheels: []providerstore.ArtifactDescriptor{localWheel},
	}
}

func containsWheelDigest(wheels []providerstore.ArtifactDescriptor, digest canonical.Digest) bool {
	for _, wheel := range wheels {
		if wheel.SHA256 == digest {
			return true
		}
	}
	return false
}

func writeReuseTestWheel(t *testing.T, filename string, name string, version string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	distInfo := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info/"
	for _, item := range []struct{ name, content string }{
		{name: distInfo + "METADATA", content: "Metadata-Version: 2.1\nName: " + name + "\nVersion: " + version + "\n\n"},
		{name: distInfo + "WHEEL", content: "Wheel-Version: 1.0\nGenerator: reploy-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{name: distInfo + "RECORD", content: ""},
	} {
		writer, err := archive.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func reuseTestFileDigest(t *testing.T, filename string) canonical.Digest {
	t.Helper()
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return canonical.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(content)))
}

func reuseTestDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}
