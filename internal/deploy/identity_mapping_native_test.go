package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/identitymapping"
)

func nativeMappingFixtureV1(t *testing.T) (string, IdentityMappingOperationsV1, StateV1, *mappingOperationEvidenceV1, identitymapping.Geometry) {
	t.Helper()
	path, ops, state, evidence := operationFixtureV1(t, false, false)
	lock := identityPolicyBuildLockFixtureV1(t)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Runtime.IdentityPolicy = &lock.IdentityPolicy.Policy
	document.Environment.Runtime.User = blueprint.DefaultRuntimeUser
	state.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	lock.BlueprintDigest, err = blueprint.ResolvedDocumentDigestV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ops.Operation.PublishBuildLock(lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	state.Current = &EnvironmentGenerationState{Reference: state.Current.Reference, ImageDigest: lock.FinalImage.Digest, RootFSSubject: lock.FinalImage.RootFSSubject, Platform: lock.Platform, RuntimePolicyDigest: policy, BuildLockDigest: digest}
	state.Platform = lock.Platform
	writeMappingFixtureV1(t, path, state)
	ops.ValidateProfileOwner = acceptBuildLockProfile
	state, err = ops.UpgradeNativeMapping(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	evidence.handles = []string{"owned-data"}
	return path, ops, state, evidence, canonicalExecutionGeometryV1(t, executionInputV1(t).Candidate)
}

func nativeBindingV1(process string) NativeIdentityOperationV1 {
	return NativeIdentityOperationV1{Engine: "rootless-podman-fixture", Process: process, RuntimeUsers: []string{"worker"}, RuntimeGroups: []string{"worker"}}
}

func nativeEffectsV1(t *testing.T, ops IdentityMappingOperationsV1, state StateV1, candidate identitymapping.Geometry, evidence *mappingOperationEvidenceV1, process string) StateV1 {
	t.Helper()
	intent, err := ops.PrepareNativeExecution(t.Context(), state, candidate, process, nativeBindingV1(process))
	if err != nil {
		t.Fatal(err)
	}
	effects, err := ops.BeginEffects(t.Context(), intent, process)
	if err != nil {
		t.Fatal(err)
	}
	evidence.effects = true
	return effects
}

func TestNativeMappingV1AdmissionIsOneDurableTransaction(t *testing.T) {
	path, ops, state, evidence, candidate := nativeMappingFixtureV1(t)
	// Recover-only history is reserved, not previously admitted.
	reserved := mappingGeometryStateV1(executionGeometryV1([]uint32{600}, []uint32{600}))
	state.IdentityMapping.UIDAssignments, state.IdentityMapping.GIDAssignments = reserved.UIDs, reserved.GIDs
	writeMappingFixtureV1(t, path, state)
	effects := nativeEffectsV1(t, ops, state, candidate, evidence, "instance-one")
	if effects.IdentityMapping.AdmittedGeneration != nil || len(*effects.IdentityMapping.AcceptedUIDs) != 0 {
		t.Fatal("intent or effects preparation created admission")
	}
	admitted, err := ops.AdmitNativeExecution(t.Context(), effects, "instance-one")
	if err != nil {
		t.Fatal(err)
	}
	m := admitted.IdentityMapping
	if m.PendingOperation.Phase != "release-attempted" || !m.PendingOperation.Inhibited || m.AdmittedGeneration == nil || *m.NativeProcess != "instance-one" || *m.Engine != "rootless-podman-fixture" || m.Supervisor != nil {
		t.Fatalf("incomplete admission/release-attempt boundary: %#v", m)
	}
	if !sameExecutionGeometryV1(*m.acceptedGeometry(), candidate) || len(m.DormantUIDs)+len(m.DormantGIDs) != 0 || len(m.UIDAssignments) <= len(candidate.UIDs) {
		t.Fatal("admission promoted recover-only reservations or lost protected history")
	}
	before, _ := os.ReadFile(path)
	for _, observation := range []StateV1{effects, admitted} {
		if _, err := ops.AdmitNativeExecution(t.Context(), observation, "instance-one"); err == nil {
			t.Fatal("repeated admission issued a second release grant")
		}
	}
	if _, err := ops.Recover(t.Context(), admitted, "instance-one", true); err == nil {
		t.Fatal("admitted release attempt was cancelled as unused")
	}
	evidence.verifyErr = errors.New("release acknowledgement or owned retirement is unknown")
	if _, err := ops.Recover(t.Context(), admitted, "instance-one", false); err == nil {
		t.Fatal("uncertain release was forgotten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected retry/cancellation/recovery changed durable authority")
	}
	evidence.verifyErr = nil
	recovered, err := ops.Recover(t.Context(), admitted, "instance-one", false)
	if err != nil || recovered.IdentityMapping.PendingOperation != nil || !reflect.DeepEqual(recovered.IdentityMapping.AcceptedUIDs, m.AcceptedUIDs) || !reflect.DeepEqual(recovered.IdentityMapping.AcceptedGIDs, m.AcceptedGIDs) {
		t.Fatalf("checked exact acknowledgement/retirement recovery: %v", err)
	}
	if _, err := ops.PrepareNativeExecution(t.Context(), recovered, candidate, "second-operation", nativeBindingV1("instance-one")); err == nil {
		t.Fatal("same admitted instance acquired another release attempt")
	}
	if _, err := ops.PrepareNativeExecution(t.Context(), recovered, candidate, "instance-two", nativeBindingV1("instance-two")); err != nil {
		t.Fatalf("fresh inactive owned replacement was refused: %v", err)
	}
}

func TestNativeMappingV1NeverAdmittedRecoveryAndConfiguration(t *testing.T) {
	for _, phase := range []string{"intent", "effects-pending"} {
		t.Run(phase, func(t *testing.T) {
			_, ops, state, evidence, candidate := nativeMappingFixtureV1(t)
			intent, err := ops.PrepareNativeExecution(t.Context(), state, candidate, "recover", nativeBindingV1("recover"))
			if err != nil {
				t.Fatal(err)
			}
			if phase == "effects-pending" {
				intent, err = ops.BeginEffects(t.Context(), intent, "recover")
				if err != nil {
					t.Fatal(err)
				}
				evidence.effects = true
			}
			if phase == "intent" {
				if _, err := ops.Recover(t.Context(), intent, "recover", false); err == nil {
					t.Fatal("unexecuted intent was recovered as effects")
				}
			}
			recovered, err := ops.Recover(t.Context(), intent, "recover", phase == "intent")
			if err != nil || recovered.IdentityMapping.AdmittedGeneration != nil || len(*recovered.IdentityMapping.AcceptedUIDs) != 0 || len(*recovered.IdentityMapping.AcceptedGIDs) != 0 {
				t.Fatalf("recovery created admission: %v", err)
			}
			if phase == "effects-pending" && (!sameExecutionGeometryV1(recovered.IdentityMapping.geometry().numeric(), candidate) || !reflect.DeepEqual(recovered.IdentityMapping.RetainedStorage, evidence.handles)) {
				t.Fatal("recovery lost reserved IDs or owned objects")
			}
			// Configuration and B recovery can grow R even in v2, never P.
			configuration, err := ops.Configure(t.Context(), recovered, operationGeometryV1(100000), "configure")
			if err != nil {
				t.Fatal(err)
			}
			configuration, err = ops.BeginEffects(t.Context(), configuration, "configure")
			if err != nil {
				t.Fatal(err)
			}
			evidence.effects = true
			if _, err := ops.ObserveEffects(t.Context(), configuration, candidate, "configure"); err == nil {
				t.Fatal("configure accepted native projection semantics")
			}
			if _, err := ops.ObserveEffects(t.Context(), configuration, operationGeometryV1(100000), "configure"); err != nil {
				t.Fatal(err)
			}
			configuration, err = ops.Recover(t.Context(), configuration, "configure", false)
			if err != nil || configuration.IdentityMapping.AdmittedGeneration != nil || len(*configuration.IdentityMapping.AcceptedUIDs) != 0 {
				t.Fatalf("configuration recovery created P: %v", err)
			}
		})
	}
}

func TestNativeMappingV1AdmissionWriteFailureNeverGrantsRelease(t *testing.T) {
	for _, afterReplace := range []bool{false, true} {
		t.Run(fmt.Sprint(afterReplace), func(t *testing.T) {
			path, ops, state, evidence, candidate := nativeMappingFixtureV1(t)
			effects := nativeEffectsV1(t, ops, state, candidate, evidence, "uncertain")
			fault := errors.New("lost durable write answer")
			ops.write = func(path string, content []byte) error {
				if afterReplace {
					if err := writeAtomicStateFile(path, content, 0o600); err != nil {
						return err
					}
				}
				return fault
			}
			if _, err := ops.AdmitNativeExecution(t.Context(), effects, "uncertain"); !errors.Is(err, fault) {
				t.Fatalf("uncertain admission granted release: %v", err)
			}
			stored := readOperationStateV1(t, ops)
			if afterReplace {
				if stored.IdentityMapping.PendingOperation.Phase != "release-attempted" || stored.IdentityMapping.AdmittedGeneration == nil {
					t.Fatal("atomic replacement left partial native admission")
				}
				ops.write = nil
				if _, err := ops.AdmitNativeExecution(t.Context(), stored, "uncertain"); err == nil {
					t.Fatal("uncertain successful write was replayed")
				}
			} else if !reflect.DeepEqual(stored, effects) {
				t.Fatal("pre-replacement failure changed P or pending ownership")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("uncertain write removed state")
			}
		})
	}
}

func TestNativeMappingV1SelectedEvidenceIsFresh(t *testing.T) {
	for name, mutate := range map[string]func(*IdentityMappingOperationsV1, *StateV1, string){
		"no generation": func(_ *IdentityMappingOperationsV1, s *StateV1, _ string) { s.Current = nil },
		"legacy state": func(_ *IdentityMappingOperationsV1, s *StateV1, _ string) {
			s.IdentityMapping.Schema = IdentityMappingStateSchemaV1
			s.IdentityMapping.ProcessModel, s.IdentityMapping.AcceptedUIDs, s.IdentityMapping.AcceptedGIDs = "", nil, nil
		},
		"no validator": func(o *IdentityMappingOperationsV1, _ *StateV1, _ string) { o.ValidateProfileOwner = nil },
		"image": func(_ *IdentityMappingOperationsV1, s *StateV1, _ string) {
			s.Current.ImageDigest = buildLockTestDigest("a")
		},
		"rootfs": func(_ *IdentityMappingOperationsV1, s *StateV1, _ string) {
			s.Current.RootFSSubject = buildLockTestDigest("a")
		},
		"runtime policy": func(_ *IdentityMappingOperationsV1, s *StateV1, _ string) {
			s.Current.RuntimePolicyDigest = buildLockTestDigest("a")
		},
		"platform": func(_ *IdentityMappingOperationsV1, s *StateV1, _ string) {
			s.Current.Platform, _ = blueprint.ParsePlatform("linux/arm64")
		},
		"blueprint": func(_ *IdentityMappingOperationsV1, s *StateV1, _ string) {
			d, _ := blueprint.DecodeResolvedDocumentV1(s.Blueprint)
			d.Environment.Runtime.IdentityPolicy = nil
			s.Blueprint, _ = blueprint.EncodeResolvedDocumentV1(d)
		},
		"missing lock": func(_ *IdentityMappingOperationsV1, s *StateV1, _ string) {
			s.Current.BuildLockDigest = buildLockTestDigest("a")
		},
		"corrupt lock": func(_ *IdentityMappingOperationsV1, s *StateV1, path string) {
			p := filepath.Join(filepath.Dir(path), buildLockDirectoryName, buildLockFilename(s.Current.BuildLockDigest))
			if err := os.WriteFile(p, []byte("corrupt"), 0o600); err != nil {
				panic(err)
			}
		},
		"host authority": func(o *IdentityMappingOperationsV1, _ *StateV1, _ string) {
			o.hostCheck = func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
				return identitymapping.HostIdentity{}, errors.New("host authority changed")
			}
		},
		"no inventory": func(o *IdentityMappingOperationsV1, _ *StateV1, _ string) { o.Evidence = nil },
	} {
		t.Run(name, func(t *testing.T) {
			path, ops, state, _, candidate := nativeMappingFixtureV1(t)
			mutate(&ops, &state, path)
			writeMappingFixtureV1(t, path, state)
			before, _ := os.ReadFile(path)
			if _, err := ops.PrepareNativeExecution(t.Context(), state, candidate, "rejected", nativeBindingV1("rejected")); err == nil {
				t.Fatal("unverified native intent accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("failed evidence check changed authority")
			}
		})
	}
}

func TestNativeMappingV1UpgradePreservesLegacyReservationsAndPendingOwnership(t *testing.T) {
	for _, phase := range []string{"none", "intent", "effects-pending", "verified"} {
		t.Run(phase, func(t *testing.T) {
			path, ops, state, evidence := operationFixtureV1(t, false, false)
			wire := mappingGeometryStateV1(operationGeometryV1(100000))
			state.IdentityMapping.UIDAssignments, state.IdentityMapping.GIDAssignments = wire.UIDs, wire.GIDs
			state.IdentityMapping.RetainedStorage = []string{"historical-data"}
			writeMappingFixtureV1(t, path, state)
			var err error
			if phase != "none" {
				state, err = ops.Configure(t.Context(), state, operationGeometryV1(100000), "legacy-owner")
				if err != nil {
					t.Fatal(err)
				}
				if phase != "intent" {
					state, err = ops.BeginEffects(t.Context(), state, "legacy-owner")
					if err != nil {
						t.Fatal(err)
					}
					evidence.effects = true
				}
				if phase == "verified" {
					state.IdentityMapping.PendingOperation.Phase = phase
					writeMappingFixtureV1(t, path, state)
				}
			}
			prior := cloneMappingStateV1(state)
			upgraded, err := ops.UpgradeNativeMapping(t.Context(), state)
			if err != nil {
				t.Fatal(err)
			}
			m := upgraded.IdentityMapping
			if m.Schema != IdentityMappingStateSchemaV2 || m.acceptedGeometry() != nil || m.AdmittedGeneration != nil || !reflect.DeepEqual(m.UIDAssignments, prior.IdentityMapping.UIDAssignments) || !reflect.DeepEqual(m.RetainedStorage, prior.IdentityMapping.RetainedStorage) {
				t.Fatal("upgrade changed reservation or admission provenance")
			}
			if phase != "none" {
				if m.PendingOperation.OperationID != "legacy-owner" || m.PendingOperation.Phase != phase {
					t.Fatal("upgrade replaced pending owner")
				}
				if _, err := ops.Configure(t.Context(), upgraded, operationGeometryV1(100000), "legacy-owner"); phase == "intent" && err != nil {
					t.Fatal(err)
				}
				if _, err := ops.Recover(t.Context(), upgraded, "legacy-owner", phase == "intent"); err != nil {
					t.Fatalf("original owner cannot reconcile: %v", err)
				}
			}
		})
	}
	for _, binding := range []string{"supervisor", "engine", "admitted"} {
		t.Run(binding, func(t *testing.T) {
			path, ops, state, _ := operationFixtureV1(t, false, false)
			value := "legacy-binding"
			if binding == "supervisor" || binding == "admitted" {
				state.IdentityMapping.Supervisor = &value
			}
			if binding == "engine" || binding == "admitted" {
				state.IdentityMapping.Engine = &value
			}
			if binding == "admitted" {
				wire := mappingGeometryStateV1(operationGeometryV1(100000))
				state.IdentityMapping.UIDAssignments, state.IdentityMapping.GIDAssignments = wire.UIDs, wire.GIDs
				state.IdentityMapping.AdmittedGeneration = state.Current
			}
			writeMappingFixtureV1(t, path, state)
			before, _ := os.ReadFile(path)
			if _, err := ops.UpgradeNativeMapping(t.Context(), state); err == nil {
				t.Fatal("ambiguous/admitted legacy binding migrated")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected legacy upgrade changed authority")
			}
		})
	}
}

func TestNativeMappingV1RejectsStalePlanAndAdmissionEvidence(t *testing.T) {
	for _, failure := range []string{"stale-owner", "stale-generation", "stale-revision", "host-change", "late-host-change", "unknown-runtime", "stored-projection", "no-evidence", "unverified-instance", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			path, ops, state, evidence, candidate := nativeMappingFixtureV1(t)
			effects := nativeEffectsV1(t, ops, state, candidate, evidence, "fenced")
			expected := cloneMappingStateV1(effects)
			switch failure {
			case "stale-owner":
				expected.IdentityMapping.InstallationID = "different-owner"
			case "stale-generation":
				expected.Current.Reference += "-different"
			case "stale-revision":
				expected.IdentityMapping.Revision = "99"
			case "host-change":
				ops.hostCheck = func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
					return identitymapping.HostIdentity{}, errors.New("delegation changed")
				}
			case "late-host-change":
				calls := 0
				ops.hostCheck = func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
					calls++
					if calls > 2 {
						return identitymapping.HostIdentity{}, errors.New("delegation changed after observation")
					}
					return identitymapping.HostIdentity{Username: "operator", UID: 1000, GID: 1000}, nil
				}
			case "unknown-runtime":
				expected.IdentityMapping.PendingOperation.Native.RuntimeGroups = []string{"undeclared"}
				writeMappingFixtureV1(t, path, expected)
			case "stored-projection":
				expected.IdentityMapping.PendingOperation.Native.Active.UIDs = expected.IdentityMapping.PendingOperation.Native.Active.UIDs[:1]
				writeMappingFixtureV1(t, path, expected)
			case "no-evidence":
				ops.Evidence = nil
			case "unverified-instance":
				evidence.verifyErr = errors.New("owned instance not verified")
			case "cancelled":
			}
			ctx := t.Context()
			if failure == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before, _ := os.ReadFile(path)
			if _, err := ops.AdmitNativeExecution(ctx, expected, "fenced"); err == nil {
				t.Fatal("stale or unverified admission succeeded")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected admission changed reservations/provenance")
			}
		})
	}
}

func TestNativeMappingV1PrepareInputAndInventoryFailures(t *testing.T) {
	for _, failure := range []string{"operation-id", "engine-id", "process-id", "geometry", "runtime-user", "inventory-error", "inventory-order", "held-lock", "pending-build", "policy-lock", "invalid-observation"} {
		t.Run(failure, func(t *testing.T) {
			path, ops, state, evidence, candidate := nativeMappingFixtureV1(t)
			native := nativeBindingV1("prepare")
			operationID := "prepare"
			switch failure {
			case "operation-id":
				operationID = "-invalid"
			case "engine-id":
				native.Engine = "-invalid"
			case "process-id":
				native.Process = "-invalid"
			case "geometry":
				candidate.UIDs[0].Count = 0
			case "runtime-user":
				native.RuntimeUsers = []string{"undeclared"}
			case "inventory-error":
				evidence.planErr = errors.New("inventory unavailable")
			case "inventory-order":
				evidence.handles = []string{"b", "a"}
			case "held-lock":
				if err := ops.Operation.Unlock(); err != nil {
					t.Fatal(err)
				}
			case "pending-build":
				if err := ops.Operation.WritePendingBuild(validPendingBuild(t)); err != nil {
					t.Fatal(err)
				}
			case "policy-lock":
				lock, found, err := ops.Operation.ReadBuildLock(state.Current.BuildLockDigest, acceptBuildLockProfile)
				if err != nil || !found {
					t.Fatal(err)
				}
				lock.IdentityPolicy = nil
				state.Current.BuildLockDigest, err = ops.Operation.PublishBuildLock(lock, acceptBuildLockProfile)
				if err != nil {
					t.Fatal(err)
				}
				writeMappingFixtureV1(t, path, state)
			case "invalid-observation":
				state.IdentityMapping.AcceptedUIDs = nil
			}
			before, _ := os.ReadFile(path)
			if _, err := ops.PrepareNativeExecution(t.Context(), state, candidate, operationID, native); err == nil {
				t.Fatal("invalid or unowned native intent accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("failed prepare changed authority")
			}
			if _, err := ops.AdmitNativeExecution(t.Context(), state, operationID); err == nil {
				t.Fatal("missing pending operation admitted")
			}
		})
	}
}

func TestNativeMappingV1UpgradeRejectsStaleIntent(t *testing.T) {
	path, ops, state, _ := operationFixtureV1(t, false, false)
	intent, err := ops.Configure(t.Context(), state, operationGeometryV1(100000), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	intent.IdentityMapping.Revision = "99"
	writeMappingFixtureV1(t, path, intent)
	before, _ := os.ReadFile(path)
	if _, err := ops.UpgradeNativeMapping(t.Context(), intent); err == nil {
		t.Fatal("stale legacy intent was upgraded")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected upgrade changed legacy ownership")
	}
}
