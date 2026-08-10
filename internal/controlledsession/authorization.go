// Package controlledsession defines the host-owned authorization, wire
// protocol, and lifecycle state machine for one controlled session.
//
// The package is intentionally independent of Docker orchestration. Callers
// must construct and validate a complete immutable authorization before they
// create runtime resources or expose a session channel.
package controlledsession

import (
	"crypto/rand"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/endpointname"
	"github.com/omry/reploy/internal/runtimeidentity"
)

const AuthorizationSchemaV1 = "controlled-session-authorization-v1"

type OperationV1 string

const (
	OperationInputV1     OperationV1 = "input"
	OperationResizeV1    OperationV1 = "resize"
	OperationTerminateV1 OperationV1 = "terminate"
	OperationCompleteV1  OperationV1 = "complete"
)

// RuntimeIdentityV1 records the exact container-local identity selected before
// the session starts.
type RuntimeIdentityV1 = runtimeidentity.IdentityV1

// EnvironmentAuthorizationV1 binds one session participant to the exact
// environment generation, build, runtime identity, and immutable execution
// plan selected before either container starts.
type EnvironmentAuthorizationV1 struct {
	DeploymentID        string            `json:"deployment_id"`
	GenerationReference string            `json:"generation_reference"`
	BuildIdentity       canonical.Digest  `json:"build_identity"`
	PlanDigest          canonical.Digest  `json:"plan_digest"`
	RuntimeIdentity     RuntimeIdentityV1 `json:"runtime_identity"`
}

// AuthorizationV1 binds one opaque session handle to one prospective,
// immutable controller/workload plan pair. The plan digests cover all details
// that are not repeated here, including mounts, environment, network, and
// commands. Host admission binds the validated record to live runtime state.
//
// Ownership and lifetime are deliberately host runtime state rather than
// transferable fields in this record. The host binds the validated value to
// its LiveRunID, permits exactly one controller connection to claim that lease,
// and ends the lease when that connection is lost or the host cancels it.
type AuthorizationV1 struct {
	Schema      string                     `json:"schema"`
	Handle      string                     `json:"handle"`
	LiveRunID   string                     `json:"live_run_id"`
	Controller  EnvironmentAuthorizationV1 `json:"controller"`
	Workload    EnvironmentAuthorizationV1 `json:"workload"`
	Operations  []OperationV1              `json:"operations"`
	EndpointIDs []string                   `json:"endpoint_ids"`
}

var sessionHandlePatternV1 = regexp.MustCompile(`^session-[0-9a-f]{64}$`)

func NewHandleV1() (string, error) {
	return newHandleV1(rand.Reader)
}

func newHandleV1(random io.Reader) (string, error) {
	if random == nil {
		return "", fmt.Errorf("create controlled-session handle requires randomness")
	}
	var value [32]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return "", fmt.Errorf("create controlled-session handle: %w", err)
	}
	return fmt.Sprintf("session-%x", value), nil
}

func AuthorizationDigestV1(authorization AuthorizationV1) (canonical.Digest, error) {
	if err := ValidateAuthorizationV1(authorization); err != nil {
		return "", err
	}
	return canonical.Sum("controlled-session-authorization", AuthorizationSchemaV1, authorization)
}

func ValidateAuthorizationV1(authorization AuthorizationV1) error {
	if authorization.Schema != AuthorizationSchemaV1 {
		return fmt.Errorf("controlled-session authorization schema must be %q", AuthorizationSchemaV1)
	}
	if !sessionHandlePatternV1.MatchString(authorization.Handle) {
		return fmt.Errorf("controlled-session handle must use session- followed by 64 lowercase hexadecimal characters")
	}
	if err := deploy.ValidateLiveRunIDV1(authorization.LiveRunID); err != nil {
		return fmt.Errorf("controlled-session live-run ID: %w", err)
	}
	if err := validateEnvironmentAuthorizationV1("controller", authorization.Controller); err != nil {
		return err
	}
	if err := validateEnvironmentAuthorizationV1("workload", authorization.Workload); err != nil {
		return err
	}
	if authorization.Operations == nil || authorization.EndpointIDs == nil {
		return fmt.Errorf("controlled-session authorization collections must use arrays")
	}
	for index, operation := range authorization.Operations {
		switch operation {
		case OperationInputV1, OperationResizeV1, OperationTerminateV1, OperationCompleteV1:
		default:
			return fmt.Errorf("controlled-session operation %q is unsupported", operation)
		}
		if index > 0 && authorization.Operations[index-1] >= operation {
			return fmt.Errorf("controlled-session operations must be unique and sorted")
		}
	}
	for index, endpointID := range authorization.EndpointIDs {
		if err := endpointname.Validate(endpointID); err != nil {
			return fmt.Errorf("controlled-session endpoint ID %q: %w", endpointID, err)
		}
		if index > 0 && authorization.EndpointIDs[index-1] >= endpointID {
			return fmt.Errorf("controlled-session endpoint IDs must be unique and sorted")
		}
	}
	return nil
}

func cloneAuthorizationV1(authorization AuthorizationV1) AuthorizationV1 {
	result := authorization
	result.Controller.RuntimeIdentity.SupplementaryGIDs = append([]string{}, authorization.Controller.RuntimeIdentity.SupplementaryGIDs...)
	result.Workload.RuntimeIdentity.SupplementaryGIDs = append([]string{}, authorization.Workload.RuntimeIdentity.SupplementaryGIDs...)
	result.Operations = append([]OperationV1{}, authorization.Operations...)
	result.EndpointIDs = append([]string{}, authorization.EndpointIDs...)
	return result
}

func validateEnvironmentAuthorizationV1(role string, environment EnvironmentAuthorizationV1) error {
	if err := validateSafeTextV1(role+" deployment ID", environment.DeploymentID); err != nil {
		return err
	}
	if err := validateSafeTextV1(role+" generation reference", environment.GenerationReference); err != nil {
		return err
	}
	if err := environment.BuildIdentity.Validate(); err != nil {
		return fmt.Errorf("controlled-session %s build identity: %w", role, err)
	}
	if err := environment.PlanDigest.Validate(); err != nil {
		return fmt.Errorf("controlled-session %s plan: %w", role, err)
	}
	if err := runtimeidentity.ValidateIdentityV1(environment.RuntimeIdentity); err != nil {
		return fmt.Errorf("controlled-session %s runtime identity: %w", role, err)
	}
	return nil
}

func validateSafeTextV1(field string, value string) error {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("controlled-session %s must be nonempty safe text", field)
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
			return fmt.Errorf("controlled-session %s must be nonempty safe text", field)
		}
	}
	return nil
}
