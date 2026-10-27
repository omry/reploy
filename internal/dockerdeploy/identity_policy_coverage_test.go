package dockerdeploy

import (
	"context"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func lockedCoverageFixtureV1(t *testing.T, mapped, retained bool) (string, *deploy.OperationLock, providerstore.Store, CurrentBuild) {
	t.Helper()
	dir, operation, store, lock, state := currentBuildFixture(t, false)
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document = buildIdentityDocumentV1(document)
	if mapped {
		source, err := blueprint.Decode([]byte("blueprint: {schema: 1}\nenvironment:\n  runtime:\n    identity_mapping: {schema: 1, profile: exact, uids: [{container_start: 0, host_start: 100000, count: 16}], gids: [{container_start: 0, host_start: 100000, count: 16}]}\n"))
		if err != nil {
			t.Fatal(err)
		}
		document.Environment.Runtime.IdentityMapping = source.Environment.Runtime.IdentityMapping
	}
	current := CurrentBuild{State: state, Generation: *state.Current, Lock: lock}
	current.State.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	current.State.BlueprintSource = "blueprint.yaml"
	bindBuildIdentityFixtureV1(t, &current, document)
	if _, err := operation.PublishBuildLock(current.Lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitStateV1(state.Current, current.State); err != nil {
		t.Fatal(err)
	}
	if retained {
		current.State = retainedIdentityRecordV1(t, dir, current.State, "retained")
	}
	return dir, operation, store, current
}

func setCoverageFixtureReservationsV1(t *testing.T, dir string, current *CurrentBuild) {
	t.Helper()
	current.State.IdentityMapping.UIDAssignments[0].Count = "201"
	current.State.IdentityMapping.GIDAssignments[0].Count = "201"
	content, err := deploy.EncodeStateV1(current.State)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func coverageCandidateV1(count uint32) identitymapping.Geometry {
	intervals := []identitymapping.Interval{{ContainerStart: 0, HostStart: 100000, Count: count}}
	return identitymapping.Geometry{Profile: "exact", UIDs: intervals, GIDs: append([]identitymapping.Interval(nil), intervals...)}
}

type coverageNoEffectsV1 struct{ t *testing.T }

func (e coverageNoEffectsV1) Plan(context.Context, deploy.StateV1, identitymapping.Geometry) ([]string, error) {
	e.t.Fatal("coverage failure reached mapping effect inventory")
	return nil, nil
}
func (e coverageNoEffectsV1) Unused(context.Context, deploy.StateV1) error {
	e.t.Fatal("coverage failure reached unused proof")
	return nil
}
func (e coverageNoEffectsV1) Verify(context.Context, deploy.StateV1) error {
	e.t.Fatal("coverage failure reached effect verification")
	return nil
}

func TestIdentityPolicyCoverageV1ConfigurationUsesSelectedLockBeforeEffects(t *testing.T) {
	dir, operation, _, current := lockedCoverageFixtureV1(t, true, false)
	defer operation.Unlock()
	before := identityGuardTreeV1(t, dir)
	_, err := configureStagedIdentityMappingV1(t.Context(), operation, dir, "", "coverage-check", coverageNoEffectsV1{t})
	if err == nil || !strings.Contains(err.Error(), "UID 200") || !strings.Contains(err.Error(), "GID 200") || !strings.Contains(err.Error(), string(current.Generation.BuildLockDigest)) {
		t.Fatalf("selected missing-ID evidence not checked: %v", err)
	}
	if !maps.Equal(before, identityGuardTreeV1(t, dir)) {
		t.Fatal("coverage failure changed mapping state or selected lock")
	}
	// Numeric coverage alone is read-only and does not grant a mapping owner.
	if err := validateStagedIdentityPolicyCoverageV1(t.Context(), operation, dir, current.State, coverageCandidateV1(201)); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(before, identityGuardTreeV1(t, dir)) {
		t.Fatal("successful pure coverage created reservations or admission")
	}
}

func TestIdentityPolicyCoverageV1IncludesOmittedProtectedReservations(t *testing.T) {
	dir, operation, _, current := lockedCoverageFixtureV1(t, true, true)
	defer operation.Unlock()
	setCoverageFixtureReservationsV1(t, dir, &current)
	before := identityGuardTreeV1(t, dir)
	candidate := coverageCandidateV1(16)
	if err := validateStagedIdentityPolicyCoverageV1(t.Context(), operation, dir, current.State, candidate); err != nil {
		t.Fatalf("protected UID/GID200 was treated as absent: %v", err)
	}
	if candidate.UIDs[0].Count != 16 || !maps.Equal(before, identityGuardTreeV1(t, dir)) {
		t.Fatal("coverage expanded input or accepted an intent")
	}
}

func TestIdentityPolicyCoverageV1RequiresCurrentEvidenceAndHeldOwner(t *testing.T) {
	for _, mode := range []string{"missing generation", "changed blueprint", "missing reservations", "wrong owner", "released lock"} {
		t.Run(mode, func(t *testing.T) {
			dir, operation, store, current := lockedCoverageFixtureV1(t, true, false)
			defer operation.Unlock()
			reserved := coverageCandidateV1(201)
			var geometry *identitymapping.Geometry = &reserved
			switch mode {
			case "missing generation":
				current.State.Current = nil
				if err := operation.CommitStateV1(&current.Generation, current.State); err != nil {
					t.Fatal(err)
				}
			case "changed blueprint":
				document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
				if err != nil {
					t.Fatal(err)
				}
				document.Environment.ControlScript = "substituted"
				current.State.Blueprint = testResolvedBlueprintV1(t, document)
				if err := operation.CommitStateV1(&current.Generation, current.State); err != nil {
					t.Fatal(err)
				}
			case "missing reservations":
				geometry = nil
			case "wrong owner":
				dir = t.TempDir()
			case "released lock":
				if err := operation.Unlock(); err != nil {
					t.Fatal(err)
				}
			}
			before := identityGuardTreeV1(t, dir)
			if err := validateLockedIdentityCoverageV1(t.Context(), operation, store, "demo", dir, geometry); err == nil {
				t.Fatal("coverage accepted missing or substituted locked authority")
			}
			if !maps.Equal(before, identityGuardTreeV1(t, dir)) {
				t.Fatal("failed evidence load changed files")
			}
		})
	}
}

func TestIdentityPolicyCoverageV1MappedVerificationPrecedesImageAndRuntimeWork(t *testing.T) {
	dir, operation, store, current := lockedCoverageFixtureV1(t, true, true)
	before := identityGuardTreeV1(t, dir)
	_, err := verifyCurrentBuildV1(t.Context(), VerifyCurrentBuildInputV1{DeploymentDir: dir}, verifyCurrentBuildBackendV1{
		acquire:  func(context.Context, string) (*deploy.OperationLock, error) { return operation, nil },
		newStore: func(string) (providerstore.Store, error) { return store, nil },
		readState: func(op *deploy.OperationLock) (deploy.StateV1, bool, error) {
			return op.ReadStateV1()
		},
		loadCurrent: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
			t.Fatal("missing coverage reached image reference verification")
			return CurrentBuild{}, false, nil
		},
		plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			t.Fatal("missing coverage reached runtime planning")
			return CurrentRuntimePlanV1{}, nil
		},
		verify: func(context.Context, CurrentBuildVerificationInputV1) (CurrentBuildVerificationResultV1, error) {
			t.Fatal("missing coverage reached image validation")
			return CurrentBuildVerificationResultV1{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "UID 200") || !strings.Contains(err.Error(), string(current.Generation.BuildLockDigest)) {
		t.Fatalf("verification omitted selected locked coverage: %v", err)
	}
	if !maps.Equal(before, identityGuardTreeV1(t, dir)) {
		t.Fatal("read-only mapped verification changed files")
	}
}

func TestIdentityPolicyCoverageV1LockedBuildStillCannotExecute(t *testing.T) {
	for _, mapped := range []bool{false, true} {
		for _, entry := range []string{"shell", "command", "test", "up", "restart", "down", "install"} {
			t.Run(entry+map[bool]string{true: "/mapped", false: "/policy-only"}[mapped], func(t *testing.T) {
				dir, operation, _, current := lockedCoverageFixtureV1(t, mapped, mapped)
				if mapped {
					setCoverageFixtureReservationsV1(t, dir, &current)
					if err := validateStagedIdentityPolicyCoverageV1(t.Context(), operation, dir, current.State, coverageCandidateV1(16)); err != nil {
						t.Fatal("complete coverage fixture rejected", err)
					}
				}
				if err := operation.Unlock(); err != nil {
					t.Fatal(err)
				}
				before := identityGuardTreeV1(t, dir)
				var err error
				switch entry {
				case "shell":
					err = Shell(ShellOptions{Dir: dir, Stdout: io.Discard})
				case "command":
					err = AppCommand(AppCommandOptions{Dir: dir, CommandArgs: []string{"config"}, Stdout: io.Discard})
				case "test":
					err = RunCurrentRuntimeTestV1(t.Context(), CurrentRuntimeTestInputV1{DeploymentDir: dir})
				case "install":
					_, err = RunProviderInstallV1(t.Context(), ProviderInstallInputV1{SourceDeploymentDir: dir, DestinationDeploymentDir: filepath.Join(t.TempDir(), "not-created"), Scope: InstallScopeUser, Runtime: blueprintRuntimeFixtureV1(), Start: true})
				default:
					err = RunCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: entry, ControlMode: ControlAdmissionForceV1})
				}
				requirePolicyGuardErrorV1(t, err)
				if !maps.Equal(before, identityGuardTreeV1(t, dir)) {
					t.Fatal("locked evidence or coverage enabled a payload effect")
				}
			})
		}
	}
}
