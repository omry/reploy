package deploy

import (
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/identitymapping"
)

const IdentityMappingStateSchemaV1 = "identity-mapping-v1"

// IdentityMappingStateV1 is deployment-local retained authority, not execution
// admission. InstallationID identifies the mapping owner across uninstall.
// InstallationBinding identifies only its currently installed service.
type IdentityMappingStateV1 struct {
	Schema              string                      `json:"schema"`
	InstallationID      string                      `json:"installation_id"`
	OwnerDir            string                      `json:"owner_dir"`
	InstallationBinding *string                     `json:"installation_binding"`
	Revision            string                      `json:"revision"`
	OperatorUID         string                      `json:"operator_uid"`
	OperatorGID         string                      `json:"operator_gid"`
	Profile             string                      `json:"profile"`
	UIDAssignments      []IdentityMappingIntervalV1 `json:"uid_assignments"`
	GIDAssignments      []IdentityMappingIntervalV1 `json:"gid_assignments"`
	DormantUIDs         []IdentityMappingIntervalV1 `json:"dormant_uids"`
	DormantGIDs         []IdentityMappingIntervalV1 `json:"dormant_gids"`
	Supervisor          *string                     `json:"supervisor"`
	Engine              *string                     `json:"engine"`
	AdmittedGeneration  *EnvironmentGenerationState `json:"admitted_generation"`
	RetainedStorage     []string                    `json:"retained_storage"`
	Revoked             bool                        `json:"revoked"`
	PendingOperation    *IdentityMappingOperationV1 `json:"pending_operation"`
}

// IdentityMappingOperationV1 retains an inhibited candidate separately from
// protected assignments. A pending candidate is reserved, never admitted.
// B5 owns operation transitions; later effect owners must verify exact handles.
type IdentityMappingOperationV1 struct {
	OperationID        string                      `json:"operation_id"`
	Kind               string                      `json:"kind"`
	ExpectedRevision   string                      `json:"expected_revision"`
	ExpectedGeneration *EnvironmentGenerationState `json:"expected_generation"`
	Candidate          IdentityMappingGeometryV1   `json:"candidate"`
	PriorAccepted      *EnvironmentGenerationState `json:"prior_accepted"`
	OwnedObjects       []string                    `json:"owned_objects"`
	Phase              string                      `json:"phase"`
	Inhibited          bool                        `json:"inhibited"`
}

func validateIdentityMappingStateV1(state StateV1) error {
	m := state.IdentityMapping
	if m == nil {
		return nil
	}
	if m.Schema != IdentityMappingStateSchemaV1 || !safeRecoveryIdentity(m.InstallationID) || !validMappingDecimalV1(m.Revision, 64, true) {
		return fmt.Errorf("identity mapping requires its schema, stable owner ID and positive revision")
	}
	if !filepath.IsAbs(m.OwnerDir) || filepath.Clean(m.OwnerDir) != m.OwnerDir || !safeRecoveryIdentity(m.OwnerDir) {
		return fmt.Errorf("identity mapping requires a canonical absolute owner directory")
	}
	if !validMappingDecimalV1(m.OperatorUID, 32, true) || !validMappingDecimalV1(m.OperatorGID, 32, true) {
		return fmt.Errorf("identity mapping requires a non-root usable operator UID/GID")
	}
	if m.Profile != "exact" && m.Profile != "bounded" {
		return fmt.Errorf("identity mapping profile must be exact or bounded")
	}
	if state.Staging == nil && state.Deployment == nil {
		return fmt.Errorf("identity mapping requires retained staging or installed authority")
	}
	if state.Deployment == nil {
		if m.InstallationBinding != nil {
			return fmt.Errorf("staged identity mapping cannot retain an active installation binding")
		}
	} else if m.InstallationBinding == nil || *m.InstallationBinding != state.Deployment.Installation.InstanceID {
		return fmt.Errorf("identity mapping installation binding does not match installed facts")
	}
	if (len(m.UIDAssignments) == 0) != (len(m.GIDAssignments) == 0) {
		return fmt.Errorf("identity mapping requires both protected ID domains or neither")
	}
	if len(m.UIDAssignments) != 0 {
		if err := requireCanonicalMappingGeometryV1(m.geometry()); err != nil {
			return err
		}
	}
	for _, binding := range []*string{m.Supervisor, m.Engine} {
		if binding != nil && !safeRecoveryIdentity(*binding) {
			return fmt.Errorf("identity mapping binding must identify retained authority")
		}
	}
	if m.AdmittedGeneration != nil {
		if err := ValidateEnvironmentGenerationState(*m.AdmittedGeneration); err != nil {
			return fmt.Errorf("identity mapping admitted generation: %w", err)
		}
		if len(m.UIDAssignments) == 0 || m.Supervisor == nil || m.Engine == nil {
			return fmt.Errorf("identity mapping admission requires retained assignments and bindings")
		}
	}
	for i, values := range [][]IdentityMappingIntervalV1{m.DormantUIDs, m.DormantGIDs} {
		if len(values) == 0 {
			continue
		}
		if m.AdmittedGeneration == nil {
			return fmt.Errorf("dormant assignments require prior admitted authority")
		}
		if err := requireCanonicalMappingGeometryV1(IdentityMappingGeometryV1{Profile: "exact", UIDs: values, GIDs: values}); err != nil {
			return err
		}
		protected := m.UIDAssignments
		if i == 1 {
			protected = m.GIDAssignments
		}
		for _, value := range values {
			if !slices.ContainsFunc(protected, func(old IdentityMappingIntervalV1) bool {
				return mappingDecimalV1(value.ContainerStart) >= mappingDecimalV1(old.ContainerStart) &&
					mappingDecimalV1(value.ContainerStart)+mappingDecimalV1(value.Count) <= mappingDecimalV1(old.ContainerStart)+mappingDecimalV1(old.Count) &&
					int64(mappingDecimalV1(value.HostStart))-int64(mappingDecimalV1(value.ContainerStart)) == int64(mappingDecimalV1(old.HostStart))-int64(mappingDecimalV1(old.ContainerStart))
			}) {
				return fmt.Errorf("dormant assignment is not a retained admitted association")
			}
		}
	}
	if err := validateMappingHandlesV1(m.RetainedStorage); err != nil {
		return err
	}
	if pending := m.PendingOperation; pending != nil {
		if !safeRecoveryIdentity(pending.OperationID) || !safeRecoveryIdentity(pending.Kind) || !pending.Inhibited ||
			!validMappingDecimalV1(pending.ExpectedRevision, 64, true) || mappingDecimalV1(pending.ExpectedRevision) > mappingDecimalV1(m.Revision) ||
			(pending.Phase != "intent" && pending.Phase != "effects-pending" && pending.Phase != "verified") {
			return fmt.Errorf("identity mapping pending operation requires exact inhibited authority and phase")
		}
		if pending.Candidate.Profile != m.Profile {
			return fmt.Errorf("identity mapping pending profile differs from retained profile")
		}
		if err := requireCanonicalMappingGeometryV1(pending.Candidate); err != nil {
			return err
		}
		if len(m.UIDAssignments) != 0 {
			protected := m.geometry()
			if _, err := identitymapping.RetainGeometry(pending.Candidate.numeric(), geometryPointerV1(protected.numeric())); err != nil {
				return err
			}
		}
		for _, generation := range []*EnvironmentGenerationState{pending.ExpectedGeneration, pending.PriorAccepted} {
			if generation != nil {
				if err := ValidateEnvironmentGenerationState(*generation); err != nil {
					return err
				}
			}
		}
		if err := validateMappingHandlesV1(pending.OwnedObjects); err != nil {
			return err
		}
	}
	return nil
}

func (m IdentityMappingStateV1) geometry() IdentityMappingGeometryV1 {
	return IdentityMappingGeometryV1{Profile: m.Profile, UIDs: m.UIDAssignments, GIDs: m.GIDAssignments}
}

func requireCanonicalMappingGeometryV1(value IdentityMappingGeometryV1) error {
	for _, values := range [][]IdentityMappingIntervalV1{value.UIDs, value.GIDs} {
		for _, v := range values {
			if !validMappingDecimalV1(v.ContainerStart, 32, false) || !validMappingDecimalV1(v.HostStart, 32, false) || !validMappingDecimalV1(v.Count, 64, true) || mappingDecimalV1(v.Count) > math.MaxUint32 {
				return fmt.Errorf("mapping intervals require canonical decimal IDs and counts")
			}
		}
	}
	checked, err := identitymapping.ValidateGeometry(value.numeric())
	if err != nil || !reflect.DeepEqual(checked, value.numeric()) {
		return fmt.Errorf("identity mapping geometry must be valid and canonical: %v", err)
	}
	return nil
}

func validateMappingHandlesV1(handles []string) error {
	for i, handle := range handles {
		if !safeRecoveryIdentity(handle) || i > 0 && handles[i-1] >= handle {
			return fmt.Errorf("identity mapping handles must be safe, unique and sorted")
		}
	}
	return nil
}

// State uses decimal strings, as required by the canonical state encoder.
type IdentityMappingIntervalV1 struct {
	ContainerStart string `json:"container_start"`
	HostStart      string `json:"host_start"`
	Count          string `json:"count"`
}
type IdentityMappingGeometryV1 struct {
	Profile string                      `json:"profile"`
	UIDs    []IdentityMappingIntervalV1 `json:"uids"`
	GIDs    []IdentityMappingIntervalV1 `json:"gids"`
}

func mappingDecimalV1(value string) uint64 { n, _ := strconv.ParseUint(value, 10, 64); return n }
func validMappingDecimalV1(value string, bits int, positive bool) bool {
	n, err := strconv.ParseUint(value, 10, bits)
	return err == nil && strconv.FormatUint(n, 10) == value && (!positive || n > 0) && (bits != 32 || n < math.MaxUint32)
}
func (g IdentityMappingGeometryV1) numeric() identitymapping.Geometry {
	result := identitymapping.Geometry{Profile: g.Profile}
	for i, values := range [][]IdentityMappingIntervalV1{g.UIDs, g.GIDs} {
		var out []identitymapping.Interval
		for _, v := range values {
			out = append(out, identitymapping.Interval{ContainerStart: uint32(mappingDecimalV1(v.ContainerStart)), HostStart: uint32(mappingDecimalV1(v.HostStart)), Count: uint32(mappingDecimalV1(v.Count))})
		}
		if i == 0 {
			result.UIDs = out
		} else {
			result.GIDs = out
		}
	}
	return result
}
func geometryPointerV1(g identitymapping.Geometry) *identitymapping.Geometry { return &g }
