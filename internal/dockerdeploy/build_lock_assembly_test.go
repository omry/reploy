package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestAssembleBuildLockPublishesCompleteGraphLock(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	lockedNode := fixture.lock.Nodes[0]
	bundle, err := providers.LoadResolvedBundleManifest(fixture.store, lockedNode.BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	baseImage, err := realizedImageFromDescriptor(fixture.lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	overlay := deploy.EmptyRequestOverlayV1()
	overlayDigest, err := deploy.RequestOverlayDigestV1(overlay)
	if err != nil {
		t.Fatal(err)
	}
	request := providers.ResolvedRequestV1{
		Schema:        providers.ResolvedRequestSchemaV1,
		OverlayDigest: overlayDigest, Platform: fixture.request.Platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: fixture.request.Plan.Nodes[1].Components[0], Provider: blueprint.ComponentTypePython, Request: fixture.request.Plan.Nodes[1].Request},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: fixture.request.Plan.Nodes[0].Request},
		},
		Sources: fixture.request.SourceCandidates,
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(fixture.lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	finalEvidence := lockedNode.ValidationEvidence
	finalEvidence.SubjectRootFS = fixture.lock.RuntimeLayer.Result.RootFSSubject
	validationReference, err := deploy.PublishPrefixValidation(context.Background(), fixture.store, deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: fixture.lock.RuntimeLayer.Result.RootFSSubject,
		Profiles: []providers.ValidationEvidence{finalEvidence}, RuntimePolicy: policyDigest,
		ExposedOutputs: []providers.ExecutableEvidence{},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := providers.GraphExecutionResult{
		Plan: fixture.request.Plan, SelectedEdges: fixture.request.Plan.Edges,
		Bundles: []providers.ResolvedBundle{bundle}, Profiles: []providers.RequirementProfile{lockedNode.RequirementProfile},
		ValidationEvidence: []providers.ValidationEvidence{lockedNode.ValidationEvidence},
		PrefixImages:       []providers.RealizedImageV1{baseImage, lockedNode.Result},
		Materializations: []providers.GraphNodeMaterializeResult{{
			Image: lockedNode.Result, TransactionDigest: lockedNode.TransactionDigest,
			GeneratedExecutables: lockedNode.GeneratedExecutables, Outputs: lockedNode.Outputs,
		}},
		Catalog: append([]providers.RealizedOutput{}, fixture.request.EarlierCatalog...),
	}
	base := fixture.lock.Base
	base.AuthorReference = "debian:bookworm-slim"
	portableTools := buildLockAssemblyPortableToolsV1(t, fixture.store, graph.Plan, fixture.request.NodeID)
	lock, err := AssembleBuildLock(context.Background(), fixture.store, BuildLockAssemblyInput{
		BlueprintDigest: rendererDigest("b"), ResolvedRequest: request, Overlay: overlay,
		PackageOverrides: fixture.lock.PackageOverrides, Base: base, Graph: graph,
		PortableTools: &portableTools,
		RuntimePolicy: fixture.lock.RuntimePolicy, RuntimeLayer: fixture.lock.RuntimeLayer,
		ValidationRecord: validationReference, FinalImage: fixture.lock.FinalImage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lock.BlueprintDigest != rendererDigest("b") || len(lock.Nodes) != 1 || lock.Nodes[0].NodeID != fixture.request.NodeID || lock.Nodes[0].ResolverCacheKey == "" || lock.ResolvedRequestDigest == "" || !reflect.DeepEqual(lock.Catalog, graph.Catalog) {
		t.Fatalf("assembled lock = %#v", lock)
	}
	portableTools.Acquisitions[0].Outcome.HistoricalLocators[0] = "https://mutated.example/demo.tar"
	if lock.PortableTools == nil || lock.PortableTools.Acquisitions[0].Outcome.HistoricalLocators[0] != "https://upstream.example/demo.tar" {
		t.Fatal("assembled build lock did not retain an ownership-independent portable tool lock")
	}
	if _, err := providers.LoadResolvedBundleManifest(fixture.store, lock.Nodes[0].BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal(err)
	}
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
}

func buildLockAssemblyPortableToolsV1(
	t *testing.T,
	store providerstore.Store,
	providerPlan providers.ProviderPlanV1,
	owner providers.NodeID,
) providers.PortableToolLockV1 {
	t.Helper()
	descriptor, err := store.Publish(context.Background(), "portable/demo.tar", "jdk-archive", strings.NewReader("portable"))
	if err != nil {
		t.Fatal(err)
	}
	recordValue := canonical.Object{
		"schema": "portable-tool-payload-v1", "id": "tool:demo/releases/1.0.0/payloads/demo-linux-amd64",
		"name": "demo", "revision": "1", "upstream_version": "1.0.0", "platform": "linux/amd64",
		"logical_path": descriptor.LogicalPath, "kind": descriptor.Kind, "size": descriptor.Size, "sha256": string(descriptor.SHA256),
		"resolver": "https-sha256", "entries": "1", "unpacked_size": descriptor.Size,
		"install_directory": "demo", "archive_root": "demo-root", "executables": []any{"demo-root/bin/demo"},
	}
	recordDigest, err := canonical.Sum("portable-tool-record", "portable-tool-record-v1", recordValue)
	if err != nil {
		t.Fatal(err)
	}
	artifact := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: recordValue["id"].(string), Digest: recordDigest},
		Record:    providers.CanonicalProviderData{Schema: "portable-tool-payload-v1", Value: recordValue},
	}
	plan := providers.PortableToolPlanV1{
		Schema: providers.PortableToolPlanSchemaV1,
		Tools: []providers.PortableToolPlanEntryV1{{
			Scope: "application", SelectedClosureDigest: rendererDigest("8"),
			Provenance: providers.PortableToolReleaseProvenanceV1{
				Tool: "demo", Version: "1.0.0", Revision: "1", ManifestDigest: rendererDigest("9"),
			},
			Responsibilities: providers.PortableToolResponsibilitiesV1{
				BindingContracts: []providers.PortableToolSelectedRecordV1{}, BindingArtifacts: []providers.PortableToolSelectedRecordV1{},
				Payloads: []providers.PortableToolSelectedRecordV1{artifact}, NativePackageSets: []providers.PortableToolSelectedRecordV1{},
			},
			Exports: []providers.PortableToolExportV1{}, ValidationProfiles: []providers.PortableToolValidationProfileV1{},
		}},
	}
	sourceValue := canonical.Object{
		"schema": providers.PortableToolArtifactSourceRecordSchemaV1,
		"id":     "tool:demo/releases/1.0.0/revisions/1/sources/demo", "sha256": string(descriptor.SHA256),
		"mirrors": []any{"https://mirror.example/demo.tar"}, "provenance": []any{"https://upstream.example/demo.tar"}, "diagnostics": []any{},
	}
	sourceDigest, err := canonical.Sum("portable-tool-record", "portable-tool-record-v1", sourceValue)
	if err != nil {
		t.Fatal(err)
	}
	source := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: sourceValue["id"].(string), Digest: sourceDigest},
		Record:    providers.CanonicalProviderData{Schema: providers.PortableToolArtifactSourceRecordSchemaV1, Value: sourceValue},
	}
	manifestValue := canonical.Object{
		"schema": providers.PortableToolReleaseManifestRecordSchemaV1,
		"id":     "tool:demo/releases/1.0.0/revisions/1/manifest",
		"tool":   "demo", "version": "1.0.0", "revision": "1",
		"aliases": []any{}, "provenance": []any{},
		"targets":             []any{canonical.Object{"id": "tool:demo/releases/1.0.0/targets/debian/12/amd64", "digest": string(rendererDigest("8"))}},
		"validation_profiles": []any{canonical.Object{"id": "tool:demo/releases/1.0.0/validation/profiles/default", "digest": string(rendererDigest("9"))}},
		"contract":            canonical.Object{"id": "tool:demo/releases/1.0.0/contract", "digest": string(rendererDigest("7"))},
		"artifact_sources": []any{canonical.Object{
			"artifact_sha256": string(descriptor.SHA256),
			"artifact":        canonical.Object{"id": artifact.Reference.ID, "digest": string(artifact.Reference.Digest)},
			"source":          canonical.Object{"id": source.Reference.ID, "digest": string(source.Reference.Digest)},
		}},
	}
	manifestDigest, err := canonical.Sum("portable-tool-record", "portable-tool-record-v1", manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifest := providers.PortableToolSelectedRecordV1{
		Reference: providers.PortableToolRecordReferenceV1{ID: manifestValue["id"].(string), Digest: manifestDigest},
		Record:    providers.CanonicalProviderData{Schema: providers.PortableToolReleaseManifestRecordSchemaV1, Value: manifestValue},
	}
	plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	domain := providers.PortableToolDomainAuthorityV1{ID: "application", Owner: owner}
	dag, err := providers.BuildPortableToolProviderDAGV1(providerPlan, plan, []providers.PortableToolProviderDomainSetV1{{
		Scope: "application", PackageManager: domain, Binding: domain, Filesystem: domain,
		Environment: domain, Exports: domain, Capabilities: domain,
	}})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := providers.BuildPortableToolLockV1(dag, []providers.PortableToolReleaseManifestInputV1{{
		Scope: "application", Tool: "demo", Manifest: manifest,
	}}, []providers.PortableToolArtifactAcquisitionInputV1{{
		Scope: "application", Tool: "demo", Artifact: artifact.Reference, Descriptor: descriptor, Source: source,
		Provenance: providerstore.AcquisitionProvenance{Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: source.Reference.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestAssembleBuildLockRejectsMisalignedGraphBeforePublication(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	overlay := deploy.EmptyRequestOverlayV1()
	overlayDigest, err := deploy.RequestOverlayDigestV1(overlay)
	if err != nil {
		t.Fatal(err)
	}
	request := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: overlayDigest,
		Platform: fixture.request.Platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: fixture.request.Plan.Nodes[1].Components[0], Provider: blueprint.ComponentTypePython, Request: fixture.request.Plan.Nodes[1].Request},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: fixture.request.Plan.Nodes[0].Request},
		},
		Sources: fixture.request.SourceCandidates,
	}
	_, err = AssembleBuildLock(context.Background(), fixture.store, BuildLockAssemblyInput{
		BlueprintDigest: rendererDigest("b"), ResolvedRequest: request, Overlay: overlay,
		PackageOverrides: fixture.lock.PackageOverrides, Base: fixture.lock.Base,
		Graph:         providers.GraphExecutionResult{Plan: fixture.request.Plan, PrefixImages: []providers.RealizedImageV1{}},
		RuntimePolicy: fixture.lock.RuntimePolicy, ValidationRecord: fixture.lock.ValidationRecord, FinalImage: fixture.lock.FinalImage,
	})
	if err == nil || !strings.Contains(err.Error(), "collections do not align") {
		t.Fatalf("misaligned graph error = %v", err)
	}
}
