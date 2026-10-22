package identitymapping

import (
	"cmp"
	"fmt"
	"slices"
)

// Geometry is numeric mapping input, not host authority or execution admission.
// UID and GID assignments occupy separate collision domains.
type Geometry struct {
	Profile string
	UIDs    []Interval
	GIDs    []Interval
}

// ValidateGeometry returns an independent canonical copy. Work scales with
// explicit intervals, never the number of IDs represented by an interval.
func ValidateGeometry(value Geometry) (Geometry, error) {
	if value.Profile != "exact" && value.Profile != "bounded" {
		return Geometry{}, fmt.Errorf("mapping profile must be exact or bounded")
	}
	result := Geometry{Profile: value.Profile}
	for i, values := range [][]Interval{value.UIDs, value.GIDs} {
		kind := []string{"UID", "GID"}[i]
		if len(values) == 0 || value.Profile == "bounded" && len(values) != 1 {
			return Geometry{}, fmt.Errorf("%s mapping requires nonempty intervals; bounded requires exactly one", kind)
		}
		ordered := slices.Clone(values)
		sortContainerIntervals(ordered)
		for j, interval := range ordered {
			if err := ValidateInterval(interval); err != nil {
				return Geometry{}, fmt.Errorf("%s geometry: %w", kind, err)
			}
			if j > 0 && uint64(interval.ContainerStart) < containerEnd(ordered[j-1]) {
				return Geometry{}, fmt.Errorf("%s container intervals overlap", kind)
			}
		}
		if _, err := hostSpans(ordered); err != nil {
			return Geometry{}, fmt.Errorf("%s geometry: %w", kind, err)
		}
		// Non-overlapping adjacent intervals may describe one affine binding.
		canonical, err := combineReservations(ordered)
		if err != nil {
			return Geometry{}, err
		}
		if i == 0 {
			result.UIDs = canonical
		} else {
			result.GIDs = canonical
		}
	}
	return result, nil
}

// RetainGeometry checks a proposed mapping against protected reservations and
// returns their union. Omitting an assignment never releases its reservation.
// The result does not classify IDs as active or dormant. In particular, a
// never-admitted candidate is not evidence of a previously admitted identity.
// Callers must retain admission and pending-operation provenance separately.
func RetainGeometry(proposed Geometry, retained *Geometry) (Geometry, error) {
	candidate, err := ValidateGeometry(proposed)
	if err != nil {
		return Geometry{}, fmt.Errorf("proposed mapping: %w", err)
	}
	if retained == nil {
		return candidate, nil
	}
	previous, err := ValidateGeometry(*retained)
	if err != nil {
		return Geometry{}, fmt.Errorf("retained mapping: %w", err)
	}
	if candidate.Profile != previous.Profile {
		return Geometry{}, fmt.Errorf("retained mapping profile cannot change")
	}
	if candidate.Profile == "bounded" {
		if !slices.Equal(candidate.UIDs, previous.UIDs) || !slices.Equal(candidate.GIDs, previous.GIDs) {
			return Geometry{}, fmt.Errorf("retained bounded mapping geometry cannot change")
		}
		return previous, nil
	}
	result := Geometry{Profile: candidate.Profile}
	for i, values := range [][]Interval{candidate.UIDs, candidate.GIDs} {
		protected := previous.UIDs
		if i == 1 {
			protected = previous.GIDs
		}
		combined := append(slices.Clone(protected), values...)
		sortContainerIntervals(combined)
		reserved, err := combineReservations(combined)
		if err != nil {
			return Geometry{}, fmt.Errorf("%s retained mapping: %w", []string{"UID", "GID"}[i], err)
		}
		// A different container ID cannot take a retained host ID, including
		// one omitted by the new candidate. UID and GID checks stay separate.
		if _, err := hostSpans(reserved); err != nil {
			return Geometry{}, fmt.Errorf("%s retained mapping: %w", []string{"UID", "GID"}[i], err)
		}
		if i == 0 {
			result.UIDs = reserved
		} else {
			result.GIDs = reserved
		}
	}
	return result, nil
}

func containerEnd(value Interval) uint64 {
	return uint64(value.ContainerStart) + uint64(value.Count)
}

func sortContainerIntervals(values []Interval) {
	slices.SortFunc(values, func(a, b Interval) int {
		return cmp.Compare(a.ContainerStart, b.ContainerStart)
	})
}

func bindingOffset(value Interval) int64 {
	return int64(value.HostStart) - int64(value.ContainerStart)
}

// Inputs have individually validated arithmetic and are container-sorted.
// Compare affine bindings on intersections, including partial intersections.
func combineReservations(values []Interval) ([]Interval, error) {
	result := make([]Interval, 0, len(values))
	for _, value := range values {
		if len(result) == 0 {
			result = append(result, value)
			continue
		}
		last := &result[len(result)-1]
		overlaps := uint64(value.ContainerStart) < containerEnd(*last)
		adjacent := uint64(value.ContainerStart) == containerEnd(*last)
		sameBinding := bindingOffset(value) == bindingOffset(*last)
		if overlaps && !sameBinding {
			return nil, fmt.Errorf("established container-to-host assignment cannot change")
		}
		if sameBinding && (overlaps || adjacent) {
			end := max(containerEnd(*last), containerEnd(value))
			last.Count = uint32(end - uint64(last.ContainerStart))
		} else {
			result = append(result, value)
		}
	}
	return result, nil
}
