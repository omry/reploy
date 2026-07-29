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
	"github.com/omry/reploy/internal/providerstore"
)

func acceptanceBundle(transaction providers.MaterializationTransaction, platform blueprint.Platform) providers.ResolvedBundle {
	return providers.ResolvedBundle{
		Identity: transaction.Mounts[1].SourceDigest,
		Payload: providers.ResolvedBundleIdentityV1{
			NodeID: transaction.NodeID, RecipeVersion: transaction.RecipeVersion,
			Platform: platform, Upstream: transaction.Upstream,
			Artifacts: []providerstore.ArtifactDescriptor{}, Outputs: []providers.ResolvedOutput{},
		},
	}
}

func acceptedMaterializationCandidate(t *testing.T, transaction providers.MaterializationTransaction, platform blueprint.Platform) InspectedMaterializationLayerCandidate {
	t.Helper()
	key, keyDigest, err := MaterializationAssemblyKey(transaction, platform)
	if err != nil {
		t.Fatal(err)
	}
	disabledHealthcheck, err := canonicalizeDockerHealthcheck(dockerHealthcheck{Test: []string{"NONE"}})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := deploy.ImageDescriptor{
		Schema: deploy.ImageDescriptorSchemaV1, Platform: platform,
		AuthorReference: string(rendererDigest("7")), ImmutableReference: string(rendererDigest("7")),
		ConfigDigest: rendererDigest("7"), RootFSDiffIDs: []canonical.Digest{rendererDigest("8")},
	}
	rootFS, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	config := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		User: transaction.FinalImageConfig.User, WorkingDir: transaction.FinalImageConfig.WorkingDir,
		Entrypoint: []string{}, Command: []string{}, Healthcheck: disabledHealthcheck,
		StopSignal: transaction.FinalImageConfig.StopSignal, OnBuild: []string{}, Volumes: []string{},
	}
	return InspectedMaterializationLayerCandidate{
		AssemblyKey: key, AssemblyKeyDigest: keyDigest,
		Image: InspectedImageCandidate{
			Descriptor: descriptor, Config: config, Labels: map[string]string{},
			Image: providers.RealizedImageV1{Digest: descriptor.ConfigDigest, ConfigDigest: descriptor.ConfigDigest, RootFSSubject: rootFS},
		},
	}
}

func acceptedGeneratedExecutable(transaction providers.MaterializationTransaction) providers.RealizedGeneratedExecutable {
	declaration := transaction.GeneratedExecutables[0]
	return providers.RealizedGeneratedExecutable{
		Declaration: declaration,
		Evidence: providers.GeneratedExecutableEvidence{
			Schema: providers.GeneratedExecutableEvidenceSchemaV1, InvocationPath: declaration.Path,
			LinkChain: []providers.LinkEvidence{},
			Terminal: providers.GeneratedFileEvidence{
				Path: declaration.Path, Kind: "regular", Mode: "0755", Size: "100", SHA256: rendererDigest("9"),
			},
			Access: providers.PortableAccessEvidence{
				Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1,
				Paths: []providers.AccessPathEvidence{{Path: declaration.Path, Kind: "regular", Mode: "0755", Required: "other-read-execute"}},
			},
			Facts: providers.CanonicalProviderData{Schema: "python-generated-v1", Value: canonical.Object{}},
		},
	}
}

func TestAcceptMaterializationLayerRequiresCompleteGeneratedEvidence(t *testing.T) {
	transaction := rendererTransaction()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	input := MaterializationEvidenceInput{
		Candidate:   acceptedMaterializationCandidate(t, transaction, platform),
		Transaction: transaction, Bundle: acceptanceBundle(transaction, platform),
	}
	wantGenerated := acceptedGeneratedExecutable(transaction)
	result, err := AcceptMaterializationLayer(context.Background(), input, func(_ context.Context, got MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
		if !reflect.DeepEqual(got, input) {
			return nil, nil, errors.New("evidence input changed")
		}
		return []providers.RealizedGeneratedExecutable{wantGenerated}, []providers.RealizedOutput{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantTransactionDigest, err := providers.MaterializationTransactionDigest(transaction)
	if err != nil {
		t.Fatal(err)
	}
	if result.Image != input.Candidate.Image.Image || result.TransactionDigest != wantTransactionDigest || !reflect.DeepEqual(result.GeneratedExecutables, []providers.RealizedGeneratedExecutable{wantGenerated}) || len(result.Outputs) != 0 {
		t.Fatalf("accepted result = %#v", result)
	}

	result, err = AcceptMaterializationLayer(context.Background(), input, func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
		return []providers.RealizedGeneratedExecutable{}, []providers.RealizedOutput{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "count") || !reflect.DeepEqual(result, providers.GraphNodeMaterializeResult{}) {
		t.Fatalf("missing generated evidence result = %#v, error = %v", result, err)
	}
}

func TestAcceptMaterializationLayerRequiresEveryPublicOutput(t *testing.T) {
	transaction := rendererTransaction()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	bundle := acceptanceBundle(transaction, platform)
	bundle.Payload.Outputs = []providers.ResolvedOutput{{
		SupplierComponent: "web", SupplierNode: transaction.NodeID, Name: "serve",
		Candidate: providers.ExecutableCandidate{
			InvocationPath: "/opt/reploy/providers/python/web/bin/serve",
			Provenance:     providers.CanonicalProviderData{Schema: "python-entry-point-v1", Value: canonical.Object{}},
		},
	}}
	result, err := AcceptMaterializationLayer(context.Background(), MaterializationEvidenceInput{
		Candidate: acceptedMaterializationCandidate(t, transaction, platform), Transaction: transaction, Bundle: bundle,
	}, func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
		return []providers.RealizedGeneratedExecutable{acceptedGeneratedExecutable(transaction)}, []providers.RealizedOutput{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "do not match") || !reflect.DeepEqual(result, providers.GraphNodeMaterializeResult{}) {
		t.Fatalf("missing public output result = %#v, error = %v", result, err)
	}
}

func TestBuildAndAcceptMaterializationLayerRunsEvidenceAfterInspection(t *testing.T) {
	store, request := materializationLayerFixture(t)
	transaction := request.Transaction
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	wheel := providerstore.ArtifactDescriptor{LogicalPath: "hydra.whl", Kind: "wheel", Size: "10", SHA256: rendererDigest("6")}
	bundle := acceptanceBundle(transaction, request.Platform)
	bundle.Payload.RecipeVersion = "python-resolve-v1"
	bundle.Payload.Artifacts = []providerstore.ArtifactDescriptor{wheel, transaction.Script}
	order := []string{}
	result, err := buildAndAcceptMaterializationLayer(
		context.Background(), store, transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			order = append(order, "evidence")
			return []providers.RealizedGeneratedExecutable{acceptedGeneratedExecutable(transaction)}, []providers.RealizedOutput{}, nil
		}, nil, RunOptions{},
		func(_ providerstore.Store, got MaterializationLayerRequest, gotOptions RunOptions) (MaterializationLayerCandidate, error) {
			order = append(order, "build")
			if gotOptions.Context == nil || len(got.MountInputs) != 2 || got.MountInputs[0].ID != "script" || got.MountInputs[1].Files[0].Artifact != wheel {
				return MaterializationLayerCandidate{}, errors.New("mount inputs do not match the bundle")
			}
			key, digest, keyErr := MaterializationAssemblyKey(transaction, request.Platform)
			return MaterializationLayerCandidate{Built: BuiltImageCandidate{ImageID: rendererDigest("7")}, AssemblyKey: key, AssemblyKeyDigest: digest}, keyErr
		},
		func(_ context.Context, _ MaterializationLayerCandidate, got MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			order = append(order, "inspect")
			if !reflect.DeepEqual(got.Transaction, transaction) {
				return InspectedMaterializationLayerCandidate{}, errors.New("transaction changed before inspection")
			}
			return acceptedMaterializationCandidate(t, transaction, request.Platform), nil
		},
		func(_ context.Context, candidate BuiltImageCandidate, image providers.RealizedImageV1) error {
			order = append(order, "retain")
			if candidate.ImageID != rendererDigest("7") || image.ConfigDigest != rendererDigest("7") {
				return errors.New("retention did not receive the accepted image")
			}
			return nil
		},
		func(context.Context, BuiltImageCandidate) error {
			order = append(order, "remove")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"build", "inspect", "evidence", "retain"}) || result.Image.Digest != rendererDigest("7") {
		t.Fatalf("order = %#v; result = %#v", order, result)
	}
}

func TestBuildAndAcceptMaterializationLayerRejectsBindingBeforeBuild(t *testing.T) {
	store, request := materializationLayerFixture(t)
	bundle := acceptanceBundle(request.Transaction, request.Platform)
	bundle.Payload.NodeID = "python/other"
	called := false
	_, err := buildAndAcceptMaterializationLayer(
		context.Background(), store, request.Transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			called = true
			return nil, nil, nil
		}, nil, RunOptions{},
		func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
			called = true
			return MaterializationLayerCandidate{}, nil
		},
		func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			called = true
			return InspectedMaterializationLayerCandidate{}, nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			called = true
			return nil
		},
		func(context.Context, BuiltImageCandidate) error {
			called = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "bundle node") || called {
		t.Fatalf("error = %v; backend called = %t", err, called)
	}
}

func TestBuildAndAcceptMaterializationLayerRemovesRejectedCandidate(t *testing.T) {
	store, request := materializationLayerFixture(t)
	transaction := request.Transaction
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	wheel := providerstore.ArtifactDescriptor{LogicalPath: "hydra.whl", Kind: "wheel", Size: "10", SHA256: rendererDigest("6")}
	bundle := acceptanceBundle(transaction, request.Platform)
	bundle.Payload.Artifacts = []providerstore.ArtifactDescriptor{wheel, transaction.Script}
	wantCandidate := BuiltImageCandidate{ImageID: rendererDigest("7")}
	removed := false
	result, err := buildAndAcceptMaterializationLayer(
		context.Background(), store, transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			return nil, nil, errors.New("generated executable probe failed")
		}, nil, RunOptions{},
		func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
			key, digest, keyErr := MaterializationAssemblyKey(transaction, request.Platform)
			return MaterializationLayerCandidate{Built: wantCandidate, AssemblyKey: key, AssemblyKeyDigest: digest}, keyErr
		},
		func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			return acceptedMaterializationCandidate(t, transaction, request.Platform), nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			t.Fatal("rejected candidate was retained")
			return nil
		},
		func(ctx context.Context, got BuiltImageCandidate) error {
			removed = true
			if ctx.Err() != nil || got != wantCandidate {
				return errors.New("cleanup did not receive the exact candidate with a live context")
			}
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "generated executable probe failed") || !removed || !reflect.DeepEqual(result, providers.GraphNodeMaterializeResult{}) {
		t.Fatalf("result = %#v; removed = %t; error = %v", result, removed, err)
	}
}

func TestBuildAndAcceptMaterializationLayerRemovesAfterCancellation(t *testing.T) {
	store, request := materializationLayerFixture(t)
	transaction := request.Transaction
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	wheel := providerstore.ArtifactDescriptor{LogicalPath: "hydra.whl", Kind: "wheel", Size: "10", SHA256: rendererDigest("6")}
	bundle := acceptanceBundle(transaction, request.Platform)
	bundle.Payload.Artifacts = []providerstore.ArtifactDescriptor{wheel, transaction.Script}
	ctx, cancel := context.WithCancel(context.Background())
	removedWithLiveContext := false
	_, err := buildAndAcceptMaterializationLayer(
		ctx, store, transaction, bundle, request.Platform,
		func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
			t.Fatal("evidence ran after cancellation")
			return nil, nil, nil
		}, nil, RunOptions{},
		func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
			cancel()
			return MaterializationLayerCandidate{Built: BuiltImageCandidate{ImageID: rendererDigest("7")}}, nil
		},
		func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
			t.Fatal("inspection ran after cancellation")
			return InspectedMaterializationLayerCandidate{}, nil
		},
		func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error {
			t.Fatal("retention ran after cancellation")
			return nil
		},
		func(cleanupContext context.Context, _ BuiltImageCandidate) error {
			removedWithLiveContext = cleanupContext.Err() == nil
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) || !removedWithLiveContext {
		t.Fatalf("error = %v; removed with live context = %t", err, removedWithLiveContext)
	}
}

func TestRemoveBuiltImageCandidateUsesOnlyExactImageID(t *testing.T) {
	candidate := BuiltImageCandidate{ImageID: rendererDigest("7")}
	if err := removeBuiltImageCandidate(context.Background(), candidate, func(_ context.Context, args ...string) (string, error) {
		want := []string{"image", "rm", string(candidate.ImageID)}
		if !reflect.DeepEqual(args, want) {
			return "", errors.New("unexpected Docker command")
		}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := removeBuiltImageCandidate(context.Background(), BuiltImageCandidate{}, func(context.Context, ...string) (string, error) {
		t.Fatal("Docker ran for an invalid candidate")
		return "", nil
	}); err == nil || !strings.Contains(err.Error(), "image ID") {
		t.Fatalf("invalid candidate error = %v", err)
	}
}

func TestRemoveBuiltImageCandidateUsesExactOwnedReference(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := temporaryBuildOutputReference(store.Root(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	candidate := BuiltImageCandidate{
		ImageID:            rendererDigest("7"),
		TemporaryReference: reference,
		Workspace:          workspace,
	}
	var calls [][]string
	err = removeBuiltImageCandidate(t.Context(), candidate, func(
		_ context.Context,
		args ...string,
	) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if args[1] == "ls" {
			return string(candidate.ImageID), nil
		}
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "ls", "--quiet", "--no-trunc", candidate.TemporaryReference},
		{"image", "rm", candidate.TemporaryReference},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Docker calls = %v, want %v", calls, want)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("candidate workspace remains after cleanup: %v", err)
	}
}

func TestRemoveBuiltImageCandidateRejectsMismatchedWorkspaceReference(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = removeBuiltImageCandidate(t.Context(), BuiltImageCandidate{
		ImageID:            rendererDigest("7"),
		TemporaryReference: temporaryBuildReferencePrefix + "12345678:build-output",
		Workspace:          workspace,
	}, func(context.Context, ...string) (string, error) {
		called = true
		return "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "does not own") || called {
		t.Fatalf("called = %t, error = %v", called, err)
	}
}
