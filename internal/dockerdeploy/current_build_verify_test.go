package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type currentBuildVerificationFixtureV1 struct {
	store        providerstore.Store
	current      CurrentBuild
	runtime      CurrentRuntimePlanV1
	base         InspectedImageCandidate
	runtimeImage InspectedImageCandidate
	final        InspectedImageCandidate
	recordPath   string
}

func TestVerifyLoadedCurrentBuildV1AuditsWithoutPublishing(t *testing.T) {
	fixture := baseOnlyCurrentBuildVerificationFixtureV1(t)
	before, err := os.Stat(fixture.recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var inspected []canonical.Digest
	validationCalls := 0
	result, err := verifyLoadedCurrentBuildV1(
		t.Context(),
		CurrentBuildVerificationInputV1{
			Store: fixture.store, Current: fixture.current, Runtime: fixture.runtime,
			RunValidation: func(_ context.Context, input FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
				validationCalls++
				if input.Image.Image != fixture.base.Image && input.Image.Image != fixture.runtimeImage.Image ||
					len(input.Profiles) != 0 ||
					len(input.Outputs) != 0 ||
					!reflect.DeepEqual(input.RuntimePolicy, fixture.current.Lock.RuntimePolicy) {
					t.Fatalf("validation input = %#v", input)
				}
				return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
			},
		},
		currentBuildVerificationBackendV1{
			verifyClosure: deploy.BuildLockStoreClosure,
			inspectImage: func(_ context.Context, candidate BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
				inspected = append(inspected, candidate.ImageID)
				if platform != fixture.current.Lock.Platform {
					t.Fatalf("inspection platform = %s", platform.Canonical)
				}
				switch candidate.ImageID {
				case fixture.base.Image.ConfigDigest:
					return fixture.base, nil
				case fixture.runtimeImage.Image.ConfigDigest:
					return fixture.runtimeImage, nil
				case fixture.final.Image.ConfigDigest:
					return fixture.final, nil
				default:
					return InspectedImageCandidate{}, errors.New("unexpected image")
				}
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(fixture.recordPath)
	if err != nil {
		t.Fatal(err)
	}
	wantInspected := []canonical.Digest{
		fixture.base.Image.ConfigDigest,
		fixture.runtimeImage.Image.ConfigDigest,
		fixture.final.Image.ConfigDigest,
	}
	if !reflect.DeepEqual(inspected, wantInspected) ||
		validationCalls != 2 ||
		result.StoreObjects != 1 ||
		result.Images != 3 ||
		result.Commands != 0 {
		t.Fatalf(
			"inspected=%v validation=%d result=%#v",
			inspected,
			validationCalls,
			result,
		)
	}
	if before.ModTime() != after.ModTime() || before.Size() != after.Size() {
		t.Fatalf("verification record changed: before=%#v after=%#v", before, after)
	}
}

func TestVerifyLoadedCurrentBuildV1RejectsFinalLabelDrift(t *testing.T) {
	fixture := baseOnlyCurrentBuildVerificationFixtureV1(t)
	delete(fixture.final.Labels, deploy.ValidationRecordLabel)
	_, err := verifyLoadedCurrentBuildV1(
		t.Context(),
		CurrentBuildVerificationInputV1{
			Store: fixture.store, Current: fixture.current, Runtime: fixture.runtime,
			RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
				return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
			},
		},
		currentBuildVerificationBackendV1{
			verifyClosure: deploy.BuildLockStoreClosure,
			inspectImage: func(_ context.Context, candidate BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
				if candidate.ImageID == fixture.base.Image.ConfigDigest {
					return fixture.base, nil
				}
				if candidate.ImageID == fixture.runtimeImage.Image.ConfigDigest {
					return fixture.runtimeImage, nil
				}
				return fixture.final, nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "missing reserved validation label") {
		t.Fatalf("label drift error = %v", err)
	}
}

func TestVerifyLoadedCurrentBuildV1ReportsStoreClosureFailureBeforeImages(t *testing.T) {
	fixture := baseOnlyCurrentBuildVerificationFixtureV1(t)
	want := errors.New("artifact digest changed")
	inspections := 0
	_, err := verifyLoadedCurrentBuildV1(
		t.Context(),
		CurrentBuildVerificationInputV1{
			Store: fixture.store, Current: fixture.current, Runtime: fixture.runtime,
			RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
				t.Fatal("store failure reached image validation")
				return nil, nil, nil
			},
		},
		currentBuildVerificationBackendV1{
			verifyClosure: func(
				deploy.BuildLockV1,
				providerstore.Store,
				providers.RequirementProfileOwnerValidator,
				providers.ResolvedBundleOwnerValidator,
			) ([]providerstore.StoreObjectRef, error) {
				return nil, want
			},
			inspectImage: func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
				inspections++
				return InspectedImageCandidate{}, nil
			},
		},
	)
	if !errors.Is(err, want) ||
		!strings.Contains(err.Error(), "provider store") ||
		inspections != 0 {
		t.Fatalf("inspections=%d error=%v", inspections, err)
	}
}

func TestVerifyLockedImagesV1ExplainsMissingProviderLayer(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	lock := fixture.lock
	if len(lock.Nodes) != 1 || lock.Nodes[0].Provider != blueprint.ComponentTypePython {
		t.Fatalf("Python fixture nodes = %#v", lock.Nodes)
	}
	baseImage, err := realizedImageFromDescriptor(lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	base := InspectedImageCandidate{
		Descriptor: lock.Base,
		Config: deploy.BaseConfig{
			Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
			Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
		},
		Labels: map[string]string{},
		Image:  baseImage,
	}
	layerID := lock.Nodes[0].Result.ConfigDigest
	_, err = verifyLockedImagesV1(
		t.Context(),
		lock,
		deploy.PrefixValidationV1{},
		func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			t.Fatal("missing provider layer reached content validation")
			return nil, nil, nil
		},
		func(_ context.Context, candidate BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
			if candidate.ImageID == lock.Base.ConfigDigest {
				return base, nil
			}
			return InspectedImageCandidate{}, &dockerImageNotFoundError{
				ImageID: candidate.ImageID,
				cause: errors.New(
					"docker image inspect: [] Error response from daemon: No such image",
				),
			}
		},
		nil,
		"",
		providerstore.Store{},
	)
	var missing *CurrentBuildImageMissingErrorV1
	if !errors.As(err, &missing) ||
		missing.Subject != "cached Python layer image" ||
		missing.ImageID != layerID {
		t.Fatalf("missing provider image error = %#v / %v", missing, err)
	}
	for _, want := range []string{
		"cached Python layer image",
		string(layerID),
		"missing from Docker",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing provider image error lacks %q: %v", want, err)
		}
	}
	for _, unwanted := range []string{
		string(lock.Nodes[0].NodeID),
		"inspect materialization candidate",
		"Error response from daemon",
		"[]",
	} {
		if strings.Contains(err.Error(), unwanted) {
			t.Fatalf("missing provider image error exposes %q: %v", unwanted, err)
		}
	}
}

func TestVerifyLockedImagesV1RerunsCumulativeLayerValidation(t *testing.T) {
	_, _, _, lock, _ := newPreparedAPTGraphReuseFixture(t)
	config := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
	}
	baseImage, err := realizedImageFromDescriptor(lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	base := InspectedImageCandidate{
		Descriptor: lock.Base, Config: config, Labels: map[string]string{},
		Image: baseImage,
	}
	layerDescriptor := lock.Base
	layerDescriptor.AuthorReference = string(rendererDigest("a"))
	layerDescriptor.ImmutableReference = string(rendererDigest("a"))
	layerDescriptor.ConfigDigest = rendererDigest("a")
	layerDescriptor.RootFSDiffIDs = []canonical.Digest{rendererDigest("b")}
	layerImage, err := realizedImageFromDescriptor(layerDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	lock.Nodes[0].Result = layerImage
	runtimeDescriptor := layerDescriptor
	runtimeDescriptor.AuthorReference = string(rendererDigest("c"))
	runtimeDescriptor.ImmutableReference = string(rendererDigest("c"))
	runtimeDescriptor.ConfigDigest = rendererDigest("c")
	runtimeDescriptor.RootFSDiffIDs = append(append([]canonical.Digest{}, layerDescriptor.RootFSDiffIDs...), rendererDigest("d"), rendererDigest("f"))
	runtimeImage, err := realizedImageFromDescriptor(runtimeDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	lock.RuntimeLayer = testApplicationRuntimeLayerV1(t, lock.Platform, layerImage, runtimeImage)
	finalDescriptor := runtimeDescriptor
	finalDescriptor.AuthorReference = string(rendererDigest("e"))
	finalDescriptor.ImmutableReference = string(rendererDigest("e"))
	finalDescriptor.ConfigDigest = rendererDigest("e")
	lock.FinalImage = providers.RealizedImageV1{
		Digest: rendererDigest("e"), ConfigDigest: rendererDigest("e"),
		RootFSSubject: runtimeImage.RootFSSubject,
	}
	profileDigest, err := providers.RequirementProfileDigest(
		lock.Nodes[0].RequirementProfile,
		registry.ValidateRequirementProfileV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := providers.NewValidationEvidence(layerImage.RootFSSubject, profileDigest)
	if err != nil {
		t.Fatal(err)
	}
	runtimeEvidence, err := providers.NewValidationEvidence(runtimeImage.RootFSSubject, profileDigest)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	record := deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: runtimeImage.RootFSSubject,
		Profiles: []providers.ValidationEvidence{runtimeEvidence}, RuntimePolicy: policyDigest,
		ExposedOutputs: []providers.ExecutableEvidence{},
	}
	referenceDigest, err := deploy.PrefixValidationDigest(record)
	if err != nil {
		t.Fatal(err)
	}
	lock.ValidationRecord = providerstore.StoreObjectRef{
		Kind: providerstore.ValidationRecordKind, Digest: referenceDigest,
	}
	layer := InspectedImageCandidate{
		Descriptor: layerDescriptor, Config: config, Labels: map[string]string{},
		Image: layerImage,
	}
	runtimeLayer := InspectedImageCandidate{
		Descriptor: runtimeDescriptor, Config: config, Labels: map[string]string{}, Image: runtimeImage,
	}
	finalLabels := map[string]string{}
	labels, err := deploy.PrefixValidationLabels(
		lock.FinalImage.RootFSSubject,
		lock.ValidationRecord,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range labels {
		finalLabels[label.Name] = label.Value
	}
	final := InspectedImageCandidate{
		Descriptor: finalDescriptor, Config: config, Labels: finalLabels,
		Image: lock.FinalImage,
	}
	var inspected []canonical.Digest
	validationCalls := 0
	images, err := verifyLockedImagesV1(
		t.Context(),
		lock,
		record,
		func(_ context.Context, input FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			validationCalls++
			if input.Image.Image != layerImage && input.Image.Image != runtimeImage ||
				len(input.Profiles) != 1 ||
				len(input.Outputs) != 0 {
				t.Fatalf("layer validation input = %#v", input)
			}
			if input.Image.Image == runtimeImage {
				return []providers.ValidationEvidence{runtimeEvidence}, []providers.ExecutableEvidence{}, nil
			}
			return []providers.ValidationEvidence{evidence}, []providers.ExecutableEvidence{}, nil
		},
		func(_ context.Context, candidate BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
			inspected = append(inspected, candidate.ImageID)
			switch candidate.ImageID {
			case base.Image.ConfigDigest:
				return base, nil
			case layer.Image.ConfigDigest:
				return layer, nil
			case runtimeLayer.Image.ConfigDigest:
				return runtimeLayer, nil
			case final.Image.ConfigDigest:
				return final, nil
			default:
				return InspectedImageCandidate{}, errors.New("unexpected image")
			}
		},
		nil,
		"",
		providerstore.Store{},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantInspected := []canonical.Digest{
		base.Image.ConfigDigest,
		layer.Image.ConfigDigest,
		runtimeLayer.Image.ConfigDigest,
		final.Image.ConfigDigest,
	}
	if images != 4 ||
		validationCalls != 2 ||
		!reflect.DeepEqual(inspected, wantInspected) {
		t.Fatalf(
			"images=%d validation=%d inspected=%v",
			images,
			validationCalls,
			inspected,
		)
	}
}

func TestVerifyLockedPortableRuntimeLayerV1AuditsLockedImageAndBytes(t *testing.T) {
	store, lock, image, payloads := currentPortableRuntimeVerificationFixtureV1(t)
	previousMaterialize := materializePortableRuntimePayloadsForCurrentBuildV1
	previousInventory := verifyPortableRuntimeInventoryForCurrentBuildV1
	t.Cleanup(func() {
		materializePortableRuntimePayloadsForCurrentBuildV1 = previousMaterialize
		verifyPortableRuntimeInventoryForCurrentBuildV1 = previousInventory
	})
	materializePortableRuntimePayloadsForCurrentBuildV1 = func(
		context.Context, providerstore.Store, providers.PortableToolLockV1,
	) (*PortableRuntimePayloadsV1, error) {
		return payloads, nil
	}
	verifiedInventory := false
	verifyPortableRuntimeInventoryForCurrentBuildV1 = func(
		context.Context, providerstore.Store, InspectedImageCandidate, []portableRuntimeInventoryEntryV1,
	) error {
		verifiedInventory = true
		return nil
	}
	if err := verifyLockedPortableRuntimeLayerV1(
		t.Context(), store, lock, lock.PortableRuntimeLayer.Upstream, image,
	); err != nil {
		t.Fatal(err)
	}
	if !verifiedInventory {
		t.Fatal("locked portable runtime inventory was not audited")
	}
}

func TestVerifyLockedPortableRuntimeLayerV1RejectsMissingLockedBytes(t *testing.T) {
	store, lock, image, payloads := currentPortableRuntimeVerificationFixtureV1(t)
	if err := payloads.Cleanup(); err != nil {
		t.Fatal(err)
	}
	previousMaterialize := materializePortableRuntimePayloadsForCurrentBuildV1
	t.Cleanup(func() { materializePortableRuntimePayloadsForCurrentBuildV1 = previousMaterialize })
	want := errors.New("open verified store artifact: selected runtime payload is missing")
	materializePortableRuntimePayloadsForCurrentBuildV1 = func(
		context.Context, providerstore.Store, providers.PortableToolLockV1,
	) (*PortableRuntimePayloadsV1, error) {
		return nil, want
	}
	err := verifyLockedPortableRuntimeLayerV1(
		t.Context(), store, lock, lock.PortableRuntimeLayer.Upstream, image,
	)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "selected bytes") {
		t.Fatalf("missing locked portable bytes error = %v", err)
	}
}

func currentPortableRuntimeVerificationFixtureV1(t *testing.T) (
	providerstore.Store, deploy.BuildLockV1, InspectedImageCandidate, *PortableRuntimePayloadsV1,
) {
	t.Helper()
	fixture := newPortableToolPythonLockedTestFixture(t)
	portableLock := providers.ClonePortableToolLockV1(fixture.lock)
	environment := []providers.PortableToolEnvironmentVariableV1{}
	for _, entry := range portableLock.Plan.PortableToolPlan.Tools {
		if entry.Runtime == nil {
			continue
		}
		environment = append(environment, entry.Runtime.Environment...)
	}
	payloads := &PortableRuntimePayloadsV1{
		Lock: portableLock, authorityLock: providers.ClonePortableToolLockV1(portableLock),
		Environment:          append([]providers.PortableToolEnvironmentVariableV1{}, environment...),
		authorityEnvironment: append([]providers.PortableToolEnvironmentVariableV1{}, environment...),
		sealedInventory: []portableRuntimeInventoryEntryV1{{
			path: "/opt/reploy/tools/playwright", kind: providerstore.ArchiveEntryKindDirectory,
		}},
	}
	upstreamDescriptor := testProbeImageDescriptor(t, "linux/amd64")
	upstream, err := realizedImageFromDescriptor(upstreamDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	resultDescriptor := upstreamDescriptor
	resultDescriptor.AuthorReference = string(rendererDigest("c"))
	resultDescriptor.ImmutableReference = string(rendererDigest("c"))
	resultDescriptor.ConfigDigest = rendererDigest("c")
	resultDescriptor.RootFSDiffIDs = append(append([]canonical.Digest{}, upstreamDescriptor.RootFSDiffIDs...), rendererDigest("d"))
	result, err := realizedImageFromDescriptor(resultDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := deploy.PortableRuntimeLayerTransactionDigestV1(fixture.lock, upstream, result)
	if err != nil {
		t.Fatal(err)
	}
	lock := deploy.BuildLockV1{
		PortableTools: &fixture.lock,
		PortableRuntimeLayer: &deploy.PortableRuntimeLayerV1{
			Schema: deploy.PortableRuntimeLayerSchemaV1, Upstream: upstream,
			Result: result, TransactionDigest: transaction,
		},
	}
	configEnvironment := make([]deploy.ConfigEnvironmentVariable, 0, len(payloads.Environment))
	for _, variable := range payloads.Environment {
		configEnvironment = append(configEnvironment, deploy.ConfigEnvironmentVariable{Name: variable.Name, Value: variable.Value})
	}
	return fixture.store, lock, InspectedImageCandidate{
		Descriptor: resultDescriptor,
		Config:     deploy.BaseConfig{Environment: configEnvironment},
		Image:      result,
	}, payloads
}

func TestVerifyLockedRuntimeV1ResolvesEveryCommandAndTrigger(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	if len(fixture.request.EarlierCatalog) == 0 {
		t.Fatal("fixture has no reusable output")
	}
	output := fixture.request.EarlierCatalog[0]
	output.SupplierComponent = "application/application/python"
	output.Name = "demo"
	output.Evidence.Output = providers.QualifiedOutput{
		Component: output.SupplierComponent,
		Name:      output.Name,
	}
	lock := fixture.lock
	lock.Catalog = []providers.RealizedOutput{output}
	document := commandTestDocument()
	runtime := CurrentRuntimePlanV1{Document: document, Docker: DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(1000, 1000)}}
	plans, err := RuntimePlansV1(document, runtime.Docker)
	if err != nil {
		t.Fatal(err)
	}
	lock.RuntimePolicy, err = CompileRuntimePolicyFromLockV1(document, lock, plans)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyLockedRuntimeV1(lock, runtime); err != nil {
		t.Fatal(err)
	}
	lock.Catalog = []providers.RealizedOutput{}
	if err := verifyLockedRuntimeV1(lock, runtime); err == nil ||
		!strings.Contains(err.Error(), "absent from the final provider graph") {
		t.Fatalf("missing command output error = %v", err)
	}
}

func baseOnlyCurrentBuildVerificationFixtureV1(t *testing.T) currentBuildVerificationFixtureV1 {
	t.Helper()
	dir := t.TempDir()
	store, lock := publicationLockFixture(t, dir, "4", "5", "6")
	lock.FinalImage.Digest = lock.FinalImage.ConfigDigest
	document, _ := testSelectedPlatformDocumentV1(t)
	resolved := testResolvedBlueprintV1(t, document)
	document, err := blueprint.DecodeResolvedDocumentV1(resolved)
	if err != nil {
		t.Fatal(err)
	}
	runtime := CurrentRuntimePlanV1{Document: document, Docker: DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(1000, 1000)}}
	plans, err := RuntimePlansV1(runtime.Document, runtime.Docker)
	if err != nil {
		t.Fatal(err)
	}
	lock.RuntimePolicy, err = CompileRuntimePolicyFromLockV1(document, lock, plans)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	validation := deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: lock.FinalImage.RootFSSubject,
		Profiles: []providers.ValidationEvidence{}, RuntimePolicy: policyDigest,
		ExposedOutputs: []providers.ExecutableEvidence{},
	}
	lock.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), store, validation)
	if err != nil {
		t.Fatal(err)
	}
	lockDigest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, dir, 0x71)
	generation := deploy.EnvironmentGenerationState{
		Reference: references.Generation, ImageDigest: lock.FinalImage.Digest,
		RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
		Platform: lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: resolved,
		Platform: lock.Platform, Overlay: lock.Overlay, Current: &generation,
	}
	if err := deploy.ValidateStateV1(state); err != nil {
		t.Fatal(err)
	}

	config := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
	}
	baseImage, err := realizedImageFromDescriptor(lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	base := InspectedImageCandidate{
		Descriptor: lock.Base, Config: config,
		Labels: map[string]string{"org.example.vendor": "inherited"},
		Image:  baseImage,
	}
	runtimeDescriptor := lock.Base
	runtimeDescriptor.RootFSDiffIDs = append(append([]canonical.Digest{}, lock.Base.RootFSDiffIDs...), rendererDigest("e"), rendererDigest("f"))
	runtimeDescriptor.AuthorReference = string(lock.RuntimeLayer.Result.ConfigDigest)
	runtimeDescriptor.ImmutableReference = string(lock.RuntimeLayer.Result.ConfigDigest)
	runtimeDescriptor.ConfigDigest = lock.RuntimeLayer.Result.ConfigDigest
	runtimeImage := InspectedImageCandidate{
		Descriptor: runtimeDescriptor, Config: config,
		Labels: map[string]string{"org.example.vendor": "inherited"}, Image: lock.RuntimeLayer.Result,
	}
	finalDescriptor := runtimeDescriptor
	finalDescriptor.AuthorReference = string(lock.FinalImage.ConfigDigest)
	finalDescriptor.ImmutableReference = string(lock.FinalImage.ConfigDigest)
	finalDescriptor.ConfigDigest = lock.FinalImage.ConfigDigest
	finalLabels := map[string]string{"org.example.vendor": "inherited"}
	labels, err := deploy.PrefixValidationLabels(
		lock.FinalImage.RootFSSubject,
		lock.ValidationRecord,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range labels {
		finalLabels[label.Name] = label.Value
	}
	final := InspectedImageCandidate{
		Descriptor: finalDescriptor, Config: config, Labels: finalLabels,
		Image: lock.FinalImage,
	}
	recordPath, err := store.ValidationRecordPath(lock.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	return currentBuildVerificationFixtureV1{
		store: store,
		current: CurrentBuild{
			State: state, Generation: generation, Lock: lock,
		},
		runtime:      runtime,
		base:         base,
		runtimeImage: runtimeImage,
		final:        final,
		recordPath:   recordPath,
	}
}
