package dockerdeploy

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func portableToolPythonFreshResolverWheelV1(t *testing.T) []byte {
	t.Helper()
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	files := map[string]string{
		"playwright/__main__.py":                       "def main():\n    return 0\n",
		"playwright-1.61.0.dist-info/METADATA":         "Metadata-Version: 2.1\nName: playwright\nVersion: 1.61.0\nRequires-Python: >=3.12\n\n",
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

func TestAcquirePortableToolPythonFreshWheelsBuildsExactSelectedRequests(t *testing.T) {
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	component := fresh.Projection.Components[0]
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(fresh.Closures)
	if err != nil {
		t.Fatal(err)
	}
	interpreter := providers.ExecutableEvidence{InvocationPath: "/usr/bin/python3"}
	sentinel := []pythonprovider.PortableToolVerifiedWheelInputV1{{Scope: component.Bindings[0].Scope}}
	previousProducer := producePortableToolPythonFreshWheelsV1
	t.Cleanup(func() { producePortableToolPythonFreshWheelsV1 = previousProducer })
	calls := 0
	producePortableToolPythonFreshWheelsV1 = func(
		_ context.Context,
		_ providerstore.Store,
		projection pythonprovider.PortableToolPythonProjectionV1,
		requests []pythonprovider.PortableToolPythonVerifiedWheelRequestV1,
	) ([]pythonprovider.PortableToolVerifiedWheelInputV1, error) {
		calls++
		if !reflect.DeepEqual(projection.Components, []pythonprovider.PortableToolPythonComponentV1{component}) || len(requests) != 1 {
			t.Fatalf("fresh producer inputs = %#v, %#v", projection, requests)
		}
		request := requests[0]
		entry := fresh.Plan.Tools[0]
		binding := component.Bindings[0]
		if !reflect.DeepEqual(request.SelectedPlanEntry, entry) || !reflect.DeepEqual(request.Component, component) ||
			!reflect.DeepEqual(request.Binding, binding) || !reflect.DeepEqual(request.Interpreter, interpreter) ||
			request.ContractRecord.Reference != binding.Contract || request.ArtifactRecord.Reference != binding.Artifact ||
			request.Mode != pythonprovider.PortableToolVerifiedWheelFreshV1 || request.LockedAcquisition != nil {
			t.Fatalf("fresh verified-wheel request = %#v", request)
		}
		var selectedSource *toolcatalog.EmbeddedPortableToolArtifactSourceV1
		for index := range records.Artifacts {
			if records.Artifacts[index].Artifact == binding.Artifact {
				selectedSource = &records.Artifacts[index]
				break
			}
		}
		if selectedSource == nil || request.SourceRecord.Reference != selectedSource.Source.Reference ||
			request.Acquisition.Artifact != selectedSource.Descriptor ||
			request.Acquisition.Source.ID != selectedSource.Source.Reference.ID ||
			request.Acquisition.Source.SHA256 != selectedSource.Descriptor.SHA256 ||
			!reflect.DeepEqual(request.Acquisition.Source.Mirrors, selectedSource.Mirrors) ||
			request.Acquisition.Policy != providerstore.DefaultAcquisitionPolicy() {
			t.Fatalf("fresh acquisition join = %#v, source = %#v", request.Acquisition, selectedSource)
		}
		if request.ManifestRecord.Reference.Digest != entry.Provenance.ManifestDigest {
			t.Fatalf("manifest = %#v, entry = %#v", request.ManifestRecord, entry.Provenance)
		}
		return sentinel, nil
	}

	got, err := acquirePortableToolPythonFreshWheelsV1(
		context.Background(), providerstore.Store{}, &fresh, component, interpreter,
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(got, sentinel) {
		t.Fatalf("producer calls = %d, result = %#v", calls, got)
	}
}

func TestAcquirePortableToolPythonFreshWheelsRejectsIncompleteJoinBeforeProducer(t *testing.T) {
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	component := fresh.Projection.Components[0]
	previousRecords := portableToolPythonFreshLockRecordsV1
	previousProducer := producePortableToolPythonFreshWheelsV1
	t.Cleanup(func() {
		portableToolPythonFreshLockRecordsV1 = previousRecords
		producePortableToolPythonFreshWheelsV1 = previousProducer
	})
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(fresh.Closures)
	if err != nil {
		t.Fatal(err)
	}
	records.Artifacts = []toolcatalog.EmbeddedPortableToolArtifactSourceV1{}
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}
	producerCalls := 0
	producePortableToolPythonFreshWheelsV1 = func(
		context.Context,
		providerstore.Store,
		pythonprovider.PortableToolPythonProjectionV1,
		[]pythonprovider.PortableToolPythonVerifiedWheelRequestV1,
	) ([]pythonprovider.PortableToolVerifiedWheelInputV1, error) {
		producerCalls++
		return nil, nil
	}

	_, err = acquirePortableToolPythonFreshWheelsV1(
		context.Background(), providerstore.Store{}, &fresh, component, providers.ExecutableEvidence{},
	)
	if err == nil || !strings.Contains(err.Error(), "joins no exact selected artifact source") {
		t.Fatalf("error = %v", err)
	}
	if producerCalls != 0 {
		t.Fatalf("fresh producer ran %d times after an incomplete join", producerCalls)
	}
}

func TestPortableToolPythonFreshPlanRejectsOmittedSelectedBinding(t *testing.T) {
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	fresh.Projection.Components = []pythonprovider.PortableToolPythonComponentV1{}

	err := validatePortableToolPythonFreshPlanV1(&fresh)
	if err == nil || !strings.Contains(err.Error(), "projection does not exactly match selected plan") {
		t.Fatalf("error = %v", err)
	}
}

func TestAcquirePortableToolPythonFreshWheelsUsesSamePathForNeutralBinding(t *testing.T) {
	fresh, records := portableToolPythonFreshNeutralFixtureV1(t)
	component := fresh.Projection.Components[0]
	previousRecords := portableToolPythonFreshLockRecordsV1
	previousProducer := producePortableToolPythonFreshWheelsV1
	t.Cleanup(func() {
		portableToolPythonFreshLockRecordsV1 = previousRecords
		producePortableToolPythonFreshWheelsV1 = previousProducer
	})
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}
	sentinel := []pythonprovider.PortableToolVerifiedWheelInputV1{{
		Scope: component.Bindings[0].Scope, Tool: fresh.Plan.Tools[0].Provenance.Tool,
	}}
	producerCalls := 0
	producePortableToolPythonFreshWheelsV1 = func(
		_ context.Context,
		_ providerstore.Store,
		projection pythonprovider.PortableToolPythonProjectionV1,
		requests []pythonprovider.PortableToolPythonVerifiedWheelRequestV1,
	) ([]pythonprovider.PortableToolVerifiedWheelInputV1, error) {
		producerCalls++
		if !reflect.DeepEqual(projection.Components, []pythonprovider.PortableToolPythonComponentV1{component}) || len(requests) != 1 {
			t.Fatalf("neutral fresh producer inputs = %#v, %#v", projection, requests)
		}
		request := requests[0]
		if request.SelectedPlanEntry.Provenance.Tool != "fixture-tool" ||
			request.Binding.Distribution != "neutral-binding" || request.Binding.CLI.Name != "neutral-cli" ||
			request.ContractRecord.Reference != request.Binding.Contract ||
			request.ArtifactRecord.Reference != request.Binding.Artifact ||
			request.SourceRecord.Reference != records.Artifacts[0].Source.Reference ||
			request.ManifestRecord.Reference != records.Releases[0].Manifest.Reference ||
			request.Acquisition.Artifact != records.Artifacts[0].Descriptor {
			t.Fatalf("neutral fresh request = %#v", request)
		}
		return sentinel, nil
	}

	got, err := acquirePortableToolPythonFreshWheelsV1(
		context.Background(), providerstore.Store{}, &fresh, component, providers.ExecutableEvidence{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if producerCalls != 1 || !reflect.DeepEqual(got, sentinel) {
		t.Fatalf("neutral producer calls = %d, result = %#v", producerCalls, got)
	}
}

func portableToolPythonFreshNeutralFixtureV1(t *testing.T) (
	PortableToolPythonFreshPlanV1,
	toolcatalog.EmbeddedPortableToolLockRecordSetV1,
) {
	t.Helper()
	const (
		scope     = "application:neutral"
		tool      = "fixture-tool"
		namespace = "tool:fixture-tool/releases/1.0.0"
	)
	contract := portabletool.BindingContractV1{
		Schema: portabletool.BindingContractSchemaV1, ID: namespace + "/bindings/python/contract",
		Name: "python", Package: "neutral-binding", Requirements: []string{"neutral-binding==1.0.0"},
		SupportedPython: []string{"3.12"}, SupportedTags: []string{"py3-none-any"},
		BundledComponents: []portabletool.BundledComponentV1{},
		CLI:               portabletool.ToolExportV1{Name: "neutral-cli", Path: "/opt/neutral/bin/neutral-cli"},
	}
	contractRecord := portableToolPythonFreshRecordV1(t, contract)
	contractReference := portableToolPythonFreshReferenceV1(t, contract.ID, contractRecord)
	artifactDigest := portableToolPythonFreshDigestV1(t, "neutral-wheel")
	artifact := portabletool.BindingArtifactRecordV1{
		Schema: portabletool.BindingArtifactSchemaV1, ID: namespace + "/bindings/python/artifacts/linux-amd64",
		Binding: "python", Contract: contractReference, Name: "neutral-binding", EcosystemVersion: "1.0.0",
		Platform: "linux/amd64", Filename: "neutral_binding-1.0.0-py3-none-any.whl",
		Size: "1", SHA256: artifactDigest, Resolver: "https-sha256", Tags: []string{"py3-none-any"},
		RequiresPython: ">=3.12,<3.13", BundledComponents: []portabletool.BundledComponentV1{},
	}
	artifactRecord := portableToolPythonFreshRecordV1(t, artifact)
	artifactReference := portableToolPythonFreshReferenceV1(t, artifact.ID, artifactRecord)
	closureDigest := portableToolPythonFreshDigestV1(t, "neutral-closure")
	manifestDigest := portableToolPythonFreshDigestV1(t, "neutral-manifest")
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: scope, SelectedClosureDigest: closureDigest,
			Provenance: providers.PortableToolReleaseProvenanceV1{
				Tool: tool, Version: "1.0.0", Revision: "1", ManifestDigest: manifestDigest,
			},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{{Reference: contractReference, Record: contractRecord}},
				BindingArtifacts: []providers.PortableToolSelectedRecordV1{{Reference: artifactReference, Record: artifactRecord}},
				Payloads:         []providers.PortableToolSelectedRecordV1{}, NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports:            []providers.PortableToolExportV1{{Name: "neutral-cli", Path: "/opt/neutral/bin/neutral-cli"}},
			ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	closure := toolcatalog.SelectedClosureV1{
		Scope: scope,
		Provenance: toolcatalog.ReleaseProvenanceV1{
			Tool: tool, Version: "1.0.0", Revision: "1", ManifestDigest: manifestDigest,
		},
		Identity: closureDigest,
	}
	descriptor := providerstore.ArtifactDescriptor{
		LogicalPath: "wheels/" + artifact.Filename, Kind: "wheel", Size: "1", SHA256: artifactDigest,
	}
	source := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{
			ID: namespace + "/bindings/python/artifacts/linux-amd64/source", Digest: portableToolPythonFreshDigestV1(t, "neutral-source"),
		},
		Record: providers.CanonicalProviderData{Schema: portabletool.ArtifactSourceRecordSchemaV1, Value: canonical.Object{}},
	}
	manifest := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: namespace + "/revisions/1/manifest", Digest: manifestDigest},
		Record:    providers.CanonicalProviderData{Schema: portabletool.ReleaseManifestSchemaV1, Value: canonical.Object{}},
	}
	records := toolcatalog.EmbeddedPortableToolLockRecordSetV1{
		Releases: []providers.PortableToolReleaseManifestInputV1{{Scope: scope, Tool: tool, Manifest: manifest}},
		Artifacts: []toolcatalog.EmbeddedPortableToolArtifactSourceV1{{
			Scope: scope, Tool: tool, Artifact: artifactReference, Descriptor: descriptor, Source: source,
			Mirrors: []string{"https://example.invalid/neutral-binding.whl"},
		}},
	}
	return PortableToolPythonFreshPlanV1{Plan: plan, Projection: projection, Closures: []toolcatalog.SelectedClosureV1{closure}}, records
}

func portableToolPythonFreshRecordV1(t *testing.T, value any) providers.CanonicalProviderData {
	t.Helper()
	encoded, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object canonical.Object
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	schema, ok := object["schema"].(string)
	if !ok {
		t.Fatal("neutral record schema is missing")
	}
	return providers.CanonicalProviderData{Schema: schema, Value: object}
}

func portableToolPythonFreshReferenceV1(
	t *testing.T,
	id string,
	record providers.CanonicalProviderData,
) providers.PortableToolRecordReferenceV1 {
	t.Helper()
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, record.Value)
	if err != nil {
		t.Fatal(err)
	}
	return providers.PortableToolRecordReferenceV1{ID: id, Digest: digest}
}

func portableToolPythonFreshDigestV1(t *testing.T, seed string) canonical.Digest {
	t.Helper()
	digest, err := canonical.Sum("portable-tool-fresh-test", "portable-tool-fresh-test-v1", seed)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func portableToolPythonFreshPlaywrightFixtureV1(t *testing.T, applicationScope string) PortableToolPythonFreshPlanV1 {
	t.Helper()
	group := toolcatalog.CanonicalRequirementGroupV1{
		Scope: applicationScope, Tool: "playwright", VersionConstraints: []string{"==1.61.0"}, Context: "runtime",
		Binding:    toolcatalog.CanonicalBindingDemandV1{Infer: true},
		Selections: map[string][]string{"browser": {"chromium"}},
	}
	target := toolcatalog.TargetIdentityV1{
		Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12",
		OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt",
	}
	domains := []toolcatalog.ProviderDomainSetV1{{
		Scope: applicationScope, PackageManager: applicationScope + "/packages",
		Filesystem: applicationScope + "/filesystem", Environment: applicationScope + "/environment",
		Exports: applicationScope + "/exports", Capabilities: applicationScope + "/capabilities",
	}}
	envelope := func(schema string) canonical.Envelope {
		return canonical.Envelope{Schema: schema, Value: canonical.Object{"identity": schema}}
	}
	operation := toolcatalog.ResolutionOperationInputsV1{
		Blueprint: envelope("test-blueprint-v1"), Reploy: envelope("test-reploy-v1"),
		ActiveProviders: toolcatalog.ActiveProviderConstraintsV1{
			Schema:  toolcatalog.ActiveProviderConstraintsSchemaV1,
			Sources: []toolcatalog.ActiveProviderConstraintSourceV1{},
		},
	}
	plan, resolution, err := toolcatalog.ResolveEmbeddedPortableToolPlanV1(
		[]toolcatalog.CanonicalRequirementGroupV1{group}, target,
		toolcatalog.ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil, domains, operation,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	return PortableToolPythonFreshPlanV1{
		Plan: plan, Projection: projection,
		Closures: append([]toolcatalog.SelectedClosureV1{}, resolution.Closures...),
	}
}
