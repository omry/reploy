package deploy

import (
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/identitymapping"
)

// NativeIdentityOperationV1 binds the pending create and one-shot release to
// its computed active map. Engine/Process identify the trusted effect owner's
// exact owned instance, not an image name or an inspect-based adoption claim.
type NativeIdentityOperationV1 struct {
	Active        IdentityMappingGeometryV1 `json:"active"`
	Engine        string                    `json:"engine"`
	Process       string                    `json:"process"`
	RuntimeUsers  []string                  `json:"runtime_users"`
	RuntimeGroups []string                  `json:"runtime_groups"`
}

func (m IdentityMappingStateV1) acceptedGeometry() *identitymapping.Geometry {
	if m.AcceptedUIDs == nil || m.AcceptedGIDs == nil || len(*m.AcceptedUIDs) == 0 {
		return nil
	}
	return geometryPointerV1((IdentityMappingGeometryV1{Profile: m.Profile, UIDs: *m.AcceptedUIDs, GIDs: *m.AcceptedGIDs}).numeric())
}

func validateNativeMappingStateV1(m *IdentityMappingStateV1) error {
	p := m.PendingOperation
	if m.Schema == IdentityMappingStateSchemaV1 {
		if m.ProcessModel != "" || m.AcceptedUIDs != nil || m.AcceptedGIDs != nil || m.NativeProcess != nil || p != nil && (p.Native != nil || p.Kind == "native-execute") {
			return fmt.Errorf("legacy mapping cannot carry native admission authority")
		}
		return nil
	}
	if m.ProcessModel != IdentityMappingNativeProcessModelV1 || m.Supervisor != nil || m.AcceptedUIDs == nil || m.AcceptedGIDs == nil || *m.AcceptedUIDs == nil || *m.AcceptedGIDs == nil {
		return fmt.Errorf("native mapping requires its process model and explicit accepted arrays without a supervisor")
	}
	if (len(*m.AcceptedUIDs) == 0) != (len(*m.AcceptedGIDs) == 0) {
		return fmt.Errorf("native admission requires both accepted ID domains")
	}
	accepted := m.acceptedGeometry()
	if accepted == nil {
		if m.AdmittedGeneration != nil || m.Engine != nil || m.NativeProcess != nil || len(m.DormantUIDs)+len(m.DormantGIDs) != 0 {
			return fmt.Errorf("empty accepted provenance cannot carry admission or dormancy")
		}
	} else {
		wire := IdentityMappingGeometryV1{Profile: m.Profile, UIDs: *m.AcceptedUIDs, GIDs: *m.AcceptedGIDs}
		if err := requireCanonicalMappingGeometryV1(wire); err != nil {
			return err
		}
		reserved := m.geometry().numeric()
		union, err := identitymapping.RetainGeometry(*accepted, &reserved)
		if err != nil || !sameExecutionGeometryV1(union, reserved) || m.AdmittedGeneration == nil || m.NativeProcess == nil || !safeRecoveryIdentity(*m.NativeProcess) {
			return fmt.Errorf("native accepted provenance requires protected identical bindings and native admission")
		}
		active := identitymapping.Geometry{Profile: m.Profile, UIDs: subtractExecutionIntervalsV1(accepted.UIDs, (IdentityMappingGeometryV1{UIDs: m.DormantUIDs}).numeric().UIDs), GIDs: subtractExecutionIntervalsV1(accepted.GIDs, (IdentityMappingGeometryV1{GIDs: m.DormantGIDs}).numeric().GIDs)}
		if _, err := identitymapping.ValidateGeometry(active); err != nil || m.Profile == "bounded" && (len(m.DormantUIDs)+len(m.DormantGIDs) != 0 || !sameExecutionGeometryV1(*accepted, reserved)) {
			return fmt.Errorf("native admission requires valid active geometry and unchanged bounded authority")
		}
	}
	if p == nil {
		return nil
	}
	if p.Kind == "configure" && p.Native == nil {
		return nil
	}
	if p.Kind != "native-execute" || p.Native == nil || p.ExpectedGeneration == nil || p.ExpectedRevision != m.Revision || !safeRecoveryIdentity(p.Native.Engine) || !safeRecoveryIdentity(p.Native.Process) || p.Phase == "verified" {
		return fmt.Errorf("native operation requires exact kind, generation, revision and owned bindings")
	}
	if err := requireCanonicalMappingGeometryV1(p.Native.Active); err != nil {
		return err
	}
	union, err := identitymapping.RetainGeometry(p.Native.Active.numeric(), geometryPointerV1(p.Candidate.numeric()))
	if err != nil || !sameExecutionGeometryV1(union, p.Candidate.numeric()) {
		return fmt.Errorf("native active map must be a subset of the candidate")
	}
	if p.Phase == "release-attempted" {
		if !reflect.DeepEqual(m.AdmittedGeneration, p.ExpectedGeneration) || m.Engine == nil || *m.Engine != p.Native.Engine || m.NativeProcess == nil || *m.NativeProcess != p.Native.Process || accepted == nil {
			return fmt.Errorf("release attempt requires its exact committed native admission")
		}
		active := identitymapping.Geometry{Profile: m.Profile, UIDs: subtractExecutionIntervalsV1(accepted.UIDs, (IdentityMappingGeometryV1{UIDs: m.DormantUIDs}).numeric().UIDs), GIDs: subtractExecutionIntervalsV1(accepted.GIDs, (IdentityMappingGeometryV1{GIDs: m.DormantGIDs}).numeric().GIDs)}
		if !sameExecutionGeometryV1(active, p.Native.Active.numeric()) {
			return fmt.Errorf("release attempt active map differs from admitted provenance")
		}
	}
	return nil
}
