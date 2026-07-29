package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func ignoreFinalizedCandidateRemoval(context.Context, BuiltImageCandidate) error {
	return nil
}

func TestCompleteProviderBuildOrdersValidationAssemblyAndPublication(t *testing.T) {
	input, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input.NoCache = true
	validationReference := providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: rendererDigest("a")}
	finalImage := providers.RealizedImageV1{Digest: rendererDigest("b"), ConfigDigest: rendererDigest("b"), RootFSSubject: input.Validation.Final.Image.Image.RootFSSubject}
	finalCandidate := BuiltImageCandidate{ImageID: finalImage.ConfigDigest}
	wantLock := deploy.BuildLockV1{Schema: deploy.BuildLockSchemaV1}
	wantState := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, input.Document),
		Platform: input.ResolvedRequest.Platform, Overlay: deploy.EmptyRequestOverlayV1(),
	}
	order := []string{}
	blueprintDigest := testResolvedBlueprintDigestV1(t, input.Document)
	backend := providerBuildCompletionBackend{
		validateAndFinalize: func(_ context.Context, gotStore providerstore.Store, layers []FullImageValidationInput, final FullImageValidationInput, validateOwner providers.RequirementProfileOwnerValidator, run FullImageValidationRunner, options RunOptions) (FinalizedBuildValidationResult, error) {
			order = append(order, "validate")
			if gotStore.Root() != store.Root() || !reflect.DeepEqual(layers, input.Validation.Layers) || !reflect.DeepEqual(final, input.Validation.Final) || validateOwner == nil || run == nil || options.Context == nil {
				t.Fatalf("validation arguments were not preserved")
			}
			return FinalizedBuildValidationResult{
				Validation: BuildValidationResult{Layers: []PublishedImageValidation{}, Final: PublishedImageValidation{Reference: validationReference}},
				Image:      InspectedImageCandidate{Image: finalImage},
				Candidate:  finalCandidate,
			}, nil
		},
		assemble: func(_ context.Context, gotStore providerstore.Store, got BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
			order = append(order, "assemble")
			if gotStore.Root() != store.Root() || got.BlueprintDigest != blueprintDigest || got.ValidationRecord != validationReference || got.FinalImage != finalImage || !reflect.DeepEqual(got.Graph, input.Graph) || !reflect.DeepEqual(got.RuntimePolicy, input.Validation.Final.RuntimePolicy) {
				t.Fatalf("assembly input = %#v", got)
			}
			return wantLock, nil
		},
		publish: func(_ context.Context, gotOperation *deploy.OperationLock, gotStore providerstore.Store, got BuildPublicationInput) (deploy.StateV1, error) {
			order = append(order, "publish")
			if gotOperation != operation || gotStore.Root() != store.Root() || got.Environment != input.Environment || got.DeploymentDir != input.DeploymentDir || !reflect.DeepEqual(got.Document, input.Document) || !reflect.DeepEqual(got.Lock, wantLock) || !got.NoCache {
				t.Fatalf("publication input = %#v", got)
			}
			return wantState, nil
		},
		removeFinalized: func(_ context.Context, candidate BuiltImageCandidate) error {
			order = append(order, "cleanup")
			if candidate != finalCandidate {
				t.Fatalf("finalized candidate cleanup = %#v", candidate)
			}
			return nil
		},
	}
	result, err := completeProviderBuild(t.Context(), operation, store, input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"validate", "assemble", "publish", "cleanup"}) || !reflect.DeepEqual(result, ProviderBuildCompletionResult{State: wantState, Lock: wantLock}) {
		t.Fatalf("order/result = %#v/%#v", order, result)
	}
}

func TestCompleteProviderBuildWarnsWhenPublishedFinalCandidateCleanupFails(t *testing.T) {
	input, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	cause := errors.New("injected final candidate cleanup failure")
	var progress bytes.Buffer
	input.RunOptions.Progress = &progress
	finalImage := providers.RealizedImageV1{
		Digest: rendererDigest("b"), ConfigDigest: rendererDigest("b"),
		RootFSSubject: input.Validation.Final.Image.Image.RootFSSubject,
	}
	wantLock := deploy.BuildLockV1{Schema: deploy.BuildLockSchemaV1}
	wantState := deploy.StateV1{Schema: deploy.StateSchemaV1}
	result, err := completeProviderBuild(
		t.Context(),
		operation,
		store,
		input,
		providerBuildCompletionBackend{
			validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, RunOptions) (FinalizedBuildValidationResult, error) {
				return FinalizedBuildValidationResult{
					Image:     InspectedImageCandidate{Image: finalImage},
					Candidate: BuiltImageCandidate{ImageID: finalImage.ConfigDigest},
				}, nil
			},
			assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
				return wantLock, nil
			},
			publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
				return wantState, nil
			},
			removeFinalized: func(context.Context, BuiltImageCandidate) error {
				return cause
			},
		},
	)
	if err != nil ||
		!reflect.DeepEqual(result, ProviderBuildCompletionResult{
			State: wantState, Lock: wantLock,
		}) ||
		!strings.Contains(progress.String(), "warning: build succeeded") ||
		!strings.Contains(progress.String(), cause.Error()) {
		t.Fatalf(
			"result = %#v, error = %v, progress = %q",
			result,
			err,
			progress.String(),
		)
	}
}

func TestCompleteProviderBuildValidationPublishesCandidateWithoutChangingCurrentState(t *testing.T) {
	input, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input.ValidateChoices = true
	var progress bytes.Buffer
	input.RunOptions.Progress = &progress
	validationReference := providerstore.StoreObjectRef{
		Kind: providerstore.ValidationRecordKind, Digest: rendererDigest("a"),
	}
	finalImage := providers.RealizedImageV1{
		Digest: rendererDigest("b"), ConfigDigest: rendererDigest("b"),
		RootFSSubject: input.Validation.Final.Image.Image.RootFSSubject,
	}
	lock := deploy.BuildLockV1{Schema: deploy.BuildLockSchemaV1}
	resolved, err := blueprint.EncodeResolvedDocumentV1(input.Document)
	if err != nil {
		t.Fatal(err)
	}
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: resolved, BlueprintSource: "test blueprint",
		Platform: input.ResolvedRequest.Platform, Overlay: input.Overlay,
		Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1},
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	record := deploy.ValidatedBuildV1{
		Schema:         deploy.ValidatedBuildSchemaV1,
		PendingCleanup: []deploy.ValidatedBuildReferenceV1{{ImageReference: "superseded"}},
	}
	cleanupCause := errors.New("injected validation candidate cleanup failure")
	publishedCurrent := false
	result, err := completeProviderBuild(t.Context(), operation, store, input, providerBuildCompletionBackend{
		validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, RunOptions) (FinalizedBuildValidationResult, error) {
			return FinalizedBuildValidationResult{
				Validation: BuildValidationResult{
					Layers: []PublishedImageValidation{},
					Final:  PublishedImageValidation{Reference: validationReference},
				},
				Image: InspectedImageCandidate{Image: finalImage},
			}, nil
		},
		assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
			return lock, nil
		},
		publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
			publishedCurrent = true
			return deploy.StateV1{}, nil
		},
		publishValidated: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string, deploy.BuildLockV1, ValidatedBuildInputsV1) (deploy.ValidatedBuildV1, error) {
			return record, nil
		},
		removeFinalized: func(context.Context, BuiltImageCandidate) error {
			return cleanupCause
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if publishedCurrent || !result.Validated || !reflect.DeepEqual(result.State, state) ||
		!reflect.DeepEqual(result.ValidatedBuild, record) ||
		!strings.Contains(progress.String(), "warning: validation succeeded") ||
		!strings.Contains(progress.String(), cleanupCause.Error()) {
		t.Fatalf(
			"published-current=%v result=%#v progress=%q",
			publishedCurrent,
			result,
			progress.String(),
		)
	}
}

func TestCompleteProviderBuildDoesNotAssembleOrPublishAfterValidationFailure(t *testing.T) {
	input, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	want := errors.New("full validation failed")
	assembled := false
	published := false
	_, err := completeProviderBuild(t.Context(), operation, store, input, providerBuildCompletionBackend{
		validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, RunOptions) (FinalizedBuildValidationResult, error) {
			return FinalizedBuildValidationResult{}, want
		},
		assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
			assembled = true
			return deploy.BuildLockV1{}, nil
		},
		publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
			published = true
			return deploy.StateV1{}, nil
		},
		removeFinalized: ignoreFinalizedCandidateRemoval,
	})
	if !errors.Is(err, want) || assembled || published {
		t.Fatalf("error=%v assembled=%v published=%v", err, assembled, published)
	}
}

func TestCompleteProviderBuildDoesNotPublishAfterAssemblyFailure(t *testing.T) {
	input, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	want := errors.New("lock assembly failed")
	published := false
	candidate := BuiltImageCandidate{
		ImageID:            rendererDigest("c"),
		TemporaryReference: temporaryBuildReferencePrefix + "12345678:finalize-output",
	}
	removed := false
	_, err := completeProviderBuild(t.Context(), operation, store, input, providerBuildCompletionBackend{
		validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, RunOptions) (FinalizedBuildValidationResult, error) {
			return FinalizedBuildValidationResult{Candidate: candidate}, nil
		},
		assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
			return deploy.BuildLockV1{}, want
		},
		publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
			published = true
			return deploy.StateV1{}, nil
		},
		removeFinalized: func(cleanupContext context.Context, got BuiltImageCandidate) error {
			removed = cleanupContext.Err() == nil && got == candidate
			return nil
		},
	})
	if !errors.Is(err, want) || published || !removed {
		t.Fatalf("error=%v published=%v removed=%v", err, published, removed)
	}
}

func TestCompleteProviderBuildRejectsValidationPlanDriftBeforeBackendWork(t *testing.T) {
	input, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input.Validation.Layers[0].Outputs = nil
	calls := 0
	_, err := completeProviderBuild(t.Context(), operation, store, input, providerBuildCompletionBackend{
		validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, RunOptions) (FinalizedBuildValidationResult, error) {
			calls++
			return FinalizedBuildValidationResult{}, nil
		},
		assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
			calls++
			return deploy.BuildLockV1{}, nil
		},
		publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
			calls++
			return deploy.StateV1{}, nil
		},
		removeFinalized: ignoreFinalizedCandidateRemoval,
	})
	if err == nil || !strings.Contains(err.Error(), "validation layer") || calls != 0 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestCompleteProviderBuildRejectsRuntimePlanDriftBeforeBackendWork(t *testing.T) {
	input, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	input.DockerPlan.TemporaryHome = "/mnt/changed-home"
	calls := 0
	_, err := completeProviderBuild(t.Context(), operation, store, input, providerBuildCompletionBackend{
		validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, RunOptions) (FinalizedBuildValidationResult, error) {
			calls++
			return FinalizedBuildValidationResult{}, nil
		},
		assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
			calls++
			return deploy.BuildLockV1{}, nil
		},
		publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
			calls++
			return deploy.StateV1{}, nil
		},
		removeFinalized: ignoreFinalizedCandidateRemoval,
	})
	if err == nil || !strings.Contains(err.Error(), "validation layer") || calls != 0 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestCompleteProviderBuildRejectsDocumentRequestDriftBeforeBackendWork(t *testing.T) {
	input, operation, store := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	application := input.Document.Environment.Applications["application"]
	application.Packages.Python.Requirements = append(application.Packages.Python.Requirements, "other==1.0")
	input.Document.Environment.Applications["application"] = application
	if err := input.Document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err := completeProviderBuild(t.Context(), operation, store, input, providerBuildCompletionBackend{
		validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, RunOptions) (FinalizedBuildValidationResult, error) {
			calls++
			return FinalizedBuildValidationResult{}, nil
		},
		assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
			calls++
			return deploy.BuildLockV1{}, nil
		},
		publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
			calls++
			return deploy.StateV1{}, nil
		},
		removeFinalized: ignoreFinalizedCandidateRemoval,
	})
	if err == nil || !strings.Contains(err.Error(), "resolved request does not match") || calls != 0 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestValidateProviderBuildCompletionRejectsBaseOnlyRuntimePolicyDrift(t *testing.T) {
	input, operation, _ := providerBuildCompletionFixture(t)
	defer operation.Unlock()
	baseImage, err := realizedImageFromDescriptor(input.Base)
	if err != nil {
		t.Fatal(err)
	}
	input.Graph.Bundles = []providers.ResolvedBundle{}
	input.Graph.Profiles = []providers.RequirementProfile{}
	input.Graph.ValidationEvidence = []providers.ValidationEvidence{}
	input.Graph.SelectedSources = []providers.ResolvedSourceInput{}
	for _, node := range input.Graph.Plan.Nodes {
		if node.ID == "base" {
			input.Graph.Plan.Nodes = []providers.NodeSpec{node}
			break
		}
	}
	input.Graph.Plan.Edges = []providers.ProviderEdgeV1{}
	input.Graph.PrefixImages = []providers.RealizedImageV1{baseImage}
	input.Graph.Materializations = []providers.GraphNodeMaterializeResult{}
	input.Graph.Catalog = append([]providers.RealizedOutput{}, input.BaseCatalog...)
	delete(input.Document.Environment.Applications, "application")
	if err := input.Document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	for _, component := range input.ResolvedRequest.Components {
		if component.Component == "base" {
			input.ResolvedRequest.Components = []providers.ResolvedComponentRequestV1{component}
			break
		}
	}
	input.ResolvedRequest.Sources = []providers.ResolvedSourceInput{}
	input.PackageOverrides = deploy.EmptyPackageOverrideIntentV1(input.Environment)
	policy, err := providerBuildRuntimePolicyV1(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Validation = ProviderGraphValidationPlan{Layers: []FullImageValidationInput{}, Final: FullImageValidationInput{
		Image: inspectedValidationCandidate(t, input.Base), Profiles: []providers.RequirementProfile{},
		Outputs: append([]providers.RealizedOutput{}, input.BaseCatalog...), RuntimePolicy: policy,
	}}
	input.Validation.Final.RuntimePolicy.Plans = append([]deploy.RuntimePlanV1{}, input.Validation.Final.RuntimePolicy.Plans...)
	input.Validation.Final.RuntimePolicy.Plans[0].Mounts = append([]deploy.RuntimeMountV1{}, input.Validation.Final.RuntimePolicy.Plans[0].Mounts...)
	input.Validation.Final.RuntimePolicy.Plans[0].Mounts[0].ReadOnly = !input.Validation.Final.RuntimePolicy.Plans[0].Mounts[0].ReadOnly
	if err := validateProviderBuildCompletionInput(input, policy); err == nil || !strings.Contains(err.Error(), "base-only") {
		t.Fatalf("base-only policy drift error = %v", err)
	}
}

func providerBuildCompletionFixture(t *testing.T) (ProviderBuildCompletionInput, *deploy.OperationLock, providerstore.Store) {
	t.Helper()
	fixture := newPreparedPythonGraphReuseFixture(t)
	bundle, err := providers.LoadResolvedBundleManifest(fixture.store, fixture.lock.Nodes[0].BundleManifest, pythonprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	layerDescriptor := providerBaseDescriptor(t, false)
	layerDescriptor.ConfigDigest = rendererDigest("d")
	layerDescriptor.AuthorReference = string(layerDescriptor.ConfigDigest)
	layerDescriptor.ImmutableReference = string(layerDescriptor.ConfigDigest)
	layerDescriptor.RootFSDiffIDs = []canonical.Digest{rendererDigest("e")}
	layerImage, err := realizedImageFromDescriptor(layerDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	baseImage, err := realizedImageFromDescriptor(fixture.lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	locked := fixture.lock.Nodes[0]
	graph := providers.GraphExecutionResult{
		Plan: fixture.request.Plan, SelectedEdges: append([]providers.ProviderEdgeV1{}, fixture.request.Plan.Edges...),
		SelectedSources: append([]providers.ResolvedSourceInput{}, bundle.Payload.SelectedSources...),
		Bundles:         []providers.ResolvedBundle{bundle}, Profiles: []providers.RequirementProfile{locked.RequirementProfile},
		ValidationEvidence: []providers.ValidationEvidence{locked.ValidationEvidence},
		PrefixImages:       []providers.RealizedImageV1{baseImage, layerImage},
		Materializations: []providers.GraphNodeMaterializeResult{{
			Image: layerImage, TransactionDigest: locked.TransactionDigest,
			GeneratedExecutables: []providers.RealizedGeneratedExecutable{}, Outputs: []providers.RealizedOutput{},
		}},
		Catalog: append([]providers.RealizedOutput{}, fixture.request.EarlierCatalog...),
	}
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{fixture.request.Platform}}},
		Environment: blueprint.Environment{
			ID: "demo",
			Applications: map[string]blueprint.Application{"application": {
				Packages: blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{
					Interpreter:  blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
					Requirements: []string{"demo-server==1.0"},
				}},
				Options: map[string]blueprint.ApplicationOption{},
			}},
			Base: blueprint.BaseComponent{
				Image:   "debian:bookworm-slim",
				Exports: map[string]blueprint.BaseExecutableExport{"python": {Executable: "/usr/bin/python3"}},
			},
		},
	}
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	dockerPlan := DockerExecutionPlan{}
	plans, err := RuntimePlansV1(document, dockerPlan)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := CompileRuntimePolicyV1(document, graph, plans)
	if err != nil {
		t.Fatal(err)
	}
	components := make([]providers.ResolvedComponentRequestV1, 0, len(graph.Plan.Nodes))
	for _, node := range graph.Plan.Nodes {
		for _, component := range node.Components {
			components = append(components, providers.ResolvedComponentRequestV1{Component: component, Provider: node.Provider, Request: node.Request})
		}
	}
	sort.Slice(components, func(left int, right int) bool { return components[left].Component < components[right].Component })
	overlay := deploy.EmptyRequestOverlayV1()
	overlayDigest, err := deploy.RequestOverlayDigestV1(overlay)
	if err != nil {
		t.Fatal(err)
	}
	request := providers.ResolvedRequestV1{
		Schema:        providers.ResolvedRequestSchemaV1,
		OverlayDigest: overlayDigest, Platform: fixture.request.Platform, Components: components,
		Sources: append([]providers.ResolvedSourceInput{}, fixture.request.SourceCandidates...),
	}
	candidate := inspectedValidationCandidate(t, layerDescriptor)
	validation := ProviderGraphValidationPlan{
		Layers: []FullImageValidationInput{{
			Image: candidate, Profiles: []providers.RequirementProfile{locked.RequirementProfile},
			Outputs: append([]providers.RealizedOutput{}, fixture.request.EarlierCatalog...), RuntimePolicy: policy,
		}},
	}
	validation.Final = validation.Layers[0]
	dir := t.TempDir()
	store, err := providerstore.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return ProviderBuildCompletionInput{
		Environment: "demo", DeploymentDir: dir, Document: document, DockerPlan: dockerPlan,
		ResolvedRequest: request, Overlay: overlay,
		PackageOverrides: fixture.lock.PackageOverrides,
		Base:             fixture.lock.Base, BaseCatalog: append([]providers.RealizedOutput{}, fixture.request.EarlierCatalog...),
		Graph: graph, Validation: validation,
		RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return nil, nil, nil
		},
	}, operation, store
}
