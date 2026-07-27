package dockerdeploy

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestLoadPreparedPythonGraphReuseUsesOnlyCurrentCompatibleContent(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.SourceCandidates, fixture.sourceWheels, &fixture.lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	cached, found := reuse.CachedResolutions[fixture.request.NodeID]
	if !found {
		t.Fatalf("cached resolutions = %#v", reuse.CachedResolutions)
	}
	if len(cached.SelectedSources) != 1 || !reflect.DeepEqual(cached.SelectedSources[0], fixture.request.SourceCandidates[0]) {
		t.Fatalf("cached selected sources = %#v", cached.SelectedSources)
	}
	if got := len(reuse.NodeConfigs[fixture.request.NodeID].ReusableWheels); got != 2 {
		t.Fatalf("reusable wheels = %d, want 2", got)
	}
	if got := len(reuse.ReusableArtifacts[fixture.request.NodeID]); got != 2 {
		t.Fatalf("reusable references = %d, want 2", got)
	}

	changed := append([]providers.ResolvedSourceInput{}, fixture.request.SourceCandidates...)
	changed[0].BuilderProfile = "different-builder"
	newLocal, err := fixture.store.Publish(context.Background(), "wheels/demo_server-1.1-py3-none-any.whl", "wheel", strings.NewReader("new local wheel"))
	if err != nil {
		t.Fatal(err)
	}
	changed[0].OutputArtifactDigest = newLocal.SHA256
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

func TestLoadPreparedPythonGraphReuseIgnoresUnrelatedPythonOverride(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	plan := fixture.request.Plan
	plan.Nodes = append([]providers.NodeSpec{}, plan.Nodes...)
	requirement, err := pythonprovider.CanonicalPackageRequestV1("demo-server==1.0")
	if err != nil {
		t.Fatal(err)
	}
	request, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component:    "application/application/python",
		Interpreter:  blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
		Overrides: []pythonprovider.PythonPackageOverrideV1{
			{Distribution: "demo-server", Kind: "local"},
			{Distribution: "unused", Kind: "version", Version: "99"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Nodes {
		if plan.Nodes[index].ID != fixture.request.NodeID {
			continue
		}
		plan.Nodes[index].Request = request
		plan.Nodes[index].Requirements.ProviderData = providers.CanonicalProviderData{
			Schema: request.Schema, Value: request.Value,
		}
	}
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, plan, fixture.request.Platform,
		fixture.request.SourceCandidates, fixture.sourceWheels, &fixture.lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := reuse.CachedResolutions[fixture.request.NodeID]; !found {
		t.Fatal("unrelated Python override invalidated the cached node resolution")
	}
}

func TestLoadPreparedPythonGraphReuseIgnoresUnusedSourceCandidate(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	unusedWheel, err := fixture.store.Publish(
		context.Background(), "wheels/unused_source-1-py3-none-any.whl", "wheel",
		strings.NewReader("unused local wheel"),
	)
	if err != nil {
		t.Fatal(err)
	}
	unused := fixture.request.SourceCandidates[0]
	unused.LogicalPackage = "unused-source"
	unused.SourceInputDigest = reuseTestDigest("e")
	unused.OutputArtifactDigest = unusedWheel.SHA256
	candidates := append(append([]providers.ResolvedSourceInput{}, fixture.request.SourceCandidates...), unused)
	wheels := append(append([]providerstore.ArtifactDescriptor{}, fixture.sourceWheels...), unusedWheel)

	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, candidates, wheels, &fixture.lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	cached, found := reuse.CachedResolutions[fixture.request.NodeID]
	if !found || !reflect.DeepEqual(cached.SelectedSources, fixture.request.SourceCandidates) {
		t.Fatalf("unused candidate invalidated cached resolution: %#v", reuse.CachedResolutions)
	}
	if got := len(reuse.NodeConfigs[fixture.request.NodeID].ReusableWheels); got != 3 {
		t.Fatalf("reusable wheels = %d, want selected, downloaded, and unused candidates", got)
	}
}

func TestLoadPreparedPythonGraphReuseRequiresCurrentSourceWheelOnFirstBuild(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.SourceCandidates, fixture.sourceWheels, nil,
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
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.SourceCandidates, []providerstore.ArtifactDescriptor{}, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "has no wheel descriptor") {
		t.Fatalf("missing source wheel error = %v", err)
	}
}

func TestLoadPreparedPythonGraphReuseInitializesAPTAlongsidePython(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	aptNode := aptResolverTestNode(t, aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "python3"}))
	plan := fixture.request.Plan
	plan.Nodes = []providers.NodeSpec{aptNode, plan.Nodes[0], plan.Nodes[1]}
	if err := providers.ValidateProviderPlanV1(plan); err != nil {
		t.Fatal(err)
	}
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, plan, fixture.request.Platform, fixture.request.SourceCandidates, fixture.sourceWheels, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	aptConfig, found := reuse.APTNodeConfigs["apt"]
	if !found || len(aptConfig.ReusableDebs) != 0 {
		t.Fatalf("APT config = %#v, found = %v", aptConfig, found)
	}
	wantRoot := pythonprovider.InstallRoot + "/application/application"
	if len(aptConfig.ExclusiveRoots) != 1 || aptConfig.ExclusiveRoots[0] != wantRoot {
		t.Fatalf("APT exclusive roots = %#v, want %q", aptConfig.ExclusiveRoots, wantRoot)
	}
	if len(reuse.ReusableArtifacts["apt"]) != 0 || len(reuse.NodeConfigs[fixture.request.NodeID].ReusableWheels) != 1 {
		t.Fatalf("mixed reuse = %#v", reuse)
	}
}

func TestLoadPreparedPythonGraphReuseUsesCurrentLockedAPTBundle(t *testing.T) {
	store, plan, platform, lock, deb := newPreparedAPTGraphReuseFixture(t)
	reuse, err := LoadPreparedPythonGraphReuse(store, plan, platform, []providers.ResolvedSourceInput{}, []providerstore.ArtifactDescriptor{}, &lock)
	if err != nil {
		t.Fatal(err)
	}
	resolution, found := reuse.CachedResolutions["apt"]
	if !found || resolution.Bundle.Identity == "" {
		t.Fatalf("cached APT resolution = %#v, found = %v", resolution, found)
	}
	config := reuse.APTNodeConfigs["apt"]
	if len(config.ReusableDebs) != 1 || config.ReusableDebs[0] != deb {
		t.Fatalf("reusable APT archives = %#v, want %#v", config.ReusableDebs, deb)
	}
	if len(reuse.ReusableArtifacts["apt"]) != 1 {
		t.Fatalf("reusable APT references = %#v", reuse.ReusableArtifacts["apt"])
	}
}

func TestProviderBuildLockRejectsNodeAndProfileOwnerMismatch(t *testing.T) {
	_, _, _, lock, _ := newPreparedAPTGraphReuseFixture(t)
	lock.Graph.Nodes = []providers.NodeID{"base", "python/application"}
	lock.Nodes[0].NodeID = "python/application"
	lock.Nodes[0].Provider = blueprint.ComponentTypePython

	err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1)
	if err == nil || !strings.Contains(err.Error(), "profile provider") {
		t.Fatalf("provider/profile mismatch error = %v", err)
	}
}

func TestReusableAPTArchivesUsesOnlyPresentBundleDebs(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	present, err := store.Publish(context.Background(), "debs/present.deb", "deb", strings.NewReader("present"))
	if err != nil {
		t.Fatal(err)
	}
	missing := present
	missing.LogicalPath = "debs/missing.deb"
	missing.SHA256 = reuseTestDigest("d")
	bundle := aptprovider.BundleV1{BundlePackages: []aptprovider.BundlePackage{
		{Artifact: missing}, {Artifact: present},
	}}
	debs := reusableAPTArchives(store, bundle)
	if len(debs) != 1 || debs[0] != present {
		t.Fatalf("reusable APT archives = %#v", debs)
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
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.SourceCandidates, fixture.sourceWheels, &fixture.lock,
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

func TestLoadPreparedPythonGraphReuseRetainsOtherWheelsWhileRebuildingMissingSourceWheel(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	path, err := fixture.store.BlobPath(fixture.localWheelDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform,
		[]providers.ResolvedSourceInput{}, []providerstore.ArtifactDescriptor{}, &fixture.lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := reuse.CachedResolutions[fixture.request.NodeID]; found {
		t.Fatal("missing source wheel retained cached resolution")
	}
	wheels := reuse.NodeConfigs[fixture.request.NodeID].ReusableWheels
	if len(wheels) != 1 || wheels[0].SHA256 != fixture.downloadedWheelDigest {
		t.Fatalf("wheels retained during source repair = %#v", wheels)
	}
}

func TestPreparedPythonCachedValidationHashesStagedWheelBeforeConsumerWork(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	reuse, err := LoadPreparedPythonGraphReuse(
		fixture.store, fixture.request.Plan, fixture.request.Platform, fixture.request.SourceCandidates, fixture.sourceWheels, &fixture.lock,
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
	return newPreparedPythonGraphReuseFixtureWithManifest(t, reuseTestDigest("1"))
}

func newPreparedPythonGraphReuseFixtureWithManifest(t *testing.T, sourceManifest canonical.Digest) preparedPythonGraphReuseFixture {
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
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourceArtifact, err := store.Publish(
		context.Background(), "sdists/demo-server-1.0.tar.gz", "sdist", strings.NewReader("source distribution"),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.SourceCandidates = []providers.ResolvedSourceInput{
		testPythonResolvedSourceWithSourceArtifact(
			"application/application/python", "demo-server", "1.0", sourceManifest, sourceArtifact, localDigest,
		),
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
		PackageOverrides: deploy.PackageOverrideIntentV1{
			Schema: deploy.PackageOverrideIntentSchemaV1, EnvironmentID: "demo",
			Choices: []deploy.PackageOverrideIntentChoiceV1{{
				Provider: "python", Package: "demo-server", Kind: "local",
			}},
		},
		ResolvedRequestDigest: reuseTestDigest("3"), Platform: request.Platform, Base: descriptor,
		Graph: deploy.ProviderGraphLockV1{Nodes: []providers.NodeID{"base", request.NodeID}, Edges: append([]providers.ProviderEdgeV1{}, request.Plan.Edges...)},
		Nodes: []deploy.NodeLockV1{{
			NodeID: request.NodeID, Provider: blueprint.ComponentTypePython, PlanDigest: planDigest,
			ResolverCacheKey: reuseTestDigest("4"), RequirementProfile: resolution.Profile,
			ValidationEvidence: resolution.Evidence, BundleManifest: manifest, TransactionDigest: reuseTestDigest("5"),
			Upstream: request.Upstream, Result: resultImage,
			GeneratedExecutables: []providers.RealizedGeneratedExecutable{}, Outputs: []providers.RealizedOutput{},
		}},
		Catalog: append([]providers.RealizedOutput{}, request.EarlierCatalog...),
		RuntimePolicy: deploy.RuntimePolicyV1{
			Schema:         deploy.RuntimePolicySchemaV1,
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

func newPreparedAPTGraphReuseFixture(t *testing.T) (
	providerstore.Store,
	providers.ProviderPlanV1,
	blueprint.Platform,
	deploy.BuildLockV1,
	providerstore.ArtifactDescriptor,
) {
	t.Helper()
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	baseNode := preparedPythonResolveRequest(t, descriptor).Plan.Nodes[0]
	request := aptResolverTestRequest(t, blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}})
	aptNode := aptResolverTestNode(t, request)
	plan := providers.ProviderPlanV1{
		Schema: providers.ProviderPlanSchemaV1,
		Nodes:  []providers.NodeSpec{aptNode, baseNode},
		Edges:  []providers.ProviderEdgeV1{},
	}
	if err := providers.ValidateProviderPlanV1(plan); err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deb, err := store.Publish(context.Background(), "debs/hello_1_amd64.deb", "deb", strings.NewReader("deb"))
	if err != nil {
		t.Fatal(err)
	}
	resolvePlan := aptprovider.ResolvePlanV1{Schema: aptprovider.ResolvePlanSchemaV1, Packages: []aptprovider.ResolvePlanPackageV1{{
		Name: "hello", ResolverArchitecture: "amd64", SelectedVersion: "1",
	}}}
	bundlePackage, err := aptprovider.NewBundlePackageV1(
		resolvePlan.Packages[0],
		aptprovider.PackageTuple{Name: "hello", Version: "1", Architecture: "amd64", Status: aptprovider.InstalledPackageStatusV1},
		deb,
		reuseTestDigest("f"),
		[]aptprovider.PackageTuple{},
	)
	if err != nil {
		t.Fatal(err)
	}
	bundleData, err := aptprovider.NewBundleV1("amd64", resolvePlan, []aptprovider.PackageTuple{}, []aptprovider.BundlePackage{bundlePackage})
	if err != nil {
		t.Fatal(err)
	}
	if err := aptprovider.PublishMaterializationArtifactsV1(context.Background(), store, bundleData); err != nil {
		t.Fatal(err)
	}
	profile := preparedAPTReuseProfile(t, descriptor.Platform, aptNode.Requirements)
	profileDigest, err := providers.RequirementProfileDigest(profile, aptprovider.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	providerPayload, err := aptprovider.CanonicalBundleDataV1(bundleData)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := []providerstore.ArtifactDescriptor{deb, bundleData.Script, bundleData.StateManifest}
	sort.Slice(artifacts, func(left int, right int) bool { return artifacts[left].LogicalPath < artifacts[right].LogicalPath })
	upstream, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := providers.NewResolvedBundle(providers.ResolvedBundleIdentityV1{
		Schema: providers.ResolvedBundleSchemaV1, NodeID: "apt", Provider: blueprint.ComponentTypeAPT,
		Request: request, RequirementProfileDigest: profileDigest, RecipeVersion: aptprovider.RecipeVersion,
		Platform: descriptor.Platform, Upstream: upstream, SelectedSources: []providers.ResolvedSourceInput{}, Artifacts: artifacts,
		Outputs: []providers.ResolvedOutput{}, ProviderPayload: providerPayload,
	}, aptprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := providers.PublishResolvedBundleManifest(context.Background(), store, bundle, aptprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := providers.NewValidationEvidence(upstream.RootFSSubject, profileDigest)
	if err != nil {
		t.Fatal(err)
	}
	planDigest, err := providers.ProviderNodePlanDigest(aptNode)
	if err != nil {
		t.Fatal(err)
	}
	resultImage := providers.RealizedImageV1{Digest: reuseTestDigest("8"), ConfigDigest: reuseTestDigest("9"), RootFSSubject: reuseTestDigest("a")}
	lock := deploy.BuildLockV1{
		Schema: deploy.BuildLockSchemaV1, BlueprintDigest: reuseTestDigest("2"), Overlay: deploy.EmptyRequestOverlayV1(),
		PackageOverrides:      deploy.EmptyPackageOverrideIntentV1("demo"),
		ResolvedRequestDigest: reuseTestDigest("3"), Platform: descriptor.Platform, Base: descriptor,
		Graph: deploy.ProviderGraphLockV1{Nodes: []providers.NodeID{"apt", "base"}, Edges: []providers.ProviderEdgeV1{}},
		Nodes: []deploy.NodeLockV1{{
			NodeID: "apt", Provider: blueprint.ComponentTypeAPT, PlanDigest: planDigest,
			ResolverCacheKey: reuseTestDigest("4"), RequirementProfile: profile,
			ValidationEvidence: evidence, BundleManifest: manifest, TransactionDigest: reuseTestDigest("5"),
			Upstream: upstream, Result: resultImage,
			GeneratedExecutables: []providers.RealizedGeneratedExecutable{}, Outputs: []providers.RealizedOutput{},
		}},
		Catalog: []providers.RealizedOutput{},
		RuntimePolicy: deploy.RuntimePolicyV1{
			Schema:         deploy.RuntimePolicySchemaV1,
			ProtectedPaths: []deploy.ProtectedPathV1{}, Plans: []deploy.RuntimePlanV1{},
		},
		ValidationRecord: providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: reuseTestDigest("6")},
		FinalImage:       resultImage,
	}
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	return store, plan, descriptor.Platform, lock, deb
}

func preparedAPTReuseProfile(t *testing.T, platform blueprint.Platform, declaration providers.RequirementDeclaration) providers.RequirementProfile {
	t.Helper()
	tools := aptprovider.RequiredBaseToolsV1()
	for index := range tools {
		switch tools[index].Name {
		case "apt_get", "dpkg", "dpkg_deb", "dpkg_query", "sha256sum":
			tools[index].Version = tools[index].Name + " 1"
		}
	}
	base, err := aptprovider.NewBaseProfileEvidenceV1(platform, map[string]string{"ID": "debian", "VERSION_ID": "13"}, tools, "amd64", []string{})
	if err != nil {
		t.Fatal(err)
	}
	executables := make([]providers.ValidatedExecutableInput, 0, len(tools))
	for _, tool := range tools {
		role := providers.ExecutableRoleProviderPrerequisite
		component := "apt"
		if tool.Name == "sh" {
			role, component = providers.ExecutableRoleCarrier, "backend"
		} else if tool.Name == "env" {
			role, component = providers.ExecutableRoleEnvironmentLauncher, "backend"
		}
		executable := rendererExecutable(tool.Name, role, tool.Path)
		executable.Evidence.Output.Component = component
		executable.Evidence.Facts = providers.CanonicalProviderData{
			Schema: "apt-required-tool-v1", Value: canonical.Object{"interface": tool.Interface, "version": tool.Version},
		}
		executables = append(executables, executable)
	}
	facts, err := aptprovider.CanonicalProfileFactsV1(base, executables)
	if err != nil {
		t.Fatal(err)
	}
	return providers.RequirementProfile{
		Schema: providers.RequirementProfileSchemaV1, Provider: blueprint.ComponentTypeAPT, Declaration: declaration,
		SelectedExecutables: []providers.ExecutableEvidence{}, SelectedFiles: []providers.FileEvidence{},
		Platform: platform, Facts: facts,
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
