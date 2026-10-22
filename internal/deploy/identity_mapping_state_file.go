package deploy

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// RequireNoRetainedIdentityV1 is a before-effects barrier, including for legacy
// recovery. Presence alone rejects; a compatibility decoder must not erase it.
func (lock *OperationLock) RequireNoRetainedIdentityV1() error {
	if lock == nil {
		return fmt.Errorf("identity protection requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.statePathV1Locked()
	if err != nil {
		return err
	}
	return requireNoRetainedIdentityPathV1(path)
}

func requireNoRetainedIdentityPathV1(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("identity state must be a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return err
	}
	for key := range fields {
		if strings.EqualFold(key, "identity_mapping") {
			return fmt.Errorf("retained identity mapping requires checked safe release; replacement or directory removal is unavailable")
		}
	}
	return nil
}

func validateMappingOwnerPathV1(path string, state StateV1) error {
	if state.IdentityMapping == nil {
		return nil
	}
	owner, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		return err
	}
	if owner != state.IdentityMapping.OwnerDir {
		return fmt.Errorf("identity mapping belongs to %q, not selected directory %q; copied authority cannot be adopted", state.IdentityMapping.OwnerDir, owner)
	}
	return nil
}

func compareIdentityObservationV1(actual *IdentityMappingStateV1, expected []*IdentityMappingStateV1) error {
	if len(expected) > 1 || len(expected) == 0 && actual != nil || len(expected) == 1 && !reflect.DeepEqual(actual, expected[0]) {
		return fmt.Errorf("identity revision changed or was omitted before installation state commit")
	}
	return nil
}

// CommitIdentityMappingV1 compares both independent revisions under the existing
// operation lock. This metadata transaction cannot grant runtime admission.
func (lock *OperationLock) CommitIdentityMappingV1(expectedGeneration *EnvironmentGenerationState, expectedID string, expectedRevision uint64, next IdentityMappingStateV1) (StateV1, error) {
	if lock == nil {
		return StateV1{}, fmt.Errorf("identity commit requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.requireWritableLocked(); err != nil {
		return StateV1{}, err
	}
	path, err := lock.statePathV1Locked()
	if err != nil {
		return StateV1{}, err
	}
	state, found, err := readStateV1Path(path)
	if err != nil {
		return StateV1{}, err
	}
	if !found || !reflect.DeepEqual(state.Current, expectedGeneration) {
		return StateV1{}, fmt.Errorf("identity generation changed or state is missing")
	}
	// Detach all caller-owned slices and pointers before checking or returning.
	payload, err := json.Marshal(next)
	if err != nil {
		return StateV1{}, err
	}
	if err := json.Unmarshal(payload, &next); err != nil {
		return StateV1{}, err
	}
	old := state.IdentityMapping
	if old == nil {
		if expectedID != "" || expectedRevision != 0 || state.Deployment != nil || state.Staging == nil {
			return StateV1{}, fmt.Errorf("first mapping configuration requires an unconfigured staged owner; installed opt-in requires its reviewed adapter")
		}
		if next.InstallationID != "" || next.OwnerDir != "" || next.Revision != "" || next.InstallationBinding != nil ||
			len(next.UIDAssignments)+len(next.GIDAssignments)+len(next.DormantUIDs)+len(next.DormantGIDs)+len(next.RetainedStorage) != 0 ||
			next.AdmittedGeneration != nil || next.Supervisor != nil || next.Engine != nil || next.Revoked || next.PendingOperation != nil {
			return StateV1{}, fmt.Errorf("first mapping configuration cannot supply retained authority")
		}
		next.InstallationID, err = newOpaqueAdmissionIDV1(rand.Reader, "mapping", "mapping owner")
		if err != nil {
			return StateV1{}, err
		}
		next.OwnerDir, err = filepath.EvalSymlinks(filepath.Dir(filepath.Dir(path)))
		if err != nil {
			return StateV1{}, err
		}
		next.Revision = "1"
	} else {
		if old.PendingOperation != nil {
			return StateV1{}, fmt.Errorf("pending identity mapping requires its checked operation")
		}
		if old.InstallationID != expectedID || mappingDecimalV1(old.Revision) != expectedRevision || expectedRevision == math.MaxUint64 {
			return StateV1{}, fmt.Errorf("identity owner or revision changed before commit")
		}
		// B5 supplies checked pending-operation transitions. Existing authority is
		// immutable here; generic metadata writes cannot replace accepted history.
		if !reflect.DeepEqual(*old, next) {
			return StateV1{}, fmt.Errorf("identity authority transition requires its checked operation")
		}
		next.Revision = strconv.FormatUint(expectedRevision+1, 10)
	}
	state.IdentityMapping = &next
	content, err := EncodeStateV1(state)
	if err != nil {
		return StateV1{}, err
	}
	if err := validateMappingOwnerPathV1(path, state); err != nil {
		return StateV1{}, err
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return StateV1{}, err
	}
	return state, nil
}

// RetainedStagingAfterUninstallV1 validates the whole candidate before host
// cleanup. The final commit compares the exact prior mapping and installation.
func RetainedStagingAfterUninstallV1(state StateV1, expected InstallationStateV1) (StateV1, error) {
	if state.Deployment == nil || !reflect.DeepEqual(state.Deployment.Installation, expected) {
		return StateV1{}, fmt.Errorf("installed facts changed before retained staging")
	}
	state.Deployment = nil
	if m := state.IdentityMapping; m != nil {
		if m.PendingOperation != nil || mappingDecimalV1(m.Revision) == math.MaxUint64 {
			return StateV1{}, fmt.Errorf("identity operation must be reconciled before uninstall")
		}
		copy := *m
		copy.InstallationBinding = nil
		copy.Revision = strconv.FormatUint(mappingDecimalV1(m.Revision)+1, 10)
		state.IdentityMapping = &copy
		state.Staging = &StagingStateV1{Schema: StagingStateSchemaV1}
	}
	return state, ValidateStateV1(state)
}
