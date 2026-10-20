package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/omry/reploy/internal/blueprint"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mappingStateFixtureV1(t *testing.T, installed bool) (string, *OperationLock, StateV1) {
	t.Helper()
	dir := t.TempDir()
	path := writeOverlayTestState(t, dir)
	state := readOverlayTestState(t, path)
	state.Staging = &StagingStateV1{Schema: StagingStateSchemaV1}
	state.BlueprintSource = "retained blueprint.yaml"
	owner, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	state.IdentityMapping = &IdentityMappingStateV1{
		Schema: IdentityMappingStateSchemaV1, InstallationID: "mapping-fixture", OwnerDir: owner,
		Revision: "1", OperatorUID: "1000", OperatorGID: "1000", Profile: "exact",
	}
	if installed {
		installation := installationStateV1Fixture(dir)
		state.Staging = nil
		state.Deployment = &DeploymentStateV1{Schema: DeploymentStateSchemaV1, Installation: installation}
		state.IdentityMapping.InstallationBinding = &installation.InstanceID
	}
	writeMappingFixtureV1(t, path, state)
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })
	return path, lock, state
}

func writeMappingFixtureV1(t *testing.T, path string, state StateV1) {
	t.Helper()
	content, err := EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityMappingOptionalStateAndStrictValidation(t *testing.T) {
	path, _, state := mappingStateFixtureV1(t, false)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeStateV1(content)
	if err != nil || !reflect.DeepEqual(state, decoded) {
		t.Fatalf("round trip: %v", err)
	}
	for name, mutate := range map[string]func(*StateV1){
		"schema":                func(s *StateV1) { s.IdentityMapping.Schema = "future" },
		"profile":               func(s *StateV1) { s.IdentityMapping.Profile = "implicit" },
		"missing owner kind":    func(s *StateV1) { s.Staging = nil },
		"zero revision":         func(s *StateV1) { s.IdentityMapping.Revision = "0" },
		"relative owner":        func(s *StateV1) { s.IdentityMapping.OwnerDir = "relative" },
		"root operator":         func(s *StateV1) { s.IdentityMapping.OperatorUID = "0" },
		"staged active binding": func(s *StateV1) { id := "service"; s.IdentityMapping.InstallationBinding = &id },
		"one assignment domain": func(s *StateV1) {
			s.IdentityMapping.UIDAssignments = []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "1"}}
		},
		"admission without bindings": func(s *StateV1) { s.IdentityMapping.AdmittedGeneration = s.Current },
		"dormant without admission": func(s *StateV1) {
			s.IdentityMapping.DormantUIDs = []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "1"}}
		},
		"duplicate retained handle": func(s *StateV1) { s.IdentityMapping.RetainedStorage = []string{"x", "x"} },
		"unsafe pending":            func(s *StateV1) { s.IdentityMapping.PendingOperation = &IdentityMappingOperationV1{} },
		"wrong installed binding": func(s *StateV1) {
			s.Staging = nil
			s.Deployment = &DeploymentStateV1{Schema: DeploymentStateSchemaV1, Installation: installationStateV1Fixture(s.IdentityMapping.OwnerDir)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var next StateV1
			if err := json.Unmarshal(content, &next); err != nil {
				t.Fatal(err)
			}
			mutate(&next)
			if _, err := EncodeStateV1(next); err == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
	state.IdentityMapping = nil
	ordinary, err := EncodeStateV1(state)
	if err != nil || strings.Contains(string(ordinary), "identity_mapping") {
		t.Fatalf("ordinary representation changed: %s %v", ordinary, err)
	}
}

func TestIdentityMappingCompareRevisionAndGeneration(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, false)
	before, _ := os.ReadFile(path)
	for name, mutate := range map[string]func(*StateV1){
		"omitted": func(s *StateV1) { s.IdentityMapping = nil },
		"stale":   func(s *StateV1) { m := *s.IdentityMapping; m.Revision = "2"; s.IdentityMapping = &m },
		"replace": func(s *StateV1) { m := *s.IdentityMapping; m.InstallationID = "other"; s.IdentityMapping = &m },
	} {
		t.Run(name, func(t *testing.T) {
			next := state
			mutate(&next)
			if err := lock.CommitStateV1(state.Current, next); err == nil {
				t.Fatal("authority overwrite accepted")
			}
		})
	}
	if _, err := lock.CommitIdentityMappingV1(nil, state.IdentityMapping.InstallationID, 1, *state.IdentityMapping); err == nil {
		t.Fatal("stale generation accepted")
	}
	if _, err := lock.CommitIdentityMappingV1(state.Current, "other", 1, *state.IdentityMapping); err == nil {
		t.Fatal("foreign owner accepted")
	}
	if _, err := lock.CommitIdentityMappingV1(state.Current, state.IdentityMapping.InstallationID, 2, *state.IdentityMapping); err == nil {
		t.Fatal("stale revision accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("rejected writes changed state")
	}
	next, err := lock.CommitIdentityMappingV1(state.Current, state.IdentityMapping.InstallationID, 1, *state.IdentityMapping)
	if err != nil || next.IdentityMapping.Revision != "2" {
		t.Fatalf("revision commit: %#v %v", next.IdentityMapping, err)
	}
	if _, err := lock.CommitIdentityMappingV1(state.Current, state.IdentityMapping.InstallationID, 1, *state.IdentityMapping); err == nil {
		t.Fatal("old observation accepted after mapping-only change")
	}
	info, err := os.Stat(path)
	if err != nil || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o600 {
		t.Fatalf("private state mode: %v %v", info, err)
	}
}

func TestIdentityMappingFirstOwnerAndCopiedState(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, false)
	state.IdentityMapping = nil
	writeMappingFixtureV1(t, path, state)
	input := IdentityMappingStateV1{Schema: IdentityMappingStateSchemaV1, OperatorUID: "1000", OperatorGID: "1000", Profile: "bounded"}
	created, err := lock.CommitIdentityMappingV1(state.Current, "", 0, input)
	if err != nil || created.IdentityMapping.InstallationID == "" || created.IdentityMapping.Revision != "1" {
		t.Fatalf("first owner: %#v %v", created.IdentityMapping, err)
	}
	if _, err := lock.CommitIdentityMappingV1(state.Current, "", 0, input); err == nil {
		t.Fatal("retry allocated another owner")
	}
	other := t.TempDir()
	otherPath := writeOverlayTestState(t, other)
	writeMappingFixtureV1(t, otherPath, created)
	copyLock, err := AcquireOperationLock(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}
	defer copyLock.Unlock()
	if _, _, err := copyLock.ReadStateV1(); err == nil || !strings.Contains(err.Error(), "copied") {
		t.Fatalf("copied owner: %v", err)
	}
	if err := copyLock.RequireNoRetainedIdentityV1(); err == nil {
		t.Fatal("copied mapping passed destructive guard")
	}
}

func TestIdentityMappingUninstallIsAtomicRetainedStaging(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, true)
	expected := state.Deployment.Installation
	before, _ := os.ReadFile(path)
	if _, _, err := lock.ClearInstallationStateV1(expected); err == nil {
		t.Fatal("omitted mapping observation accepted")
	}
	bad := state
	bad.BlueprintSource = ""
	if _, err := RetainedStagingAfterUninstallV1(bad, expected); err == nil {
		t.Fatal("missing retained input accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("rejection changed installed facts")
	}
	staged, changed, err := lock.ClearInstallationStateV1(expected, state.IdentityMapping)
	if err != nil || !changed {
		t.Fatal(err)
	}
	if staged.Deployment != nil || staged.Staging == nil || staged.IdentityMapping.InstallationBinding != nil || staged.IdentityMapping.Revision != "2" {
		t.Fatalf("invalid retained staging: %#v", staged)
	}
	if staged.IdentityMapping.InstallationID != state.IdentityMapping.InstallationID || !reflect.DeepEqual(staged.Current, state.Current) {
		t.Fatal("owner or generation changed")
	}
	if _, _, err := lock.ReadStateV1(); err != nil {
		t.Fatal(err)
	}
	if err := lock.RequireNoRetainedIdentityV1(); err == nil {
		t.Fatal("uninstall released mapping")
	}
	if _, _, err := lock.SetInstallationStateV1(expected); err == nil {
		t.Fatal("retained staging implicitly rebound to installation")
	}
}

func TestIdentityMappingPresenceBarrierIncludesMalformedAndLegacy(t *testing.T) {
	path, lock, _ := mappingStateFixtureV1(t, false)
	for _, content := range []string{
		`{"schema":"legacy","identity_mapping":null}`,
		`{"schema":"legacy","Identity_Mapping":false}`,
		`{"identity_mapping":{},"identity_mapping":null}`,
		`{"identity_mapping":`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := lock.RequireNoRetainedIdentityV1(); err == nil {
			t.Fatalf("guard accepted %s", content)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":"legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lock.RequireNoRetainedIdentityV1(); err != nil {
		t.Fatalf("presence guard broke legacy recovery: %v", err)
	}
}

func TestIdentityMappingOrdinaryWriterFamilyPreservesAuthority(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, false)
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(filepath.Dir(path))
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetStagedDesiredStateV1(t.Context(), dir, document, state.Platform, overlayTestPackageValidator, "updated source", false); err != nil {
		t.Fatal(err)
	}
	if _, err := SetDesiredPlatformV1(t.Context(), dir, state.Platform, overlayTestPackageValidator); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateRequestOverlayV1(t.Context(), dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		overlay.SelectedOptions = append(overlay.SelectedOptions, QualifiedOption{Application: "app", Option: "debug"})
		return overlay, nil
	}); err != nil {
		t.Fatal(err)
	}
	after := readOverlayTestState(t, path)
	if !reflect.DeepEqual(state.IdentityMapping, after.IdentityMapping) {
		t.Fatal("stage/platform/overlay update changed mapping authority")
	}
	if after.BlueprintSource != "updated source" || len(after.Overlay.SelectedOptions) != 1 {
		t.Fatal("ordinary desired updates did not run")
	}
}

func TestIdentityMappingUninstallPreservesAdmittedAndDormantHistory(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, true)
	m := state.IdentityMapping
	values := []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "16"}}
	m.UIDAssignments, m.GIDAssignments, m.DormantUIDs, m.DormantGIDs = values, values, values, values
	m.AdmittedGeneration = state.Current
	supervisor, engine := "supervisor-1", "engine-1"
	m.Supervisor, m.Engine = &supervisor, &engine
	m.RetainedStorage = []string{"data-1"}
	m.Revoked = true
	writeMappingFixtureV1(t, path, state)
	old := *m
	installation := state.Deployment.Installation
	result, _, err := lock.ClearInstallationStateV1(installation, m)
	if err != nil {
		t.Fatal(err)
	}
	old.InstallationBinding = nil
	old.Revision = "2"
	if !reflect.DeepEqual(*result.IdentityMapping, old) {
		t.Fatal("uninstall released retained ownership history")
	}
	// Repeating a stale installed observation cannot create a second transition.
	before, _ := os.ReadFile(path)
	if _, _, err := lock.ClearInstallationStateV1(installation, m); err == nil {
		t.Fatal("stale uninstall accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("stale uninstall rewrote staging")
	}
}

func TestIdentityMappingUninstallRejectsPendingAndInvalidInputBeforeCommit(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(strings.ToLower(fmt.Sprint(pending)), func(t *testing.T) {
			path, lock, state := mappingStateFixtureV1(t, true)
			if pending {
				values := []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "16"}}
				state.IdentityMapping.PendingOperation = &IdentityMappingOperationV1{OperationID: "operation-1", Kind: "configure", ExpectedRevision: "1", Candidate: IdentityMappingGeometryV1{Profile: "exact", UIDs: values, GIDs: values}, Phase: "effects-pending", Inhibited: true}
			} else {
				state.BlueprintSource = ""
			}
			writeMappingFixtureV1(t, path, state)
			before, _ := os.ReadFile(path)
			if _, _, err := lock.ClearInstallationStateV1(state.Deployment.Installation, state.IdentityMapping); err == nil {
				t.Fatal("unproved retained staging accepted")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("invalid uninstall changed state")
			}
		})
	}
}

func TestIdentityMappingInstalledWritersCompareObservation(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, true)
	installation := state.Deployment.Installation
	before, _ := os.ReadFile(path)
	if _, _, err := lock.SetInstallationStateV1(installation); err == nil {
		t.Fatal("set omitted mapping revision")
	}
	if _, _, err := lock.MarkInstallationReadyV1(installation); err == nil {
		t.Fatal("ready omitted mapping revision")
	}
	stale := *state.IdentityMapping
	stale.Revision = "2"
	if _, _, err := lock.SetInstallationStateV1(installation, &stale); err == nil {
		t.Fatal("set accepted stale mapping")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("rejected installation writer changed state")
	}
	if _, changed, err := lock.SetInstallationStateV1(installation, state.IdentityMapping); err != nil || changed {
		t.Fatalf("preserved installation: changed=%v err=%v", changed, err)
	}
}

func TestIdentityMappingUninstallCrashLeavesCompleteOldOrNewState(t *testing.T) {
	for _, afterReplace := range []bool{false, true} {
		t.Run(fmt.Sprint(afterReplace), func(t *testing.T) {
			path, lock, state := mappingStateFixtureV1(t, true)
			replace, sync := replaceAtomicStateFile, syncAtomicStateFileDirectory
			t.Cleanup(func() { replaceAtomicStateFile = replace; syncAtomicStateFileDirectory = sync })
			fault := errors.New("injected uninstall publication fault")
			if afterReplace {
				syncAtomicStateFileDirectory = func(string) error { return fault }
			} else {
				replaceAtomicStateFile = func(string, string) error { return fault }
			}
			if _, _, err := lock.ClearInstallationStateV1(state.Deployment.Installation, state.IdentityMapping); !errors.Is(err, fault) {
				t.Fatalf("fault not reached: %v", err)
			}
			stored := readOverlayTestState(t, path)
			if stored.IdentityMapping.InstallationID != state.IdentityMapping.InstallationID {
				t.Fatal("crash changed stable owner")
			}
			if afterReplace {
				if stored.Deployment != nil || stored.Staging == nil || stored.IdentityMapping.InstallationBinding != nil || stored.IdentityMapping.Revision != "2" {
					t.Fatal("incomplete retained staging after replace")
				}
			} else if !reflect.DeepEqual(stored, state) {
				t.Fatal("pre-replace failure changed installed authority")
			}
		})
	}
}

func TestIdentityMappingLegacyRecoveryCannotStripRetainedRecord(t *testing.T) {
	dir := t.TempDir()
	op, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Unlock()
	content, _ := legacyComponentsStagingStateFixtureV1(t)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(content, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["identity_mapping"] = json.RawMessage(`null`)
	content, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", stateFilenameV1)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = op.PrepareLegacyComponentsStagingRecoveryV1(func(blueprint.Document) (blueprint.Platform, error) {
		t.Fatal("legacy platform selection reached")
		return blueprint.Platform{}, nil
	}, overlayTestPackageValidator, true)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("legacy identity barrier: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(content) != string(after) {
		t.Fatal("legacy recovery erased retained identity")
	}
}

func TestIdentityMappingProtectedCandidateAndWireGeometry(t *testing.T) {
	_, _, state := mappingStateFixtureV1(t, false)
	m := state.IdentityMapping
	values := []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "16"}}
	m.UIDAssignments, m.GIDAssignments = values, values
	m.PendingOperation = &IdentityMappingOperationV1{OperationID: "operation-1", Kind: "configure", ExpectedRevision: "1", ExpectedGeneration: state.Current, PriorAccepted: state.Current, Candidate: IdentityMappingGeometryV1{Profile: "exact", UIDs: values, GIDs: values}, Phase: "intent", Inhibited: true}
	payload, err := EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*IdentityMappingStateV1){
		"leading zero":                func(m *IdentityMappingStateV1) { m.UIDAssignments[0].HostStart = "0100000" },
		"sentinel ID":                 func(m *IdentityMappingStateV1) { m.OperatorGID = "4294967295" },
		"overflow count":              func(m *IdentityMappingStateV1) { m.UIDAssignments[0].Count = "4294967296" },
		"range overflow":              func(m *IdentityMappingStateV1) { m.GIDAssignments[0].HostStart = "4294967290" },
		"reassigned candidate":        func(m *IdentityMappingStateV1) { m.PendingOperation.Candidate.UIDs[0].HostStart = "200000" },
		"different profile":           func(m *IdentityMappingStateV1) { m.PendingOperation.Candidate.Profile = "bounded" },
		"invalid candidate geometry":  func(m *IdentityMappingStateV1) { m.PendingOperation.Candidate.GIDs[0].Count = "0" },
		"future expected revision":    func(m *IdentityMappingStateV1) { m.PendingOperation.ExpectedRevision = "2" },
		"invalid expected generation": func(m *IdentityMappingStateV1) { m.PendingOperation.ExpectedGeneration = &EnvironmentGenerationState{} },
		"unsafe accepted binding":     func(m *IdentityMappingStateV1) { s := "-invalid"; m.Engine = &s },
		"unsafe pending handle":       func(m *IdentityMappingStateV1) { m.PendingOperation.OwnedObjects = []string{"-invalid"} },
		"unretained dormant assignment": func(m *IdentityMappingStateV1) {
			supervisor, engine := "supervisor-1", "engine-1"
			m.Supervisor, m.Engine, m.AdmittedGeneration = &supervisor, &engine, state.Current
			m.DormantUIDs = []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "17"}}
		},
		"invalid admitted generation": func(m *IdentityMappingStateV1) { m.AdmittedGeneration = &EnvironmentGenerationState{} },
		"invalid dormant geometry": func(m *IdentityMappingStateV1) {
			supervisor, engine := "supervisor-1", "engine-1"
			m.Supervisor, m.Engine, m.AdmittedGeneration = &supervisor, &engine, state.Current
			m.DormantUIDs = []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "0"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var next StateV1
			if err := json.Unmarshal(payload, &next); err != nil {
				t.Fatal(err)
			}
			mutate(next.IdentityMapping)
			if err := ValidateStateV1(next); err == nil {
				t.Fatal("invalid retained authority accepted")
			}
		})
	}
	// Cardinality is not a policy cap; host root remains excluded.
	all := IdentityMappingGeometryV1{Profile: "exact", UIDs: []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "4000000000"}}, GIDs: []IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "4000000000"}}}
	if err := requireCanonicalMappingGeometryV1(all); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityMappingCreationAndUnsupportedChangesDoNotPublish(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, true)
	state.IdentityMapping = nil
	writeMappingFixtureV1(t, path, state)
	input := IdentityMappingStateV1{Schema: IdentityMappingStateSchemaV1, OperatorUID: "1000", OperatorGID: "1000", Profile: "exact"}
	if _, err := lock.CommitIdentityMappingV1(state.Current, "", 0, input); err == nil {
		t.Fatal("installed opt-in bypassed destination adapter")
	}
	state.Deployment = nil
	state.Staging = &StagingStateV1{Schema: StagingStateSchemaV1}
	writeMappingFixtureV1(t, path, state)
	for _, field := range []string{"owner", "revision", "pending"} {
		next := input
		switch field {
		case "owner":
			next.InstallationID = "foreign"
		case "revision":
			next.Revision = "1"
		case "pending":
			next.PendingOperation = &IdentityMappingOperationV1{OperationID: "supplied"}
		}
		if _, err := lock.CommitIdentityMappingV1(state.Current, "", 0, next); err == nil {
			t.Fatalf("first creation accepted %s", field)
		}
	}
	created, err := lock.CommitIdentityMappingV1(state.Current, "", 0, input)
	if err != nil {
		t.Fatal(err)
	}
	next := *created.IdentityMapping
	next.Revoked = true
	if _, err := lock.CommitIdentityMappingV1(state.Current, next.InstallationID, 1, next); err == nil {
		t.Fatal("unchecked authority transition accepted")
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.CommitIdentityMappingV1(state.Current, next.InstallationID, 1, *created.IdentityMapping); err == nil {
		t.Fatal("released lock authorized mapping update")
	}
}

func TestIdentityMappingInstalledPublicationCannotAdoptEitherOwner(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, false)
	dir := filepath.Dir(filepath.Dir(path))
	installation := installationStateV1Fixture(dir)
	destination := *state.Current
	destination.Reference += "-destination"
	before, _ := os.ReadFile(path)
	if _, _, err := lock.CommitInstalledStateV1(state.Current, state, destination, installation); err == nil || !strings.Contains(err.Error(), "mapped") {
		t.Fatalf("mapped source adoption: %v", err)
	}
	state.IdentityMapping = nil
	if _, _, err := lock.CommitInstalledStateV1(state.Current, state, destination, installation); err == nil || !strings.Contains(err.Error(), "mapped") {
		t.Fatalf("mapped destination adoption: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("rejected install publication changed authority")
	}
}

func TestIdentityMappingReadyTransitionPreservesObservedAuthority(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, true)
	state.Deployment.Installation.Status = InstallationStatusConfiguring
	writeMappingFixtureV1(t, path, state)
	ready := state.Deployment.Installation
	ready.Status = InstallationStatusReady
	next, changed, err := lock.MarkInstallationReadyV1(ready, state.IdentityMapping)
	if err != nil || !changed {
		t.Fatalf("ready transition changed=%v: %v", changed, err)
	}
	if !reflect.DeepEqual(next.IdentityMapping, state.IdentityMapping) {
		t.Fatal("ready transition changed mapping authority")
	}
}

func TestIdentityMappingDirectTerminalGuardCannotAuthorizeRemoval(t *testing.T) {
	path, lock, state := mappingStateFixtureV1(t, false)
	before, _ := os.ReadFile(path)
	if err := lock.BeginTerminalRemovalV1(state); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("begin mapped removal: %v", err)
	}
	if err := lock.ResumeTerminalRemovalV1(state); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("resume mapped removal: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("terminal guard changed retained authority")
	}
}
