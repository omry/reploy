package deploy

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/identitymapping"
)

// IdentityExecutionInputV1 contains caller-authenticated generation evidence and
// numeric history. The locked owner must source Reservations and Accepted from
// versioned state; these values alone cannot prove historical admission.
type IdentityExecutionInputV1 struct {
	Generation    canonical.Digest
	Policy        *IdentityPolicyLockV1
	Candidate     identitymapping.Geometry
	Reservations  *identitymapping.Geometry
	Accepted      *identitymapping.Geometry
	RuntimeUsers  []string
	RuntimeGroups []string
}

// IdentityExecutionPlanV1 is a pure prospective partition, not an admission or
// release token. Each field owns its slices independently of all other fields
// and inputs. Never-admitted reservations remain outside ProspectiveAccepted.
type IdentityExecutionPlanV1 struct {
	Active                  identitymapping.Geometry
	ProspectiveReservations identitymapping.Geometry
	ProspectiveAccepted     identitymapping.Geometry
	DormantUIDs             []identitymapping.Interval
	DormantGIDs             []identitymapping.Interval
}

// PlanIdentityExecutionV1 compiles execution maps without I/O or state changes.
// Work scales with declarations and interval boundaries, never range size.
func PlanIdentityExecutionV1(input IdentityExecutionInputV1) (IdentityExecutionPlanV1, error) {
	fail := func(reason string) (IdentityExecutionPlanV1, error) {
		return IdentityExecutionPlanV1{}, fmt.Errorf("identity execution plan: %s", reason)
	}
	if err := ValidateIdentityPolicyCoverageV1(input.Policy, input.Candidate, input.Generation); err != nil {
		return fail(err.Error())
	}
	candidate, _ := identitymapping.ValidateGeometry(input.Candidate)
	reserved, err := identitymapping.RetainGeometry(candidate, input.Reservations)
	if err != nil {
		return fail(err.Error())
	}
	var accepted *identitymapping.Geometry
	if input.Accepted != nil {
		if input.Reservations == nil {
			return fail("accepted history requires protected reservations")
		}
		previous, err := identitymapping.ValidateGeometry(*input.Reservations)
		if err != nil {
			return fail(err.Error())
		}
		union, err := identitymapping.RetainGeometry(*input.Accepted, &previous)
		if err != nil || !sameExecutionGeometryV1(union, previous) {
			return fail("accepted history must be a subset of protected bindings")
		}
		value, _ := identitymapping.ValidateGeometry(*input.Accepted)
		accepted = &value
	}
	for _, name := range input.RuntimeUsers {
		if _, ok := input.Policy.Policy.Users[name]; !ok {
			return fail("runtime user requires explicit image-verified declaration")
		}
	}
	for _, name := range input.RuntimeGroups {
		if _, ok := input.Policy.Policy.Groups[name]; !ok {
			return fail("runtime group requires explicit image-verified declaration")
		}
	}
	active := candidate
	if candidate.Profile == "exact" {
		uids, gids := map[uint32]bool{}, map[uint32]bool{}
		for _, user := range input.Policy.Policy.Users {
			id, _ := strconv.ParseUint(user.UID, 10, 32)
			uids[uint32(id)] = true
		}
		for _, group := range input.Policy.Policy.Groups {
			id, _ := strconv.ParseUint(group, 10, 32)
			gids[uint32(id)] = true
		}
		active, err = identitymapping.ValidateGeometry(identitymapping.Geometry{
			Profile: candidate.Profile,
			UIDs:    projectExecutionIDsV1(candidate.UIDs, uids),
			GIDs:    projectExecutionIDsV1(candidate.GIDs, gids),
		})
		if err != nil {
			return fail(err.Error())
		}
	}
	prospective, err := identitymapping.RetainGeometry(active, accepted)
	if err != nil {
		return fail(err.Error())
	}
	// Union equality proves C is contained in A union P, with the same affine
	// bindings. In particular, a never-admitted row in R cannot justify surplus C.
	union, err := identitymapping.RetainGeometry(candidate, &prospective)
	if err != nil || !sameExecutionGeometryV1(union, prospective) {
		return fail("candidate contains identities without current intent or prior admission")
	}
	return IdentityExecutionPlanV1{
		Active: active, ProspectiveReservations: reserved, ProspectiveAccepted: prospective,
		DormantUIDs: subtractExecutionIntervalsV1(prospective.UIDs, active.UIDs),
		DormantGIDs: subtractExecutionIntervalsV1(prospective.GIDs, active.GIDs),
	}, nil
}

func sameExecutionGeometryV1(a, b identitymapping.Geometry) bool {
	return a.Profile == b.Profile && slices.Equal(a.UIDs, b.UIDs) && slices.Equal(a.GIDs, b.GIDs)
}

// The coverage check has already proved each ID is in a validated interval.
func projectExecutionIDsV1(intervals []identitymapping.Interval, ids map[uint32]bool) []identitymapping.Interval {
	ordered := make([]uint32, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	slices.Sort(ordered)
	result := make([]identitymapping.Interval, 0, len(ids))
	i := 0
	for _, id := range ordered {
		for uint64(id) >= uint64(intervals[i].ContainerStart)+uint64(intervals[i].Count) {
			i++
		}
		interval := intervals[i]
		result = append(result, identitymapping.Interval{
			ContainerStart: id, HostStart: interval.HostStart + (id - interval.ContainerStart), Count: 1,
		})
	}
	return result
}

// Both inputs are canonical with matching bindings on intersections. Subtract
// by boundaries so even a whole uint32-sized range takes no per-ID iteration.
func subtractExecutionIntervalsV1(from, remove []identitymapping.Interval) []identitymapping.Interval {
	var result []identitymapping.Interval
	j := 0
	for _, interval := range from {
		start := uint64(interval.ContainerStart)
		end := start + uint64(interval.Count)
		appendSpan := func(lo, hi uint64) {
			if lo < hi {
				result = append(result, identitymapping.Interval{
					ContainerStart: uint32(lo), HostStart: interval.HostStart + uint32(lo-uint64(interval.ContainerStart)), Count: uint32(hi - lo),
				})
			}
		}
		for j < len(remove) && uint64(remove[j].ContainerStart)+uint64(remove[j].Count) <= start {
			j++
		}
		for j < len(remove) && uint64(remove[j].ContainerStart) < end {
			cut := remove[j]
			appendSpan(start, min(end, uint64(cut.ContainerStart)))
			start = max(start, uint64(cut.ContainerStart)+uint64(cut.Count))
			if start >= end {
				break
			}
			j++
		}
		appendSpan(start, end)
	}
	return result
}
