package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeIDFromNativeWidthV1PreservesUnsigned32BitValues(t *testing.T) {
	tests := []struct {
		name    string
		value   int64
		bits    int
		want    uint32
		wantErr bool
	}{
		{name: "32-bit sign bit", value: -1 << 31, bits: 32, want: 1 << 31},
		{name: "32-bit maximum runtime ID", value: -2, bits: 32, want: 1<<32 - 2},
		{name: "32-bit unchanged sentinel", value: -1, bits: 32, wantErr: true},
		{name: "64-bit maximum runtime ID", value: 1<<32 - 2, bits: 64, want: 1<<32 - 2},
		{name: "64-bit unchanged sentinel", value: 1<<32 - 1, bits: 64, wantErr: true},
		{name: "64-bit negative", value: -2, bits: 64, wantErr: true},
		{name: "invalid width", value: 1, bits: 16, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := runtimeIDFromNativeWidthV1(test.value, test.bits)
			if test.wantErr {
				if err == nil {
					t.Fatalf("runtime ID = %d, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("runtime ID = %d, %v, want %d", got, err, test.want)
			}
		})
	}
}

func TestApplicationPlanningCarriesFullRangeRuntimeIDsToProbeArgv(t *testing.T) {
	const uid = uint32(1<<32 - 2)
	const gid = uint32(1<<32 - 3)
	const supplementaryGID = uint32(1<<32 - 4)
	user := RuntimeUserPlan{
		UID: uid, GID: gid, SupplementaryGIDs: []uint32{supplementaryGID},
		DockerUser: runtimeIDStringV1(uid) + ":" + runtimeIDStringV1(gid),
	}
	plan := DockerExecutionPlan{Sandbox: newApplicationSandboxPlanV1(user)}
	if err := ValidateApplicationSandboxPlanV1(plan.Sandbox); err != nil {
		t.Fatal(err)
	}
	argv := sandboxApplicationArgvV1(plan, []string{"/application"}, false, nil)
	wantPrefix := []string{
		"restricted-exec", "--uid", "4294967294", "--gid", "4294967293",
		"--groups", "4294967292", "--", "/application",
	}
	if !reflect.DeepEqual(argv, wantPrefix) {
		t.Fatalf("probe argv = %#v, want %#v", argv, wantPrefix)
	}
	if !strings.Contains(temporaryHomeMountForPlan(plan), "uid=4294967294,gid=4294967293") {
		t.Fatalf("temporary home mount = %q", temporaryHomeMountForPlan(plan))
	}
}

func TestParseNumericInstallIDUsesCanonicalRuntimeIDRange(t *testing.T) {
	if got, ok := parseNumericInstallID("4294967294"); !ok || got != 1<<32-2 {
		t.Fatalf("maximum install runtime ID = %d, %t", got, ok)
	}
	for _, value := range []string{"4294967295", "4294967296", "042", "+42", "-1"} {
		if got, ok := parseNumericInstallID(value); ok {
			t.Fatalf("invalid install runtime ID %q = %d", value, got)
		}
	}
}
