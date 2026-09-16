package python

import (
	"bytes"
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func producePortableToolPythonVerifiedWheelsForTestV1(
	ctx context.Context,
	store providerstore.Store,
	requests []PortableToolPythonVerifiedWheelRequestV1,
) ([]PortableToolVerifiedWheelInputV1, error) {
	components := map[string]PortableToolPythonComponentV1{}
	for _, request := range requests {
		components[request.Component.Component] = request.Component
	}
	projection := PortableToolPythonProjectionV1{
		Schema:     PortableToolPythonProjectionSchemaV1,
		Components: make([]PortableToolPythonComponentV1, 0, len(components)),
	}
	for _, component := range components {
		projection.Components = append(projection.Components, component)
	}
	sort.Slice(projection.Components, func(i, j int) bool {
		return projection.Components[i].Component < projection.Components[j].Component
	})
	return ProducePortableToolPythonVerifiedWheelsV1(ctx, store, projection, requests)
}

func TestProducePortableToolPythonVerifiedWheelsV1FreshAndDetached(t *testing.T) {
	request, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	root := t.TempDir()
	store, err := providerstore.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	result, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), store, []PortableToolPythonVerifiedWheelRequestV1{request})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Descriptor != descriptor || result[0].ConsoleScript.Name != "demo" || result[0].Provenance.Outcome != providerstore.AcquisitionOutcomeCacheHit {
		t.Fatalf("verified wheels = %#v", result)
	}
	result[0].Inspection.InternalTags[0] = "changed"
	result[0].Provenance.Attempts = append(result[0].Provenance.Attempts, providerstore.AcquisitionAttempt{Mirror: "changed"})
	result[0].Source.Record.Value["mirrors"].([]interface{})[0] = "changed"
	if request.SourceRecord.Record.Value["mirrors"].([]any)[0] == "changed" {
		t.Fatal("verified source aliases request source record")
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1RejectsDuplicateBeforeAcquisition(t *testing.T) {
	request, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	_, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), providerstore.Store{}, []PortableToolPythonVerifiedWheelRequestV1{request, request})
	if err == nil || !strings.Contains(err.Error(), "duplicates exact artifact acquisition") {
		t.Fatalf("error = %v, want duplicate preflight error", err)
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1RejectsIncompleteProjectionBeforeAcquisition(t *testing.T) {
	first, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	second, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	portableToolVerifiedWheelSetScopeV1(&second, "application:z", "application/z/python")
	projection := PortableToolPythonProjectionV1{
		Schema:     PortableToolPythonProjectionSchemaV1,
		Components: []PortableToolPythonComponentV1{first.Component, second.Component},
	}
	_, err := ProducePortableToolPythonVerifiedWheelsV1(context.Background(), providerstore.Store{}, projection, []PortableToolPythonVerifiedWheelRequestV1{first})
	if err == nil || !strings.Contains(err.Error(), "cover 1 of 2 selected bindings") {
		t.Fatalf("error = %v, want incomplete projection rejection", err)
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1RejectsMixedModesForDistinctArtifactsBeforeAcquisition(t *testing.T) {
	fresh, descriptor, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	replay := portableToolVerifiedWheelDistinctToolReplayRequestV1(t, descriptor)
	for _, request := range []PortableToolPythonVerifiedWheelRequestV1{fresh, replay} {
		if err := preflightPortableToolPythonVerifiedWheelRequestV1(request); err != nil {
			t.Fatalf("fixture is not a valid individual request: %v", err)
		}
	}
	projection := PortableToolPythonProjectionV1{
		Schema:     PortableToolPythonProjectionSchemaV1,
		Components: []PortableToolPythonComponentV1{fresh.Component, replay.Component},
	}
	_, err := ProducePortableToolPythonVerifiedWheelsV1(context.Background(), providerstore.Store{}, projection,
		[]PortableToolPythonVerifiedWheelRequestV1{fresh, replay})
	if err == nil || !strings.Contains(err.Error(), "must use one acquisition mode") {
		t.Fatalf("error = %v, want mixed-mode rejection before acquisition", err)
	}
	if fresh.Binding.Artifact == replay.Binding.Artifact {
		t.Fatal("test requests must have distinct artifact references")
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1FreshSharedArtifactAcquiredOnce(t *testing.T) {
	first, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	second, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	portableToolVerifiedWheelSetScopeV1(&first, "application:z", "application/z/python")
	portableToolVerifiedWheelSetScopeV1(&second, "application:a", "application/a/python")
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	result, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), store, []PortableToolPythonVerifiedWheelRequestV1{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Provenance.OperationID == "" || result[0].Provenance.OperationID != result[1].Provenance.OperationID {
		t.Fatalf("shared artifact provenance = %#v, want one acquisition operation", result)
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1FreshSharedBytesDifferentRevisions(t *testing.T) {
	first, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	second, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	portableToolVerifiedWheelSetScopeV1(&first, "application:a", "application/a/python")
	portableToolVerifiedWheelSetScopeV1(&second, "application:z", "application/z/python")
	portableToolVerifiedWheelSetSecondRevisionSourceV1(t, &second, descriptor)
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	result, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), store, []PortableToolPythonVerifiedWheelRequestV1{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Source.Reference != first.SourceRecord.Reference || result[1].Source.Reference != second.SourceRecord.Reference || result[0].Descriptor != result[1].Descriptor {
		t.Fatalf("cross-revision verified wheels = %#v", result)
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1RejectsConflictingSharedSourceBeforeAcquisition(t *testing.T) {
	first, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	second, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	portableToolVerifiedWheelSetScopeV1(&first, "application:z", "application/z/python")
	portableToolVerifiedWheelSetScopeV1(&second, "application:a", "application/a/python")
	secondSource := second.SourceRecord
	secondSource.Record.Value["mirrors"] = []any{"https://other.invalid/demo.whl"}
	secondSource.Reference.Digest = portableToolVerifiedWheelSourceDigestV1(t, secondSource.Record.Value)
	second.SourceRecord = secondSource
	second.Acquisition.Source.Mirrors = []string{"https://other.invalid/demo.whl"}
	_, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), providerstore.Store{}, []PortableToolPythonVerifiedWheelRequestV1{first, second})
	if err == nil || !strings.Contains(err.Error(), "release manifest must authorize") {
		t.Fatalf("error = %v, want pre-acquisition source authorization rejection", err)
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1RejectsSharedPolicyConflict(t *testing.T) {
	first, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	second, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	portableToolVerifiedWheelSetScopeV1(&first, "application:z", "application/z/python")
	portableToolVerifiedWheelSetScopeV1(&second, "application:a", "application/a/python")
	second.Acquisition.Policy = providerstore.DefaultAcquisitionPolicy()
	_, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), providerstore.Store{}, []PortableToolPythonVerifiedWheelRequestV1{first, second})
	if err == nil || !strings.Contains(err.Error(), "acquisition policy or operation ID differs") {
		t.Fatalf("error = %v, want pre-acquisition policy conflict", err)
	}
}

// The repository does not store the catalog-pinned Playwright wheel because
// it is large. When supplied by the caller, run it through the complete
// acquisition, inspection, and binding-verification orchestration.
func TestProducePortableToolPythonVerifiedWheelsV1SelectedPlaywrightWheel(t *testing.T) {
	wheelPath := os.Getenv("REPLOY_TEST_PLAYWRIGHT_WHEEL")
	if wheelPath == "" {
		t.Skip("REPLOY_TEST_PLAYWRIGHT_WHEEL is not set")
	}
	contractRecord := portableToolProjectionDefinitionRecordForTest(t,
		"../../toolcatalog/definitions/playwright/releases/1.61.0/bindings/python/contract.json")
	artifactRecord := portableToolProjectionDefinitionRecordForTest(t,
		"../../toolcatalog/definitions/playwright/releases/1.61.0/bindings/python/linux-amd64.json")
	manifestRecord := portableToolProjectionDefinitionRecordForTest(t,
		"../../toolcatalog/definitions/playwright/releases/1.61.0/revisions/1/manifest.json")
	sourceRecordData := portableToolProjectionDefinitionRecordForTest(t,
		"../../toolcatalog/definitions/playwright/releases/1.61.0/revisions/1/sources/python-linux-amd64.json")
	contractReference := portableToolProjectionReferenceForTest(t, contractRecord.Value["id"].(string), contractRecord)
	artifactReference := portableToolProjectionReferenceForTest(t, artifactRecord.Value["id"].(string), artifactRecord)
	manifestReference := portableToolProjectionReferenceForTest(t, manifestRecord.Value["id"].(string), manifestRecord)
	sourceReference := portableToolProjectionReferenceForTest(t, sourceRecordData.Value["id"].(string), sourceRecordData)
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: "application:browser", SelectedClosureDigest: portableToolProjectionTestDigest,
			Provenance: providers.PortableToolReleaseProvenanceV1{
				Tool: "playwright", Version: "1.61.0", Revision: "1", ManifestDigest: manifestReference.Digest,
			},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{{Reference: contractReference, Record: contractRecord}},
				BindingArtifacts: []providers.PortableToolSelectedRecordV1{{Reference: artifactReference, Record: artifactRecord}},
				Payloads:         []providers.PortableToolSelectedRecordV1{}, NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports:            []providers.PortableToolExportV1{{Name: "playwright", Path: "/opt/reploy/tools/playwright/bin/playwright"}},
			ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	component, binding := projection.Components[0], projection.Components[0].Bindings[0]
	descriptor := providerstore.ArtifactDescriptor{LogicalPath: "wheels/" + binding.Wheel.Filename, Kind: "wheel", Size: binding.Wheel.Size, SHA256: binding.Wheel.SHA256}
	facts := portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp312")
	facts.Version = "3.12.2"
	facts.CompatibleTags = append([]string{}, binding.SupportedTags...)
	sourceRecord := providers.PortableToolSelectedRecordV1{Reference: sourceReference, Record: sourceRecordData}
	manifest := providers.PortableToolSelectedRecordV1{Reference: manifestReference, Record: manifestRecord}
	mirror := sourceRecordData.Value["mirrors"].([]any)[0].(string)
	request := PortableToolPythonVerifiedWheelRequestV1{
		SelectedPlanEntry: plan.Tools[0], Component: component, Binding: binding,
		ContractRecord: plan.Tools[0].Responsibilities.BindingContracts[0], ArtifactRecord: plan.Tools[0].Responsibilities.BindingArtifacts[0],
		SourceRecord: sourceRecord, ManifestRecord: manifest, Interpreter: providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)},
		Acquisition: providerstore.AcquisitionRequest{Artifact: descriptor, Source: providerstore.ArtifactSource{ID: sourceRecord.Reference.ID, SHA256: descriptor.SHA256, Mirrors: []string{mirror}}},
		Mode:        PortableToolVerifiedWheelFreshV1,
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wheel, err := os.Open(wheelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wheel.Close()
	if _, err := store.PublishExpected(context.Background(), descriptor, wheel); err != nil {
		t.Fatal(err)
	}
	result, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), store, []PortableToolPythonVerifiedWheelRequestV1{request})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Inspection.Distribution != "playwright" || result[0].ConsoleScript.Name != "playwright" || result[0].ConsoleScript.Target != "playwright.__main__:main" {
		t.Fatalf("Playwright verified wheel = %#v", result)
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1SortsSharedArtifactPerScope(t *testing.T) {
	first, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelLockedReplayV1)
	second, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelLockedReplayV1)
	first.SelectedPlanEntry.Scope = "application:z"
	first.Binding.Scope = first.SelectedPlanEntry.Scope
	first.Component.Component = "application/z/python"
	first.Binding.Component = first.Component.Component
	first.Component.Bindings[0].Component = first.Component.Component
	first.Component.Bindings[0].Scope = first.SelectedPlanEntry.Scope
	first.LockedAcquisition.Scope = first.SelectedPlanEntry.Scope
	first.SourceRecord = providers.PortableToolSelectedRecordV1{}
	second.SelectedPlanEntry.Scope = "application:a"
	second.Binding.Scope = second.SelectedPlanEntry.Scope
	second.Component.Component = "application/a/python"
	second.Binding.Component = second.Component.Component
	second.Component.Bindings[0].Component = second.Component.Component
	second.Component.Bindings[0].Scope = second.SelectedPlanEntry.Scope
	second.LockedAcquisition.Scope = second.SelectedPlanEntry.Scope
	second.SourceRecord = providers.PortableToolSelectedRecordV1{}
	root := t.TempDir()
	store, err := providerstore.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	result, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), store, []PortableToolPythonVerifiedWheelRequestV1{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Scope != "application:a" || result[1].Scope != "application:z" {
		t.Fatalf("sorted verified wheels = %#v", result)
	}
	result[0].Source.Record.Value["mirrors"].([]any)[0] = "changed"
	if result[1].Source.Record.Value["mirrors"].([]any)[0] == "changed" {
		t.Fatal("shared replay outputs alias mutable source records")
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1LockedReplayUsesStoreOnly(t *testing.T) {
	request, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelLockedReplayV1)
	root := t.TempDir()
	store, err := providerstore.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	result, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), store, []PortableToolPythonVerifiedWheelRequestV1{request})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Provenance.OperationID != "locked-replay" || result[0].Provenance.Outcome != providerstore.AcquisitionOutcomeCacheHit {
		t.Fatalf("locked replay result = %#v", result)
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1LockedReplaySharedBytesDifferentOutcomes(t *testing.T) {
	first, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelLockedReplayV1)
	second, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelLockedReplayV1)
	portableToolVerifiedWheelSetScopeV1(&first, "application:a", "application/a/python")
	portableToolVerifiedWheelSetScopeV1(&second, "application:z", "application/z/python")
	portableToolVerifiedWheelSetSecondRevisionSourceV1(t, &second, descriptor)
	first.LockedAcquisition.Outcome.Kind = providerstore.AcquisitionOutcomeNetwork
	first.LockedAcquisition.Outcome.SuccessfulDeclaredLocator = first.Acquisition.Source.Mirrors[0]
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	result, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), store, []PortableToolPythonVerifiedWheelRequestV1{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Provenance.Outcome != providerstore.AcquisitionOutcomeNetwork || result[1].Provenance.Outcome != providerstore.AcquisitionOutcomeCacheHit || result[0].Source.Reference != first.SourceRecord.Reference || result[1].Source.Reference != second.SourceRecord.Reference {
		t.Fatalf("cross-revision replay outcomes = %#v", result)
	}
}

func TestProducePortableToolPythonVerifiedWheelsV1LockedReplayFailsMissingOrCorruptStore(t *testing.T) {
	request, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelLockedReplayV1)
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, store providerstore.Store)
		want    string
	}{
		{name: "missing", want: "open verified store artifact"},
		{name: "corrupt", prepare: func(t *testing.T, store providerstore.Store) {
			if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
				t.Fatal(err)
			}
			blob, err := store.BlobPath(descriptor.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(blob, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(blob, []byte("corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "open provider artifact"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := providerstore.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if test.prepare != nil {
				test.prepare(t, store)
			}
			if _, err := producePortableToolPythonVerifiedWheelsForTestV1(context.Background(), store, []PortableToolPythonVerifiedWheelRequestV1{request}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func portableToolVerifiedWheelRequestFixtureV1(t *testing.T, mode PortableToolVerifiedWheelModeV1) (PortableToolPythonVerifiedWheelRequestV1, providerstore.ArtifactDescriptor, []byte) {
	t.Helper()
	verification, descriptor := portableToolPythonBindingVerificationFixtureV1(t, "")
	input := PortableToolPythonVerifiedWheelRequestV1{
		SelectedPlanEntry: verification.SelectedPlanEntry,
		Component:         verification.Component,
		Binding:           verification.Binding,
		ContractRecord:    verification.ContractRecord,
		ArtifactRecord:    verification.ArtifactRecord,
		Interpreter:       verification.Interpreter,
	}
	content := testInspectionWheel(t, descriptor.LogicalPath[len("wheels/"):], "demo", "1.0.0",
		"Name: demo\nVersion: 1.0.0\nRequires-Python: >=3.12,<3.13\n",
		"Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-manylinux1_x86_64\n",
		"[console_scripts]\ndemo = demo.cli:main\n")
	sourceValue := canonical.Object{
		"schema":      portabletool.ArtifactSourceRecordSchemaV1,
		"id":          "tool:one/releases/1.0.0/revisions/1/sources/demo",
		"sha256":      descriptor.SHA256,
		"mirrors":     []any{"https://example.invalid/demo.whl"},
		"provenance":  []any{"https://example.invalid/source-record"},
		"diagnostics": []any{},
	}
	sourceDigest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, sourceValue)
	if err != nil {
		t.Fatal(err)
	}
	input.SourceRecord = providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: sourceValue["id"].(string), Digest: sourceDigest},
		Record:    providers.CanonicalProviderData{Schema: portabletool.ArtifactSourceRecordSchemaV1, Value: sourceValue},
	}
	input.ManifestRecord = portableToolVerifiedWheelManifestRecordV1(t, input.SelectedPlanEntry, input.ArtifactRecord, input.SourceRecord, descriptor)
	input.SelectedPlanEntry.Provenance.ManifestDigest = input.ManifestRecord.Reference.Digest
	input.Acquisition = providerstore.AcquisitionRequest{
		Artifact: descriptor,
		Source: providerstore.ArtifactSource{
			ID: sourceValue["id"].(string), SHA256: descriptor.SHA256,
			Mirrors: []string{"https://example.invalid/demo.whl"},
		},
	}
	input.Mode = mode
	if mode == PortableToolVerifiedWheelLockedReplayV1 {
		input.LockedAcquisition = &providers.PortableToolArtifactAcquisitionLockV1{
			Scope: input.Binding.Scope, Tool: input.SelectedPlanEntry.Provenance.Tool,
			Artifact: input.Binding.Artifact, Descriptor: descriptor, Source: input.SourceRecord,
			Outcome: providers.PortableToolAcquisitionOutcomeLockV1{
				Kind: providerstore.AcquisitionOutcomeCacheHit, RedirectHops: "0",
				HistoricalLocators: []string{"https://example.invalid/source-record"},
			},
		}
	}
	return input, descriptor, content
}

func portableToolVerifiedWheelDistinctToolReplayRequestV1(t *testing.T, descriptor providerstore.ArtifactDescriptor) PortableToolPythonVerifiedWheelRequestV1 {
	t.Helper()
	plan := portableToolPythonPlanForTest(t, "application:z", "two", "demo", "demo==1.0.0")
	artifact := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	artifact.Record.Value["size"] = descriptor.Size
	artifact.Record.Value["sha256"] = descriptor.SHA256
	portableToolProjectionRebindRecordForTest(t, artifact)
	_, projection, err := ProjectPortableToolPythonBindingsV1(plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	component := projection.Components[0]
	binding := component.Bindings[0]
	source := portableToolVerifiedWheelSourceRecordV1(t, descriptor,
		"tool:two/releases/1.0.0/revisions/1/sources/demo", "https://example.invalid/demo.whl")
	manifest := portableToolVerifiedWheelManifestRecordV1(t, plan.Tools[0], *artifact, source, descriptor)
	plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	facts := portablePythonEligibilityFactsForTest(component.TestedTags, "cpython", "cp312")
	facts.Version = "3.12.2"
	facts.CompatibleTags = append([]string{}, binding.SupportedTags...)
	return PortableToolPythonVerifiedWheelRequestV1{
		SelectedPlanEntry: plan.Tools[0], Component: component, Binding: binding,
		ContractRecord: plan.Tools[0].Responsibilities.BindingContracts[0], ArtifactRecord: *artifact,
		SourceRecord: source, ManifestRecord: manifest,
		Interpreter: providers.ExecutableEvidence{Facts: CanonicalInterpreterFactsV2(facts)},
		Acquisition: providerstore.AcquisitionRequest{Artifact: descriptor, Source: providerstore.ArtifactSource{
			ID: source.Reference.ID, SHA256: descriptor.SHA256, Mirrors: []string{"https://example.invalid/demo.whl"},
		}},
		Mode: PortableToolVerifiedWheelLockedReplayV1,
		LockedAcquisition: &providers.PortableToolArtifactAcquisitionLockV1{
			Scope: binding.Scope, Tool: plan.Tools[0].Provenance.Tool,
			Artifact: binding.Artifact, Descriptor: descriptor, Source: source,
			Outcome: providers.PortableToolAcquisitionOutcomeLockV1{
				Kind: providerstore.AcquisitionOutcomeCacheHit, RedirectHops: "0",
				HistoricalLocators: []string{"https://example.invalid/source-record"},
			},
		},
	}
}

func portableToolVerifiedWheelSetScopeV1(request *PortableToolPythonVerifiedWheelRequestV1, scope, component string) {
	request.SelectedPlanEntry.Scope = scope
	request.Binding.Scope = scope
	request.Binding.Component = component
	request.Component.Component = component
	request.Component.Bindings[0].Scope = scope
	request.Component.Bindings[0].Component = component
	if request.LockedAcquisition != nil {
		request.LockedAcquisition.Scope = scope
	}
}

func portableToolVerifiedWheelSetSecondRevisionSourceV1(t *testing.T, request *PortableToolPythonVerifiedWheelRequestV1, descriptor providerstore.ArtifactDescriptor) {
	t.Helper()
	request.SelectedPlanEntry.Provenance.Revision = "2"
	source := portableToolVerifiedWheelSourceRecordV1(t, descriptor,
		"tool:one/releases/1.0.0/revisions/2/sources/demo", "https://other.invalid/demo.whl")
	request.SourceRecord = source
	request.Acquisition.Source = providerstore.ArtifactSource{
		ID: source.Reference.ID, SHA256: descriptor.SHA256, Mirrors: []string{"https://other.invalid/demo.whl"},
	}
	request.ManifestRecord = portableToolVerifiedWheelManifestRecordV1(t, request.SelectedPlanEntry, request.ArtifactRecord, source, descriptor)
	request.SelectedPlanEntry.Provenance.ManifestDigest = request.ManifestRecord.Reference.Digest
	if request.LockedAcquisition != nil {
		request.LockedAcquisition.Source = source
	}
}

func portableToolVerifiedWheelSourceDigestV1(t *testing.T, value canonical.Object) canonical.Digest {
	t.Helper()
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func portableToolVerifiedWheelSourceRecordV1(t *testing.T, descriptor providerstore.ArtifactDescriptor, id, mirror string) providers.PortableToolSelectedRecordV1 {
	t.Helper()
	value := canonical.Object{
		"schema":      portabletool.ArtifactSourceRecordSchemaV1,
		"id":          id,
		"sha256":      descriptor.SHA256,
		"mirrors":     []any{mirror},
		"provenance":  []any{"https://example.invalid/source-record"},
		"diagnostics": []any{},
	}
	return providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: id, Digest: portableToolVerifiedWheelSourceDigestV1(t, value)},
		Record:    providers.CanonicalProviderData{Schema: portabletool.ArtifactSourceRecordSchemaV1, Value: value},
	}
}

func portableToolVerifiedWheelManifestRecordV1(
	t *testing.T,
	entry providers.PortableToolPlanEntryV1,
	artifact, source providers.PortableToolSelectedRecordV1,
	descriptor providerstore.ArtifactDescriptor,
) providers.PortableToolSelectedRecordV1 {
	t.Helper()
	provenance := entry.Provenance
	namespace := "tool:" + provenance.Tool + "/releases/" + provenance.Version
	id := namespace + "/revisions/" + provenance.Revision + "/manifest"
	ref := func(id string, digest canonical.Digest) canonical.Object {
		return canonical.Object{"id": id, "digest": string(digest)}
	}
	value := canonical.Object{
		"schema": portabletool.ReleaseManifestSchemaV1, "id": id,
		"tool": provenance.Tool, "version": provenance.Version, "revision": provenance.Revision,
		"aliases": []any{}, "provenance": []any{},
		"validation_profiles": []any{ref(namespace+"/validation/profiles/default", portableToolProjectionTestDigest)},
		"contract":            ref(namespace+"/contract", portableToolProjectionTestDigest),
		"targets":             []any{ref(namespace+"/targets/debian/12/amd64", portableToolProjectionTestDigest)},
		"artifact_sources": []any{canonical.Object{
			"artifact":        ref(artifact.Reference.ID, artifact.Reference.Digest),
			"artifact_sha256": descriptor.SHA256,
			"source":          ref(source.Reference.ID, source.Reference.Digest),
		}},
	}
	return providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: id, Digest: portableToolVerifiedWheelSourceDigestV1(t, value)},
		Record:    providers.CanonicalProviderData{Schema: portabletool.ReleaseManifestSchemaV1, Value: value},
	}
}
