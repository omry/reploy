package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/omry/reploy/internal/toolcatalog"
)

func stubCaseEvidenceExecutorV1(t *testing.T, outcome string, cleanupErr error) *int {
	t.Helper()
	previousWorkspace, previousRun := preparePortableToolValidationWorkspace, runScheduledPortableToolValidationProfile
	t.Cleanup(func() {
		preparePortableToolValidationWorkspace, runScheduledPortableToolValidationProfile = previousWorkspace, previousRun
	})
	preparePortableToolValidationWorkspace = func(_ context.Context, _ providerstore.Store, platform blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return testPreparedProbeWorkspace(t, platform, t.TempDir()), func() error { return cleanupErr }, nil
	}
	calls := new(int)
	runScheduledPortableToolValidationProfile = func(_ context.Context, descriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace, profile toolcatalog.ValidationProfileRecordV1, runtime *providers.PortableToolRuntimeProjectionV1) (PortableToolProbeEvidenceV1, error) {
		*calls++
		observed := portableToolStubEvidenceV1(t, descriptor, profile, runtime, outcome, len(profile.Probes))
		output := newBoundedPortableToolProbeOutput(portableToolProbeOutputLimit, nil)
		_, _ = output.Write([]byte("original executor output\n"))
		observed.Results[0].Stdout = output.Evidence()
		return observed, nil
	}
	return calls
}

func applicationCaseObservationForTestV1(t *testing.T) (PortableToolCaseObservationV1, providerstore.Store, toolcatalog.IntegrationCaseV1) {
	t.Helper()
	input, caseV1, lock, image := applicationCaseFixtureV1(t)
	calls := stubCaseEvidenceExecutorV1(t, PortableToolProbeOutcomePassV1, nil)
	observation, err := observeApplicationBuildCaseV1(t.Context(), input, caseV1, "application:demo",
		func(ctx context.Context, got LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
			if err := got.observeFinalImage(ctx, image, lock); err != nil {
				return LockedProviderBuildExecutionResultV1{}, err
			}
			return LockedProviderBuildExecutionResultV1{Lock: lock}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if *calls != len(caseV1.Profiles) {
		t.Fatalf("executor called %d times, want %d", *calls, len(caseV1.Profiles))
	}
	return observation, input.Preparation.Store, caseV1
}

func TestPortableToolCaseEvidencePersistsOriginalOutputAndExactV1Bindings(t *testing.T) {
	observation, store, caseV1 := applicationCaseObservationForTestV1(t)
	closure, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, "application:demo")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := PersistPortableToolCaseEvidenceV1(t.Context(), store, observation)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := PersistPortableToolCaseEvidenceV1(t.Context(), store, observation); err != nil || again != ref {
		t.Fatalf("identical publication: %v %v", again, err)
	}
	loaded, err := store.LoadValidationRecord(ref)
	if err != nil || !bytes.Equal(loaded, observation.payload) {
		t.Fatalf("persisted bytes differ: %v", err)
	}
	var bundle portableToolCaseEvidenceBundleV1
	if err := json.Unmarshal(loaded, &bundle); err != nil {
		t.Fatal(err)
	}
	output := newBoundedPortableToolProbeOutput(portableToolProbeOutputLimit, nil)
	_, _ = output.Write([]byte("original executor output\n"))
	if bundle.Observations[0].Results[0].Stdout.Content != output.Evidence().Content {
		t.Fatal("original executor output was discarded or replaced")
	}
	record, err := MatchPortableToolCaseEvidenceV1(store, caseV1, "application:demo", ref)
	if err != nil {
		t.Fatal(err)
	}
	if record.Schema != toolcatalog.ValidationEvidenceSchemaV1 || record.Tool != caseV1.Manifest.Tool ||
		record.Version != caseV1.Manifest.Version || record.Revision != caseV1.Manifest.Revision ||
		record.ManifestDigest != caseV1.ManifestReference.Digest || record.SelectedClosureDigest != closure.Identity ||
		record.Context != caseV1.Support.Context || record.Target != caseV1.Fixture.Target ||
		record.BaseImageDigest != caseV1.Fixture.BaseImageDigest || record.Fixture != caseV1.Fixture.ID ||
		!reflect.DeepEqual(record.Bindings, caseV1.Support.Bindings) || !reflect.DeepEqual(record.Selections, caseV1.Support.Selections) ||
		record.ValidatorVersion != PortableToolCaseValidatorVersionV1 || record.Result != "pass" || record.ValidatorOutputDigest.Validate() != nil {
		t.Fatalf("incomplete bound evidence: %#v", record)
	}
	after, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, "application:demo")
	if err != nil || !reflect.DeepEqual(closure, after) {
		t.Fatalf("external evidence changed definition or closure identity: %v", err)
	}
	requests := []PortableToolCaseEvidenceRequestV1{{Case: caseV1, Scope: "application:demo"}}
	if err := RequireCurrentPortableToolCaseEvidenceV1(store, requests, map[canonical.Digest]providerstore.StoreObjectRef{caseV1.ID: ref}); err != nil {
		t.Fatal(err)
	}
}

func publishCaseBundleForTestV1(t *testing.T, store providerstore.Store, bundle portableToolCaseEvidenceBundleV1) providerstore.StoreObjectRef {
	t.Helper()
	encoded, err := canonical.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := canonical.Sum("portable-tool-case-evidence", portableToolCaseEvidenceSchemaV1, bundle)
	if err != nil {
		t.Fatal(err)
	}
	ref := providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: digest}
	if err := store.PublishValidationRecord(t.Context(), ref, encoded); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestPortableToolCaseEvidenceRejectsChangedBindingsAndFailedOutput(t *testing.T) {
	observation, store, caseV1 := applicationCaseObservationForTestV1(t)
	mutations := map[string]func(*portableToolCaseEvidenceBundleV1){
		"schema":                 func(b *portableToolCaseEvidenceBundleV1) { b.Schema = "old" },
		"case":                   func(b *portableToolCaseEvidenceBundleV1) { b.CaseID = rendererDigest("f") },
		"scope":                  func(b *portableToolCaseEvidenceBundleV1) { b.Scope = "application:other" },
		"tool":                   func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Tool = "other" },
		"version":                func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Version = "1.0.0" },
		"revision":               func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Revision = "2" },
		"manifest":               func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.ManifestDigest = rendererDigest("f") },
		"closure":                func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.SelectedClosureDigest = rendererDigest("f") },
		"context":                func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Context = "build" },
		"target":                 func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Target.OCIArchitecture = "arm64" },
		"base":                   func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.BaseImageDigest = rendererDigest("f") },
		"bindings":               func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Bindings = []string{"node"} },
		"selections":             func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Selections["browser"] = []string{"firefox"} },
		"fixture":                func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Fixture += "-other" },
		"validator":              func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.ValidatorVersion = "old" },
		"result":                 func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.Result = "fail" },
		"output digest":          func(b *portableToolCaseEvidenceBundleV1) { b.Evidence.ValidatorOutputDigest = rendererDigest("f") },
		"missing observations":   func(b *portableToolCaseEvidenceBundleV1) { b.Observations = nil },
		"duplicate observations": func(b *portableToolCaseEvidenceBundleV1) { b.Observations = append(b.Observations, b.Observations[0]) },
		"output bytes": func(b *portableToolCaseEvidenceBundleV1) {
			b.Observations[0].Results[0].Stdout.Content = "c3Vic3RpdHV0ZWQ="
		},
		"subject":  func(b *portableToolCaseEvidenceBundleV1) { b.Observations[0].SubjectRootFS = rendererDigest("f") },
		"executor": func(b *portableToolCaseEvidenceBundleV1) { b.Observations[0].ExecutorVersion = "old" },
		"network":  func(b *portableToolCaseEvidenceBundleV1) { b.Observations[0].Policy.NetworkDisabled = false },
		"profile":  func(b *portableToolCaseEvidenceBundleV1) { b.Observations[0].Profile.Digest = rendererDigest("f") },
		"probe failure": func(b *portableToolCaseEvidenceBundleV1) {
			b.Observations[0].Results[0].Outcome = PortableToolProbeOutcomeExitV1
		},
		"wrong observed platform": func(b *portableToolCaseEvidenceBundleV1) {
			b.Image.Platform = testProbeImageDescriptor(t, "linux/arm64").Platform
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var bundle portableToolCaseEvidenceBundleV1
			if err := json.Unmarshal(observation.payload, &bundle); err != nil {
				t.Fatal(err)
			}
			mutate(&bundle)
			if name != "output digest" {
				// A caller that recomputes the output identity still cannot
				// substitute failed, misattributed or invalid observations.
				digest, err := canonical.Sum("portable-tool-case-output", portableToolCaseEvidenceSchemaV1, bundle.Observations)
				if err != nil {
					t.Fatal(err)
				}
				bundle.Evidence.ValidatorOutputDigest = digest
			}
			// Even a new content-addressed reference cannot hide a changed binding.
			ref := publishCaseBundleForTestV1(t, store, bundle)
			if record, err := MatchPortableToolCaseEvidenceV1(store, caseV1, "application:demo", ref); err == nil || record.Schema != "" {
				t.Fatalf("mutation accepted: %#v %v", record, err)
			}
		})
	}
}

func TestPortableToolCaseEvidenceRejectsMissingStaleAndRecordOnlyEvidence(t *testing.T) {
	observation, store, caseV1 := applicationCaseObservationForTestV1(t)
	ref, err := PersistPortableToolCaseEvidenceV1(t.Context(), store, observation)
	if err != nil {
		t.Fatal(err)
	}
	wrongScope := "application:other"
	if _, err := MatchPortableToolCaseEvidenceV1(store, caseV1, wrongScope, ref); err == nil {
		t.Fatal("different current scope passed")
	}
	changed := caseV1
	changed.Fixture.BaseImageDigest = rendererDigest("f")
	if _, err := MatchPortableToolCaseEvidenceV1(store, changed, "application:demo", ref); err == nil {
		t.Fatal("stale current case passed")
	}
	missing := providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: rendererDigest("f")}
	if _, err := MatchPortableToolCaseEvidenceV1(store, caseV1, "application:demo", missing); err == nil {
		t.Fatal("missing record passed")
	}
	var bundle portableToolCaseEvidenceBundleV1
	if err := json.Unmarshal(observation.payload, &bundle); err != nil {
		t.Fatal(err)
	}
	encoded, err := canonical.Marshal(bundle.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishValidationRecord(t.Context(), missing, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := MatchPortableToolCaseEvidenceV1(store, caseV1, "application:demo", missing); err == nil {
		t.Fatal("hand-authored v1 record presence established support")
	}
	requests := []PortableToolCaseEvidenceRequestV1{{Case: caseV1, Scope: "application:demo"}}
	for _, refs := range []map[canonical.Digest]providerstore.StoreObjectRef{nil, {rendererDigest("f"): ref}, {caseV1.ID: ref, rendererDigest("f"): ref}} {
		if err := RequireCurrentPortableToolCaseEvidenceV1(store, requests, refs); err == nil {
			t.Fatal("incomplete or substituted current set passed")
		}
	}
	if err := RequireCurrentPortableToolCaseEvidenceV1(store, nil, nil); err == nil {
		t.Fatal("empty coverage passed")
	}
	if err := RequireCurrentPortableToolCaseEvidenceV1(store, append(requests, requests[0]), map[canonical.Digest]providerstore.StoreObjectRef{caseV1.ID: ref, rendererDigest("f"): ref}); err == nil {
		t.Fatal("duplicate expected case passed")
	}
}

func TestPortableToolCaseEvidenceRejectsCorruptAndNoncanonicalStoredBytes(t *testing.T) {
	for _, corruption := range []string{"content substitution", "unknown field", "duplicate field", "trailing value"} {
		t.Run(corruption, func(t *testing.T) {
			observation, store, caseV1 := applicationCaseObservationForTestV1(t)
			ref, err := PersistPortableToolCaseEvidenceV1(t.Context(), store, observation)
			if err != nil {
				t.Fatal(err)
			}
			payload := append([]byte(nil), observation.payload...)
			switch corruption {
			case "content substitution":
				payload = bytes.Replace(payload, []byte(`"result":"pass"`), []byte(`"result":"fail"`), 1)
			case "unknown field":
				payload = append([]byte(`{"unexpected":true,`), payload[1:]...)
			case "duplicate field":
				payload = append([]byte(`{"schema":"old",`), payload[1:]...)
			case "trailing value":
				payload = append(payload, []byte(`{}`)...)
			}
			path, err := store.ValidationRecordPath(ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := MatchPortableToolCaseEvidenceV1(store, caseV1, "application:demo", ref); err == nil {
				t.Fatal("corrupt or noncanonical workflow bytes passed")
			}
		})
	}
}

func TestPortableToolCaseEvidenceAtomicFailureReturnsNoReference(t *testing.T) {
	observation, store, _ := applicationCaseObservationForTestV1(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		ctx context.Context
		obs PortableToolCaseObservationV1
	}{{nil, observation}, {ctx, observation}, {t.Context(), PortableToolCaseObservationV1{}}} {
		if ref, err := PersistPortableToolCaseEvidenceV1(test.ctx, store, test.obs); err == nil || ref != (providerstore.StoreObjectRef{}) {
			t.Fatalf("failed publication returned reference: %v %v", ref, err)
		}
	}
	var bundle portableToolCaseEvidenceBundleV1
	if err := json.Unmarshal(observation.payload, &bundle); err != nil {
		t.Fatal(err)
	}
	digest, err := canonical.Sum("portable-tool-case-evidence", portableToolCaseEvidenceSchemaV1, bundle)
	if err != nil {
		t.Fatal(err)
	}
	ref := providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: digest}
	if _, err := store.LoadValidationRecord(ref); err == nil {
		t.Fatal("cancelled publication exposed a record")
	}
	if err := store.PublishValidationRecord(t.Context(), ref, []byte("conflicting bytes")); err != nil {
		t.Fatal(err)
	}
	if got, err := PersistPortableToolCaseEvidenceV1(t.Context(), store, observation); err == nil || got != (providerstore.StoreObjectRef{}) {
		t.Fatalf("conflicting publication passed: %v %v", got, err)
	}
	if preserved, err := store.LoadValidationRecord(ref); err != nil || string(preserved) != "conflicting bytes" {
		t.Fatal("publication failure changed existing record")
	}
}

func TestObserveSourceBuilderCaseCapturesOneExecutionAfterCleanup(t *testing.T) {
	for _, fault := range []string{"", "failed probe", "workspace cleanup", "image cleanup", "compact evidence only"} {
		t.Run(fault, func(t *testing.T) {
			tools, store := materializeSourceBuilderJavaForTest(t, &sourceBuilderMaterializationStub{}, "alpha")
			defer tools.Cleanup()
			caseV1 := buildIntegrationCaseForSourceBuilderTest(t)
			scope := tools.Plan.Recipes["alpha"].Scope
			stub := &sourceBuilderEnvironmentStub{}
			if fault == "image cleanup" {
				stub.removeErr = errors.New(fault)
			}
			stubSourceBuilderEnvironment(t, stub, sourceBuilderTestImageDescriptor(t, "2"))
			outcome, cleanupErr := PortableToolProbeOutcomePassV1, error(nil)
			if fault == "failed probe" {
				outcome = PortableToolProbeOutcomeExitV1
			}
			if fault == "workspace cleanup" {
				cleanupErr = errors.New(fault)
			}
			calls := stubCaseEvidenceExecutorV1(t, outcome, cleanupErr)
			if fault != "compact evidence only" {
				validateSourceBuilderMaterializationV1 = ValidatePortableToolMaterializationV1
			}
			observation, err := ObserveSourceBuilderBuildCaseV1(t.Context(), store, tools, sourceBuilderCaseUpstreamForTest(t, caseV1), RunOptions{}, caseV1, scope)
			if fault != "" {
				if err == nil || len(observation.payload) != 0 {
					t.Fatalf("failed lifecycle returned persistable observation: %v", err)
				}
				return
			}
			if err != nil || *calls != len(caseV1.Profiles) || len(stub.removed) != 1 {
				t.Fatalf("observation lifecycle: calls=%d removed=%d err=%v", *calls, len(stub.removed), err)
			}
			ref, err := PersistPortableToolCaseEvidenceV1(t.Context(), store, observation)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := MatchPortableToolCaseEvidenceV1(store, caseV1, scope, ref); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestObserveApplicationCaseCannotRetainFailedLifecycle(t *testing.T) {
	for _, fault := range []string{"missing callback", "probe", "cleanup", "publication", "cancel after callback"} {
		t.Run(fault, func(t *testing.T) {
			input, caseV1, lock, image := applicationCaseFixtureV1(t)
			outcome, cleanupErr := PortableToolProbeOutcomePassV1, error(nil)
			if fault == "probe" {
				outcome = PortableToolProbeOutcomeExitV1
			}
			if fault == "cleanup" {
				cleanupErr = errors.New(fault)
			}
			stubCaseEvidenceExecutorV1(t, outcome, cleanupErr)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			observation, err := observeApplicationBuildCaseV1(ctx, input, caseV1, "application:demo",
				func(ctx context.Context, got LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
					if fault != "missing callback" {
						if err := got.observeFinalImage(ctx, image, lock); err != nil {
							return LockedProviderBuildExecutionResultV1{}, err
						}
					}
					if fault == "publication" {
						return LockedProviderBuildExecutionResultV1{}, errors.New(fault)
					}
					if fault == "cancel after callback" {
						cancel()
					}
					return LockedProviderBuildExecutionResultV1{Lock: lock}, nil
				})
			if err == nil || len(observation.payload) != 0 {
				t.Fatalf("failed lifecycle produced persistable observation: %v", err)
			}
		})
	}
	input, caseV1, _, _ := applicationCaseFixtureV1(t)
	if observation, err := ObserveApplicationBuildCaseV1(nil, input, caseV1, "application:demo"); err == nil || len(observation.payload) != 0 {
		t.Fatal("nil context produced application observation")
	}
	if observation, err := ObserveApplicationBuildCaseV1(t.Context(), input, caseV1, "invalid"); err == nil || len(observation.payload) != 0 || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("unsupported application scope did not fail before execution: %v", err)
	}
}
