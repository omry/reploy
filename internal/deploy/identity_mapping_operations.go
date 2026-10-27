package deploy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/identitymapping"
)

// IdentityMappingEvidenceV1 is a trusted effect-owner seam, not operator input.
// Plan proves complete inactive inventory and compatible storage/history and
// returns exact planned retained-storage handles without effects. Unused proves that the
// exact intent's candidate has never been used, including storage/preparation.
// Verify reconciles the recorded owner, operation, generations and exact handles.
// Container absence alone cannot satisfy any of these proofs. G/H supply real
// effect owners; B5 exercises their metadata boundary with injected effects.
type IdentityMappingEvidenceV1 interface {
	Plan(context.Context, StateV1, identitymapping.Geometry) ([]string, error)
	Unused(context.Context, StateV1) error
	Verify(context.Context, StateV1) error
}

// IdentityMappingOperationsV1 holds no independent authority or admission cache.
// Each call rereads the selected owner under its existing operation lock.
type IdentityMappingOperationsV1 struct {
	Operation *OperationLock
	Evidence  IdentityMappingEvidenceV1
	hostCheck func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error)
	write     func(string, []byte) error
}

type IdentityMappingStatusV1 struct {
	State        StateV1
	Status       string
	Reservations *identitymapping.Geometry
}

// Status creates nothing and reports recorded authority even when desired input
// is missing. Reservations include the candidate as well as protected history.
func (ops IdentityMappingOperationsV1) Status() (IdentityMappingStatusV1, error) {
	state, found, err := ops.Operation.ReadStateV1()
	if err != nil || !found || state.TerminalRemoval || state.Staging == nil && state.Deployment == nil {
		return IdentityMappingStatusV1{}, fmt.Errorf("mapping status requires an existing selected owner: %v", err)
	}
	result := IdentityMappingStatusV1{State: state, Status: "ordinary"}
	m := state.IdentityMapping
	if m == nil {
		document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
		if err != nil {
			return IdentityMappingStatusV1{}, err
		}
		if document.Environment.Runtime.IdentityMapping != nil {
			result.Status = "inhibited"
		}
		return result, nil
	}
	result.Status = "retained"
	if m.AdmittedGeneration != nil {
		result.Status = "admitted"
	}
	var geometry *identitymapping.Geometry
	if len(m.UIDAssignments) != 0 {
		geometry = geometryPointerV1(m.geometry().numeric())
	}
	if p := m.PendingOperation; p != nil {
		reserved, err := identitymapping.RetainGeometry(p.Candidate.numeric(), geometry)
		if err != nil {
			return IdentityMappingStatusV1{}, err
		}
		geometry = &reserved
		result.Status = "inhibited"
		if p.Phase == "intent" {
			result.Status = "candidate"
		}
	}
	if m.Revoked {
		result.Status = "revoked"
	}
	result.Reservations = geometry
	return result, nil
}

// Check validates one captured effective input without modifying any files.
// A successful check does not accept a candidate or grant execution admission.
func (ops IdentityMappingOperationsV1) Check(ctx context.Context, candidate identitymapping.Geometry) (StateV1, error) {
	status, err := ops.Status()
	if err != nil {
		return StateV1{}, err
	}
	if _, _, err := ops.check(ctx, status.State, candidate); err != nil {
		return StateV1{}, err
	}
	return status.State, nil
}

func (ops IdentityMappingOperationsV1) check(ctx context.Context, state StateV1, candidate identitymapping.Geometry) (identitymapping.Geometry, identitymapping.HostIdentity, error) {
	if ctx == nil {
		return identitymapping.Geometry{}, identitymapping.HostIdentity{}, fmt.Errorf("mapping operation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return identitymapping.Geometry{}, identitymapping.HostIdentity{}, err
	}
	if state.Deployment != nil && state.IdentityMapping == nil {
		return identitymapping.Geometry{}, identitymapping.HostIdentity{}, fmt.Errorf("first installed mapping opt-in requires its reviewed destination adapter")
	}
	reserved, err := RetainIdentityMappingGeometryV1(state, candidate)
	if err != nil {
		return reserved, identitymapping.HostIdentity{}, err
	}
	check := ops.hostCheck
	if check == nil {
		check = identitymapping.CheckHost
	}
	identity, err := ops.Operation.checkIdentityMappingHostV1(ctx, reserved.UIDs, reserved.GIDs, check)
	if err != nil {
		return reserved, identity, err
	}
	// Check old and replacement candidates independently against protected
	// history. A never-used candidate may be corrected to a different binding;
	// it remains reserved until the separate Unused proof permits replacement.
	if m := state.IdentityMapping; m != nil && m.PendingOperation != nil {
		old, err := RetainIdentityMappingGeometryV1(state, m.PendingOperation.Candidate.numeric())
		if err != nil {
			return reserved, identity, err
		}
		other, err := ops.Operation.checkIdentityMappingHostV1(ctx, old.UIDs, old.GIDs, check)
		if err != nil || other != identity {
			return reserved, identity, fmt.Errorf("pending reservations no longer pass the same host check: %v", err)
		}
	}
	if m := state.IdentityMapping; m != nil && (m.OperatorUID != strconv.FormatUint(uint64(identity.UID), 10) || m.OperatorGID != strconv.FormatUint(uint64(identity.GID), 10)) {
		return reserved, identity, fmt.Errorf("mapping operator differs from retained authority")
	}
	return reserved, identity, nil
}

// Configure records only an inhibited intent. It never builds, starts, chowns,
// accepts proposed assignments or changes admission. expected is the caller's
// complete locked observation, binding selected input, generation and revision.
func (ops IdentityMappingOperationsV1) Configure(ctx context.Context, expected StateV1, candidate identitymapping.Geometry, operationID string) (StateV1, error) {
	if !safeRecoveryIdentity(operationID) {
		return StateV1{}, fmt.Errorf("mapping configuration requires a safe operation ID")
	}
	canonical, err := identitymapping.ValidateGeometry(candidate)
	if err != nil {
		return StateV1{}, err
	}
	_, operator, err := ops.check(ctx, expected, canonical)
	if err != nil {
		return StateV1{}, err
	}
	return ops.change(ctx, expected, func(state *StateV1) error {
		m := state.IdentityMapping
		if m != nil && m.PendingOperation != nil && m.PendingOperation.Kind != "configure" {
			return fmt.Errorf("pending operation %s has unsupported kind %q", m.PendingOperation.OperationID, m.PendingOperation.Kind)
		}
		if ops.Evidence == nil {
			return fmt.Errorf("mapping configuration requires complete inactive inventory")
		}
		observedCandidate, _ := identitymapping.ValidateGeometry(canonical)
		handles, err := ops.Evidence.Plan(ctx, cloneMappingStateV1(*state), observedCandidate)
		handles = append([]string(nil), handles...)
		if err != nil || validateMappingHandlesV1(handles) != nil {
			return fmt.Errorf("mapping inventory is unknown or invalid: %v", err)
		}
		if m == nil {
			if state.Staging == nil {
				return fmt.Errorf("first installed mapping opt-in requires its reviewed destination adapter")
			}
			id, err := newOpaqueAdmissionIDV1(rand.Reader, "mapping", "mapping owner")
			if err != nil {
				return err
			}
			owner, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(ops.Operation.path)))
			if err != nil {
				return err
			}
			m = &IdentityMappingStateV1{Schema: IdentityMappingStateSchemaV1, InstallationID: id, OwnerDir: owner, Revision: "0", OperatorUID: strconv.FormatUint(uint64(operator.UID), 10), OperatorGID: strconv.FormatUint(uint64(operator.GID), 10), Profile: canonical.Profile}
			state.IdentityMapping = m
		}
		if p := m.PendingOperation; p != nil {
			if !reflect.DeepEqual(p.ExpectedGeneration, state.Current) || !reflect.DeepEqual(p.PriorAccepted, m.AdmittedGeneration) || p.Phase == "intent" && p.ExpectedRevision != m.Revision {
				return fmt.Errorf("pending intent observation is stale")
			}
			if p.OperationID == operationID && p.Phase == "intent" && reflect.DeepEqual(p.Candidate.numeric(), canonical) && reflect.DeepEqual(p.OwnedObjects, handles) {
				return nil // exact retry; change detects identical bytes
			}
			if p.OperationID == operationID || p.Phase != "intent" {
				return fmt.Errorf("pending operation %s requires exact recovery", p.OperationID)
			}
			if err := ops.Evidence.Unused(ctx, cloneMappingStateV1(*state)); err != nil {
				return fmt.Errorf("candidate replacement lacks never-used proof: %w", err)
			}
		}
		m.PendingOperation = &IdentityMappingOperationV1{OperationID: operationID, Kind: "configure", ExpectedRevision: strconv.FormatUint(mappingDecimalV1(m.Revision)+1, 10), ExpectedGeneration: state.Current, Candidate: mappingGeometryStateV1(canonical), PriorAccepted: m.AdmittedGeneration, OwnedObjects: append([]string(nil), handles...), Phase: "intent", Inhibited: true}
		return nil
	})
}

// BeginEffects durably inhibits the exact intent before its trusted effect owner
// can issue a request. This method itself performs no runtime or storage effect.
func (ops IdentityMappingOperationsV1) BeginEffects(ctx context.Context, expected StateV1, operationID string) (StateV1, error) {
	if err := ops.checkPending(ctx, expected, operationID); err != nil {
		return StateV1{}, err
	}
	return ops.change(ctx, expected, func(state *StateV1) error {
		p := state.IdentityMapping.PendingOperation
		if p.Phase != "intent" {
			return fmt.Errorf("effects require the exact intent phase")
		}
		if ops.Evidence == nil {
			return fmt.Errorf("effects require fresh inactive inventory")
		}
		handles, err := ops.Evidence.Plan(ctx, cloneMappingStateV1(*state), p.Candidate.numeric())
		handles = append([]string(nil), handles...)
		if err != nil || !reflect.DeepEqual(handles, p.OwnedObjects) {
			return fmt.Errorf("effect inventory differs from the recorded intent: %v", err)
		}
		p.Phase = "effects-pending"
		return nil
	})
}

// Recover never infers replacement input or reissues effects. Cancellation is
// restricted to a proved-unused intent; completion verifies exact effects again,
// including after a crash in verified phase. Accepted reservations cannot shrink.
func (ops IdentityMappingOperationsV1) Recover(ctx context.Context, expected StateV1, operationID string, cancel bool) (StateV1, error) {
	if err := ops.checkPending(ctx, expected, operationID); err != nil {
		return StateV1{}, err
	}
	if ops.Evidence == nil {
		return StateV1{}, fmt.Errorf("mapping recovery requires exact inventory evidence")
	}
	if cancel {
		return ops.change(ctx, expected, func(state *StateV1) error {
			if state.IdentityMapping.PendingOperation.Phase != "intent" {
				return fmt.Errorf("effects-pending or verified candidates cannot be cancelled")
			}
			if err := ops.Evidence.Unused(ctx, cloneMappingStateV1(*state)); err != nil {
				return fmt.Errorf("cancellation lacks never-used proof: %w", err)
			}
			state.IdentityMapping.PendingOperation = nil
			return nil
		})
	}
	if expected.IdentityMapping.PendingOperation.Phase == "intent" {
		return StateV1{}, fmt.Errorf("intent has no effects to reconcile; retain it or prove it unused")
	}
	verified, err := ops.change(ctx, expected, func(state *StateV1) error {
		if err := ops.Evidence.Verify(ctx, cloneMappingStateV1(*state)); err != nil {
			return fmt.Errorf("exact effects remain unverified: %w", err)
		}
		state.IdentityMapping.PendingOperation.Phase = "verified"
		return nil
	})
	if err != nil {
		return verified, err
	}
	return ops.change(ctx, verified, func(state *StateV1) error {
		m := state.IdentityMapping
		var previous *identitymapping.Geometry
		if len(m.UIDAssignments) != 0 {
			previous = geometryPointerV1(m.geometry().numeric())
		}
		reserved, err := identitymapping.RetainGeometry(m.PendingOperation.Candidate.numeric(), previous)
		if err != nil {
			return err
		}
		geometry := mappingGeometryStateV1(reserved)
		m.UIDAssignments, m.GIDAssignments = geometry.UIDs, geometry.GIDs
		m.RetainedStorage = slices.Compact(slices.Sorted(slices.Values(append(m.RetainedStorage, m.PendingOperation.OwnedObjects...))))
		m.PendingOperation = nil
		return nil
	})
}

func (ops IdentityMappingOperationsV1) checkPending(ctx context.Context, expected StateV1, operationID string) error {
	m := expected.IdentityMapping
	if m == nil || m.PendingOperation == nil || m.PendingOperation.Kind != "configure" || m.PendingOperation.OperationID != operationID ||
		!reflect.DeepEqual(m.PendingOperation.ExpectedGeneration, expected.Current) || !reflect.DeepEqual(m.PendingOperation.PriorAccepted, m.AdmittedGeneration) ||
		m.PendingOperation.Phase == "intent" && m.PendingOperation.ExpectedRevision != m.Revision {
		return fmt.Errorf("recovery requires its exact pending owner, operation and generations")
	}
	_, _, err := ops.check(ctx, expected, m.PendingOperation.Candidate.numeric())
	return err
}

func (ops IdentityMappingOperationsV1) change(ctx context.Context, expected StateV1, update func(*StateV1) error) (StateV1, error) {
	lock := ops.Operation
	if lock == nil || ctx == nil {
		return StateV1{}, fmt.Errorf("mapping change requires its context and operation lock")
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
	if err != nil || !found || !reflect.DeepEqual(state, expected) {
		return StateV1{}, fmt.Errorf("mapping owner, input, generation or revision changed: %v", err)
	}
	if state.TerminalRemoval || state.Staging == nil && state.Deployment == nil {
		return StateV1{}, fmt.Errorf("mapping change requires a nonterminal staged or installed owner")
	}
	if m := state.IdentityMapping; m != nil && (m.Revoked || mappingDecimalV1(m.Revision) == math.MaxUint64) {
		return StateV1{}, fmt.Errorf("mapping is revoked or its revision is exhausted")
	}
	before := cloneMappingStateV1(state)
	if err := update(&state); err != nil {
		return before, err
	}
	if reflect.DeepEqual(before, state) {
		return state, nil
	}
	state.IdentityMapping.Revision = strconv.FormatUint(mappingDecimalV1(state.IdentityMapping.Revision)+1, 10)
	content, err := EncodeStateV1(state)
	if err != nil {
		return before, err
	}
	if err := ctx.Err(); err != nil {
		return before, err
	}
	if err := lock.requireWritableLocked(); err != nil {
		return before, err
	}
	if err := validateMappingOwnerPathV1(path, state); err != nil {
		return before, err
	}
	write := ops.write
	if write == nil {
		write = func(path string, content []byte) error { return writeAtomicStateFile(path, content, 0o600) }
	}
	if err := write(path, content); err != nil {
		return before, fmt.Errorf("mapping operation remains subject to exact status/recovery: %w", err)
	}
	return state, nil
}

func cloneMappingStateV1(state StateV1) StateV1 {
	// State was strictly decoded before this call; JSON detaches all pointers and
	// slices so a trusted observer cannot accidentally mutate commit authority.
	content, _ := json.Marshal(state)
	var result StateV1
	_ = json.Unmarshal(content, &result)
	return result
}

func mappingGeometryStateV1(geometry identitymapping.Geometry) IdentityMappingGeometryV1 {
	result := IdentityMappingGeometryV1{Profile: geometry.Profile}
	for i, values := range [][]identitymapping.Interval{geometry.UIDs, geometry.GIDs} {
		var out []IdentityMappingIntervalV1
		for _, v := range values {
			out = append(out, IdentityMappingIntervalV1{ContainerStart: strconv.FormatUint(uint64(v.ContainerStart), 10), HostStart: strconv.FormatUint(uint64(v.HostStart), 10), Count: strconv.FormatUint(uint64(v.Count), 10)})
		}
		if i == 0 {
			result.UIDs = out
		} else {
			result.GIDs = out
		}
	}
	return result
}
