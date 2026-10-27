package deploy

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/identitymapping"
)

func coverageGeometryV1(uids, gids []uint32) identitymapping.Geometry {
	result := identitymapping.Geometry{Profile: "exact"}
	for i, ids := range [][]uint32{uids, gids} {
		var values []identitymapping.Interval
		for n, id := range ids {
			values = append(values, identitymapping.Interval{ContainerStart: id, HostStart: uint32(100000 + n), Count: 1})
		}
		if i == 0 {
			result.UIDs = values
		} else {
			result.GIDs = values
		}
	}
	return result
}

func TestIdentityPolicyCoverageV1AllDeclaredIDsAndNoMutation(t *testing.T) {
	lock := identityPolicyBuildLockFixtureV1(t)
	geometry := coverageGeometryV1([]uint32{200, 20}, []uint32{450, 300, 200, 40, 20})
	before, _ := json.Marshal([]any{lock.IdentityPolicy, geometry})
	if err := ValidateIdentityPolicyCoverageV1(lock.IdentityPolicy, geometry, buildLockTestDigest("a")); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal([]any{lock.IdentityPolicy, geometry})
	if string(before) != string(after) {
		t.Fatal("coverage changed policy, evidence or caller geometry")
	}
	// The image also has undeclared root and build accounts. They need no
	// application reservation; a declared user with no role still needs one.
	for _, test := range []struct {
		name string
		uid  bool
		id   uint32
		path string
	}{
		{"unused user", true, 20, "users.zeta.uid"},
		{"worker UID", true, 200, "users.worker.uid"},
		{"primary group", false, 200, "users.worker.primary_group"},
		{"first additional group", false, 40, "users.worker.supplementary_groups[0]"},
		{"second additional group", false, 300, "users.worker.supplementary_groups[1]"},
		{"unused group", false, 450, "groups.unused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := coverageGeometryV1([]uint32{20, 200}, []uint32{20, 40, 200, 300, 450})
			values := &candidate.GIDs
			if test.uid {
				values = &candidate.UIDs
			}
			for n, interval := range *values {
				if interval.ContainerStart == test.id {
					*values = append((*values)[:n:n], (*values)[n+1:]...)
					break
				}
			}
			generation := buildLockTestDigest("b")
			err := ValidateIdentityPolicyCoverageV1(lock.IdentityPolicy, candidate, generation)
			if err == nil || !strings.Contains(err.Error(), "environment.runtime.identity_policy."+test.path) || !strings.Contains(err.Error(), string(generation)) {
				t.Fatalf("missing declaration or selected generation diagnostic: %v", err)
			}
		})
	}
}

func TestIdentityPolicyCoverageV1DiagnosticsAreNumericAndDeterministic(t *testing.T) {
	lock := identityPolicyBuildLockFixtureV1(t)
	geometry := coverageGeometryV1([]uint32{1}, []uint32{1})
	var first string
	for i := 0; i < 30; i++ {
		err := ValidateIdentityPolicyCoverageV1(lock.IdentityPolicy, geometry, buildLockTestDigest("c"))
		if err == nil {
			t.Fatal("missing reservations accepted")
		}
		if i == 0 {
			first = err.Error()
		} else if first != err.Error() {
			t.Fatal("map iteration changed diagnostic order")
		}
	}
	for _, kind := range []string{"UID", "GID"} {
		if strings.Index(first, kind+" 20 at") > strings.Index(first, kind+" 200 at") {
			t.Fatal("IDs sorted as text instead of numbers", first)
		}
	}
	if strings.Contains(first, "SECRET") || strings.Contains(first, "Build.User") {
		t.Fatal("coverage leaked unselected image accounts or account data")
	}
}

func TestIdentityPolicyCoverageV1BoundedIntervalsAndUsableIDLimits(t *testing.T) {
	policy, image, platform, files := imageIdentityFixtureV1(t)
	user := policy.Users["zeta"]
	user.UID = "4294967294"
	policy.Users["zeta"] = user
	policy.Groups["zeta"] = "4294967294"
	files["etc/passwd"] = []byte(strings.ReplaceAll(string(files["etc/passwd"]), "zeta:x:20:20:", "zeta:x:4294967294:4294967294:"))
	files["etc/group"] = []byte(strings.ReplaceAll(string(files["etc/group"]), "zeta:x:20:", "zeta:x:4294967294:"))
	evidence, err := VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err != nil {
		t.Fatal(err)
	}
	lock := &IdentityPolicyLockV1{Schema: IdentityPolicyLockSchemaV1, Policy: *policy, Evidence: evidence}
	interval := identitymapping.Interval{ContainerStart: 1, HostStart: 1, Count: math.MaxUint32 - 1}
	geometry := identitymapping.Geometry{Profile: "bounded", UIDs: []identitymapping.Interval{interval}, GIDs: []identitymapping.Interval{interval}}
	if err := ValidateIdentityPolicyCoverageV1(lock, geometry, buildLockTestDigest("d")); err != nil {
		t.Fatal("huge bounded interval did not cover highest usable ID", err)
	}
	geometry.UIDs[0].Count--
	if err := ValidateIdentityPolicyCoverageV1(lock, geometry, buildLockTestDigest("d")); err == nil || !strings.Contains(err.Error(), "UID 4294967294") {
		t.Fatalf("exclusive interval end accepted: %v", err)
	}
	policy, image, platform, files = imageIdentityFixtureV1(t)
	policy.Users["root"] = blueprint.IdentityPolicyUserV1{UID: "0", PrimaryGroup: "root", SupplementaryGroups: []string{}}
	policy.Groups["root"] = "0"
	evidence, err = VerifyImageIdentityFilesV1(policy, image, platform, files)
	if err != nil {
		t.Fatal(err)
	}
	lock = &IdentityPolicyLockV1{Schema: IdentityPolicyLockSchemaV1, Policy: *policy, Evidence: evidence}
	geometry = coverageGeometryV1([]uint32{0, 20, 200}, []uint32{0, 20, 40, 200, 300, 450})
	if err := ValidateIdentityPolicyCoverageV1(lock, geometry, buildLockTestDigest("e")); err != nil {
		t.Fatal("declared container root rejected despite non-root host reservation", err)
	}
}

func TestIdentityPolicyCoverageV1RejectsMissingOrInvalidAuthority(t *testing.T) {
	for _, name := range []string{"nil lock", "generation", "policy", "evidence", "geometry", "host root"} {
		t.Run(name, func(t *testing.T) {
			lock := identityPolicyBuildLockFixtureV1(t).IdentityPolicy
			geometry := coverageGeometryV1([]uint32{20, 200}, []uint32{20, 40, 200, 300, 450})
			generation := buildLockTestDigest("f")
			switch name {
			case "nil lock":
				lock = nil
			case "generation":
				generation = canonical.Digest("invalid")
			case "policy":
				lock.Policy.Groups["unused"] = "451"
			case "evidence":
				lock.Evidence.Users[0].UID = "21"
			case "geometry":
				geometry.Profile = "other"
			case "host root":
				geometry.UIDs[0].HostStart = 0
			}
			before, _ := json.Marshal([]any{lock, geometry})
			if err := ValidateIdentityPolicyCoverageV1(lock, geometry, generation); err == nil {
				t.Fatal("invalid coverage authority accepted")
			}
			after, _ := json.Marshal([]any{lock, geometry})
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed coverage changed inputs")
			}
		})
	}
}
