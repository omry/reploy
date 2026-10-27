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
)

func buildIdentityDocumentV1(document blueprint.Document) blueprint.Document {
	document.Environment.Runtime.User = blueprint.DefaultRuntimeUser
	document.Environment.Runtime.IdentityPolicy = &blueprint.IdentityPolicyV1{
		Schema: blueprint.IdentityPolicySchemaV1,
		Users:  map[string]blueprint.IdentityPolicyUserV1{"worker": {UID: "200", PrimaryGroup: "worker", SupplementaryGroups: []string{}}},
		Groups: map[string]string{"worker": "200"},
		Roles: map[string]blueprint.IdentityPolicyRoleV1{
			"app":    {User: "worker", Capabilities: []string{}},
			"helper": {User: "worker", Capabilities: []string{"CAP_CHOWN"}},
		},
	}
	return document
}

func buildIdentityEvidenceV1(t *testing.T, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) deploy.ImageIdentityEvidenceV1 {
	t.Helper()
	evidence, err := deploy.VerifyImageIdentityFilesV1(policy, image, platform, map[string][]byte{
		"etc/passwd":        []byte("worker:x:200:200::/:/bin/false\n"),
		"etc/group":         []byte("worker:x:200:\n"),
		"etc/nsswitch.conf": []byte("passwd: files\ngroup: files\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func bindBuildIdentityFixtureV1(t *testing.T, current *CurrentBuild, document blueprint.Document) {
	t.Helper()
	policy := document.Environment.Runtime.IdentityPolicy
	current.State.Blueprint = testResolvedBlueprintV1(t, document)
	current.Lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, document)
	current.Lock.IdentityPolicy = &deploy.IdentityPolicyLockV1{
		Schema: deploy.IdentityPolicyLockSchemaV1, Policy: *policy,
		Evidence: buildIdentityEvidenceV1(t, policy, current.Lock.FinalImage, current.Lock.Platform),
	}
	refreshCurrentBuildReuseGeneration(t, current)
}

func TestIdentityPolicyLockV1CompletionCollectsExactFinalImageBeforePublication(t *testing.T) {
	for _, validate := range []bool{false, true} {
		for _, outcome := range []string{"success", "export failure", "upstream evidence", "cancel"} {
			t.Run(outcome+map[bool]string{true: "/validated", false: "/current"}[validate], func(t *testing.T) {
				input, operation, store := providerBuildCompletionFixture(t)
				defer operation.Unlock()
				input.Document = buildIdentityDocumentV1(input.Document)
				input.ValidateChoices = validate
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				finalImage := providers.RealizedImageV1{Digest: rendererDigest("b"), ConfigDigest: rendererDigest("b"), RootFSSubject: rendererDigest("c")}
				candidate := BuiltImageCandidate{ImageID: finalImage.ConfigDigest}
				order := []string{}
				var session any
				var locked *deploy.IdentityPolicyLockV1
				backend := providerBuildCompletionBackend{
					validateAndFinalize: func(ctx context.Context, _ providerstore.Store, _ []FullImageValidationInput, _ FullImageValidationInput, _ providers.RequirementProfileOwnerValidator, _ FullImageValidationRunner, _ deploy.ApplicationStartupVerifierV1, _ deploy.ApplicationLocalAccountV1, _ RunOptions) (FinalizedBuildValidationResult, error) {
						order = append(order, "finalize")
						session = ctx.Value(generatedBuilderSessionKey{})
						return FinalizedBuildValidationResult{Image: InspectedImageCandidate{Image: finalImage}, Candidate: candidate}, nil
					},
					identityEvidence: func(ctx context.Context, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) (deploy.ImageIdentityEvidenceV1, error) {
						order = append(order, "evidence")
						if session == nil || session != ctx.Value(generatedBuilderSessionKey{}) || image != finalImage || platform != input.ResolvedRequest.Platform || !reflect.DeepEqual(policy, input.Document.Environment.Runtime.IdentityPolicy) {
							t.Fatal("evidence did not use the exact final image, policy, platform and engine session")
						}
						if outcome == "export failure" {
							return deploy.ImageIdentityEvidenceV1{}, errors.New("export failed")
						}
						if outcome == "upstream evidence" {
							image = input.Validation.Final.Image.Image
						}
						if outcome == "cancel" {
							cancel()
						}
						return buildIdentityEvidenceV1(t, policy, image, platform), nil
					},
					assemble: func(_ context.Context, _ providerstore.Store, got BuildLockAssemblyInput) (deploy.BuildLockV1, error) {
						order = append(order, "assemble")
						locked = got.IdentityPolicy
						if locked == nil || locked.Evidence.Image != finalImage || got.FinalImage != finalImage {
							t.Fatal("assembly lost final-image evidence")
						}
						return deploy.BuildLockV1{IdentityPolicy: locked}, nil
					},
					publish: func(_ context.Context, _ *deploy.OperationLock, _ providerstore.Store, got BuildPublicationInput) (deploy.StateV1, error) {
						order = append(order, "publish")
						if got.Lock.IdentityPolicy != locked {
							t.Fatal("publication lost evidence")
						}
						return deploy.StateV1{}, nil
					},
					publishValidated: func(_ context.Context, _ *deploy.OperationLock, _ providerstore.Store, _, _ string, got deploy.BuildLockV1, _ ValidatedBuildInputsV1) (deploy.ValidatedBuildV1, error) {
						order = append(order, "publish")
						if got.IdentityPolicy != locked {
							t.Fatal("validated publication lost evidence")
						}
						return deploy.ValidatedBuildV1{}, nil
					},
					removeFinalized: func(ctx context.Context, got BuiltImageCandidate) error {
						order = append(order, "cleanup")
						if got != candidate || ctx.Err() != nil {
							t.Fatal("candidate cleanup lost ownership or cancellation independence")
						}
						return nil
					},
				}
				// Validated completion reads selected state after publication.
				if validate {
					if err := operation.CommitStateV1(nil, deploy.StateV1{Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, input.Document), Platform: input.ResolvedRequest.Platform, Overlay: input.Overlay}); err != nil {
						t.Fatal(err)
					}
				}
				_, err := completeProviderBuild(ctx, operation, store, input, backend)
				want := []string{"finalize", "evidence", "cleanup"}
				if outcome == "success" {
					want = []string{"finalize", "evidence", "assemble", "publish", "cleanup"}
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("failed evidence published a build")
				}
				if !reflect.DeepEqual(order, want) {
					t.Fatalf("effects = %v, want %v", order, want)
				}
			})
		}
	}
}

func TestIdentityPolicyLockV1ReuseAndRebindingRequireCompleteOriginalInventory(t *testing.T) {
	for _, change := range []string{"none", "metadata", "role", "capabilities", "unused profile", "profile role", "binary", "command selection", "drop policy", "selected bytes"} {
		t.Run(change, func(t *testing.T) {
			current, input := currentBuildReuseFixture(t)
			document := buildIdentityDocumentV1(input.Document)
			document.Environment.Applications = map[string]blueprint.Application{"application": {Packages: blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{Interpreter: blueprint.CommandRequirement{Command: "python"}}}, Executables: map[string]blueprint.Executable{
				"main":   {Source: "python", Binary: "main", Role: "app"},
				"unused": {Source: "python", Binary: "unused", Role: "helper"},
			}}}
			document.Environment.Commands = map[string]blueprint.Command{"serve": {Executable: "application.main"}}
			bindBuildIdentityFixtureV1(t, &current, document)
			// Decode a separate desired object so a mutation cannot rewrite the old authority.
			desired, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "metadata":
				desired.Environment.ControlScript = "new-control"
			case "role":
				desired.Environment.Runtime.IdentityPolicy.Roles["new"] = blueprint.IdentityPolicyRoleV1{User: "worker", Capabilities: []string{}}
			case "capabilities":
				desired.Environment.Runtime.IdentityPolicy.Roles["helper"] = blueprint.IdentityPolicyRoleV1{User: "worker", Capabilities: []string{"CAP_SETUID"}}
			case "unused profile":
				delete(desired.Environment.Applications["application"].Executables, "unused")
			case "profile role":
				p := desired.Environment.Applications["application"].Executables["unused"]
				p.Role = "app"
				desired.Environment.Applications["application"].Executables["unused"] = p
			case "binary":
				p := desired.Environment.Applications["application"].Executables["unused"]
				p.Binary = "replacement"
				desired.Environment.Applications["application"].Executables["unused"] = p
			case "command selection":
				desired.Environment.Commands["serve"] = blueprint.Command{Executable: "application.unused"}
			case "drop policy":
				desired.Environment.Runtime.IdentityPolicy = nil
				desired.Environment.Applications = nil
				desired.Environment.Commands = nil
			case "selected bytes":
				current.State.Blueprint = testResolvedBlueprintV1(t, func() blueprint.Document { d := desired; d.Environment.ControlScript = "tampered"; return d }())
			}
			rebound, err := rebindCurrentBuildLockV1(current, desired)
			if change == "none" || change == "metadata" {
				if err != nil || !reflect.DeepEqual(rebound.IdentityPolicy, current.Lock.IdentityPolicy) || rebound.BlueprintDigest != testResolvedBlueprintDigestV1(t, desired) {
					t.Fatalf("valid rebind failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("rebound changed policy/inventory or unauthenticated selected bytes")
			}
		})
	}
}

func TestIdentityPolicyLockV1ReuseAcceptsExactEvidenceAndRejectsMissingEvidence(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	input.Document = buildIdentityDocumentV1(input.Document)
	bindBuildIdentityFixtureV1(t, &current, input.Document)
	match, err := CurrentBuildMatches(current, input)
	if err != nil || !match {
		t.Fatalf("exact policy reuse: %v %v", match, err)
	}
	current.Lock.IdentityPolicy = nil
	refreshCurrentBuildReuseGeneration(t, &current)
	match, err = CurrentBuildMatches(current, input)
	if err == nil || match {
		t.Fatal("policy-bearing current reused without evidence")
	}
}

func TestIdentityPolicyLockV1CurrentLoadRejectsSelectedBlueprintSubstitutionBeforeImageEffects(t *testing.T) {
	dir, operation, store, lock, state := currentBuildFixture(t, false)
	defer operation.Unlock()
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document = buildIdentityDocumentV1(document)
	current := CurrentBuild{State: state, Generation: *state.Current, Lock: lock}
	bindBuildIdentityFixtureV1(t, &current, document)
	if _, err := operation.PublishBuildLock(current.Lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitStateV1(state.Current, current.State); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := LoadRecordedCurrentBuildV1(t.Context(), operation, store, "demo", dir)
	if err != nil || !found || loaded.Lock.IdentityPolicy == nil {
		t.Fatalf("valid policy load: %v", err)
	}
	old := current.State
	document.Environment.ControlScript = "substituted"
	current.State.Blueprint = testResolvedBlueprintV1(t, document)
	if err := operation.CommitStateV1(old.Current, current.State); err != nil {
		t.Fatal(err)
	}
	_, _, err = validateCurrentBuild(t.Context(), operation, store, "demo", dir, func(context.Context, providers.RealizedImageV1, string, string, string) error {
		t.Fatal("substituted blueprint reached image reference effects")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "selected blueprint") {
		t.Fatalf("substituted selected bytes accepted: %v", err)
	}
}

func TestIdentityPolicyLockV1DirectPublicationPersistsEvidenceAndRejectsMismatch(t *testing.T) {
	for _, validated := range []bool{false, true} {
		for _, mismatch := range []bool{false, true} {
			t.Run(map[bool]string{true: "validated", false: "current"}[validated]+map[bool]string{true: "/mismatch", false: "/exact"}[mismatch], func(t *testing.T) {
				dir, operation, store, lock, state := currentBuildFixture(t, false)
				defer operation.Unlock()
				document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
				if err != nil {
					t.Fatal(err)
				}
				document = buildIdentityDocumentV1(document)
				current := CurrentBuild{State: state, Generation: *state.Current, Lock: lock}
				bindBuildIdentityFixtureV1(t, &current, document)
				if _, err := operation.PublishBuildLock(current.Lock, registry.ValidateRequirementProfileV1); err != nil {
					t.Fatal(err)
				}
				if err := operation.CommitStateV1(state.Current, current.State); err != nil {
					t.Fatal(err)
				}
				// Keep a fully valid candidate evidence object with a different
				// capability policy. Only comparison with the selected document
				// can detect this substitution.
				if mismatch {
					other := buildIdentityDocumentV1(document).Environment.Runtime.IdentityPolicy
					other.Roles["app"] = blueprint.IdentityPolicyRoleV1{User: "worker", Capabilities: []string{"CAP_SETUID"}}
					current.Lock.IdentityPolicy = &deploy.IdentityPolicyLockV1{Schema: deploy.IdentityPolicyLockSchemaV1, Policy: *other, Evidence: buildIdentityEvidenceV1(t, other, current.Lock.FinalImage, current.Lock.Platform)}
				}
				effects := 0
				refs := fixedPublicationReferences(t, dir, 31)
				if validated {
					inputs, inputErr := ValidatedBuildInputs(document, state.Overlay, deploy.EmptyPackageOverridesV1("demo"), dir, state.Platform)
					if inputErr != nil {
						t.Fatal(inputErr)
					}
					_, err = publishValidatedBuild(t.Context(), operation, store, "demo", dir, current.Lock, inputs, publishValidatedBuildBackendV1{
						newReferences: func(string, string) (EnvironmentImageReferences, error) { effects++; return refs, nil },
						createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
							effects++
							return nil
						},
						removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error { effects++; return nil },
					})
				} else {
					_, err = publishBuild(t.Context(), operation, store, BuildPublicationInput{Environment: "demo", DeploymentDir: dir, Document: document, Lock: current.Lock}, buildPublicationBackend{
						newReferences: func(string, string) (EnvironmentImageReferences, error) { effects++; return refs, nil },
						createReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
							effects++
							return nil
						},
						removeReference: func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error {
							effects++
							return nil
						},
					})
				}
				if mismatch {
					if err == nil || effects != 0 {
						t.Fatalf("mismatched publication effects=%d error=%v", effects, err)
					}
				} else if err != nil || effects == 0 {
					t.Fatalf("valid policy publication effects=%d error=%v", effects, err)
				}
			})
		}
	}
}

func TestIdentityPolicyLockV1CurrentVerificationRejectsUnboundSelectedBytesBeforeEffects(t *testing.T) {
	fixture := baseOnlyCurrentBuildVerificationFixtureV1(t)
	document := buildIdentityDocumentV1(fixture.runtime.Document)
	bindBuildIdentityFixtureV1(t, &fixture.current, document)
	document.Environment.ControlScript = "tampered"
	fixture.current.State.Blueprint = testResolvedBlueprintV1(t, document)
	fixture.runtime.Document = document
	_, err := verifyLoadedCurrentBuildV1(t.Context(), CurrentBuildVerificationInputV1{
		Current: fixture.current, Runtime: fixture.runtime, Store: fixture.store,
		RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			t.Fatal("unbound selected bytes reached validation execution")
			return nil, nil, nil
		},
	}, currentBuildVerificationBackendV1{
		verifyClosure: func(deploy.BuildLockV1, providerstore.Store, providers.RequirementProfileOwnerValidator, providers.ResolvedBundleOwnerValidator) ([]providerstore.StoreObjectRef, error) {
			t.Fatal("unbound selected bytes reached store closure")
			return nil, nil
		},
		inspectImage: func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
			t.Fatal("unbound selected bytes reached engine inspection")
			return InspectedImageCandidate{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "selected blueprint") {
		t.Fatalf("unbound selected bytes accepted: %v", err)
	}
}

func TestIdentityPolicyLockV1AssemblyRejectsInvalidEvidenceBeforeStoreAccess(t *testing.T) {
	_, err := AssembleBuildLock(t.Context(), providerstore.Store{}, BuildLockAssemblyInput{IdentityPolicy: &deploy.IdentityPolicyLockV1{Schema: "invalid"}})
	if err == nil || !strings.Contains(err.Error(), "identity policy lock") {
		t.Fatalf("assembly reached graph/store work before rejecting invalid evidence: %v", err)
	}
}

func TestIdentityPolicyLockV1RestagingBuildsFreshWithoutTrustingOldBlueprint(t *testing.T) {
	for _, change := range []string{"add policy", "remove policy", "capabilities", "unused profile", "binary", "command selection", "metadata"} {
		t.Run(change, func(t *testing.T) {
			dir, operation, store, lock, state := currentBuildFixture(t, false)
			document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			current := CurrentBuild{State: state, Generation: *state.Current, Lock: lock}
			if change != "add policy" {
				document = buildIdentityDocumentV1(document)
				if change == "unused profile" || change == "binary" || change == "command selection" {
					document.Environment.Applications = map[string]blueprint.Application{"application": {
						Packages:    blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{Interpreter: blueprint.CommandRequirement{Command: "python"}}},
						Executables: map[string]blueprint.Executable{"main": {Source: "python", Binary: "main", Role: "app"}, "unused": {Source: "python", Binary: "unused", Role: "helper"}},
					}}
					document.Environment.Commands = map[string]blueprint.Command{"serve": {Executable: "application.main"}}
				}
				bindBuildIdentityFixtureV1(t, &current, document)
			}
			if _, err := operation.PublishBuildLock(current.Lock, registry.ValidateRequirementProfileV1); err != nil {
				t.Fatal(err)
			}
			if err := operation.CommitStateV1(state.Current, current.State); err != nil {
				t.Fatal(err)
			}
			if err := operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "add policy":
				document = buildIdentityDocumentV1(document)
			case "remove policy":
				document.Environment.Runtime.IdentityPolicy = nil
			case "capabilities":
				document.Environment.Runtime.IdentityPolicy.Roles["app"] = blueprint.IdentityPolicyRoleV1{User: "worker", Capabilities: []string{"CAP_SETUID"}}
			case "unused profile":
				delete(document.Environment.Applications["application"].Executables, "unused")
			case "binary":
				profile := document.Environment.Applications["application"].Executables["unused"]
				profile.Binary = "replacement"
				document.Environment.Applications["application"].Executables["unused"] = profile
			case "command selection":
				document.Environment.Commands["serve"] = blueprint.Command{Executable: "application.unused"}
			case "metadata":
				document.Environment.ControlScript = "new-control"
			}
			staged, err := deploy.SetDesiredStateV1(t.Context(), dir, document, state.Platform, registry.ValidatePackageRequest)
			if err != nil {
				t.Fatal(err)
			}
			if !staged.Changed || !reflect.DeepEqual(staged.State.Current, current.State.Current) {
				t.Fatal("restaging lost retained generation")
			}
			operation, err = deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer operation.Unlock()
			if _, _, err := LoadRecordedCurrentBuildV1(t.Context(), operation, store, "demo", dir); err == nil {
				t.Fatal("strict current load trusted mismatched selected bytes")
			}
			input, loaded, fixtureCurrent, selected, prepared := providerBuildPreparationFixture(t)
			input.Operation, input.Store, input.Environment, input.DeploymentDir = operation, store, "demo", dir
			loaded.State, loaded.Document = staged.State, document
			order := []string{}
			backend := providerBuildPreparationTestBackend(t, loaded, fixtureCurrent, selected, prepared, &order)
			backend.validateCurrent = func(ctx context.Context, op *deploy.OperationLock, store providerstore.Store, environment, dir string) (CurrentBuild, bool, error) {
				order = append(order, "current")
				return LoadRecordedCurrentBuildV1(ctx, op, store, environment, dir)
			}
			backend.cacheAvailable = func(deploy.BuildLockV1, providerstore.Store) (bool, error) {
				t.Fatal("unbound generation reached provider cache reuse")
				return false, nil
			}
			backend.matches = func(CurrentBuild, CurrentBuildReuseInput) (bool, error) {
				t.Fatal("unbound selected bytes reached image reuse")
				return false, nil
			}
			result, err := prepareLockedProviderBuildV1(t.Context(), input, backend)
			if err != nil {
				t.Fatal(err)
			}
			if result.Reused || result.ReusableLock != nil || result.PublicationLock != nil || result.PreparedBase == nil {
				t.Fatal("changed desired input did not select fresh build")
			}
			if !reflect.DeepEqual(order, []string{"recover", "load", "current", "select", "realize"}) {
				t.Fatalf("fresh build order = %v", order)
			}
			retained, found, err := operation.ReadStateV1()
			if err != nil || !found || !reflect.DeepEqual(retained.Current, current.State.Current) {
				t.Fatalf("fresh preparation lost old cleanup ownership: %v", err)
			}
			if _, found, err := operation.ReadBuildLock(retained.Current.BuildLockDigest, registry.ValidateRequirementProfileV1); err != nil || !found {
				t.Fatalf("old generation lock lost: %v", err)
			}
		})
	}
}
