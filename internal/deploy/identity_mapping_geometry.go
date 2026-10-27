package deploy

import (
	"fmt"
	"strconv"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/identitymapping"
)

// RetainIdentityMappingGeometryV1 preserves established assignments while
// checking a proposed geometry. Pending intents remain separate, as in Configure.
func RetainIdentityMappingGeometryV1(state StateV1, candidate identitymapping.Geometry) (identitymapping.Geometry, error) {
	if err := validateIdentityMappingStateV1(state); err != nil {
		return identitymapping.Geometry{}, err
	}
	var protected *identitymapping.Geometry
	if m := state.IdentityMapping; m != nil {
		if m.Revoked || m.Profile != candidate.Profile {
			return identitymapping.Geometry{}, fmt.Errorf("mapping is revoked or its retained profile differs")
		}
		if len(m.UIDAssignments) != 0 {
			protected = geometryPointerV1(m.geometry().numeric())
		}
	}
	return identitymapping.RetainGeometry(candidate, protected)
}

// ResolveIdentityMappingGeometryV1 checks one operation's effective input. The
// numeric result grants neither protected reservations nor execution admission.
func ResolveIdentityMappingGeometryV1(document blueprint.Document, override *blueprint.IdentityMappingOverrideV1) (*identitymapping.Geometry, error) {
	resolved, err := blueprint.ResolveIdentityMappingV1(document, override)
	if err != nil || resolved == nil {
		return nil, err
	}
	geometry := identitymapping.Geometry{Profile: resolved.Profile.Value}
	for i, values := range [][]blueprint.MappingIntervalV1{resolved.UIDs, resolved.GIDs} {
		intervals := make([]identitymapping.Interval, len(values))
		for j, value := range values {
			fields := [3]uint32{}
			for k, scalar := range []blueprint.MappingScalarV1{value.ContainerStart, value.HostStart, value.Count} {
				number, err := strconv.ParseUint(scalar.Value, 10, 32)
				if err != nil || scalar.Kind != "integer" || strconv.FormatUint(number, 10) != scalar.Value {
					return nil, fmt.Errorf("mapping %s[%d] requires resolved integer IDs", []string{"uids", "gids"}[i], j)
				}
				fields[k] = uint32(number)
			}
			intervals[j] = identitymapping.Interval{ContainerStart: fields[0], HostStart: fields[1], Count: fields[2]}
		}
		if i == 0 {
			geometry.UIDs = intervals
		} else {
			geometry.GIDs = intervals
		}
	}
	checked, err := identitymapping.ValidateGeometry(geometry)
	if err != nil {
		return nil, fmt.Errorf("environment %q mapping geometry: %w", document.Environment.ID, err)
	}
	return &checked, nil
}
