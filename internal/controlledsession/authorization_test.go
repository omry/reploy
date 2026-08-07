package controlledsession

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func testAuthorizationV1() AuthorizationV1 {
	digest := canonical.Digest("sha256:" + strings.Repeat("1", 64))
	return AuthorizationV1{
		Schema: AuthorizationSchemaV1, Handle: "session-" + strings.Repeat("a", 64),
		DeploymentID: "demo", GenerationReference: "reploy/env/demo:g-current", BuildIdentity: digest,
		LiveRunID: "run-0000000000000001", WorkloadPlan: digest, ControllerPlan: digest,
		RuntimeIdentity: RuntimeIdentityV1{Username: "reploy", UID: "1000", GID: "1000", SupplementaryGIDs: []string{"10", "100"}},
		Operations:      []OperationV1{OperationCompleteV1, OperationInputV1, OperationResizeV1, OperationTerminateV1},
		EndpointIDs:     []string{"browser", "terminal"},
	}
}

func TestAuthorizationV1ValidatesAndHashesCompletePlan(t *testing.T) {
	authorization := testAuthorizationV1()
	if err := ValidateAuthorizationV1(authorization); err != nil {
		t.Fatalf("ValidateAuthorizationV1() error = %v", err)
	}
	first, err := AuthorizationDigestV1(authorization)
	if err != nil {
		t.Fatalf("AuthorizationDigestV1() error = %v", err)
	}
	authorization.EndpointIDs = []string{"browser"}
	second, err := AuthorizationDigestV1(authorization)
	if err != nil {
		t.Fatalf("AuthorizationDigestV1(mutated) error = %v", err)
	}
	if first == second {
		t.Fatalf("authorization digest did not bind endpoint grants: %s", first)
	}
}

func TestValidateAuthorizationV1AcceptsDockerStyleEndpointIDs(t *testing.T) {
	authorization := testAuthorizationV1()
	authorization.EndpointIDs = []string{"2fa", "api--v1", "api.v1", "api__internal", "api_v1"}
	if err := ValidateAuthorizationV1(authorization); err != nil {
		t.Fatalf("ValidateAuthorizationV1() error = %v", err)
	}
}

func TestValidateAuthorizationV1RejectsOpenOrAmbiguousRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AuthorizationV1)
		want   string
	}{
		{name: "bad handle", mutate: func(value *AuthorizationV1) { value.Handle = "session-guessable" }, want: "64 lowercase"},
		{name: "bad live run", mutate: func(value *AuthorizationV1) { value.LiveRunID = "run-current" }, want: "live-run ID"},
		{name: "unsorted operations", mutate: func(value *AuthorizationV1) {
			value.Operations[0], value.Operations[1] = value.Operations[1], value.Operations[0]
		}, want: "unique and sorted"},
		{name: "unsorted endpoints", mutate: func(value *AuthorizationV1) {
			value.EndpointIDs[0], value.EndpointIDs[1] = value.EndpointIDs[1], value.EndpointIDs[0]
		}, want: "unique and sorted"},
		{name: "invalid endpoint", mutate: func(value *AuthorizationV1) { value.EndpointIDs = []string{"API"} }, want: "Docker-style"},
		{name: "nil collection", mutate: func(value *AuthorizationV1) { value.EndpointIDs = nil }, want: "must use arrays"},
		{name: "unsafe generation", mutate: func(value *AuthorizationV1) { value.GenerationReference = "bad\nreference" }, want: "safe text"},
		{name: "formatted generation", mutate: func(value *AuthorizationV1) { value.GenerationReference = "bad\u202ereference" }, want: "safe text"},
		{name: "leading zero uid", mutate: func(value *AuthorizationV1) { value.RuntimeIdentity.UID = "01000" }, want: "canonical unsigned"},
		{name: "root name for non-root", mutate: func(value *AuthorizationV1) { value.RuntimeIdentity.Username = "root" }, want: "non-root runtime identity"},
		{name: "non-root name for root", mutate: func(value *AuthorizationV1) { value.RuntimeIdentity.UID = "0" }, want: "root runtime identity"},
		{name: "root primary group", mutate: func(value *AuthorizationV1) { value.RuntimeIdentity.GID = "0" }, want: "root group"},
		{name: "root supplementary group", mutate: func(value *AuthorizationV1) { value.RuntimeIdentity.SupplementaryGIDs = []string{"0", "10"} }, want: "root group"},
		{name: "primary supplementary group", mutate: func(value *AuthorizationV1) { value.RuntimeIdentity.SupplementaryGIDs = []string{"10", "1000"} }, want: "exclude the primary"},
		{name: "unsorted numeric groups", mutate: func(value *AuthorizationV1) { value.RuntimeIdentity.SupplementaryGIDs = []string{"100", "10"} }, want: "sorted numerically"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := testAuthorizationV1()
			test.mutate(&value)
			err := ValidateAuthorizationV1(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAuthorizationV1() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestNewHandleV1UsesFullRandomCapability(t *testing.T) {
	handle, err := newHandleV1(bytes.NewReader(bytes.Repeat([]byte{0xab}, 32)))
	if err != nil {
		t.Fatalf("newHandleV1() error = %v", err)
	}
	want := "session-" + strings.Repeat("ab", 32)
	if handle != want {
		t.Fatalf("newHandleV1() = %q, want %q", handle, want)
	}
	if _, err := newHandleV1(bytes.NewReader([]byte{1})); err == nil {
		t.Fatal("newHandleV1(short randomness) unexpectedly succeeded")
	}
	if _, err := newHandleV1(nil); err == nil {
		t.Fatal("newHandleV1(nil) unexpectedly succeeded")
	}
}
