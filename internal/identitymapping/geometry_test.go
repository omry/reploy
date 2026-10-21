package identitymapping

import (
	"math"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

func geometryForTest(profile string, values ...Interval) Geometry {
	return Geometry{Profile: profile, UIDs: slices.Clone(values), GIDs: slices.Clone(values)}
}

func cloneGeometryForTest(value Geometry) Geometry {
	return Geometry{Profile: value.Profile, UIDs: slices.Clone(value.UIDs), GIDs: slices.Clone(value.GIDs)}
}

func TestGeometryRejectsInvalidInputWithoutMutation(t *testing.T) {
	valid := geometryForTest("exact", Interval{0, 100, 10})
	cases := map[string]Geometry{
		"missing profile":      {},
		"unknown profile":      geometryForTest("auto", Interval{0, 100, 10}),
		"empty UIDs":           {Profile: "exact", GIDs: valid.GIDs},
		"empty GIDs":           {Profile: "exact", UIDs: valid.UIDs},
		"bounded UID split":    {Profile: "bounded", UIDs: []Interval{{0, 100, 5}, {5, 105, 5}}, GIDs: valid.GIDs},
		"bounded GID split":    {Profile: "bounded", UIDs: valid.UIDs, GIDs: []Interval{{0, 100, 5}, {5, 105, 5}}},
		"zero count":           geometryForTest("exact", Interval{0, 100, 0}),
		"container sentinel":   geometryForTest("exact", Interval{math.MaxUint32, 100, 1}),
		"host sentinel":        geometryForTest("exact", Interval{0, math.MaxUint32, 1}),
		"container overflow":   geometryForTest("exact", Interval{math.MaxUint32 - 1, 100, 2}),
		"host overflow":        geometryForTest("exact", Interval{0, math.MaxUint32 - 1, 2}),
		"host root":            geometryForTest("exact", Interval{0, 0, 1}),
		"container overlap":    geometryForTest("exact", Interval{5, 200, 4}, Interval{0, 100, 8}),
		"duplicate container":  geometryForTest("exact", Interval{0, 100, 1}, Interval{0, 200, 1}),
		"host overlap":         geometryForTest("exact", Interval{0, 100, 8}, Interval{20, 104, 8}),
		"duplicate host":       geometryForTest("exact", Interval{0, 100, 1}, Interval{20, 100, 1}),
		"equal affine overlap": geometryForTest("exact", Interval{0, 100, 8}, Interval{4, 104, 8}),
		"GID host root":        {Profile: "exact", UIDs: valid.UIDs, GIDs: []Interval{{0, 0, 1}}},
		"GID overlap":          {Profile: "exact", UIDs: valid.UIDs, GIDs: []Interval{{0, 100, 8}, {20, 104, 8}}},
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			before := cloneGeometryForTest(value)
			got, err := ValidateGeometry(value)
			if err == nil || !reflect.DeepEqual(got, Geometry{}) {
				t.Fatalf("invalid input returned authority: %#v / %v", got, err)
			}
			if !reflect.DeepEqual(value, before) {
				t.Fatal("validation mutated input")
			}
		})
	}
}

func TestGeometryCanonicalizesAndDoesNotAliasInputs(t *testing.T) {
	value := Geometry{
		Profile: "exact",
		UIDs:    []Interval{{12, 112, 3}, {2, 102, 3}, {0, 100, 2}},
		GIDs:    []Interval{{4, 400, 2}, {0, 200, 1}},
	}
	before := cloneGeometryForTest(value)
	want := Geometry{Profile: "exact", UIDs: []Interval{{0, 100, 5}, {12, 112, 3}}, GIDs: []Interval{{0, 200, 1}, {4, 400, 2}}}
	got, err := ValidateGeometry(value)
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(value, before) {
		t.Fatalf("canonical geometry = %#v / %v; source %#v", got, err, value)
	}
	again, err := ValidateGeometry(got)
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("normal form is not idempotent: %#v / %v", again, err)
	}
	again.UIDs[0].HostStart++
	got.GIDs[0].HostStart++
	if !reflect.DeepEqual(value, before) || got.UIDs[0] != want.UIDs[0] {
		t.Fatal("result aliases source or a later result")
	}
}

func TestRetainGeometryProtectsAllEstablishedAssociations(t *testing.T) {
	old := geometryForTest("exact", Interval{0, 100, 10}, Interval{20, 200, 5})
	for _, tc := range []struct {
		name     string
		proposed Geometry
		retained *Geometry
		want     Geometry
		fail     bool
	}{
		{"first candidate", old, nil, old, false},
		{"unchanged", old, &old, old, false},
		{"equivalent splits", geometryForTest("exact", Interval{0, 100, 3}, Interval{3, 103, 7}, Interval{20, 200, 5}), &old, old, false},
		{"omitted assignment stays reserved", geometryForTest("exact", Interval{0, 100, 10}), &old, old, false},
		{"partial omission stays reserved", geometryForTest("exact", Interval{2, 102, 4}), &old, old, false},
		{"rollback subset", geometryForTest("exact", Interval{20, 200, 5}), &old, old, false},
		{"disjoint addition", geometryForTest("exact", Interval{40, 300, 3}), &old, geometryForTest("exact", Interval{0, 100, 10}, Interval{20, 200, 5}, Interval{40, 300, 3}), false},
		{"same binding extends", geometryForTest("exact", Interval{5, 105, 10}), &old, geometryForTest("exact", Interval{0, 100, 15}, Interval{20, 200, 5}), false},
		{"changed host", geometryForTest("exact", Interval{0, 300, 10}), &old, Geometry{}, true},
		{"partial reassignment", geometryForTest("exact", Interval{5, 300, 10}), &old, Geometry{}, true},
		{"omitted host reused", geometryForTest("exact", Interval{40, 200, 5}), &old, Geometry{}, true},
		{"partial host reuse", geometryForTest("exact", Interval{40, 198, 4}), &old, Geometry{}, true},
		{"host reuse starts inside", geometryForTest("exact", Interval{40, 204, 2}), &old, Geometry{}, true},
		{"profile changed", geometryForTest("bounded", Interval{0, 100, 10}), &old, Geometry{}, true},
		{"invalid candidate", geometryForTest("exact", Interval{0, 100, 0}), &old, Geometry{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := cloneGeometryForTest(tc.proposed)
			var saved Geometry
			if tc.retained != nil {
				saved = cloneGeometryForTest(*tc.retained)
			}
			got, err := RetainGeometry(tc.proposed, tc.retained)
			if (err != nil) != tc.fail || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("retained = %#v / %v; want %#v, failure %v", got, err, tc.want, tc.fail)
			}
			if !reflect.DeepEqual(before, tc.proposed) || tc.retained != nil && !reflect.DeepEqual(saved, *tc.retained) {
				t.Fatal("comparison mutated its inputs")
			}
			if err == nil {
				got.UIDs[0].HostStart++
				got.GIDs[0].HostStart++
				if !reflect.DeepEqual(before, tc.proposed) || tc.retained != nil && !reflect.DeepEqual(saved, *tc.retained) {
					t.Fatal("reservation result aliases its inputs")
				}
			}
		})
	}
}

func TestRetainGeometryRejectsMalformedProtectedAuthority(t *testing.T) {
	candidate := geometryForTest("exact", Interval{0, 100, 3})
	for _, old := range []Geometry{
		{}, {Profile: "exact", UIDs: candidate.UIDs},
		geometryForTest("exact", Interval{0, 100, 0}),
		geometryForTest("exact", Interval{0, 0, 1}),
		geometryForTest("exact", Interval{0, 100, 3}, Interval{2, 200, 2}),
		geometryForTest("exact", Interval{0, 100, 3}, Interval{20, 102, 2}),
	} {
		before := cloneGeometryForTest(old)
		if got, err := RetainGeometry(candidate, &old); err == nil || !reflect.DeepEqual(got, Geometry{}) {
			t.Fatalf("malformed protected input accepted: %#v / %v", got, err)
		}
		if !reflect.DeepEqual(before, old) {
			t.Fatal("invalid protected input mutated")
		}
	}
}

func TestBoundedGeometryCannotChangeInEitherIDDomain(t *testing.T) {
	old := Geometry{Profile: "bounded", UIDs: []Interval{{0, 100, 20}}, GIDs: []Interval{{0, 200, 30}}}
	got, err := RetainGeometry(old, &old)
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("unchanged bounded geometry rejected: %#v / %v", got, err)
	}
	for _, kind := range []string{"UID", "GID"} {
		for _, field := range []string{"container", "host", "count"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				candidate := cloneGeometryForTest(old)
				interval := &candidate.UIDs[0]
				if kind == "GID" {
					interval = &candidate.GIDs[0]
				}
				switch field {
				case "container":
					interval.ContainerStart++
				case "host":
					interval.HostStart++
				case "count":
					interval.Count++
				}
				if _, err := RetainGeometry(candidate, &old); err == nil {
					t.Fatal("bounded geometry change accepted")
				}
			})
		}
	}
	exact := cloneGeometryForTest(old)
	exact.Profile = "exact"
	if _, err := RetainGeometry(exact, &old); err == nil {
		t.Fatal("bounded-to-exact profile change accepted")
	}
}

func TestGeometryKeepsUIDAndGIDDomainsIndependent(t *testing.T) {
	old := Geometry{Profile: "exact", UIDs: []Interval{{0, 100, 2}}, GIDs: []Interval{{0, 200, 2}}}
	candidate := Geometry{Profile: "exact", UIDs: []Interval{{20, 200, 2}}, GIDs: []Interval{{20, 100, 2}}}
	got, err := RetainGeometry(candidate, &old)
	if err != nil || len(got.UIDs) != 2 || len(got.GIDs) != 2 {
		t.Fatalf("cross-domain numbers incorrectly collide: %#v / %v", got, err)
	}
	candidate.GIDs = []Interval{{20, 200, 2}}
	if _, err := RetainGeometry(candidate, &old); err == nil {
		t.Fatal("same-domain GID host reuse accepted")
	}
}

func TestGeometryLargeSpansAndSignedOffsets(t *testing.T) {
	// This represents almost 2^32 IDs with one interval. No per-ID work or
	// allocation is possible in the production comparison.
	old := geometryForTest("exact", Interval{0, 1, math.MaxUint32 - 1})
	subset := geometryForTest("exact", Interval{math.MaxUint32 - 3, math.MaxUint32 - 2, 2})
	got, err := RetainGeometry(subset, &old)
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("large reservation comparison failed: %#v / %v", got, err)
	}
	negative := geometryForTest("exact", Interval{math.MaxUint32 - 2, 1, 1})
	addition := geometryForTest("exact", Interval{math.MaxUint32 - 1, 2, 1})
	want := geometryForTest("exact", Interval{math.MaxUint32 - 2, 1, 2})
	got, err = RetainGeometry(addition, &negative)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("large negative offset failed: %#v / %v", got, err)
	}
	last := geometryForTest("exact", Interval{math.MaxUint32 - 1, math.MaxUint32 - 1, 1})
	if _, err := ValidateGeometry(last); err != nil {
		t.Fatalf("last usable IDs rejected: %v", err)
	}
}

func TestRetainGeometryAgainstFiniteAssignmentOracle(t *testing.T) {
	// The oracle deliberately enumerates only small test domains. Production
	// uses interval geometry; the independent model checks both directions.
	random := rand.New(rand.NewSource(8312))
	intervals := func() []Interval {
		a, b := random.Perm(12), random.Perm(12)
		values := make([]Interval, 1+random.Intn(6))
		for i := range values {
			values[i] = Interval{uint32(a[i]), uint32(100 + b[i]), 1}
		}
		return values
	}
	assignments := func(values []Interval) map[uint32]uint32 {
		result := map[uint32]uint32{}
		for _, value := range values {
			for n := uint32(0); n < value.Count; n++ {
				result[value.ContainerStart+n] = value.HostStart + n
			}
		}
		return result
	}
	merge := func(a, b []Interval) (map[uint32]uint32, bool) {
		result, hosts := map[uint32]uint32{}, map[uint32]uint32{}
		for _, values := range [][]Interval{a, b} {
			for container, host := range assignments(values) {
				if previous, ok := result[container]; ok && previous != host {
					return nil, false
				}
				if previous, ok := hosts[host]; ok && previous != container {
					return nil, false
				}
				result[container], hosts[host] = host, container
			}
		}
		return result, true
	}
	for n := 0; n < 500; n++ {
		old := Geometry{Profile: "exact", UIDs: intervals(), GIDs: intervals()}
		candidate := Geometry{Profile: "exact", UIDs: intervals(), GIDs: intervals()}
		if n%3 == 0 {
			candidate = cloneGeometryForTest(old)
		}
		uids, uidOK := merge(old.UIDs, candidate.UIDs)
		gids, gidOK := merge(old.GIDs, candidate.GIDs)
		got, err := RetainGeometry(candidate, &old)
		if (err == nil) != (uidOK && gidOK) {
			t.Fatalf("oracle acceptance differs at %d: %#v / %#v / %v", n, old, candidate, err)
		}
		if err == nil && (!reflect.DeepEqual(assignments(got.UIDs), uids) || !reflect.DeepEqual(assignments(got.GIDs), gids)) {
			t.Fatalf("oracle reservations differ at %d: %#v", n, got)
		}
	}
}
