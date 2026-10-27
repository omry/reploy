package deploy

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/identitymapping"
)

// ValidateIdentityPolicyCoverageV1 checks reservations, not execution admission.
// The locked owner must authenticate the selected generation and its evidence.
// Work scales with declarations and intervals, never the represented ID range.
func ValidateIdentityPolicyCoverageV1(lock *IdentityPolicyLockV1, geometry identitymapping.Geometry, generation canonical.Digest) error {
	if lock == nil || generation.Validate() != nil {
		return fmt.Errorf("identity policy coverage requires selected locked evidence and generation")
	}
	if err := ValidateIdentityPolicyLockV1(lock, lock.Evidence.Image, lock.Evidence.Platform); err != nil {
		return err
	}
	reserved, err := identitymapping.ValidateGeometry(geometry)
	if err != nil {
		return fmt.Errorf("identity policy coverage reservations: %w", err)
	}
	type missingID struct {
		kind, path string
		id         uint64
	}
	var missing []missingID
	check := func(kind, value, path string, intervals []identitymapping.Interval) {
		// Validated policy and evidence contain canonical uint32 IDs.
		id, _ := strconv.ParseUint(value, 10, 32)
		for _, interval := range intervals {
			start := uint64(interval.ContainerStart)
			if id >= start && id < start+uint64(interval.Count) {
				return
			}
		}
		missing = append(missing, missingID{kind: kind, path: path, id: id})
	}
	const policyPath = "environment.runtime.identity_policy."
	for name, user := range lock.Policy.Users {
		path := policyPath + "users." + name
		check("UID", user.UID, path+".uid", reserved.UIDs)
		check("GID", lock.Policy.Groups[user.PrimaryGroup], path+".primary_group", reserved.GIDs)
		for i, group := range user.SupplementaryGroups {
			check("GID", lock.Policy.Groups[group], fmt.Sprintf("%s.supplementary_groups[%d]", path, i), reserved.GIDs)
		}
	}
	for name, gid := range lock.Policy.Groups {
		check("GID", gid, policyPath+"groups."+name, reserved.GIDs)
	}
	slices.SortFunc(missing, func(a, b missingID) int {
		if n := cmp.Compare(a.kind, b.kind); n != 0 {
			return n
		}
		if n := cmp.Compare(a.id, b.id); n != 0 {
			return n
		}
		return cmp.Compare(a.path, b.path)
	})
	if len(missing) == 0 {
		return nil
	}
	details := make([]string, len(missing))
	for i, value := range missing {
		details[i] = fmt.Sprintf("%s %d at %s", value.kind, value.id, value.path)
	}
	return fmt.Errorf("identity policy reservation coverage for generation %s: missing %s", generation, strings.Join(details, "; "))
}
