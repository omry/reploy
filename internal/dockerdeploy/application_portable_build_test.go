package dockerdeploy

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// Keep acquisition, hash verification, extraction and lock construction real.
// Only catalog records are replaced with a coherent closure for tiny archives.
func applicationPortablePayloadFixtureForTest(t *testing.T) portableToolPythonLockedTestFixtureV1 {
	t.Helper()
	f := newPortableToolPythonLockedTestFixture(t)
	alignPTD2337LockedClosureToPlanV1(t, &f.fresh)
	closure := &f.fresh.Closures[0]
	// This bounded catalog fixture declares native dependencies available in
	// its base. Actual embedded-catalog APT projection has separate preflight
	// and prepared graph coverage; no browser support is asserted here.
	closure.Records.PackageSets = []toolcatalog.SelectedPackageSetRecordV1{}
	closure.Target.PackageSets = []toolcatalog.RecordReferenceV1{}
	for index := range closure.Target.Bindings {
		closure.Target.Bindings[index].PackageSets = []toolcatalog.RecordReferenceV1{}
	}
	for index := range closure.Target.Selections {
		closure.Target.Selections[index].PackageSets = []toolcatalog.RecordReferenceV1{}
	}
	for index := range closure.Records.Payloads {
		selected := &closure.Records.Payloads[index]
		record := &selected.Record
		var content bytes.Buffer
		archive := zip.NewWriter(&content)
		entries := map[string]bool{record.ArchiveRoot + "/": true}
		for _, executable := range record.Executables {
			entries[executable] = false
			for parent := path.Dir(executable); parent != "."; parent = path.Dir(parent) {
				entries[parent+"/"] = true
			}
		}
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			header := &zip.FileHeader{Name: name, Method: zip.Deflate}
			if entries[name] {
				header.SetMode(os.ModeDir | 0o755)
			} else {
				header.SetMode(0o755)
			}
			writer, err := archive.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if !entries[name] {
				if _, err := writer.Write([]byte("executable\n")); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		descriptor, err := f.store.Publish(t.Context(), record.LogicalPath, record.Kind, bytes.NewReader(content.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		record.Size, record.SHA256 = descriptor.Size, descriptor.SHA256
		record.Entries, record.UnpackedSize = fmt.Sprint(len(entries)), fmt.Sprint(11*len(record.Executables))
		encoded, err := canonical.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		value := canonical.Object{}
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		selected.Reference.Digest, err = canonical.Sum("portable-tool-record", "portable-tool-record-v1", value)
		if err != nil {
			t.Fatal(err)
		}
		for n := range f.records.Artifacts {
			artifact := &f.records.Artifacts[n]
			if artifact.Artifact.ID != record.ID {
				continue
			}
			artifact.Artifact.Digest, artifact.Descriptor = selected.Reference.Digest, descriptor
			artifact.Source.Record.Value["sha256"] = string(descriptor.SHA256)
			artifact.Source.Reference = portableToolPythonFreshReferenceV1(t, artifact.Source.Reference.ID, artifact.Source.Record)
		}
		for n := range closure.Target.Payloads {
			if closure.Target.Payloads[n].ID == record.ID {
				closure.Target.Payloads[n] = selected.Reference
			}
		}
		for n := range closure.Target.Selections {
			for k := range closure.Target.Selections[n].Payloads {
				if closure.Target.Selections[n].Payloads[k].ID == record.ID {
					closure.Target.Selections[n].Payloads[k] = selected.Reference
				}
			}
		}
	}
	manifest := &f.records.Releases[0].Manifest
	mappings := []any{}
	inputs := []providers.PortableToolArtifactAcquisitionInputV1{}
	for _, artifact := range f.records.Artifacts {
		mappings = append(mappings, canonical.Object{
			"artifact_sha256": string(artifact.Descriptor.SHA256),
			"artifact":        canonical.Object{"id": artifact.Artifact.ID, "digest": string(artifact.Artifact.Digest)},
			"source":          canonical.Object{"id": artifact.Source.Reference.ID, "digest": string(artifact.Source.Reference.Digest)},
		})
		inputs = append(inputs, providers.PortableToolArtifactAcquisitionInputV1{
			Scope: artifact.Scope, Tool: artifact.Tool, Artifact: artifact.Artifact, Descriptor: artifact.Descriptor, Source: artifact.Source,
			Provenance: providerstore.AcquisitionProvenance{Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: artifact.Source.Reference.ID},
		})
	}
	sort.Slice(mappings, func(i, j int) bool {
		return portableToolPythonLockedMappingDigestV1(mappings[i]) < portableToolPythonLockedMappingDigestV1(mappings[j])
	})
	manifest.Record.Value["artifact_sources"] = mappings
	manifest.Reference = portableToolPythonFreshReferenceV1(t, manifest.Reference.ID, manifest.Record)
	closure.Provenance.ManifestDigest = manifest.Reference.Digest
	closure.Identity = ptd2337PortableToolClosureIdentityV1(t, *closure)
	var err error
	f.fresh.Plan, err = toolcatalog.CompilePortableToolPlanV1(f.fresh.Closures)
	if err != nil {
		t.Fatal(err)
	}
	domains, err := applicationPortableProviderDomainsV1(f.fresh.Plan, f.lock.Plan.ProviderPlan)
	if err != nil {
		t.Fatal(err)
	}
	dag, err := providers.BuildPortableToolProviderDAGV1(f.lock.Plan.ProviderPlan, f.fresh.Plan, domains)
	if err != nil {
		t.Fatal(err)
	}
	f.lock, err = providers.BuildPortableToolLockV1(dag, f.records.Releases, inputs)
	if err != nil {
		t.Fatal(err)
	}
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(f.fresh.Plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	f.component, f.binding = projection.Components[0], projection.Components[0].Bindings[0]
	f.plan, err = buildPortableToolPythonLockedPlanV1(&f.lock)
	if err != nil {
		t.Fatal(err)
	}
	previous := embeddedPortableRuntimeLockRecordsV1
	embeddedPortableRuntimeLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return f.records, nil
	}
	t.Cleanup(func() { embeddedPortableRuntimeLockRecordsV1 = previous })
	return f
}

func TestApplicationPortableMaterializationFreshAndLockedV1(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(fmt.Sprint(locked), func(t *testing.T) {
			f := applicationPortablePayloadFixtureForTest(t)
			_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(f.fresh.Plan, []providers.ResolvedComponentRequestV1{})
			if err != nil {
				t.Fatal(err)
			}
			selected := &ApplicationPortableToolPlanV1{Plan: f.fresh.Plan, DAG: f.lock.Plan, Closures: f.fresh.Closures, PythonProjection: projection}
			sealSyntheticApplicationPortablePythonSelectionForTest(t, selected, testProbeImageDescriptor(t, "linux/amd64"), pythonConsumerTestImageConfig())
			var reusable *deploy.BuildLockV1
			if locked {
				reusable = &deploy.BuildLockV1{PortableTools: &f.lock}
			}
			other := []providers.PortableToolArtifactAcquisitionInputV1{}
			for _, acquisition := range f.lock.Acquisitions {
				if acquisition.Artifact.ID == f.descriptor.LogicalPath {
					t.Fatal("artifact identity must be catalog-owned")
				}
				if acquisition.Descriptor == f.descriptor {
					other = append(other, providers.PortableToolArtifactAcquisitionInputV1{Scope: acquisition.Scope, Tool: acquisition.Tool, Artifact: acquisition.Artifact, Descriptor: acquisition.Descriptor, Source: acquisition.Source, Provenance: providerstore.AcquisitionProvenance{Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: acquisition.Source.Reference.ID}})
				}
			}
			payloads, err := materializeApplicationPortableRuntimeV1(t.Context(), f.store, selected, reusable, other)
			if err != nil {
				t.Fatal(err)
			}
			workspace := payloads.workspace
			if len(payloads.copies) != len(f.fresh.Plan.Tools[0].Responsibilities.Payloads) || len(payloads.Lock.Acquisitions) != len(f.records.Artifacts) || len(payloads.sealedInventory) == 0 {
				t.Fatal("complete selected closure was not staged")
			}
			if err := payloads.Cleanup(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(workspace); !os.IsNotExist(err) {
				t.Fatalf("staged payloads survived cleanup: %v", err)
			}
		})
	}
}

func TestApplicationPortableBuildPublishesCompleteFreshAndLockedClosureV1(t *testing.T) {
	for _, test := range []struct {
		name   string
		replay bool
		fault  string
	}{
		{name: "fresh"}, {name: "locked", replay: true},
		{name: "missing locked payload", replay: true, fault: "missing payload"},
		{name: "changed locked provenance", replay: true, fault: "provenance"},
		{name: "omitted image payload", fault: "omitted payload"},
		{name: "publication failure", fault: "publication"},
		{name: "staging cleanup failure", fault: "staging cleanup"},
		{name: "image cleanup failure", fault: "image cleanup"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testPreparedPythonGraphLockedAcceptance(t, func(reuse preparedPythonGraphReuseFixture, f portableToolPythonLockedTestFixtureV1) {
				applicationPortableBuildAcceptanceForTest(t, reuse, f, test.replay, test.fault)
			})
		})
	}
}

func applicationPortableBuildAcceptanceForTest(t *testing.T, reuse preparedPythonGraphReuseFixture, f portableToolPythonLockedTestFixtureV1, replay bool, fault string) {
	t.Helper()
	faultError := errors.New("application acceptance " + fault)
	input, unusedOperation, _ := providerBuildCompletionFixture(t)
	if err := unusedOperation.Unlock(); err != nil {
		t.Fatal(err)
	}
	declared := applicationPortableDocumentForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	application := input.Document.Environment.Applications["application"]
	application.Packages.Tools = declared.Environment.Applications["web"].Packages.Tools
	for index := range application.Packages.Tools {
		application.Packages.Tools[index].Scope = "application:application"
	}
	input.Document.Environment.Applications["application"] = application
	loaded, err := loadBuildRequestWithInputsV1(nil, reuse.lock.PackageOverrides, "", reuse.request.SourceCandidates, deploy.StateV1{Platform: reuse.request.Platform, Overlay: input.Overlay}, input.Document)
	if err != nil {
		t.Fatal(err)
	}
	projected, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(f.fresh.Plan, loaded.Request.Components)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Plan(providers.PlanInput{Components: projected, Platform: loaded.Request.Platform})
	if err != nil {
		t.Fatal(err)
	}
	domains, err := applicationPortableProviderDomainsV1(f.fresh.Plan, plan)
	if err != nil {
		t.Fatal(err)
	}
	dag, err := providers.BuildPortableToolProviderDAGV1(plan, f.fresh.Plan, domains)
	if err != nil {
		t.Fatal(err)
	}
	selected := &ApplicationPortableToolPlanV1{Plan: f.fresh.Plan, DAG: dag, Closures: f.fresh.Closures, ProjectedComponents: projected, PythonProjection: projection}
	sealSyntheticApplicationPortablePythonSelectionForTest(t, selected, reuse.lock.Base, pythonConsumerTestImageConfig())
	selected.sealed.documentDigest, err = blueprint.DocumentDigestV1(input.Document)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Request.Components = projected
	for _, artifact := range f.records.Artifacts {
		content, err := os.ReadFile(mustBlobPath(t, f.store, artifact.Descriptor))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reuse.store.PublishExpected(t.Context(), artifact.Descriptor, bytes.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	previousRecords := portableToolPythonFreshLockRecordsV1
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return f.records, nil
	}
	t.Cleanup(func() { portableToolPythonFreshLockRecordsV1 = previousRecords })
	dir := filepath.Dir(filepath.Dir(reuse.store.Root()))
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	preparation := LockedProviderBuildPreparationV1{
		Operation: operation, Store: reuse.store, Environment: "demo", DeploymentDir: dir,
		DockerPlan: input.DockerPlan, BlueprintDigest: selected.sealed.documentDigest, ReployVersion: "0.7.0.dev1",
		Loaded: loaded, FinalImageConfig: pythonConsumerTestImageConfig(), ApplicationTools: selected,
		StartupVerifier: input.StartupVerifier,
	}
	prepared := PreparedProviderBase{Plan: plan, Descriptor: reuse.lock.Base, Config: deploy.BaseConfig{Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{}, Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{}}, Catalog: reuse.request.EarlierCatalog}
	prepared.Image, err = realizedImageFromDescriptor(prepared.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	preparation.PreparedBase = &prepared
	preparation.SelectedBase = SelectedProviderBase{Plan: plan, Descriptor: prepared.Descriptor, Config: prepared.Config}
	if replay {
		preparation.ReusableLock = &reuse.lock
	}

	// Independent Docker observations describe the verified tiny archive
	// fixture. No resolver, graph, staging, assembly or publication is replaced.
	staged, err := MaterializePortableRuntimePayloadsLockedV1(t.Context(), f.store, f.lock)
	if err != nil {
		t.Fatal(err)
	}
	archives := map[string][]byte{}
	for _, copy := range staged.copies {
		entries := []portableRuntimeTestInventoryTarEntry{}
		for _, entry := range staged.sealedInventory {
			if entry.path != copy.Destination && !strings.HasPrefix(entry.path, copy.Destination+"/") {
				continue
			}
			item := portableRuntimeTestInventoryTarEntry{path: "/" + strings.TrimPrefix(entry.path, path.Dir(copy.Destination)+"/"), kind: entry.kind, mode: int64(entry.mode.Perm())}
			if entry.kind == providerstore.ArchiveEntryKindRegular {
				if fault == "omitted payload" {
					continue
				}
				item.content = []byte("executable\n")
			}
			entries = append(entries, item)
		}
		archives[copy.Destination] = portableRuntimeTestInventoryTar(t, entries...)
	}
	response := portableRuntimeTestProbeResponse(t, staged.sealedInventory)
	if err := staged.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if fault == "provenance" {
		changed := providers.ClonePortableToolLockV1(f.lock)
		changed.Acquisitions[0].Source.Record.Value["sha256"] = string(rendererDigest("0"))
		preparation.ReusableLock.PortableTools = &changed
	}
	if fault == "missing payload" {
		descriptor := f.records.Artifacts[len(f.records.Artifacts)-1].Descriptor
		if descriptor.Kind == "wheel" {
			t.Fatal("missing payload fixture selected a binding")
		}
		if err := os.Remove(mustBlobPath(t, reuse.store, descriptor)); err != nil {
			t.Fatal(err)
		}
	}
	oldBuild, oldInspect, oldRemove := buildPortableRuntimePayloadLayerV1, inspectPortableRuntimePayloadLayerV1, removePortableRuntimePayloadLayerV1
	oldAbsent, oldWorkspace, oldSession := requirePortableRuntimeDestinationsAbsentV1, preparePortableRuntimeProbeWorkspaceV1, openPortableRuntimeProbeSessionV1
	t.Cleanup(func() {
		buildPortableRuntimePayloadLayerV1, inspectPortableRuntimePayloadLayerV1, removePortableRuntimePayloadLayerV1 = oldBuild, oldInspect, oldRemove
		requirePortableRuntimeDestinationsAbsentV1, preparePortableRuntimeProbeWorkspaceV1, openPortableRuntimeProbeSessionV1 = oldAbsent, oldWorkspace, oldSession
	})
	var payloadImage InspectedImageCandidate
	var stagingDir string
	removed := 0
	buildPortableRuntimePayloadLayerV1 = func(_ context.Context, _ providerstore.Store, upstream deploy.ImageDescriptor, contextDir string, dockerfile []byte, _ RunOptions) (BuiltImageCandidate, error) {
		stagingDir = contextDir
		if !strings.Contains(string(dockerfile), "ADD --chown=0:0") {
			t.Fatal("payload layer did not consume verified archive")
		}
		upstream.ConfigDigest, upstream.ImmutableReference, upstream.AuthorReference = rendererDigest("7"), string(rendererDigest("7")), string(rendererDigest("7"))
		upstream.RootFSDiffIDs = append(append([]canonical.Digest{}, upstream.RootFSDiffIDs...), rendererDigest("6"))
		payloadImage = inspectedValidationCandidate(t, upstream)
		for _, variable := range f.fresh.Plan.Tools[0].Runtime.Environment {
			payloadImage.Config.Environment = append(payloadImage.Config.Environment, deploy.ConfigEnvironmentVariable{Name: variable.Name, Value: variable.Value})
		}
		return BuiltImageCandidate{ImageID: upstream.ConfigDigest}, nil
	}
	inspectPortableRuntimePayloadLayerV1 = func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
		return payloadImage, nil
	}
	removePortableRuntimePayloadLayerV1 = func(context.Context, BuiltImageCandidate) error {
		removed++
		if fault == "image cleanup" {
			return faultError
		}
		return nil
	}
	requirePortableRuntimeDestinationsAbsentV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) error { return nil }
	preparePortableRuntimeProbeWorkspaceV1 = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return PreparedProbeWorkspace{}, func() error { return nil }, nil
	}
	openPortableRuntimeProbeSessionV1 = func(_ context.Context, descriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace) (*ImageValidationSession, error) {
		if descriptor.ConfigDigest != payloadImage.Image.ConfigDigest {
			t.Fatal("inventory inspected a substituted image")
		}
		session := portableRuntimeInventoryOwnershipTestSession(t, nil, response)
		run := session.runDocker
		session.runDocker = func(spec CommandSpec, options RunOptions) error {
			if spec.Args[0] == "rm" {
				if fault == "staging cleanup" {
					workspace := filepath.Dir(stagingDir)
					retained := workspace + ".test-retained"
					if err := os.Rename(workspace, retained); err != nil {
						return err
					}
					if err := os.WriteFile(workspace, []byte("cleanup fault"), 0o600); err != nil {
						return err
					}
					t.Cleanup(func() { _ = os.Remove(workspace); _ = removeSourceBuilderPortableToolWorkspaceV1(retained) })
				}
				return nil
			}
			if spec.Args[0] == "cp" {
				_, destination, _ := strings.Cut(spec.Args[1], ":")
				archive, found := archives[destination]
				if !found {
					t.Fatalf("unexpected payload inventory destination %s", destination)
				}
				_, err := options.Stdout.Write(archive)
				return err
			}
			return run(spec, options)
		}
		return session, nil
	}
	images := &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: operation, t: t, sequence: 230}
	var graph providers.GraphExecutionResult
	var finalizedImage InspectedImageCandidate
	observedFinalImage := false
	backend := providerBuildExecutionBackend{
		executeGraph: func(context.Context, PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error) {
			t.Fatal("application execution bypassed sealed graph entrypoint")
			return providers.GraphExecutionResult{}, nil
		},
		prepareValidation: func(ctx context.Context, base deploy.ImageDescriptor, catalog []providers.RealizedOutput, actual providers.GraphExecutionResult, policy deploy.RuntimePolicyV1) (ProviderGraphValidationPlan, error) {
			graph = actual
			return prepareProviderGraphValidation(ctx, base, catalog, actual, policy, func(ctx context.Context, candidate BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
				return inspectPortablePythonAliasLayerV1(ctx, candidate, platform)
			})
		},
		complete: func(ctx context.Context, held *deploy.OperationLock, store providerstore.Store, value ProviderBuildCompletionInput) (ProviderBuildCompletionResult, error) {
			return completeProviderBuild(ctx, held, store, value, providerBuildCompletionBackend{
				assemble: AssembleBuildLock, removeFinalized: ignoreFinalizedCandidateRemoval,
				publish: func(ctx context.Context, operation *deploy.OperationLock, store providerstore.Store, value BuildPublicationInput) (deploy.StateV1, error) {
					publication := images.backend(store, dir)
					if fault == "publication" {
						publication.commitState = func(*deploy.EnvironmentGenerationState, deploy.StateV1) error { return faultError }
					}
					return publishBuild(ctx, operation, store, value, publication)
				},
				validateAndFinalize: func(ctx context.Context, store providerstore.Store, layers []FullImageValidationInput, final FullImageValidationInput, validator providers.RequirementProfileOwnerValidator, run FullImageValidationRunner, verifier deploy.ApplicationStartupVerifierV1, account deploy.ApplicationLocalAccountV1, options RunOptions) (FinalizedBuildValidationResult, error) {
					_, runtimeCandidate, runtimeImage := finalValidationRuntimeFixture(t, final)
					return validateAndFinalizeBuild(ctx, store, layers, final, validator, run, verifier, account, options,
						func(providerstore.Store, ApplicationRuntimeLayerBuildRequest, RunOptions) (BuiltImageCandidate, error) {
							return runtimeCandidate, nil
						},
						func(context.Context, BuiltImageCandidate, ApplicationRuntimeLayerBuildRequest) (InspectedImageCandidate, error) {
							return runtimeImage, nil
						},
						func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error { return nil },
						func(providerstore.Store, FinalizationBuildRequest, RunOptions) (BuiltImageCandidate, error) {
							return BuiltImageCandidate{ImageID: rendererDigest("5")}, nil
						},
						func(_ context.Context, candidate BuiltImageCandidate, request FinalizationBuildRequest) (InspectedImageCandidate, error) {
							finalizedImage = request.Source
							finalizedImage.Descriptor.ConfigDigest, finalizedImage.Descriptor.ImmutableReference, finalizedImage.Descriptor.AuthorReference = candidate.ImageID, string(candidate.ImageID), string(candidate.ImageID)
							finalizedImage.Image, err = realizedImageFromDescriptor(finalizedImage.Descriptor)
							return finalizedImage, err
						}, ignoreFinalizedCandidateRemoval)
				},
			})
		},
	}
	result, err := executeLockedProviderBuildV1(t.Context(), LockedProviderBuildExecutionInputV1{
		Preparation: preparation, SourceWheels: reuse.sourceWheels, LocalOverrides: []PythonLocalOverrideV1{},
		observeFinalImage: func(_ context.Context, image InspectedImageCandidate, lock deploy.BuildLockV1) error {
			if image.Image != finalizedImage.Image || image.Image != lock.FinalImage || lock.PortableRuntimeLayer == nil {
				t.Fatal("ordinary execution did not hand off its exact finalized application materialization")
			}
			if _, err := PortableToolApplicationValidationInputFromBuildLockV1(image, lock, "application:application"); err != nil {
				return err
			}
			observedFinalImage = true
			return nil
		},
		RunValidation: func(_ context.Context, value FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			profiles := append([]providers.ValidationEvidence{}, graph.ValidationEvidence...)
			for index := range profiles {
				profiles[index].SubjectRootFS = value.Image.Image.RootFSSubject
			}
			outputs := []providers.ExecutableEvidence{}
			for _, output := range value.Outputs {
				outputs = append(outputs, output.Evidence)
			}
			return profiles, outputs, nil
		},
	}, backend)
	if fault == "" && !observedFinalImage {
		t.Fatal("ordinary execution omitted the application image callback")
	}
	if fault != "" {
		if err == nil || !reflect.DeepEqual(result, LockedProviderBuildExecutionResultV1{}) {
			t.Fatalf("fault %q returned successful build: %v", fault, err)
		}
		if fault == "publication" || fault == "image cleanup" {
			if !errors.Is(err, faultError) {
				t.Fatalf("fault %q did not reach its intended boundary: %v", fault, err)
			}
		}
		if fault == "staging cleanup" && !strings.Contains(err.Error(), "open workspace for cleanup") {
			t.Fatalf("staging cleanup fault missed its intended boundary: %v", err)
		}
		state, found, readErr := operation.ReadStateV1()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if fault != "image cleanup" && found && state.Current != nil {
			t.Fatal("failed application build committed a successful current generation")
		}
		if fault == "image cleanup" && (!found || state.Current == nil) {
			t.Fatal("cleanup failure lost the independently published owner")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Current == nil || result.Lock.PortableRuntimeLayer == nil || result.Lock.PortableTools == nil || result.Lock.FinalImage != finalizedImage.Image || removed != 1 {
		t.Fatalf("incomplete ordinary application publication: %#v", result)
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatal("successful build retained payload staging")
	}
	if _, err := deploy.BuildLockStoreClosure(result.Lock, reuse.store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal(err)
	}
	view, err := PortableToolApplicationValidationInputFromBuildLockV1(finalizedImage, result.Lock, "application:application")
	if err != nil || len(view.selected) != len(f.fresh.Plan.Tools[0].ValidationProfiles) {
		t.Fatalf("final scoped schedule: %v", err)
	}
	current, found, err := LoadRecordedCurrentBuildV1(t.Context(), operation, reuse.store, "demo", dir)
	if err != nil || !found {
		t.Fatalf("published generation lost complete lock: %v", err)
	}
	actualDigest, err := deploy.BuildLockDigestV1(current.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	expectedDigest, err := deploy.BuildLockDigestV1(result.Lock, registry.ValidateRequirementProfileV1)
	if err != nil || actualDigest != expectedDigest || expectedDigest != current.Generation.BuildLockDigest {
		t.Fatal("published generation substituted its complete lock")
	}
}

func TestApplicationPortableRequestRequiresExactSealedConsumptionV1(t *testing.T) {
	input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
	overlay := deploy.EmptyRequestOverlayV1()
	overrides := deploy.EmptyPackageOverrideIntentV1("demo")
	before, err := blueprint.DocumentDigestV1(input.Document)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBuildRequestWithInputsV1(nil, overrides, "", []providers.ResolvedSourceInput{}, deploy.StateV1{Platform: input.Platform, Overlay: overlay}, input.Document)
	if err != nil {
		t.Fatal(err)
	}
	input.Components = loaded.Request.Components
	var baseExports any
	for _, component := range input.Components {
		if component.Provider == blueprint.ComponentTypeBase {
			baseExports = component.Request.Value["exports"]
		}
	}
	if !strings.Contains(fmt.Sprint(baseExports), "/usr/local/bin/python") {
		t.Fatalf("binding-only application lost its default interpreter export: %#v", input.Components)
	}
	if _, err := resolvedRequestWithApplicationToolsV1(input.Document, overlay, overrides, input.Platform, loaded.Request.Sources, nil); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("unconsumed application request = %v", err)
	}
	observations := 0
	stubApplicationPortableTargetForTest(t, &observations)
	selected, err := PlanApplicationPortableToolsV1(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	consumed, err := resolvedRequestWithApplicationToolsV1(input.Document, overlay, overrides, input.Platform, loaded.Request.Sources, selected)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(consumed.Components, selected.ProjectedComponents) || observations != 1 {
		t.Fatalf("consumption did not retain the selected provider projection: %#v", consumed)
	}
	after, err := blueprint.DocumentDigestV1(input.Document)
	if err != nil || before != after || !reflect.DeepEqual(loaded.Document, input.Document) {
		t.Fatal("provider view changed the original blueprint authority")
	}
	changed := input.Document
	changed.Environment.ID = "other"
	if _, err := resolvedRequestWithApplicationToolsV1(changed, deploy.EmptyRequestOverlayV1(), deploy.EmptyPackageOverrideIntentV1("other"), input.Platform, loaded.Request.Sources, selected); err == nil || !strings.Contains(err.Error(), "exact blueprint") {
		t.Fatalf("foreign selection consumption = %v", err)
	}
	tampered := *selected
	tampered.sealed = nil
	if _, err := resolvedRequestWithApplicationToolsV1(input.Document, overlay, overrides, input.Platform, loaded.Request.Sources, &tampered); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("unsealed selection consumption = %v", err)
	}
}

func TestApplicationPortableBindingInferenceRetainsInterpreterSupplyV1(t *testing.T) {
	baseExports := func(components []providers.ResolvedComponentRequestV1) (any, bool) {
		for _, component := range components {
			if component.Component == "base" && component.Provider == blueprint.ComponentTypeBase {
				return component.Request.Value["exports"], true
			}
		}
		return nil, false
	}
	selectAndCheck := func(t *testing.T, input PlanApplicationPortableToolsInputV1, loaded LoadedBuildRequestV1, want string) {
		t.Helper()
		input.Components = loaded.Request.Components
		observations := 0
		stubApplicationPortableTargetForTest(t, &observations)
		selected, err := PlanApplicationPortableToolsV1(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if selected == nil || selected.sealed == nil || observations != 1 {
			t.Fatalf("application selection = %#v, observations %d", selected, observations)
		}
		loadedExports, found := baseExports(loaded.Request.Components)
		selectedExports, selectedFound := baseExports(selected.ProjectedComponents)
		loadedBytes, loadedErr := canonical.Marshal(loadedExports)
		selectedBytes, selectedErr := canonical.Marshal(selectedExports)
		if !found || !selectedFound || loadedErr != nil || selectedErr != nil ||
			!strings.Contains(fmt.Sprint(loadedExports), want) || !bytes.Equal(selectedBytes, loadedBytes) {
			t.Fatalf("interpreter supply before/after selection = %#v / %#v, want %q", loadedExports, selectedExports, want)
		}
	}

	for _, test := range []struct {
		name  string
		tools string
	}{
		{name: "omitted binding", tools: "[{tool: playwright, version: 1.61.0, select: {browser: [chromium]}}]"},
		{name: "explicit python binding", tools: "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]"},
		{name: "wildcard binding", tools: "[{tool: playwright, version: 1.61.0, binding: '*', select: {browser: [chromium]}}]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := applicationPortableInputForTest(t, test.tools)
			overrides := deploy.EmptyPackageOverrideIntentV1("demo")
			before, err := blueprint.DocumentDigestV1(input.Document)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := loadBuildRequestWithInputsV1(nil, overrides, "", []providers.ResolvedSourceInput{}, deploy.StateV1{
				Platform: input.Platform, Overlay: deploy.EmptyRequestOverlayV1(),
			}, input.Document)
			if err != nil {
				t.Fatal(err)
			}
			selectAndCheck(t, input, loaded, "/usr/local/bin/python")
			after, err := blueprint.DocumentDigestV1(input.Document)
			if err != nil || before != after {
				t.Fatal("ordinary interpreter supply changed original blueprint identity")
			}
		})
	}

	t.Run("explicit base export remains authoritative", func(t *testing.T) {
		input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, select: {browser: [chromium]}}]")
		input.Document.Environment.Base.Exports = map[string]blueprint.BaseExecutableExport{
			"python": {Executable: "/opt/project/python"},
		}
		if err := input.Document.Environment.RebuildProviderContributions(); err != nil {
			t.Fatal(err)
		}
		loaded, err := loadBuildRequestWithInputsV1(nil, deploy.EmptyPackageOverrideIntentV1("demo"), "", []providers.ResolvedSourceInput{}, deploy.StateV1{
			Platform: input.Platform, Overlay: deploy.EmptyRequestOverlayV1(),
		}, input.Document)
		if err != nil {
			t.Fatal(err)
		}
		selectAndCheck(t, input, loaded, "/opt/project/python")
		base, found := baseExports(loaded.Request.Components)
		if !found || strings.Contains(fmt.Sprint(base), "/usr/local/bin/python") {
			t.Fatalf("explicit base interpreter was replaced: %#v", base)
		}
	})

	t.Run("ordinary Python supplier remains active", func(t *testing.T) {
		input := applicationPortableInputForTest(t, "[{tool: playwright, version: 1.61.0, select: {browser: [chromium]}}]")
		application := input.Document.Environment.Applications["web"]
		application.Packages.Python = &blueprint.PythonComponent{
			Interpreter:  blueprint.CommandRequirement{Command: "python"},
			Requirements: []string{"requests"},
		}
		input.Document.Environment.Applications["web"] = application
		if err := input.Document.Environment.RebuildProviderContributions(); err != nil {
			t.Fatal(err)
		}
		ordinaryView, err := providerDocumentWithoutApplicationToolsV1(input.Document)
		if err != nil {
			t.Fatal(err)
		}
		ordinaryApplication := ordinaryView.Environment.Applications["web"]
		if !reflect.DeepEqual(ordinaryApplication.Packages.Python, application.Packages.Python) || len(ordinaryApplication.Packages.Tools) != 0 {
			t.Fatal("application tool projection changed the ordinary Python supplier")
		}
		loaded, err := loadBuildRequestWithInputsV1(nil, deploy.EmptyPackageOverrideIntentV1("demo"), "", []providers.ResolvedSourceInput{}, deploy.StateV1{
			Platform: input.Platform, Overlay: deploy.EmptyRequestOverlayV1(),
		}, input.Document)
		if err != nil {
			t.Fatal(err)
		}
		pythonComponentFound := false
		for _, component := range loaded.Request.Components {
			if component.Component == "application/web/python" && component.Provider == blueprint.ComponentTypePython {
				pythonComponentFound = true
			}
		}
		if !pythonComponentFound {
			t.Fatal("ordinary Python provider contribution was dropped")
		}
		selectAndCheck(t, input, loaded, "/usr/local/bin/python")
	})
}

func TestApplicationPortableAssemblyAndScopedScheduleV1(t *testing.T) {
	f := newPortableVerificationFixtureV1(t, false)
	lock := f.current.Lock
	tools := providers.ClonePortableToolLockV1(*lock.PortableTools)
	profile := portableToolValidationProfile(toolcatalog.RecordProbeV1{Path: "/opt/reploy/tools/demo/bin/demo", Args: []string{"--version"}})
	profile.ID = strings.Replace(profile.ID, "/1.2.3/", "/1.0.0/", 1)
	profile.Version = "1.0.0"
	tools.Plan.PortableToolPlan.Tools[0].ValidationProfiles = []providers.PortableToolValidationProfileV1{portableToolTestScheduleV1(t, profile).Entries[0].Profile}
	var err error
	reference := tools.Plan.PortableToolPlan.Tools[0].ValidationProfiles[0].Reference
	manifest := &tools.Releases[0].Manifest
	manifest.Record.Value["validation_profiles"] = []any{canonical.Object{"id": reference.ID, "digest": string(reference.Digest)}}
	manifest.Reference.Digest, err = canonical.Sum("portable-tool-record", "portable-tool-record-v1", manifest.Record.Value)
	if err != nil {
		t.Fatal(err)
	}
	tools.Plan.PortableToolPlan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	tools.Plan, err = providers.BuildPortableToolProviderDAGV1(tools.Plan.ProviderPlan, tools.Plan.PortableToolPlan, tools.Plan.Domains)
	if err != nil {
		t.Fatal(err)
	}
	builder := buildLockAssemblyPortableToolsV1(t, f.store, tools.Plan.ProviderPlan, "base")
	builder.Plan.PortableToolPlan.Tools[0].Scope = "source-builder:demo"
	builderDomain := providers.PortableToolDomainAuthorityV1{ID: "source-builder", Owner: "base"}
	builder.Plan.Domains[0] = providers.PortableToolProviderDomainSetV1{Scope: "source-builder:demo", PackageManager: builderDomain, Binding: builderDomain, Filesystem: builderDomain, Environment: builderDomain, Exports: builderDomain, Capabilities: builderDomain}
	builder.Releases[0].Scope = "source-builder:demo"
	for index := range builder.Acquisitions {
		builder.Acquisitions[index].Scope = "source-builder:demo"
	}
	builder.Plan, err = providers.BuildPortableToolProviderDAGV1(builder.Plan.ProviderPlan, builder.Plan.PortableToolPlan, builder.Plan.Domains)
	if err != nil {
		t.Fatal(err)
	}
	tools, err = mergePortableToolLocksV1(tools, builder)
	if err != nil {
		t.Fatal(err)
	}
	request, err := BuildResolvedRequestV1(f.runtime.Document, lock.Overlay, lock.Platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	graph := providers.GraphExecutionResult{
		Plan: tools.Plan.ProviderPlan, SelectedEdges: tools.Plan.ProviderPlan.Edges,
		PrefixImages: []providers.RealizedImageV1{f.base.Image},
		Bundles:      []providers.ResolvedBundle{}, Profiles: []providers.RequirementProfile{},
		ValidationEvidence: []providers.ValidationEvidence{}, Materializations: []providers.GraphNodeMaterializeResult{},
		Catalog: []providers.RealizedOutput{}, SelectedSources: []providers.ResolvedSourceInput{},
	}
	layer := *lock.PortableRuntimeLayer
	// Assembly owns the final transaction digest, including all portable scopes.
	layer.TransactionDigest = ""
	assembled, err := AssembleBuildLock(t.Context(), f.store, BuildLockAssemblyInput{
		BlueprintDigest: lock.BlueprintDigest, ResolvedRequest: request, Overlay: lock.Overlay,
		PackageOverrides: lock.PackageOverrides, Base: lock.Base, Graph: graph,
		PortableTools: &tools, PortableRuntimeLayer: &layer, RuntimePolicy: lock.RuntimePolicy,
		RuntimeLayer: lock.RuntimeLayer, ValidationRecord: lock.ValidationRecord, FinalImage: lock.FinalImage,
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := deploy.PortableRuntimeLayerTransactionDigestV1(*assembled.PortableTools, layer.Upstream, layer.Result)
	if err != nil || assembled.PortableRuntimeLayer.TransactionDigest != want || layer.TransactionDigest != "" {
		t.Fatal("assembly lost complete portable transaction ownership or mutated its caller")
	}
	if len(assembled.PortableTools.Plan.PortableToolPlan.Tools) != 2 {
		t.Fatal("assembly omitted the independent source-builder selection")
	}
	if err := deploy.ValidateBuildLockV1(assembled, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	view, err := PortableToolApplicationValidationInputFromBuildLockV1(f.final, assembled, "application:demo")
	if err != nil {
		t.Fatal(err)
	}
	if view.Image.Image != assembled.FinalImage || len(view.selected) != 1 || view.selected[0].entry.Scope != "application:demo" || view.selected[0].entry.Profile.Reference != reference {
		t.Fatalf("scoped final-image handoff = %#v", view)
	}
	for _, scope := range []string{"application:missing", "application:", "build:source", ""} {
		if _, err := PortableToolApplicationValidationInputFromBuildLockV1(f.final, assembled, scope); err == nil {
			t.Fatalf("unowned scope %q accepted", scope)
		}
	}
	if _, err := PortableToolApplicationValidationInputFromBuildLockV1(f.portable, assembled, "application:demo"); err == nil {
		t.Fatal("intermediate payload image accepted as final image")
	}
	tampered := assembled
	tampered.PortableRuntimeLayer = nil
	if _, err := PortableToolApplicationValidationInputFromBuildLockV1(f.final, tampered, "application:demo"); err == nil {
		t.Fatal("missing portable materialization accepted")
	}
	assembled.PortableTools.Plan.PortableToolPlan.Tools[0].ValidationProfiles[0].Record.Value["id"] = "mutated"
	if view.selected[0].entry.Profile.Record.Value["id"] == "mutated" {
		t.Fatal("scoped handoff retained mutable lock authority")
	}
}

func TestApplicationPortablePreparationSelectsBeforeBaseRealizationV1(t *testing.T) {
	for _, tools := range []string{
		"[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]",
		"[{tool: java, version: '21'}]",
	} {
		t.Run(tools, func(t *testing.T) {
			input, _, current, selectedBase, prepared := providerBuildPreparationFixture(t)
			document := applicationPortableDocumentForTest(t, tools)
			input.PackageOverrides = deploy.EmptyPackageOverrideIntentV1("demo")
			input.NoCache = true
			input.ReployVersion = "0.7.0.dev1"
			state := current.State
			state.Overlay = deploy.EmptyRequestOverlayV1()
			loaded, err := loadBuildRequestWithInputsV1(input.Operation, input.PackageOverrides, "", input.Sources, state, document)
			if err != nil {
				t.Fatal(err)
			}
			selectedBase.Descriptor = testProbeImageDescriptor(t, "linux/amd64")
			order := []string{}
			backend := providerBuildPreparationTestBackend(t, loaded, current, selectedBase, prepared, &order)
			backend.selectCachedBase = func(context.Context, providers.ResolvedRequestV1, deploy.ImageDescriptor) (SelectedProviderBase, bool, error) {
				return SelectedProviderBase{}, false, nil
			}
			realized := 0
			observations := 0
			stubApplicationPortableTargetForTest(t, &observations)
			backend.realizeBase = func(_ context.Context, _ providerstore.Store, base SelectedProviderBase) (PreparedProviderBase, error) {
				realized++
				if observations != 1 {
					t.Fatal("base realized before application selection")
				}
				image, err := realizedImageFromDescriptor(base.Descriptor)
				return PreparedProviderBase{Plan: base.Plan, Descriptor: base.Descriptor, Config: base.Config, Image: image, Catalog: []providers.RealizedOutput{}}, err
			}
			result, err := prepareLockedProviderBuildV1(t.Context(), input, backend)
			if strings.Contains(tools, "java") {
				if err == nil || realized != 0 {
					t.Fatalf("unsupported runtime tool reached base realization: %v, calls %d", err, realized)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.ApplicationTools == nil || result.Reused || realized != 1 || !reflect.DeepEqual(result.PreparedBase.Plan, result.ApplicationTools.sealed.providerPlan) {
				t.Fatalf("application preparation incomplete: %#v", result)
			}
		})
	}
}

func TestApplicationPortablePreparationUsesNormalBaseSelectionWithCachedBaseV1(t *testing.T) {
	for _, test := range []struct {
		name    string
		noCache bool
	}{
		{name: "no cache", noCache: true},
		{name: "no current build"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, _, current, selectedBase, _ := providerBuildPreparationFixture(t)
			input.NoCache = test.noCache
			input.PackageOverrides = deploy.EmptyPackageOverrideIntentV1("demo")
			document := applicationPortableDocumentForTest(t, "[{tool: playwright, version: 1.61.0, binding: python, select: {browser: [chromium]}}]")
			state := current.State
			state.Overlay = deploy.EmptyRequestOverlayV1()
			loaded, err := loadBuildRequestWithInputsV1(input.Operation, input.PackageOverrides, "", input.Sources, state, document)
			if err != nil {
				t.Fatal(err)
			}

			fresh := testProbeImageDescriptor(t, "linux/amd64")
			freshDigest := canonical.Digest("sha256:" + strings.Repeat("d", 64))
			fresh.AuthorReference = "docker.io/library/debian:12-slim"
			fresh.ImmutableReference = string(freshDigest)
			fresh.ConfigDigest = freshDigest
			fresh.RootFSDiffIDs = []canonical.Digest{canonical.Digest("sha256:" + strings.Repeat("e", 64))}
			selectedBase.Descriptor = fresh
			cachedBase := selectedBase
			cachedBase.Descriptor = testProbeImageDescriptor(t, "linux/amd64")

			order := []string{}
			backend := providerBuildPreparationTestBackend(t, loaded, current, selectedBase, PreparedProviderBase{}, &order)
			cachedSelections, normalSelections := 0, 0
			// The configured local hit must not be consulted for fresh application builds.
			backend.selectCachedBase = func(_ context.Context, request providers.ResolvedRequestV1, _ deploy.ImageDescriptor) (SelectedProviderBase, bool, error) {
				order = append(order, "cached-select")
				cachedSelections++
				if !reflect.DeepEqual(request, loaded.Request) {
					t.Fatal("selected a cached base for a different request")
				}
				return cachedBase, true, nil
			}
			backend.selectBase = func(_ context.Context, request providers.ResolvedRequestV1) (SelectedProviderBase, error) {
				order = append(order, "select")
				normalSelections++
				if !reflect.DeepEqual(request, loaded.Request) {
					t.Fatal("selected a normal base for a different request")
				}
				return selectedBase, nil
			}
			currentChecks := 0
			backend.validateCurrent = func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
				currentChecks++
				if test.noCache {
					t.Fatal("no-cache build checked for a current generation")
				}
				return CurrentBuild{}, false, nil
			}
			observations, realizations := 0, 0
			stubApplicationPortableTargetForTest(t, &observations)
			backend.realizeBase = func(_ context.Context, _ providerstore.Store, selected SelectedProviderBase) (PreparedProviderBase, error) {
				order = append(order, "realize")
				realizations++
				if observations != 1 || !reflect.DeepEqual(selected.Descriptor, fresh) {
					t.Fatal("base realization did not follow application selection with the fresh descriptor")
				}
				image, err := realizedImageFromDescriptor(selected.Descriptor)
				return PreparedProviderBase{Plan: selected.Plan, Descriptor: selected.Descriptor, Config: selected.Config, Image: image, Catalog: []providers.RealizedOutput{}}, err
			}

			result, err := prepareLockedProviderBuildV1(t.Context(), input, backend)
			if err != nil {
				t.Fatal(err)
			}
			if cachedSelections != 0 || normalSelections != 1 || realizations != 1 || result.Current != nil || result.ApplicationTools == nil {
				t.Fatalf("application base selection counts/result = cached %d, normal %d, realized %d, current %#v, tools %#v", cachedSelections, normalSelections, realizations, result.Current, result.ApplicationTools)
			}
			if test.noCache && currentChecks != 0 || !test.noCache && currentChecks != 1 {
				t.Fatalf("current generation checks = %d", currentChecks)
			}
			if !reflect.DeepEqual(result.SelectedBase.Descriptor, fresh) {
				t.Fatalf("selected base descriptor = %#v, want normal fresh descriptor %#v", result.SelectedBase.Descriptor, fresh)
			}
			wantSealedBase, err := canonical.Marshal(fresh)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(result.ApplicationTools.sealed.baseBytes, wantSealedBase) {
				t.Fatal("application selection seal is not bound to the normal fresh base descriptor")
			}
			if len(order) < 3 || order[len(order)-1] != "realize" || order[0] != "recover" || order[1] != "load" {
				t.Fatalf("preparation order = %v", order)
			}
		})
	}
}
