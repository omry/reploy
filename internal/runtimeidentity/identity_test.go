package runtimeidentity

import (
	"strings"
	"testing"
)

func TestValidateIdentityV1AcceptsCanonicalRootAndNonRootIdentities(t *testing.T) {
	values := []IdentityV1{
		{Username: "reploy", UID: "1000", GID: "1000", SupplementaryGIDs: []string{"10", "100"}},
		{Username: "root", UID: "0", GID: "0", SupplementaryGIDs: []string{"10"}},
	}
	for _, value := range values {
		if err := ValidateIdentityV1(value); err != nil {
			t.Fatalf("ValidateIdentityV1(%+v) error = %v", value, err)
		}
	}
}

func TestValidateIdentityV1RejectsNonCanonicalOrPrivilegedNonRootIdentities(t *testing.T) {
	base := IdentityV1{Username: "reploy", UID: "1000", GID: "1000", SupplementaryGIDs: []string{"10", "100"}}
	tests := []struct {
		name   string
		mutate func(*IdentityV1)
		want   string
	}{
		{name: "invalid username", mutate: func(value *IdentityV1) { value.Username = "Bad" }, want: "portable lowercase"},
		{name: "leading-zero UID", mutate: func(value *IdentityV1) { value.UID = "01000" }, want: "runtime UID"},
		{name: "unchanged UID sentinel", mutate: func(value *IdentityV1) { value.UID = "4294967295" }, want: "runtime UID"},
		{name: "overflow GID", mutate: func(value *IdentityV1) { value.GID = "4294967296" }, want: "runtime GID"},
		{name: "unchanged GID sentinel", mutate: func(value *IdentityV1) { value.GID = "4294967295" }, want: "runtime GID"},
		{name: "root name for non-root", mutate: func(value *IdentityV1) { value.Username = "root" }, want: "non-root runtime identity"},
		{name: "non-root name for root", mutate: func(value *IdentityV1) { value.UID = "0" }, want: "root runtime identity"},
		{name: "root primary group", mutate: func(value *IdentityV1) { value.GID = "0" }, want: "root group"},
		{name: "nil groups", mutate: func(value *IdentityV1) { value.SupplementaryGIDs = nil }, want: "must use an array"},
		{name: "primary group repeated", mutate: func(value *IdentityV1) { value.SupplementaryGIDs = []string{"1000"} }, want: "exclude the primary"},
		{name: "root supplementary group", mutate: func(value *IdentityV1) { value.SupplementaryGIDs = []string{"0", "10"} }, want: "root group"},
		{name: "unchanged supplementary GID sentinel", mutate: func(value *IdentityV1) { value.SupplementaryGIDs = []string{"10", "4294967295"} }, want: "supplementary GID"},
		{name: "unsorted groups", mutate: func(value *IdentityV1) { value.SupplementaryGIDs = []string{"100", "10"} }, want: "unique, sorted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.SupplementaryGIDs = append([]string(nil), base.SupplementaryGIDs...)
			test.mutate(&value)
			err := ValidateIdentityV1(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateIdentityV1() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
