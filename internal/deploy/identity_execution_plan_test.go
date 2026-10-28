package deploy

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/identitymapping"
)

func executionGeometryV1(uids, gids []uint32) identitymapping.Geometry {
	result := identitymapping.Geometry{Profile: "exact"}
	for i, ids := range [][]uint32{uids, gids} {
		for _, id := range ids {
			interval := identitymapping.Interval{ContainerStart: id, HostStart: 100000 + id, Count: 1}
			if i == 0 {
				result.UIDs = append(result.UIDs, interval)
			} else {
				result.GIDs = append(result.GIDs, interval)
			}
		}
	}
	return result
}

func executionInputV1(t *testing.T) IdentityExecutionInputV1 {
	t.Helper()
	lock := identityPolicyBuildLockFixtureV1(t)
	return IdentityExecutionInputV1{
		Generation: buildLockTestDigest("a"), Policy: lock.IdentityPolicy,
		Candidate:    executionGeometryV1([]uint32{20, 200}, []uint32{20, 40, 200, 300, 450}),
		RuntimeUsers: []string{"worker"}, RuntimeGroups: []string{"worker", "unused"},
	}
}

func canonicalExecutionGeometryV1(t *testing.T, value identitymapping.Geometry) identitymapping.Geometry {
	t.Helper()
	canonical, err := identitymapping.ValidateGeometry(value)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestPlanIdentityExecutionV1CurrentClosure(t *testing.T) {
	input := executionInputV1(t)
	// All declarations count, including unused users/groups and supplementary
	// groups. Candidate order is not part of the numeric identity.
	slices.Reverse(input.Candidate.UIDs)
	slices.Reverse(input.Candidate.GIDs)
	before, _ := json.Marshal(input)
	plan, err := PlanIdentityExecutionV1(input)
	if err != nil {
		t.Fatal(err)
	}
	want := canonicalExecutionGeometryV1(t, input.Candidate)
	for _, got := range []identitymapping.Geometry{plan.Active, plan.ProspectiveReservations, plan.ProspectiveAccepted} {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("wrong declaration closure: got %#v, want %#v", got, want)
		}
	}
	if len(plan.DormantUIDs) != 0 || len(plan.DormantGIDs) != 0 {
		t.Fatal("newly active declarations became dormant")
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("planner mutated its input")
	}
	// Repeated map iteration produces the same canonical plan.
	for range 20 {
		again, err := PlanIdentityExecutionV1(input)
		if err != nil || !reflect.DeepEqual(again, plan) {
			t.Fatalf("nondeterministic plan: %v", err)
		}
	}
}

func TestPlanIdentityExecutionV1HistoricalPartitions(t *testing.T) {
	for _, includeDormant := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted-dormant", true: "included-dormant"}[includeDormant], func(t *testing.T) {
			input := executionInputV1(t)
			// UID 300/GID 500 were admitted; UID 400/GID 600 were only
			// reserved by a later uncertain configuration operation.
			previous := executionGeometryV1([]uint32{200, 300, 400}, []uint32{200, 500, 600})
			accepted := executionGeometryV1([]uint32{200, 300}, []uint32{200, 500})
			input.Reservations, input.Accepted = &previous, &accepted
			if includeDormant {
				input.Candidate.UIDs = append(input.Candidate.UIDs, accepted.UIDs[1])
				input.Candidate.GIDs = append(input.Candidate.GIDs, accepted.GIDs[1])
			}
			plan, err := PlanIdentityExecutionV1(input)
			if err != nil {
				t.Fatal(err)
			}
			wantActive := canonicalExecutionGeometryV1(t, executionInputV1(t).Candidate)
			if !reflect.DeepEqual(plan.Active, wantActive) {
				t.Fatalf("history changed active declarations: %#v", plan.Active)
			}
			wantAccepted, err := identitymapping.RetainGeometry(wantActive, &accepted)
			if err != nil || !reflect.DeepEqual(plan.ProspectiveAccepted, wantAccepted) {
				t.Fatalf("wrong prospective admission: %#v, %v", plan.ProspectiveAccepted, err)
			}
			wantReserved, err := identitymapping.RetainGeometry(input.Candidate, &previous)
			if err != nil || !reflect.DeepEqual(plan.ProspectiveReservations, wantReserved) {
				t.Fatalf("lost protected history: %#v, %v", plan.ProspectiveReservations, err)
			}
			if !reflect.DeepEqual(plan.DormantUIDs, accepted.UIDs[1:]) || !reflect.DeepEqual(plan.DormantGIDs, accepted.GIDs[1:]) {
				t.Fatalf("dormancy included never-admitted reservations: %#v", plan)
			}
		})
	}
}

func TestPlanIdentityExecutionV1NeverAdmittedReservation(t *testing.T) {
	input := executionInputV1(t)
	reserved := executionGeometryV1([]uint32{300}, []uint32{500})
	input.Reservations = &reserved
	plan, err := PlanIdentityExecutionV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.ProspectiveAccepted, plan.Active) || len(plan.DormantUIDs)+len(plan.DormantGIDs) != 0 {
		t.Fatal("reservation promoted to admission or dormancy")
	}
	if len(plan.ProspectiveReservations.UIDs) != 3 || len(plan.ProspectiveReservations.GIDs) != 6 {
		t.Fatal("omitted never-admitted reservation was released")
	}
	for _, kind := range []string{"UID", "GID"} {
		t.Run(kind, func(t *testing.T) {
			candidate := input
			if kind == "UID" {
				candidate.Candidate.UIDs = append(slices.Clone(input.Candidate.UIDs), reserved.UIDs...)
			} else {
				candidate.Candidate.GIDs = append(slices.Clone(input.Candidate.GIDs), reserved.GIDs...)
			}
			if _, err := PlanIdentityExecutionV1(candidate); err == nil {
				t.Fatal("never-admitted surplus candidate authorized")
			}
		})
	}
}

func TestPlanIdentityExecutionV1RejectsInvalidEvidenceAndGeometry(t *testing.T) {
	for name, mutate := range map[string]func(*IdentityExecutionInputV1){
		"nil policy":     func(v *IdentityExecutionInputV1) { v.Policy = nil },
		"generation":     func(v *IdentityExecutionInputV1) { v.Generation = canonical.Digest("invalid") },
		"policy schema":  func(v *IdentityExecutionInputV1) { v.Policy.Schema = "other" },
		"image evidence": func(v *IdentityExecutionInputV1) { delete(v.Policy.Evidence.Files, "etc/passwd") },
		"policy evidence mismatch": func(v *IdentityExecutionInputV1) {
			v.Policy.Policy.Groups["unused"] = "451"
		},
		"missing UID":              func(v *IdentityExecutionInputV1) { v.Candidate.UIDs = v.Candidate.UIDs[:1] },
		"missing unused GID":       func(v *IdentityExecutionInputV1) { v.Candidate.GIDs = v.Candidate.GIDs[:4] },
		"runtime user undeclared":  func(v *IdentityExecutionInputV1) { v.RuntimeUsers = []string{"root"} },
		"runtime group undeclared": func(v *IdentityExecutionInputV1) { v.RuntimeGroups = []string{"root"} },
		"zero count":               func(v *IdentityExecutionInputV1) { v.Candidate.UIDs[0].Count = 0 },
		"container overlap":        func(v *IdentityExecutionInputV1) { v.Candidate.UIDs[0].Count = 181 },
		"host root":                func(v *IdentityExecutionInputV1) { v.Candidate.UIDs[0].HostStart = 0 },
		"host collision":           func(v *IdentityExecutionInputV1) { v.Candidate.UIDs[0].HostStart = v.Candidate.UIDs[1].HostStart },
		"UID sentinel":             func(v *IdentityExecutionInputV1) { v.Candidate.UIDs[0].ContainerStart = math.MaxUint32 },
		"GID sentinel":             func(v *IdentityExecutionInputV1) { v.Candidate.GIDs[0].ContainerStart = math.MaxUint32 },
		"host overflow":            func(v *IdentityExecutionInputV1) { v.Candidate.UIDs[0].HostStart = math.MaxUint32 },
		"container overflow":       func(v *IdentityExecutionInputV1) { v.Candidate.GIDs[0].Count = math.MaxUint32 },
		"empty UID domain":         func(v *IdentityExecutionInputV1) { v.Candidate.UIDs = nil },
		"empty GID domain":         func(v *IdentityExecutionInputV1) { v.Candidate.GIDs = nil },
		"unknown profile":          func(v *IdentityExecutionInputV1) { v.Candidate.Profile = "automatic" },
		"fresh surplus": func(v *IdentityExecutionInputV1) {
			v.Candidate.UIDs = append(v.Candidate.UIDs, executionGeometryV1([]uint32{300}, nil).UIDs...)
		},
		"accepted without reservations": func(v *IdentityExecutionInputV1) { v.Accepted = &v.Candidate },
		"malformed reservations": func(v *IdentityExecutionInputV1) {
			r := executionGeometryV1([]uint32{300}, []uint32{500})
			r.GIDs[0].Count = 0
			v.Reservations = &r
		},
		"changed profile": func(v *IdentityExecutionInputV1) {
			r := executionGeometryV1([]uint32{200}, []uint32{200})
			r.Profile = "bounded"
			v.Reservations = &r
		},
		"accepted outside reservations": func(v *IdentityExecutionInputV1) {
			v.Reservations = &v.Candidate
			p := executionGeometryV1([]uint32{300}, []uint32{500})
			v.Accepted = &p
		},
		"accepted mismatched binding": func(v *IdentityExecutionInputV1) {
			r := v.Candidate
			p := executionGeometryV1([]uint32{200}, []uint32{200})
			p.UIDs[0].HostStart++
			v.Reservations, v.Accepted = &r, &p
		},
		"accepted mismatched profile": func(v *IdentityExecutionInputV1) {
			r := v.Candidate
			p := executionGeometryV1([]uint32{200}, []uint32{200})
			p.Profile = "bounded"
			v.Reservations, v.Accepted = &r, &p
		},
		"malformed accepted": func(v *IdentityExecutionInputV1) {
			v.Reservations = &v.Candidate
			p := executionGeometryV1([]uint32{200}, []uint32{200})
			p.GIDs[0].Count = 0
			v.Accepted = &p
		},
		"changed protected binding": func(v *IdentityExecutionInputV1) {
			r := executionGeometryV1([]uint32{200}, []uint32{200})
			r.UIDs[0].HostStart++
			v.Reservations = &r
		},
		"reassigned omitted host": func(v *IdentityExecutionInputV1) {
			r := executionGeometryV1([]uint32{300}, []uint32{500})
			r.UIDs[0].HostStart = v.Candidate.UIDs[0].HostStart
			v.Reservations = &r
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := executionInputV1(t)
			mutate(&input)
			before, _ := json.Marshal(input)
			plan, err := PlanIdentityExecutionV1(input)
			if err == nil || !reflect.DeepEqual(plan, IdentityExecutionPlanV1{}) {
				t.Fatalf("invalid input returned a plan: %#v, %v", plan, err)
			}
			after, _ := json.Marshal(input)
			if string(before) != string(after) {
				t.Fatal("failure mutated inputs")
			}
		})
	}
}

func TestPlanIdentityExecutionV1BoundedRange(t *testing.T) {
	input := executionInputV1(t)
	input.Candidate = identitymapping.Geometry{Profile: "bounded", UIDs: []identitymapping.Interval{{ContainerStart: 0, HostStart: 100000, Count: 512}}, GIDs: []identitymapping.Interval{{ContainerStart: 0, HostStart: 200000, Count: 512}}}
	for _, history := range []string{"none", "reserved", "admitted"} {
		t.Run(history, func(t *testing.T) {
			value := input
			if history != "none" {
				value.Reservations = &input.Candidate
			}
			if history == "admitted" {
				value.Accepted = &input.Candidate
			}
			plan, err := PlanIdentityExecutionV1(value)
			if err != nil || !reflect.DeepEqual(plan.Active, input.Candidate) || !reflect.DeepEqual(plan.ProspectiveAccepted, input.Candidate) || !reflect.DeepEqual(plan.ProspectiveReservations, input.Candidate) {
				t.Fatalf("bounded authority changed: %#v, %v", plan, err)
			}
			if len(plan.DormantUIDs)+len(plan.DormantGIDs) != 0 {
				t.Fatal("whole accepted range has dormant IDs")
			}
		})
	}
	for name, mutate := range map[string]func(*IdentityExecutionInputV1){
		"out of range": func(v *IdentityExecutionInputV1) { v.Candidate.GIDs[0].Count = 300 },
		"multiple intervals": func(v *IdentityExecutionInputV1) {
			v.Candidate.UIDs = append(v.Candidate.UIDs, identitymapping.Interval{ContainerStart: 600, HostStart: 300000, Count: 1})
		},
		"changed range": func(v *IdentityExecutionInputV1) {
			r := canonicalExecutionGeometryV1(t, v.Candidate)
			r.UIDs[0].Count++
			v.Reservations = &r
		},
		"partial admitted range": func(v *IdentityExecutionInputV1) {
			r := canonicalExecutionGeometryV1(t, v.Candidate)
			p := canonicalExecutionGeometryV1(t, v.Candidate)
			p.GIDs[0].Count--
			v.Reservations, v.Accepted = &r, &p
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := input
			v.Candidate = canonicalExecutionGeometryV1(t, input.Candidate)
			mutate(&v)
			if _, err := PlanIdentityExecutionV1(v); err == nil {
				t.Fatal("invalid bounded authority accepted")
			}
		})
	}
}

func TestPlanIdentityExecutionV1RedactedDiagnostics(t *testing.T) {
	input := executionInputV1(t)
	input.RuntimeUsers = []string{"SECRET\nCONTROL"}
	_, err := PlanIdentityExecutionV1(input)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("runtime name leaked: %v", err)
	}
	input = executionInputV1(t)
	input.Candidate.UIDs = input.Candidate.UIDs[:1]
	_, err = PlanIdentityExecutionV1(input)
	if err == nil || !strings.Contains(err.Error(), string(input.Generation)) || !strings.Contains(err.Error(), "UID 200") {
		t.Fatalf("coverage diagnostic lacks stable generation and missing ID: %v", err)
	}
}

func TestPlanIdentityExecutionV1IndependentOutputs(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		t.Run(profile, func(t *testing.T) {
			input := executionInputV1(t)
			if profile == "bounded" {
				input.Candidate = identitymapping.Geometry{Profile: profile, UIDs: []identitymapping.Interval{{HostStart: 100000, Count: 512}}, GIDs: []identitymapping.Interval{{HostStart: 200000, Count: 512}}}
			} else {
				r := executionGeometryV1([]uint32{300}, []uint32{500})
				input.Reservations, input.Accepted = &r, &r
			}
			before, _ := json.Marshal(input)
			baseline, err := PlanIdentityExecutionV1(input)
			if err != nil {
				t.Fatal(err)
			}
			for field := range 8 {
				plan, err := PlanIdentityExecutionV1(input)
				if err != nil {
					t.Fatal(err)
				}
				parts := [][]identitymapping.Interval{plan.Active.UIDs, plan.Active.GIDs, plan.ProspectiveReservations.UIDs, plan.ProspectiveReservations.GIDs, plan.ProspectiveAccepted.UIDs, plan.ProspectiveAccepted.GIDs, plan.DormantUIDs, plan.DormantGIDs}
				if len(parts[field]) == 0 {
					continue
				}
				parts[field][0].HostStart = 1
				original := [][]identitymapping.Interval{baseline.Active.UIDs, baseline.Active.GIDs, baseline.ProspectiveReservations.UIDs, baseline.ProspectiveReservations.GIDs, baseline.ProspectiveAccepted.UIDs, baseline.ProspectiveAccepted.GIDs, baseline.DormantUIDs, baseline.DormantGIDs}
				for other := range parts {
					if other != field && !reflect.DeepEqual(parts[other], original[other]) {
						t.Fatalf("output %d aliases output %d", field, other)
					}
				}
				after, _ := json.Marshal(input)
				if string(before) != string(after) {
					t.Fatalf("output %d aliases input", field)
				}
			}
		})
	}
}

func TestSubtractExecutionIntervalsV1BoundaryOracle(t *testing.T) {
	// An independently enumerated small-domain oracle checks arbitrary holes,
	// adjacent removals and a removal spanning multiple source intervals.
	random := rand.New(rand.NewPCG(31, 97))
	for range 400 {
		var from, remove []identitymapping.Interval
		want := map[uint32]uint32{}
		for id := uint32(0); id < 256; id++ {
			if random.IntN(3) != 0 {
				from = append(from, identitymapping.Interval{ContainerStart: id, HostStart: 1000 + id, Count: 1})
				want[id] = 1000 + id
			}
			if random.IntN(2) == 0 {
				remove = append(remove, identitymapping.Interval{ContainerStart: id, HostStart: 1000 + id, Count: 1})
				delete(want, id)
			}
		}
		// Coalesce the small-domain inputs independently of planner helpers.
		coalesce := func(values []identitymapping.Interval) []identitymapping.Interval {
			var result []identitymapping.Interval
			for _, v := range values {
				if len(result) > 0 && result[len(result)-1].ContainerStart+result[len(result)-1].Count == v.ContainerStart {
					result[len(result)-1].Count++
				} else {
					result = append(result, v)
				}
			}
			return result
		}
		got := map[uint32]uint32{}
		for _, interval := range subtractExecutionIntervalsV1(coalesce(from), coalesce(remove)) {
			for n := uint32(0); n < interval.Count; n++ {
				id := interval.ContainerStart + n
				if _, duplicate := got[id]; duplicate {
					t.Fatal("subtraction duplicated an ID")
				}
				got[id] = interval.HostStart + n
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("boundary subtraction disagrees with independent oracle: %#v != %#v", got, want)
		}
	}
}

func TestPlanIdentityExecutionV1WideHistoryAndHighIDs(t *testing.T) {
	input := executionInputV1(t)
	// A large admitted interval surrounds every current declaration. The
	// planner must split dormancy at declaration boundaries without scanning
	// the billions of represented IDs.
	wide := identitymapping.Interval{ContainerStart: 0, HostStart: 1, Count: math.MaxUint32 - 1}
	history := identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{wide}, GIDs: []identitymapping.Interval{wide}}
	input.Reservations, input.Accepted = &history, &history
	input.Candidate = history
	plan, err := PlanIdentityExecutionV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.ProspectiveReservations, history) || !reflect.DeepEqual(plan.ProspectiveAccepted, history) {
		t.Fatal("wide history not retained")
	}
	if len(plan.DormantUIDs) != 3 || len(plan.DormantGIDs) != 6 {
		t.Fatalf("wrong wide dormant boundaries: %#v", plan)
	}
	for i, values := range [][]identitymapping.Interval{plan.DormantUIDs, plan.DormantGIDs} {
		var count uint64
		for _, interval := range values {
			if err := identitymapping.ValidateInterval(interval); err != nil {
				t.Fatal(err)
			}
			count += uint64(interval.Count)
		}
		want := uint64(wide.Count) - uint64([]int{2, 5}[i])
		if count != want {
			t.Fatalf("lost large-range authority: %d != %d", count, want)
		}
	}
	// Rebuild authenticated D evidence for the highest usable numeric ID.
	policy, image, platform, files := imageIdentityFixtureV1(t)
	policy.Users = map[string]blueprint.IdentityPolicyUserV1{"worker": {UID: "4294967294", PrimaryGroup: "worker", SupplementaryGroups: []string{}}}
	policy.Groups = map[string]string{"worker": "4294967294"}
	files["etc/passwd"] = []byte("worker:x:4294967294:4294967294::/:/bin/false\n")
	files["etc/group"] = []byte("worker:x:4294967294:\n")
	evidence, err := VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err != nil {
		t.Fatal(err)
	}
	input = IdentityExecutionInputV1{Generation: buildLockTestDigest("a"), Policy: &IdentityPolicyLockV1{Schema: IdentityPolicyLockSchemaV1, Policy: *policy, Evidence: evidence}}
	last := identitymapping.Interval{ContainerStart: math.MaxUint32 - 1, HostStart: math.MaxUint32 - 1, Count: 1}
	input.Candidate = identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{last}, GIDs: []identitymapping.Interval{last}}
	plan, err = PlanIdentityExecutionV1(input)
	if err != nil || !reflect.DeepEqual(plan.Active, input.Candidate) {
		t.Fatalf("highest usable ID not projected exactly: %#v, %v", plan, err)
	}
}

func TestPlanIdentityExecutionV1RootNeedsExplicitImageDeclaration(t *testing.T) {
	input := executionInputV1(t)
	root := identitymapping.Interval{ContainerStart: 0, HostStart: 100000, Count: 1}
	input.Candidate.UIDs = append(input.Candidate.UIDs, root)
	input.Candidate.GIDs = append(input.Candidate.GIDs, root)
	// The fixture image contains root, but image presence alone is not intent.
	if _, err := PlanIdentityExecutionV1(input); err == nil {
		t.Fatal("image root implicitly authorized")
	}
	policy, image, platform, files := imageIdentityFixtureV1(t)
	policy.Users["root"] = blueprint.IdentityPolicyUserV1{UID: "0", PrimaryGroup: "root", SupplementaryGroups: []string{}}
	policy.Groups["root"] = "0"
	evidence, err := VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err != nil {
		t.Fatal(err)
	}
	input.Policy = &IdentityPolicyLockV1{Schema: IdentityPolicyLockSchemaV1, Policy: *policy, Evidence: evidence}
	input.RuntimeUsers, input.RuntimeGroups = []string{"root"}, []string{"root"}
	plan, err := PlanIdentityExecutionV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Active.UIDs[0] != root || plan.Active.GIDs[0] != root {
		t.Fatalf("explicit root did not use selected host binding: %#v", plan.Active)
	}
}
