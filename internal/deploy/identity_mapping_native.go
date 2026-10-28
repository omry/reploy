package deploy

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/identitymapping"
)

// UpgradeNativeMapping changes only the schema/provenance representation under
// the existing checked writer. Legacy pending configuration stays owned by its
// original operation. Partial or admitted supervisor authority cannot migrate.
func (ops IdentityMappingOperationsV1) UpgradeNativeMapping(ctx context.Context, expected StateV1) (StateV1, error) {
	return ops.change(ctx, expected, func(state *StateV1) error {
		m := state.IdentityMapping
		if m == nil || m.Schema != IdentityMappingStateSchemaV1 || m.Supervisor != nil || m.Engine != nil || m.AdmittedGeneration != nil || len(m.DormantUIDs)+len(m.DormantGIDs) != 0 {
			return fmt.Errorf("native upgrade requires unambiguous never-admitted legacy authority")
		}
		m.Schema, m.ProcessModel = IdentityMappingStateSchemaV2, IdentityMappingNativeProcessModelV1
		uids, gids := []IdentityMappingIntervalV1{}, []IdentityMappingIntervalV1{}
		m.AcceptedUIDs, m.AcceptedGIDs = &uids, &gids
		// A configuration intent is unchanged except for the checked journal
		// revision, so its original owner can still begin or cancel it.
		if p := m.PendingOperation; p != nil {
			if p.Kind != "configure" || p.Phase == "intent" && p.ExpectedRevision != m.Revision || !reflect.DeepEqual(p.ExpectedGeneration, state.Current) || p.PriorAccepted != nil {
				return fmt.Errorf("legacy intent revision is stale")
			}
			if p.Phase == "intent" {
				p.ExpectedRevision = strconv.FormatUint(mappingDecimalV1(m.Revision)+1, 10)
			}
		}
		return nil
	})
}

// nativePlan authenticates recorded provenance and selected immutable build
// evidence. The final change still compares the entire observation under lock.
func (ops IdentityMappingOperationsV1) nativePlan(ctx context.Context, state StateV1, candidate identitymapping.Geometry, native NativeIdentityOperationV1) (IdentityExecutionPlanV1, error) {
	fail := func(reason string) (IdentityExecutionPlanV1, error) {
		return IdentityExecutionPlanV1{}, fmt.Errorf("native identity admission: %s", reason)
	}
	if err := ops.Operation.RequireHeld(); err != nil {
		return fail(err.Error())
	}
	m := state.IdentityMapping
	if m == nil || m.Schema != IdentityMappingStateSchemaV2 || state.Current == nil || ops.ValidateProfileOwner == nil {
		return fail("requires native state, selected generation and trusted profile validator")
	}
	if err := ValidateStateV1(state); err != nil {
		return fail(err.Error())
	}
	if _, pending, err := ops.Operation.ReadPendingBuild(); err != nil || pending {
		return fail(fmt.Sprintf("selected build publication is unresolved: %v", err))
	}
	lock, found, err := ops.Operation.ReadBuildLock(state.Current.BuildLockDigest, ops.ValidateProfileOwner)
	if err != nil || !found {
		return fail(fmt.Sprintf("selected immutable build lock is unavailable: %v", err))
	}
	generation := state.Current
	policyDigest, _ := RuntimePolicyDigestV1(lock.RuntimePolicy) // ReadBuildLock validates this policy.
	digest, err := blueprint.ResolvedDocumentDigestV1(state.Blueprint)
	if err != nil || digest != lock.BlueprintDigest || generation.ImageDigest != lock.FinalImage.Digest || generation.RootFSSubject != lock.FinalImage.RootFSSubject || generation.Platform != lock.Platform || state.Platform != lock.Platform || generation.RuntimePolicyDigest != policyDigest {
		return fail("selected blueprint or generation differs from immutable build evidence")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil || lock.IdentityPolicy == nil || !reflect.DeepEqual(document.Environment.Runtime.IdentityPolicy, &lock.IdentityPolicy.Policy) {
		return fail("selected identity policy differs from immutable image evidence")
	}
	if _, _, err := ops.check(ctx, state, candidate); err != nil {
		return fail(err.Error())
	}
	var reserved *identitymapping.Geometry
	if len(m.UIDAssignments) != 0 {
		reserved = geometryPointerV1(m.geometry().numeric())
	}
	return PlanIdentityExecutionV1(IdentityExecutionInputV1{Generation: generation.BuildLockDigest, Policy: lock.IdentityPolicy, Candidate: candidate, Reservations: reserved, Accepted: m.acceptedGeometry(), RuntimeUsers: native.RuntimeUsers, RuntimeGroups: native.RuntimeGroups})
}

// PrepareNativeExecution records an inhibited native intent. The trusted effect
// owner must identify a fresh owned instance and verify all existing inventory;
// this metadata seam performs no create or release and enables no public path.
func (ops IdentityMappingOperationsV1) PrepareNativeExecution(ctx context.Context, expected StateV1, candidate identitymapping.Geometry, operationID string, native NativeIdentityOperationV1) (StateV1, error) {
	if !safeRecoveryIdentity(operationID) || !safeRecoveryIdentity(native.Engine) || !safeRecoveryIdentity(native.Process) {
		return StateV1{}, fmt.Errorf("native intent requires exact safe operation and instance identities")
	}
	canonical, err := identitymapping.ValidateGeometry(candidate)
	if err != nil {
		return StateV1{}, err
	}
	plan, err := ops.nativePlan(ctx, expected, canonical, native)
	if err != nil {
		return StateV1{}, err
	}
	native.Active = mappingGeometryStateV1(plan.Active)
	return ops.change(ctx, expected, func(state *StateV1) error {
		m := state.IdentityMapping
		if m.PendingOperation != nil || ops.Evidence == nil || m.NativeProcess != nil && *m.NativeProcess == native.Process && m.Engine != nil && *m.Engine == native.Engine {
			return fmt.Errorf("native intent requires no pending operation and a fresh owned instance")
		}
		handles, err := ops.Evidence.Plan(ctx, cloneMappingStateV1(*state), plan.Active)
		if err != nil || validateMappingHandlesV1(handles) != nil {
			return fmt.Errorf("native inactive inventory is unknown or invalid: %v", err)
		}
		m.PendingOperation = &IdentityMappingOperationV1{OperationID: operationID, Kind: "native-execute", ExpectedRevision: strconv.FormatUint(mappingDecimalV1(m.Revision)+1, 10), ExpectedGeneration: state.Current, Candidate: mappingGeometryStateV1(canonical), PriorAccepted: m.AdmittedGeneration, OwnedObjects: append([]string(nil), handles...), Phase: "intent", Inhibited: true, Native: &native}
		*state = cloneMappingStateV1(*state)
		return nil
	})
}

func (ops IdentityMappingOperationsV1) checkNativePending(ctx context.Context, expected StateV1) (IdentityExecutionPlanV1, error) {
	p := expected.IdentityMapping.PendingOperation
	plan, err := ops.nativePlan(ctx, expected, p.Candidate.numeric(), *p.Native)
	if err != nil {
		return plan, err
	}
	if !sameExecutionGeometryV1(plan.Active, p.Native.Active.numeric()) {
		return plan, fmt.Errorf("native stored active map differs from the fresh execution plan")
	}
	return plan, nil
}

// AdmitNativeExecution commits provenance and the one-shot release attempt
// before the trusted owner may release its waiter. An uncertain write grants
// no release. A retry, including after readback, cannot issue a second grant.
func (ops IdentityMappingOperationsV1) AdmitNativeExecution(ctx context.Context, expected StateV1, operationID string) (StateV1, error) {
	m := expected.IdentityMapping
	if m == nil || m.PendingOperation == nil || m.PendingOperation.Native == nil || m.PendingOperation.Kind != "native-execute" {
		return StateV1{}, fmt.Errorf("native admission requires its recorded operation")
	}
	if _, err := ops.ObserveEffects(ctx, expected, m.PendingOperation.Native.Active.numeric(), operationID); err != nil {
		return StateV1{}, err
	}
	plan, err := ops.checkNativePending(ctx, expected)
	if err != nil {
		return StateV1{}, err
	}
	return ops.change(ctx, expected, func(state *StateV1) error {
		m := state.IdentityMapping
		p := m.PendingOperation
		if ops.Evidence == nil {
			return fmt.Errorf("native admission requires exact inhibited-instance evidence")
		}
		if err := ops.Evidence.Verify(ctx, cloneMappingStateV1(*state)); err != nil {
			return fmt.Errorf("native instance remains unverified: %w", err)
		}
		reserved, accepted := mappingGeometryStateV1(plan.ProspectiveReservations), mappingGeometryStateV1(plan.ProspectiveAccepted)
		m.UIDAssignments, m.GIDAssignments = reserved.UIDs, reserved.GIDs
		m.AcceptedUIDs, m.AcceptedGIDs = &accepted.UIDs, &accepted.GIDs
		m.DormantUIDs = mappingGeometryStateV1(identitymapping.Geometry{UIDs: plan.DormantUIDs}).UIDs
		m.DormantGIDs = mappingGeometryStateV1(identitymapping.Geometry{GIDs: plan.DormantGIDs}).GIDs
		m.AdmittedGeneration, m.Engine, m.NativeProcess = p.ExpectedGeneration, &p.Native.Engine, &p.Native.Process
		p.Phase, p.ExpectedRevision = "release-attempted", strconv.FormatUint(mappingDecimalV1(m.Revision)+1, 10)
		return nil
	})
}

// recoverNative requires phase-specific trusted evidence: exact acknowledged
// release or verified owned retirement for release-attempted, never mere engine
// presence/absence. It retains reservations and never creates admission.
func (ops IdentityMappingOperationsV1) recoverNative(ctx context.Context, expected StateV1) (StateV1, error) {
	return ops.change(ctx, expected, func(state *StateV1) error {
		if err := ops.Evidence.Verify(ctx, cloneMappingStateV1(*state)); err != nil {
			return fmt.Errorf("native operation remains uncertain: %w", err)
		}
		m := state.IdentityMapping
		var previous *identitymapping.Geometry
		if len(m.UIDAssignments) != 0 {
			previous = geometryPointerV1(m.geometry().numeric())
		}
		reserved, err := identitymapping.RetainGeometry(m.PendingOperation.Candidate.numeric(), previous)
		if err != nil {
			return err
		}
		wire := mappingGeometryStateV1(reserved)
		m.UIDAssignments, m.GIDAssignments = wire.UIDs, wire.GIDs
		m.RetainedStorage = slices.Compact(slices.Sorted(slices.Values(append(m.RetainedStorage, m.PendingOperation.OwnedObjects...))))
		m.PendingOperation = nil
		return nil
	})
}
