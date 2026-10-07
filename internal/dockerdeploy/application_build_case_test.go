package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func TestValidateApplicationBuildCasePublicBoundaryV1(t *testing.T) {
	input, caseV1, _, _ := applicationCaseFixtureV1(t)
	if observations, err := ValidateApplicationBuildCaseV1(nil, input, caseV1, "application:demo"); err == nil || observations != nil {
		t.Fatal("missing context returned observations")
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	input.Preparation.Operation = operation
	observations, err := ValidateApplicationBuildCaseV1(t.Context(), input, caseV1, "application:demo")
	if err == nil || !strings.Contains(err.Error(), "source wheels must use an array") || observations != nil {
		t.Fatalf("public case caller did not delegate exact ordinary execution: observations=%v err=%v", observations, err)
	}
}

func applicationCaseFixtureV1(t *testing.T) (LockedProviderBuildExecutionInputV1, toolcatalog.IntegrationCaseV1, deploy.BuildLockV1, InspectedImageCandidate) {
	t.Helper()
	f := newPortableVerificationFixtureV1(t, false)
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	var caseV1 toolcatalog.IntegrationCaseV1
	for _, candidate := range cases {
		if candidate.Support.Context == "runtime" && candidate.Manifest.Tool == "playwright" && candidate.Fixture.Target == sourceBuilderJavaTarget("debian", "12") {
			caseV1 = candidate
			break
		}
	}
	if caseV1.ID == "" {
		t.Fatal("no representative application case")
	}
	closure, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, "application:demo")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := toolcatalog.CompilePortableToolPlanV1([]toolcatalog.SelectedClosureV1{closure})
	if err != nil {
		t.Fatal(err)
	}
	providerPlan := f.current.Lock.PortableTools.Plan.ProviderPlan
	domain := providers.PortableToolDomainAuthorityV1{ID: "application", Owner: "base"}
	dag, err := providers.BuildPortableToolProviderDAGV1(providerPlan, plan, []providers.PortableToolProviderDomainSetV1{{
		Scope: "application:demo", PackageManager: domain, Binding: domain, Filesystem: domain, Environment: domain, Exports: domain, Capabilities: domain,
	}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1([]toolcatalog.SelectedClosureV1{closure})
	if err != nil {
		t.Fatal(err)
	}
	acquisitions := []providers.PortableToolArtifactAcquisitionInputV1{}
	for _, artifact := range records.Artifacts {
		acquisitions = append(acquisitions, providers.PortableToolArtifactAcquisitionInputV1{
			Scope: closure.Scope, Tool: closure.Provenance.Tool, Artifact: artifact.Artifact, Descriptor: artifact.Descriptor, Source: artifact.Source,
			Provenance: providerstore.AcquisitionProvenance{Outcome: providerstore.AcquisitionOutcomeCacheHit, SourceID: artifact.Source.Reference.ID},
		})
	}
	tools, err := providers.BuildPortableToolLockV1(dag, records.Releases, acquisitions)
	if err != nil {
		t.Fatal(err)
	}
	lock := f.current.Lock
	lock.PortableTools = &tools
	layer := *lock.PortableRuntimeLayer
	layer.TransactionDigest, err = deploy.PortableRuntimeLayerTransactionDigestV1(tools, layer.Upstream, layer.Result)
	if err != nil {
		t.Fatal(err)
	}
	lock.PortableRuntimeLayer = &layer
	if err := deploy.ValidateBuildLockV1(lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	base := sourceBuilderCaseUpstreamForTest(t, caseV1)
	digest, err := blueprint.DocumentDigestV1(f.runtime.Document)
	if err != nil {
		t.Fatal(err)
	}
	selected := &ApplicationPortableToolPlanV1{Target: caseV1.Fixture.Target, Plan: plan, DAG: dag, Closures: []toolcatalog.SelectedClosureV1{closure},
		sealed: &applicationPortablePythonSelectionSealV1{documentDigest: digest, closures: []toolcatalog.SelectedClosureV1{closure}}}
	input := LockedProviderBuildExecutionInputV1{Preparation: LockedProviderBuildPreparationV1{
		Store: f.store, ApplicationTools: selected, SelectedBase: SelectedProviderBase{Descriptor: base},
		PreparedBase: &PreparedProviderBase{Descriptor: base}, BlueprintDigest: digest, Loaded: LoadedBuildRequestV1{Document: f.runtime.Document},
	}}
	return input, caseV1, lock, f.final
}

func TestApplicationBuildCasePreflightRejectsBeforeExecutionV1(t *testing.T) {
	for _, fault := range []string{"scope", "other application scope", "invalid scope", "build context", "manifest", "profile", "target", "base", "selection", "sealed selection", "document", "missing materialization", "cached generation", "missing closure", "duplicate closure", "different closure"} {
		t.Run(fault, func(t *testing.T) {
			input, caseV1, _, _ := applicationCaseFixtureV1(t)
			scope := "application:demo"
			switch fault {
			case "scope":
				scope = "source-builder:demo"
			case "other application scope":
				scope = "application:other"
			case "invalid scope":
				scope = "application:\xff"
			case "build context":
				caseV1 = buildIntegrationCaseForSourceBuilderTest(t)
			case "manifest":
				caseV1.ManifestReference.Digest = rendererDigest("0")
			case "profile":
				caseV1.Fixture.ValidationProfiles[0].Digest = rendererDigest("0")
			case "target":
				input.Preparation.ApplicationTools.Target.VersionID = "13"
			case "base":
				input.Preparation.PreparedBase.Descriptor.ManifestDigest = rendererDigest("0")
			case "selection":
				input.Preparation.ApplicationTools.Closures = nil
			case "sealed selection":
				input.Preparation.ApplicationTools.sealed.closures = nil
			case "document":
				input.Preparation.BlueprintDigest = rendererDigest("0")
			case "missing materialization":
				input.Preparation.PreparedBase = nil
			case "cached generation":
				input.Preparation.Reused = true
			case "missing closure":
				input.Preparation.ApplicationTools.Closures = nil
				input.Preparation.ApplicationTools.sealed.closures = nil
			case "duplicate closure":
				selected := input.Preparation.ApplicationTools
				selected.Closures = append(selected.Closures, selected.Closures[0])
				selected.sealed.closures = selected.Closures
			case "different closure":
				selected := input.Preparation.ApplicationTools
				selected.Closures[0].Provenance.Revision = "2"
				selected.sealed.closures = selected.Closures
			}
			called := false
			observations, err := validateApplicationBuildCaseV1(t.Context(), input, caseV1, scope,
				func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
					called = true
					return LockedProviderBuildExecutionResultV1{}, nil
				},
				ValidatePortableToolMaterializationV1)
			if err == nil || called || len(observations) != 0 {
				t.Fatalf("preflight did not fail before execution: called=%v observations=%v err=%v", called, observations, err)
			}
		})
	}
}

func TestApplicationBuildCaseRejectsCancelledExecutionV1(t *testing.T) {
	input, caseV1, _, _ := applicationCaseFixtureV1(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	observations, err := ValidateApplicationBuildCaseV1(ctx, input, caseV1, "application:demo")
	if !errors.Is(err, context.Canceled) || observations != nil {
		t.Fatalf("observations=%v err=%v", observations, err)
	}
}

func TestApplicationBuildCaseObservesExactFinalScopeAndFailsClosedV1(t *testing.T) {
	for _, fault := range []string{"", "wrong image", "missing payload", "missing profile", "wrong observation", "omitted observation", "failed profile", "callback omission", "callback repetition", "publication", "cleanup", "interrupted", "substituted result"} {
		t.Run(fault, func(t *testing.T) {
			input, caseV1, lock, image := applicationCaseFixtureV1(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			cause := errors.New(fault)
			validate := func(_ context.Context, _ providerstore.Store, view PortableToolMaterializationValidationInputV1) ([]providers.ValidationEvidence, error) {
				calls++
				if view.Image.Image != image.Image || len(view.selected) != len(caseV1.Profiles) {
					t.Fatal("callback changed image or profile set")
				}
				evidence := []providers.ValidationEvidence{}
				for _, selected := range view.selected {
					if selected.entry.Scope != "application:demo" {
						t.Fatal("whole-lock schedule reached application callback")
					}
					item, err := providers.NewPortableToolValidationEvidence(image.Image.RootFSSubject, selected.entry.Profile.Reference, selected.entry.Runtime)
					if err != nil {
						t.Fatal(err)
					}
					evidence = append(evidence, item)
				}
				if fault == "wrong observation" {
					evidence[0].SubjectRootFS = rendererDigest("0")
				}
				if fault == "omitted observation" {
					return nil, nil
				}
				if fault == "failed profile" {
					return nil, cause
				}
				return evidence, nil
			}
			execute := func(ctx context.Context, got LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
				observedImage, observedLock := image, lock
				if fault == "wrong image" {
					observedImage.Image.ConfigDigest = rendererDigest("0")
				}
				if fault == "missing payload" {
					observedLock.PortableRuntimeLayer = nil
				}
				if fault == "missing profile" {
					observedLock.PortableTools.Plan.PortableToolPlan.Tools[0].ValidationProfiles = nil
				}
				if fault != "callback omission" {
					if err := got.observeFinalImage(ctx, observedImage, observedLock); err != nil {
						return LockedProviderBuildExecutionResultV1{}, err
					}
					if fault == "callback repetition" {
						_ = got.observeFinalImage(ctx, observedImage, observedLock)
					}
				}
				if fault == "publication" || fault == "cleanup" {
					return LockedProviderBuildExecutionResultV1{}, cause
				}
				if fault == "interrupted" {
					cancel()
				}
				if fault == "substituted result" {
					observedLock.FinalImage.ConfigDigest = rendererDigest("0")
				}
				return LockedProviderBuildExecutionResultV1{Lock: observedLock}, nil
			}
			observations, err := validateApplicationBuildCaseV1(ctx, input, caseV1, "application:demo", execute, validate)
			if fault == "" {
				if err != nil || calls != 1 || len(observations) != len(caseV1.Profiles) {
					t.Fatalf("observations=%v calls=%d err=%v", observations, calls, err)
				}
			} else if err == nil || len(observations) != 0 {
				t.Fatalf("failure produced passing observations: %v %v", observations, err)
			}
		})
	}
}

func TestApplicationBuildCaseUsesMaterializationValidationBoundaryV1(t *testing.T) {
	input, caseV1, lock, image := applicationCaseFixtureV1(t)
	previousWorkspace, previousRun := preparePortableToolValidationWorkspace, runScheduledPortableToolValidationProfile
	t.Cleanup(func() {
		preparePortableToolValidationWorkspace, runScheduledPortableToolValidationProfile = previousWorkspace, previousRun
	})
	cleaned := false
	preparePortableToolValidationWorkspace = func(_ context.Context, _ providerstore.Store, platform blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return testPreparedProbeWorkspace(t, platform, t.TempDir()), func() error { cleaned = true; return nil }, nil
	}
	profiles := []string{}
	runScheduledPortableToolValidationProfile = func(_ context.Context, descriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace, profile toolcatalog.ValidationProfileRecordV1, runtime *providers.PortableToolRuntimeProjectionV1) (PortableToolProbeEvidenceV1, error) {
		if !reflect.DeepEqual(descriptor, image.Descriptor) {
			t.Fatal("profile ran on a different image")
		}
		profiles = append(profiles, profile.ID)
		return portableToolStubEvidenceV1(t, descriptor, profile, runtime, PortableToolProbeOutcomePassV1, len(profile.Probes)), nil
	}
	observations, err := validateApplicationBuildCaseV1(t.Context(), input, caseV1, "application:demo",
		func(ctx context.Context, got LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			if err := got.observeFinalImage(ctx, image, lock); err != nil {
				return LockedProviderBuildExecutionResultV1{}, err
			}
			if !cleaned {
				t.Fatal("validation workspace survived callback")
			}
			return LockedProviderBuildExecutionResultV1{Lock: lock}, nil
		}, ValidatePortableToolMaterializationV1)
	if err != nil || len(observations) != len(caseV1.Profiles) || len(profiles) != len(caseV1.Profiles) {
		t.Fatalf("observations=%v profiles=%v err=%v", observations, profiles, err)
	}
}

func TestCompleteProviderBuildFinalObservationPrecedesPublicationV1(t *testing.T) {
	for _, fault := range []string{"", "observation", "interrupted", "before observation"} {
		t.Run(fault, func(t *testing.T) {
			input, operation, store := providerBuildCompletionFixture(t)
			defer operation.Unlock()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			image := input.Validation.Final.Image
			lock := deploy.BuildLockV1{Schema: deploy.BuildLockSchemaV1}
			order := []string{}
			input.observeFinalImage = func(_ context.Context, got InspectedImageCandidate, gotLock deploy.BuildLockV1) error {
				order = append(order, "observe")
				if !reflect.DeepEqual(got, image) || !reflect.DeepEqual(gotLock, lock) {
					t.Fatal("observer did not receive exact finalization and assembly result")
				}
				if fault == "observation" {
					return errors.New(fault)
				}
				if fault == "interrupted" {
					cancel()
				}
				return nil
			}
			_, err := completeProviderBuild(ctx, operation, store, input, providerBuildCompletionBackend{
				validateAndFinalize: func(context.Context, providerstore.Store, []FullImageValidationInput, FullImageValidationInput, providers.RequirementProfileOwnerValidator, FullImageValidationRunner, deploy.ApplicationStartupVerifierV1, deploy.ApplicationLocalAccountV1, RunOptions) (FinalizedBuildValidationResult, error) {
					order = append(order, "finalize")
					return FinalizedBuildValidationResult{Image: image}, nil
				},
				assemble: func(context.Context, providerstore.Store, BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
					order = append(order, "assemble")
					if fault == "before observation" {
						cancel()
					}
					return lock, nil
				},
				publish: func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error) {
					order = append(order, "publish")
					return deploy.StateV1{}, nil
				},
				removeFinalized: func(context.Context, BuiltImageCandidate) error { order = append(order, "cleanup"); return nil },
			})
			want := []string{"finalize", "assemble", "observe", "cleanup"}
			if fault == "" {
				want = []string{"finalize", "assemble", "observe", "publish", "cleanup"}
			}
			if fault == "before observation" {
				want = []string{"finalize", "assemble", "cleanup"}
			}
			if !reflect.DeepEqual(order, want) || (err != nil) != (fault != "") {
				t.Fatalf("order=%v err=%v", order, err)
			}
		})
	}
}
