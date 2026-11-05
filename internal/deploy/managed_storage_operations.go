package deploy

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/identitymapping"
)

// ManagedStorageEvidenceV1 extends the existing trusted effect owner. It proves
// exact retained ownership, held descriptor identities and complete current
// attachment/inhibition observations. Unknown inventory, missing restart fences
// or successful create/stop/exit alone must fail. In verified phase it also
// proves exact prepared objects and helper retirement, or the exact attachment.
// This is a trusted host adapter, never an application/operator assertion.
type ManagedStorageEvidenceV1 interface {
	ObserveManagedStorage(context.Context, StateV1) error
}

func (ops IdentityMappingOperationsV1) observeManagedStorage(ctx context.Context, state StateV1) error {
	proof, ok := ops.Evidence.(ManagedStorageEvidenceV1)
	if !ok || proof == nil || ctx == nil || ctx.Err() != nil {
		return fmt.Errorf("managed storage requires its current exact resource observer")
	}
	return proof.ObserveManagedStorage(ctx, cloneMappingStateV1(state))
}

// PrepareManagedStorage captures only already-authenticated retained handles.
// Allocation remains with the existing checked effect owner; this method never
// creates, adopts or mutates storage. The installation lock spans all effects.
func (ops IdentityMappingOperationsV1) PrepareManagedStorage(ctx context.Context, expected StateV1, active identitymapping.Geometry, operationID string, request ManagedStorageOperationV1) (StateV1, error) {
	if err := ops.Operation.RequireHeld(); err != nil {
		return StateV1{}, err
	}
	geometry, err := identitymapping.ValidateGeometry(active)
	if err != nil || !safeRecoveryIdentity(operationID) {
		return StateV1{}, fmt.Errorf("managed storage requires exact operation and selected geometry: %v", err)
	}
	if _, _, err := ops.check(ctx, expected, geometry); err != nil {
		return StateV1{}, err
	}
	content, err := canonical.Marshal(request)
	if err != nil {
		return StateV1{}, err
	}
	request = ManagedStorageOperationV1{}
	if err := json.Unmarshal(content, &request); err != nil {
		return StateV1{}, err
	}
	return ops.change(ctx, expected, func(state *StateV1) error {
		m := state.IdentityMapping
		if m == nil || m.Schema != IdentityMappingStateSchemaV2 || state.Current == nil || m.PendingOperation != nil ||
			!managedStorageSubsetV1(mappingGeometryStateV1(geometry), m.geometry()) {
			return fmt.Errorf("managed storage requires existing native reservations without a pending operation")
		}
		request.Active = mappingGeometryStateV1(geometry)
		ids := make([]string, len(request.Resources))
		for i := range request.Resources {
			r := &request.Resources[i]
			ids[i], r.Generation, r.Active = r.ID, state.Current, request.Active
			old := slices.IndexFunc(m.ManagedStorage, func(v ManagedStorageResourceV1) bool { return v.ID == r.ID })
			if r.Attachment != nil || old < 0 && (request.Action != "prepare" || len(r.Objects) != 0) {
				return fmt.Errorf("unrecorded or attached resources cannot be adopted")
			}
			if old >= 0 {
				prior := m.ManagedStorage[old]
				if prior.Attachment != nil || !reflect.DeepEqual(prior.Root, r.Root) || !reflect.DeepEqual(prior.Objects, r.Objects) ||
					request.Action == "attach" && (!reflect.DeepEqual(prior.Generation, state.Current) || !reflect.DeepEqual(prior.Active, request.Active) || !reflect.DeepEqual(prior.Declarations, r.Declarations)) {
					return fmt.Errorf("managed resource is attached, changed or not prepared for this selection")
				}
			}
		}
		m.PendingOperation = &IdentityMappingOperationV1{OperationID: operationID, Kind: "managed-storage", ExpectedRevision: strconv.FormatUint(mappingDecimalV1(m.Revision)+1, 10), ExpectedGeneration: state.Current, PriorAccepted: m.AdmittedGeneration, Candidate: m.geometry(), OwnedObjects: ids, Phase: "intent", Inhibited: true, Storage: &request}
		*state = cloneMappingStateV1(*state)
		// Validate with the prospective revision before asking any observer.
		candidate := cloneMappingStateV1(*state)
		candidate.IdentityMapping.Revision = candidate.IdentityMapping.PendingOperation.ExpectedRevision
		if err := ValidateStateV1(candidate); err != nil {
			return err
		}
		return ops.observeManagedStorage(ctx, candidate)
	})
}

// RecordManagedStorage binds the actual result before recovery can commit it.
// Missing/partial results keep effects-pending inhibition. No effect is replayed.
func (ops IdentityMappingOperationsV1) RecordManagedStorage(ctx context.Context, expected StateV1, operationID string, resources []ManagedStorageResourceV1) (StateV1, error) {
	m := expected.IdentityMapping
	if m == nil || m.PendingOperation == nil || m.PendingOperation.Storage == nil {
		return StateV1{}, fmt.Errorf("managed storage result requires its pending selection")
	}
	if err := ops.checkPending(ctx, expected, operationID); err != nil {
		return StateV1{}, err
	}
	if _, err := canonical.Marshal(resources); err != nil {
		return StateV1{}, err
	}
	return ops.change(ctx, expected, func(state *StateV1) error {
		p := state.IdentityMapping.PendingOperation
		s := p.Storage
		if p.Phase != "effects-pending" || len(resources) != len(s.Resources) {
			return fmt.Errorf("managed storage result is incomplete")
		}
		for i, r := range resources {
			prior := s.Resources[i]
			if r.ID != prior.ID || !reflect.DeepEqual(r.Root, prior.Root) || !reflect.DeepEqual(r.Declarations, prior.Declarations) || !reflect.DeepEqual(r.Generation, prior.Generation) || !reflect.DeepEqual(r.Active, prior.Active) ||
				s.Action == "prepare" && r.Attachment != nil || s.Action == "attach" && (!reflect.DeepEqual(r.Objects, prior.Objects) || !reflect.DeepEqual(r.Attachment, s.Attachment)) {
				return fmt.Errorf("managed storage result changes its captured authority")
			}
		}
		s.Resources, p.Phase = resources, "verified"
		*state = cloneMappingStateV1(*state)
		if err := validateManagedStorageStateV1(*state); err != nil {
			return err
		}
		return ops.observeManagedStorage(ctx, *state)
	})
}

func (ops IdentityMappingOperationsV1) recoverManagedStorage(ctx context.Context, expected StateV1) (StateV1, error) {
	return ops.change(ctx, expected, func(state *StateV1) error {
		m := state.IdentityMapping
		p := m.PendingOperation
		if p.Phase != "verified" {
			return fmt.Errorf("managed storage effects remain inhibited until their exact result is verified")
		}
		if err := ops.observeManagedStorage(ctx, *state); err != nil {
			return err
		}
		for _, r := range p.Storage.Resources {
			i := slices.IndexFunc(m.ManagedStorage, func(v ManagedStorageResourceV1) bool { return v.ID == r.ID })
			if i < 0 {
				m.ManagedStorage = append(m.ManagedStorage, r)
			} else {
				m.ManagedStorage[i] = r
			}
		}
		slices.SortFunc(m.ManagedStorage, func(a, b ManagedStorageResourceV1) int { return cmp.Compare(a.ID, b.ID) })
		m.PendingOperation = nil
		return nil
	})
}
