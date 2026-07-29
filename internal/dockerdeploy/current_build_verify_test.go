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
	store      providerstore.Store
	current    CurrentBuild
	runtime    CurrentRuntimePlanV1
	base       InspectedImageCandidate
	final      InspectedImageCandidate
	recordPath string
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
				if input.Image.Image != fixture.base.Image ||
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
		fixture.final.Image.ConfigDigest,
	}
	if !reflect.DeepEqual(inspected, wantInspected) ||
		validationCalls != 1 ||
		result.StoreObjects != 1 ||
		result.Images != 2 ||
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
	finalDescriptor := layerDescriptor
	finalDescriptor.AuthorReference = string(rendererDigest("c"))
	finalDescriptor.ImmutableReference = string(rendererDigest("c"))
	finalDescriptor.ConfigDigest = rendererDigest("c")
	lock.FinalImage = providers.RealizedImageV1{
		Digest: rendererDigest("c"), ConfigDigest: rendererDigest("c"),
		RootFSSubject: layerImage.RootFSSubject,
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
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	record := deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: layerImage.RootFSSubject,
		Profiles: []providers.ValidationEvidence{evidence}, RuntimePolicy: policyDigest,
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
			if input.Image.Image != layerImage ||
				len(input.Profiles) != 1 ||
				len(input.Outputs) != 0 {
				t.Fatalf("layer validation input = %#v", input)
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
			case final.Image.ConfigDigest:
				return final, nil
			default:
				return InspectedImageCandidate{}, errors.New("unexpected image")
			}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantInspected := []canonical.Digest{
		base.Image.ConfigDigest,
		layer.Image.ConfigDigest,
		final.Image.ConfigDigest,
	}
	if images != 3 ||
		validationCalls != 1 ||
		!reflect.DeepEqual(inspected, wantInspected) {
		t.Fatalf(
			"images=%d validation=%d inspected=%v",
			images,
			validationCalls,
			inspected,
		)
	}
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
	runtime := CurrentRuntimePlanV1{Document: document, Docker: DockerExecutionPlan{}}
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
	runtime := CurrentRuntimePlanV1{Document: document, Docker: DockerExecutionPlan{}}
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
	finalDescriptor := lock.Base
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
		runtime:    runtime,
		base:       base,
		final:      final,
		recordPath: recordPath,
	}
}
