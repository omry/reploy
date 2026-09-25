package dockerdeploy

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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

func TestAcquirePortableToolPythonFreshWheelsBuildsSelectedHandoffs(t *testing.T) {
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	component := portableToolPythonFreshComponentForTestV1(t, &fresh)
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(fresh.Closures)
	if err != nil {
		t.Fatal(err)
	}
	interpreter := providers.ExecutableEvidence{InvocationPath: "/usr/bin/python3", Facts: pythonInterpreterFactsV2ForTest("3.12.2")}
	selection, handoffs, err := preparePortableToolPythonFreshWheelsV1(&fresh, component, interpreter)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := selection.Projection()
	if err != nil || len(projection.Components) != 1 || len(handoffs) != 1 {
		t.Fatalf("fresh handoff selection = %#v, handoffs = %d, error = %v", projection, len(handoffs), err)
	}
	entry := fresh.Plan.Tools[0]
	binding := component.Bindings[0]
	selected, err := selection.Binding(component.Component, binding.Distribution)
	if err != nil {
		t.Fatal(err)
	}
	selectedEntry, err := selected.PlanEntry()
	if err != nil || !portableToolPythonFreshCanonicalEqualV1(t, selectedEntry, entry) {
		t.Fatalf("selected plan entry = %#v, error = %v", selectedEntry, err)
	}
	var selectedSource *toolcatalog.EmbeddedPortableToolArtifactSourceV1
	for index := range records.Artifacts {
		if records.Artifacts[index].Artifact == binding.Artifact {
			selectedSource = &records.Artifacts[index]
			break
		}
	}
	if selectedSource == nil {
		t.Fatal("selected artifact source is absent")
	}
	if err := handoffs[0].MatchEmbeddedSource(selectedSource.Descriptor, selectedSource.Mirrors); err != nil {
		t.Fatalf("selected source mismatch: %v", err)
	}
	wrong := *selectedSource
	wrong.Mirrors = []string{"https://example.invalid/substituted.whl"}
	if err := handoffs[0].MatchEmbeddedSource(wrong.Descriptor, wrong.Mirrors); err == nil {
		t.Fatal("substituted source accepted")
	}
}

func TestAcquirePortableToolPythonFreshWheelsRejectsIncompleteJoinBeforeProducer(t *testing.T) {
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	component := portableToolPythonFreshComponentForTestV1(t, &fresh)
	previousRecords := portableToolPythonFreshLockRecordsV1
	t.Cleanup(func() {
		portableToolPythonFreshLockRecordsV1 = previousRecords
	})
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(fresh.Closures)
	if err != nil {
		t.Fatal(err)
	}
	records.Artifacts = []toolcatalog.EmbeddedPortableToolArtifactSourceV1{}
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}
	_, _, err = preparePortableToolPythonFreshWheelsV1(&fresh, component, providers.ExecutableEvidence{})
	if err == nil || !strings.Contains(err.Error(), "joins no exact selected artifact source") {
		t.Fatalf("error = %v", err)
	}
}

func TestPortableToolPythonFreshPlanRejectsOmittedSelectedBinding(t *testing.T) {
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	component := portableToolPythonFreshComponentForTestV1(t, &fresh)
	component.Bindings = []pythonprovider.PortableToolPythonBindingV1{}
	_, _, err := preparePortableToolPythonFreshWheelsV1(&fresh, component,
		providers.ExecutableEvidence{Facts: pythonInterpreterFactsV2ForTest("3.12.2")})
	if err == nil || !strings.Contains(err.Error(), "must contain at least one binding") {
		t.Fatalf("error = %v", err)
	}
}

func TestPortableToolPythonFreshPlanDetachesAfterValidation(t *testing.T) {
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	if err := validatePortableToolPythonFreshPlanV1(&fresh); err != nil {
		t.Fatal(err)
	}
	projection, err := fresh.sealed.selection.Projection()
	if err != nil {
		t.Fatal(err)
	}
	component := projection.Components[0]
	fresh.Plan.Tools[0].Scope = "application:substituted"
	fresh.Closures[0].Identity = portableToolPythonFreshDigestV1(t, "substituted")
	_, handoffs, err := preparePortableToolPythonFreshWheelsV1(&fresh, component,
		providers.ExecutableEvidence{Facts: pythonInterpreterFactsV2ForTest("3.12.2")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(handoffs) != 1 {
		t.Fatalf("fresh handoffs = %d", len(handoffs))
	}
	selected, err := fresh.sealed.selection.Binding(component.Component, component.Bindings[0].Distribution)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := selected.PlanEntry()
	if err != nil || entry.Scope != component.Bindings[0].Scope {
		t.Fatalf("fresh handoff aliased caller plan: %#v, error = %v", entry, err)
	}
}

func TestAcquirePortableToolPythonFreshHandoffsVerifiesLocalNeutralWheel(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := preparedPortableNeutralBindingWheelV1(t)
	wheel, err := store.Publish(context.Background(), "wheels/neutral_binding-1.0.0-py3-none-any.whl", "wheel", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	fresh, records := portableToolPythonFreshNeutralFixtureV1(t)
	rebindPortableToolPythonFreshNeutralWheelV1(t, &fresh, &records, wheel)
	previousRecords := portableToolPythonFreshLockRecordsV1
	t.Cleanup(func() { portableToolPythonFreshLockRecordsV1 = previousRecords })
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}
	interpreter := providers.ExecutableEvidence{Facts: pythonprovider.CanonicalInterpreterFactsV2(pythonprovider.InterpreterInspectionFactsV2{
		Version: "3.12.2", Implementation: "cpython", ABI: "cp312",
		Libc: "glibc", LibcMajor: "2", LibcMinor: "35",
		TestedTags: []string{"py3-none-any"}, CompatibleTags: []string{"py3-none-any"},
	})}
	handoffs, err := acquirePortableToolPythonFreshHandoffsV1(context.Background(), store, &fresh, portableToolPythonFreshComponentForTestV1(t, &fresh), interpreter)
	if err != nil {
		t.Fatal(err)
	}
	if len(handoffs) != 1 {
		t.Fatalf("verified handoffs = %d", len(handoffs))
	}
	input, err := handoffs[0].MaterializationInput()
	if err != nil || input.Descriptor != wheel || input.Inspection.Distribution != "neutral-binding" {
		t.Fatalf("materialization input = %#v, error = %v", input, err)
	}
}

func TestPreparePortableToolPythonFreshWheelsRejectsSubstitutedEmbeddedMirror(t *testing.T) {
	fresh, records := portableToolPythonFreshNeutralFixtureV1(t)
	records.Artifacts[0].Mirrors = []string{"https://example.invalid/substituted.whl"}
	previousRecords := portableToolPythonFreshLockRecordsV1
	t.Cleanup(func() { portableToolPythonFreshLockRecordsV1 = previousRecords })
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}
	_, _, err := preparePortableToolPythonFreshWheelsV1(&fresh, portableToolPythonFreshComponentForTestV1(t, &fresh),
		providers.ExecutableEvidence{Facts: pythonInterpreterFactsV2ForTest("3.12.2")})
	if err == nil || !strings.Contains(err.Error(), "embedded source differs from authenticated source record") {
		t.Fatalf("substituted embedded mirror error = %v", err)
	}
}

func TestAcquirePortableToolPythonFreshWheelsUsesSamePathForNeutralBinding(t *testing.T) {
	fresh, records := portableToolPythonFreshNeutralFixtureV1(t)
	component := portableToolPythonFreshComponentForTestV1(t, &fresh)
	previousRecords := portableToolPythonFreshLockRecordsV1
	t.Cleanup(func() {
		portableToolPythonFreshLockRecordsV1 = previousRecords
	})
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}
	selection, handoffs, err := preparePortableToolPythonFreshWheelsV1(
		&fresh, component, providers.ExecutableEvidence{Facts: pythonInterpreterFactsV2ForTest("3.12.2")},
	)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := selection.Projection()
	if err != nil || len(projection.Components) != 1 || len(handoffs) != 1 {
		t.Fatalf("neutral selection = %#v, handoffs = %d, error = %v", projection, len(handoffs), err)
	}
	if err := handoffs[0].MatchEmbeddedSource(records.Artifacts[0].Descriptor, records.Artifacts[0].Mirrors); err != nil {
		t.Fatalf("neutral selected source mismatch: %v", err)
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
	sourceID := namespace + "/revisions/1/sources/neutral-binding"
	sourceRecord := portableToolPythonFreshRecordV1(t, portabletool.ArtifactSourceRecordV1{
		Schema: portabletool.ArtifactSourceRecordSchemaV1, ID: sourceID,
		SHA256: descriptor.SHA256, Mirrors: []string{"https://example.invalid/neutral-binding.whl"},
		Provenance: []string{"https://example.invalid/source-record"}, Diagnostics: []string{},
	})
	source := providers.PortableToolSelectedRecordV1{
		Reference: portableToolPythonFreshReferenceV1(t, sourceID, sourceRecord), Record: sourceRecord,
	}
	manifestID := namespace + "/revisions/1/manifest"
	ref := func(id string, digest canonical.Digest) canonical.Object {
		return canonical.Object{"id": id, "digest": string(digest)}
	}
	manifestValue := canonical.Object{
		"schema": portabletool.ReleaseManifestSchemaV1, "id": manifestID,
		"tool": tool, "version": "1.0.0", "revision": "1",
		"aliases": []any{}, "provenance": []any{},
		"validation_profiles": []any{ref(namespace+"/validation/profiles/default", manifestDigest)},
		"contract":            ref(namespace+"/contract", manifestDigest),
		"targets":             []any{ref(namespace+"/targets/debian/12/amd64", manifestDigest)},
		"artifact_sources": []any{canonical.Object{
			"artifact":        ref(artifactReference.ID, artifactReference.Digest),
			"artifact_sha256": descriptor.SHA256,
			"source":          ref(source.Reference.ID, source.Reference.Digest),
		}},
	}
	manifestRecord := providers.CanonicalProviderData{Schema: portabletool.ReleaseManifestSchemaV1, Value: manifestValue}
	manifest := providers.PortableToolSelectedRecordV1{
		Reference: portableToolPythonFreshReferenceV1(t, manifestID, manifestRecord), Record: manifestRecord,
	}
	plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	closure.Provenance.ManifestDigest = manifest.Reference.Digest
	records := toolcatalog.EmbeddedPortableToolLockRecordSetV1{
		Releases: []providers.PortableToolReleaseManifestInputV1{{Scope: scope, Tool: tool, Manifest: manifest}},
		Artifacts: []toolcatalog.EmbeddedPortableToolArtifactSourceV1{{
			Scope: scope, Tool: tool, Artifact: artifactReference, Descriptor: descriptor, Source: source,
			Mirrors: []string{"https://example.invalid/neutral-binding.whl"},
		}},
	}
	return PortableToolPythonFreshPlanV1{Plan: plan, Closures: []toolcatalog.SelectedClosureV1{closure}}, records
}

func rebindPortableToolPythonFreshNeutralWheelV1(
	t *testing.T,
	fresh *PortableToolPythonFreshPlanV1,
	records *toolcatalog.EmbeddedPortableToolLockRecordSetV1,
	wheel providerstore.ArtifactDescriptor,
) {
	t.Helper()
	artifact := &fresh.Plan.Tools[0].Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["size"] = wheel.Size
	artifact.Record.Value["sha256"] = string(wheel.SHA256)
	artifact.Reference = portableToolPythonFreshReferenceV1(t, artifact.Reference.ID, artifact.Record)
	source := &records.Artifacts[0]
	source.Artifact = artifact.Reference
	source.Descriptor = wheel
	source.Source.Record.Value["sha256"] = string(wheel.SHA256)
	source.Source.Reference = portableToolPythonFreshReferenceV1(t, source.Source.Reference.ID, source.Source.Record)
	manifest := &records.Releases[0].Manifest
	mapping := manifest.Record.Value["artifact_sources"].([]any)[0].(canonical.Object)
	mapping["artifact"] = canonical.Object{"id": artifact.Reference.ID, "digest": string(artifact.Reference.Digest)}
	mapping["artifact_sha256"] = string(wheel.SHA256)
	mapping["source"] = canonical.Object{"id": source.Source.Reference.ID, "digest": string(source.Source.Reference.Digest)}
	manifest.Reference = portableToolPythonFreshReferenceV1(t, manifest.Reference.ID, manifest.Record)
	fresh.Plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	fresh.Closures[0].Provenance.ManifestDigest = manifest.Reference.Digest
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

func portableToolPythonFreshCanonicalEqualV1(t *testing.T, left, right any) bool {
	t.Helper()
	leftBytes, err := canonical.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := canonical.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(leftBytes, rightBytes)
}

func portableToolPythonFreshComponentForTestV1(t *testing.T, fresh *PortableToolPythonFreshPlanV1) pythonprovider.PortableToolPythonComponentV1 {
	t.Helper()
	selection, err := pythonprovider.NewPortableToolPythonSelectionV1(fresh.Plan)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := selection.Projection()
	if err != nil || len(projection.Components) != 1 {
		t.Fatalf("fresh selected projection = %#v, error = %v", projection, err)
	}
	return projection.Components[0]
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
	return portableToolPythonFreshPlaywrightFixtureForTargetV1(t, applicationScope, toolcatalog.TargetIdentityV1{
		Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12",
		OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt",
	})
}

func portableToolPythonFreshPlaywrightFixtureForTargetV1(t *testing.T, applicationScope string, target toolcatalog.TargetIdentityV1) PortableToolPythonFreshPlanV1 {
	t.Helper()
	group := toolcatalog.CanonicalRequirementGroupV1{
		Scope: applicationScope, Tool: "playwright", VersionConstraints: []string{"==1.61.0"}, Context: "runtime",
		Binding:    toolcatalog.CanonicalBindingDemandV1{Infer: true},
		Selections: map[string][]string{"browser": {"chromium"}},
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
	return PortableToolPythonFreshPlanV1{
		Plan:     plan,
		Closures: append([]toolcatalog.SelectedClosureV1{}, resolution.Closures...),
	}
}
