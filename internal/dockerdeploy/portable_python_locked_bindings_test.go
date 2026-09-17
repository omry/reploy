package dockerdeploy

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func TestBuildPortableToolPythonLockedPlanUsesPersistedRecordsOnly(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	previousRecords := portableToolPythonFreshLockRecordsV1
	t.Cleanup(func() { portableToolPythonFreshLockRecordsV1 = previousRecords })
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return toolcatalog.EmbeddedPortableToolLockRecordSetV1{}, os.ErrNotExist
	}

	plan, err := buildPortableToolPythonLockedPlanV1(&fixture.lock)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || len(plan.Projection.Components) != 1 || len(plan.Projection.Components[0].Bindings) != 1 {
		t.Fatalf("locked plan = %#v", plan)
	}
	if plan.Projection.Components[0].Bindings[0].Artifact != fixture.binding.Artifact {
		t.Fatalf("locked binding = %#v", plan.Projection.Components[0].Bindings[0])
	}
}

func TestPortableToolPythonLockedAcquisitionIncludesSelectedTool(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	entry, err := portableToolPythonPlanEntryForBindingV1(fixture.plan.Plan, fixture.binding)
	if err != nil {
		t.Fatal(err)
	}

	other := fixture.lock.Acquisitions[0]
	other.Tool = "other-tool"
	fixture.lock.Acquisitions = append([]providers.PortableToolArtifactAcquisitionLockV1{other}, fixture.lock.Acquisitions...)

	acquisition, err := portableToolPythonLockedAcquisitionForBindingV1(fixture.lock, entry, fixture.binding)
	if err != nil {
		t.Fatal(err)
	}
	if acquisition.Tool != entry.Provenance.Tool {
		t.Fatalf("selected acquisition tool = %q, want %q", acquisition.Tool, entry.Provenance.Tool)
	}
}

func TestAcquirePortableToolPythonLockedWheelsReopensExactStoreObject(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	facts := pythonprovider.InterpreterInspectionFactsV2{
		Version: "3.12.2", Implementation: "cpython", ABI: "cp312",
		Libc: "glibc", LibcMajor: "2", LibcMinor: "35",
		TestedTags:     append([]string{}, fixture.component.TestedTags...),
		CompatibleTags: append([]string{}, fixture.component.TestedTags...),
	}
	verified, err := acquirePortableToolPythonLockedWheelsV1(
		context.Background(), fixture.store, fixture.plan,
		fixture.component, providers.ExecutableEvidence{Facts: pythonprovider.CanonicalInterpreterFactsV2(facts)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 || verified[0].Descriptor != fixture.descriptor ||
		verified[0].Provenance.OperationID != "locked-replay" ||
		verified[0].Source.Reference != fixture.source.Reference {
		t.Fatalf("locked verified wheels = %#v", verified)
	}
}

func TestAcquirePortableToolPythonLockedWheelsRejectsMissingOrDriftedObjectBeforeResolverStaging(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "missing", mutate: func(t *testing.T, blob string) {
			if err := os.Remove(blob); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "drifted", mutate: func(t *testing.T, blob string) {
			if err := os.Chmod(blob, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(blob, []byte("drifted"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPortableToolPythonLockedTestFixture(t)
			blob, err := fixture.store.BlobPath(fixture.descriptor.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, blob)
			facts := pythonprovider.InterpreterInspectionFactsV2{
				Version: "3.12.2", Implementation: "cpython", ABI: "cp312",
				Libc: "glibc", LibcMajor: "2", LibcMinor: "35",
				TestedTags:     append([]string{}, fixture.component.TestedTags...),
				CompatibleTags: append([]string{}, fixture.component.TestedTags...),
			}
			_, err = acquirePortableToolPythonLockedWheelsV1(
				context.Background(), fixture.store, fixture.plan,
				fixture.component, providers.ExecutableEvidence{Facts: pythonprovider.CanonicalInterpreterFactsV2(facts)},
			)
			if err == nil || !strings.Contains(err.Error(), "open verified store artifact") {
				t.Fatalf("%s object error = %v", test.name, err)
			}
		})
	}
}

func TestExecutePreparedPythonGraphDerivesLockedBindingsAndRejectsCachedSubstitution(t *testing.T) {
	reuseFixture := newPreparedPythonGraphReuseFixture(t)
	lockedFixture := newPortableToolPythonLockedTestFixture(t)
	file, err := lockedFixture.store.OpenVerifiedArtifact(lockedFixture.descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reuseFixture.store.PublishExpected(
		context.Background(), lockedFixture.descriptor, file,
	); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, node := range reuseFixture.request.Plan.Nodes {
		if node.ID != "base" {
			continue
		}
		reuseFixture.lock.Base.AuthorReference, _ = node.Request.Value["image"].(string)
		reuseFixture.lock.BasePlanDigest, err = providers.ProviderNodePlanDigest(node)
		if err != nil {
			t.Fatal(err)
		}
	}
	reuseFixture.lock.PortableTools = &lockedFixture.lock

	previousPrepare := preparePythonGraphExecutionBackend
	previousExecute := executePreparedPythonProviderGraph
	t.Cleanup(func() {
		preparePythonGraphExecutionBackend = previousPrepare
		executePreparedPythonProviderGraph = previousExecute
	})
	var configs map[providers.NodeID]PreparedPythonNodeConfig
	preparePythonGraphExecutionBackend = func(
		_ context.Context,
		_ providerstore.Store,
		_ providers.ProviderPlanV1,
		_ deploy.ImageDescriptor,
		_ providers.ImageConfigPolicy,
		value map[providers.NodeID]PreparedPythonNodeConfig,
		_ map[providers.NodeID]PreparedAPTNodeConfig,
		_ RunOptions,
	) (PreparedPythonGraphBackend, func() error, error) {
		configs = value
		return PreparedPythonGraphBackend{}, func() error { return nil }, nil
	}
	var execution providers.GraphExecutionRequest
	executePreparedPythonProviderGraph = func(
		_ context.Context, request providers.GraphExecutionRequest,
	) (providers.GraphExecutionResult, error) {
		execution = request
		return providers.GraphExecutionResult{Plan: request.Plan}, nil
	}

	_, err = ExecutePreparedPythonGraph(context.Background(), PreparedPythonGraphExecutionInput{
		Store: reuseFixture.store, Plan: reuseFixture.request.Plan,
		BaseDescriptor:   reuseFixture.lock.Base,
		BaseCatalog:      reuseFixture.request.EarlierCatalog,
		Sources:          reuseFixture.request.SourceCandidates,
		SourceWheels:     reuseFixture.sourceWheels,
		CurrentLock:      &reuseFixture.lock,
		FinalImageConfig: pythonConsumerTestImageConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	config := configs[reuseFixture.request.NodeID]
	if config.PortableToolLockedPlan == nil || config.PortableToolFreshPlan != nil ||
		config.PortableToolBindings == nil {
		t.Fatalf("locked Python node config = %#v", config)
	}
	if _, found := execution.CachedResolutions[reuseFixture.request.NodeID]; found {
		t.Fatalf("cached binding resolution was retained: %#v", execution.CachedResolutions)
	}
}

func TestPreparedPythonNodeOperationsLockedPortableWheelUsesProductionResolverSeam(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	base := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, base.Platform, t.TempDir())
	request := preparedPythonResolveRequest(t, base)
	stale, err := fixture.store.Publish(
		context.Background(), fixture.descriptor.LogicalPath, "wheel", strings.NewReader("superseded portable wheel"),
	)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, cleanupArtifacts, err := PreparePythonResolverArtifacts(
		fixture.store, []providerstore.ArtifactDescriptor{stale},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupArtifacts)
	selectedFile, err := fixture.store.OpenVerifiedArtifact(fixture.descriptor)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(selectedFile)
	if err != nil {
		_ = selectedFile.Close()
		t.Fatal(err)
	}
	if err := selectedFile.Close(); err != nil {
		t.Fatal(err)
	}
	interpreterResponse := probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{
		pythonConsumerObservation("interpreter", "/usr/bin/python3"),
	}}
	commands := stubPythonInterpreterSelectionCommands(
		t, mustCanonicalProbeResponse(t, interpreterResponse),
		[]string{string(pythonInspectionOutputV2ForTest(
			"3.12.2", fixture.component.TestedTags, fixture.component.TestedTags,
		))},
		func() error {
			writePythonIntegrationWheel(t, filepath.Join(artifacts.OutputHostDir, "demo_server-1.0-py3-none-any.whl"))
			return os.WriteFile(
				filepath.Join(artifacts.OutputHostDir, filepath.Base(fixture.descriptor.LogicalPath)),
				content, 0o600,
			)
		},
	)
	session, err := OpenPythonResolverSession(context.Background(), base, workspace, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	session.observations[pythonCarrierRequirementID] = pythonConsumerObservation(pythonCarrierRequirementID, pythonCarrierPath)
	session.observations[pythonLauncherRequirementID] = pythonConsumerObservation(pythonLauncherRequirementID, pythonLauncherPath)
	operations := PreparedPythonNodeOperations{
		Store: fixture.store,
		Validators: providers.ProviderOwnerValidators{
			Profile: pythonprovider.ValidateRequirementProfileV1,
			Bundle:  pythonprovider.ValidateResolvedBundlePayloadV1,
		},
		FinalImageConfig:       pythonConsumerTestImageConfig(),
		Artifacts:              artifacts,
		ReusableWheels:         []providerstore.ArtifactDescriptor{stale},
		LocalOverrides:         []PythonLocalOverrideV1{},
		PortableToolBindings:   &fixture.component,
		PortableToolLockedPlan: fixture.plan,
		verifiedArtifacts:      map[canonical.Digest]string{},
	}
	resolution, _, err := operations.resolveFresh(context.Background(), session, request)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := pythonprovider.DecodeCanonicalBundleDataV1(
		fixture.component.Component, resolution.Bundle.Payload.ProviderPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	selectedCount := 0
	for _, wheel := range bundle.Wheels {
		if wheel.Distribution != fixture.binding.Distribution {
			continue
		}
		selectedCount++
		if wheel.Artifact != fixture.descriptor {
			t.Fatalf("locked bundle wheel = %#v, want descriptor %#v", wheel, fixture.descriptor)
		}
	}
	if selectedCount != 1 {
		t.Fatalf("locked selected bundle wheels = %d, bundle = %#v", selectedCount, bundle.Wheels)
	}
	if len(*commands) < 5 || !containsInOrder((*commands)[4].Args, []string{fixture.binding.Distribution}) {
		t.Fatalf("resolver command does not contain the locked direct root: %#v", *commands)
	}
}

type portableToolPythonLockedTestFixtureV1 struct {
	store      providerstore.Store
	lock       providers.PortableToolLockV1
	plan       *PortableToolPythonLockedPlanV1
	component  pythonprovider.PortableToolPythonComponentV1
	binding    pythonprovider.PortableToolPythonBindingV1
	source     providers.PortableToolSelectedRecordV1
	descriptor providerstore.ArtifactDescriptor
}

func newPortableToolPythonLockedTestFixture(t *testing.T) portableToolPythonLockedTestFixtureV1 {
	t.Helper()
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(fresh.Closures)
	if err != nil {
		t.Fatal(err)
	}
	content := portableToolPythonLockedWheelContentV1(t)
	digest := canonical.Digest("sha256:" + strings.TrimSpace(strings.ToLower(fmt.Sprintf("%x", sha256.Sum256(content)))))
	descriptor := providerstore.ArtifactDescriptor{
		LogicalPath: "wheels/playwright-1.61.0-py3-none-manylinux1_x86_64.whl",
		Kind:        "wheel", Size: strconv.Itoa(len(content)), SHA256: digest,
	}
	artifact := &fresh.Plan.Tools[0].Responsibilities.BindingArtifacts[0]
	oldArtifact := artifact.Reference
	artifact.Record.Value["size"] = descriptor.Size
	artifact.Record.Value["sha256"] = string(descriptor.SHA256)
	artifact.Reference = portableToolPythonFreshReferenceV1(t, artifact.Record.Value["id"].(string), artifact.Record)
	fresh.Plan.Tools[0].Responsibilities.BindingArtifacts[0] = *artifact

	var source *toolcatalog.EmbeddedPortableToolArtifactSourceV1
	for index := range records.Artifacts {
		if records.Artifacts[index].Artifact == oldArtifact {
			source = &records.Artifacts[index]
			break
		}
	}
	if source == nil {
		t.Fatal("Playwright artifact source is missing")
	}
	source.Source.Record.Value["sha256"] = string(descriptor.SHA256)
	source.Source.Record.Value["mirrors"] = []any{"https://example.invalid/playwright.whl"}
	source.Source.Reference = portableToolPythonFreshReferenceV1(t, source.Source.Record.Value["id"].(string), source.Source.Record)
	source.Artifact = artifact.Reference
	source.Descriptor = descriptor
	source.Mirrors = []string{"https://example.invalid/playwright.whl"}

	manifest := &records.Releases[0].Manifest
	manifestValue := manifest.Record.Value
	mappings := manifestValue["artifact_sources"].([]any)
	var mapping canonical.Object
	for index, raw := range mappings {
		value, ok := raw.(map[string]any)
		if !ok {
			if object, objectOK := raw.(canonical.Object); objectOK {
				value = map[string]any(object)
				ok = true
			}
		}
		if !ok {
			t.Fatalf("artifact source mapping %d has type %T", index, raw)
		}
		artifactValue, ok := value["artifact"].(map[string]any)
		if !ok {
			if object, objectOK := value["artifact"].(canonical.Object); objectOK {
				artifactValue = map[string]any(object)
				ok = true
			}
		}
		if ok && artifactValue["id"] == oldArtifact.ID {
			mapping = canonical.Object(value)
			break
		}
	}
	if mapping == nil {
		t.Fatalf("artifact source mapping for %q is missing", oldArtifact.ID)
	}
	mapping["artifact"] = canonical.Object{"id": artifact.Reference.ID, "digest": string(artifact.Reference.Digest)}
	mapping["artifact_sha256"] = string(descriptor.SHA256)
	mapping["source"] = canonical.Object{"id": source.Source.Reference.ID, "digest": string(source.Source.Reference.Digest)}
	sort.SliceStable(mappings, func(left, right int) bool {
		return portableToolPythonLockedMappingDigestV1(mappings[left]) < portableToolPythonLockedMappingDigestV1(mappings[right])
	})
	manifestValue["artifact_sources"] = mappings
	manifest.Reference = portableToolPythonFreshReferenceV1(t, manifestValue["id"].(string), manifest.Record)
	fresh.Plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(fresh.Plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	binding := projection.Components[0].Bindings[0]

	providerPlan := preparedPythonResolveRequest(t, testProbeImageDescriptor(t, "linux/amd64")).Plan
	domain := providers.PortableToolDomainAuthorityV1{ID: "application", Owner: providerPlan.Nodes[1].ID}
	dag, err := providers.BuildPortableToolProviderDAGV1(providerPlan, fresh.Plan, []providers.PortableToolProviderDomainSetV1{{
		Scope: "application:application", PackageManager: domain, Binding: domain, Filesystem: domain,
		Environment: domain, Exports: domain, Capabilities: domain,
	}})
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]providers.PortableToolArtifactAcquisitionInputV1, 0)
	for _, entry := range fresh.Plan.Tools {
		selectedArtifacts := append([]providers.PortableToolSelectedRecordV1{}, entry.Responsibilities.BindingArtifacts...)
		selectedArtifacts = append(selectedArtifacts, entry.Responsibilities.Payloads...)
		for _, selected := range selectedArtifacts {
			var selectedSource *toolcatalog.EmbeddedPortableToolArtifactSourceV1
			for index := range records.Artifacts {
				if records.Artifacts[index].Artifact == selected.Reference {
					selectedSource = &records.Artifacts[index]
					break
				}
			}
			if selectedSource == nil {
				t.Fatalf("selected artifact source is missing for %q", selected.Reference.ID)
			}
			inputs = append(inputs, providers.PortableToolArtifactAcquisitionInputV1{
				Scope: entry.Scope, Tool: entry.Provenance.Tool, Artifact: selected.Reference,
				Descriptor: selectedSource.Descriptor, Source: selectedSource.Source,
				Provenance: providerstore.AcquisitionProvenance{
					Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: selectedSource.Source.Reference.ID,
				},
			})
		}
	}
	lock, err := providers.BuildPortableToolLockV1(dag, records.Releases, inputs)
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	lockedPlan, err := buildPortableToolPythonLockedPlanV1(&lock)
	if err != nil {
		t.Fatal(err)
	}
	return portableToolPythonLockedTestFixtureV1{
		store: store, lock: lock, plan: lockedPlan, component: projection.Components[0],
		binding: binding, source: source.Source, descriptor: descriptor,
	}
}

func portableToolPythonLockedMappingDigestV1(raw any) string {
	value, ok := raw.(map[string]any)
	if !ok {
		if object, objectOK := raw.(canonical.Object); objectOK {
			value = map[string]any(object)
			ok = true
		}
	}
	if !ok {
		return ""
	}
	if digest, ok := value["artifact_sha256"].(string); ok {
		return digest
	}
	return ""
}

func portableToolPythonLockedWheelContentV1(t *testing.T) []byte {
	t.Helper()
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	files := map[string]string{
		"playwright/__main__.py":                       "def main():\n    return 0\n",
		"playwright/driver/node":                       "node\n",
		"playwright/driver/package/index.js":           "module.exports = {};\n",
		"playwright-1.61.0.dist-info/METADATA":         "Metadata-Version: 2.1\nName: playwright\nVersion: 1.61.0\nRequires-Python: >=3.10\n\n",
		"playwright-1.61.0.dist-info/WHEEL":            "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-manylinux1_x86_64\n",
		"playwright-1.61.0.dist-info/entry_points.txt": "[console_scripts]\nplaywright = playwright.__main__:main\n",
		"playwright-1.61.0.dist-info/RECORD":           "",
	}
	for name, body := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}
