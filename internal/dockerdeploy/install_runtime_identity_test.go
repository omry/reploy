package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestBuildInstalledRuntimeIdentityV1ReusesMatchingAccount(t *testing.T) {
	current, _ := currentBuildReuseFixture(t)
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := buildInstalledRuntimeIdentityWithV1(
		t.Context(), store, current,
		DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(1000, 1000)},
		RunOptions{},
		installedRuntimeIdentityBackendV1{
			inspect: func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
				t.Fatal("matching account inspected Docker")
				return InspectedImageCandidate{}, nil
			},
			finalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, deploy.ApplicationStartupVerifierV1, deploy.ApplicationLocalAccountV1, RunOptions) (FinalizedBuildValidationResult, error) {
				t.Fatal("matching account rebuilt the runtime layer")
				return FinalizedBuildValidationResult{}, nil
			},
			remove: func(context.Context, BuiltImageCandidate) error { return nil },
		},
	)
	if err != nil || result.Adapted || !reflect.DeepEqual(result.Lock, current.Lock) {
		t.Fatalf("matching installed identity = %#v, %v", result, err)
	}
}

func TestValidateInstalledRuntimeIdentityBuildV1RejectsWrongPlannedAccount(t *testing.T) {
	current, _ := currentBuildReuseFixture(t)
	if err := validateInstalledRuntimeIdentityBuildV1(
		installedRuntimeIdentityBuildV1{Lock: current.Lock}, current,
		DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(2000, 3000)},
	); err == nil || !strings.Contains(err.Error(), "planned local account") {
		t.Fatalf("planned account mismatch error = %v", err)
	}
}

func TestBuildInstalledRuntimeIdentityV1RebuildsChangedAccount(t *testing.T) {
	current, _ := currentBuildReuseFixture(t)
	current.Lock.Base.AuthorReference = "debian:bookworm-slim"
	current.Lock.Base.ImmutableReference = "debian@" + string(rendererDigest("a"))
	current.Lock.Base.ManifestDigest = rendererDigest("a")
	baseImage, err := realizedImageFromDescriptor(current.Lock.Base)
	if err != nil {
		t.Fatal(err)
	}
	current.Lock.RuntimeLayer.Upstream = baseImage
	current.Lock.RuntimeLayer.TransactionDigest, err = deploy.ApplicationRuntimeLayerTransactionDigestV1(
		current.Lock.RuntimeLayer.Verifier,
		current.Lock.RuntimeLayer.Account,
		baseImage,
		current.Lock.Platform,
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wantAccount, err := applicationLocalAccountV1(testApplicationSandboxPlanV1(2000, 3000))
	if err != nil {
		t.Fatal(err)
	}
	wantCandidate := BuiltImageCandidate{ImageID: rendererDigest("e")}
	result, err := buildInstalledRuntimeIdentityWithV1(
		t.Context(), store, current,
		DockerExecutionPlan{Sandbox: testApplicationSandboxPlanV1(2000, 3000)},
		RunOptions{},
		installedRuntimeIdentityBackendV1{
			inspect: func(_ context.Context, candidate BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
				if candidate.ImageID != current.Lock.RuntimeLayer.Upstream.ConfigDigest || platform != current.Lock.Platform {
					t.Fatalf("upstream inspection = %#v / %#v", candidate, platform)
				}
				localDescriptor := current.Lock.Base
				localDescriptor.AuthorReference = string(localDescriptor.ConfigDigest)
				localDescriptor.ImmutableReference = string(localDescriptor.ConfigDigest)
				localDescriptor.ManifestDigest = ""
				return inspectedValidationCandidate(t, localDescriptor), nil
			},
			finalize: func(_ context.Context, gotStore providerstore.Store, layers []FullImageValidationInput, final FullImageValidationInput, validate providers.RequirementProfileOwnerValidator, run FullImageValidationRunner, verifier deploy.ApplicationStartupVerifierV1, account deploy.ApplicationLocalAccountV1, _ RunOptions) (FinalizedBuildValidationResult, error) {
				if gotStore.Root() != store.Root() || len(layers) != 0 || validate == nil || run == nil || verifier != current.Lock.RuntimeLayer.Verifier || account != wantAccount {
					t.Fatalf("finalization inputs were not preserved")
				}
				if !reflect.DeepEqual(final.Image.Descriptor, current.Lock.Base) || final.Image.Image != current.Lock.RuntimeLayer.Upstream || !reflect.DeepEqual(final.Outputs, current.Lock.Catalog) || !reflect.DeepEqual(final.RuntimePolicy, current.Lock.RuntimePolicy) {
					t.Fatalf("final validation input = %#v", final)
				}
				layer := current.Lock.RuntimeLayer
				layer.Account = account
				layer.TransactionDigest, err = deploy.ApplicationRuntimeLayerTransactionDigestV1(verifier, account, layer.Upstream, current.Lock.Platform)
				if err != nil {
					t.Fatal(err)
				}
				return FinalizedBuildValidationResult{
					RuntimeLayer: layer,
					Validation:   BuildValidationResult{Final: PublishedImageValidation{Reference: current.Lock.ValidationRecord}},
					Image:        InspectedImageCandidate{Image: current.Lock.FinalImage},
					Candidate:    wantCandidate,
				}, nil
			},
			remove: func(context.Context, BuiltImageCandidate) error {
				t.Fatal("successful adapted candidate was removed early")
				return nil
			},
		},
	)
	if err != nil || !result.Adapted || result.Candidate != wantCandidate || result.Lock.RuntimeLayer.Account != wantAccount {
		t.Fatalf("adapted installed identity = %#v, %v", result, err)
	}
}
