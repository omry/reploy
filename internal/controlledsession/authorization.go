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
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/runtimeidentity"
)

const AuthorizationSchemaV1 = "controlled-session-authorization-v1"

type OperationV1 string

const (
	OperationInputV1        OperationV1 = "input"
	OperationResizeV1       OperationV1 = "resize"
	OperationTerminateV1    OperationV1 = "terminate"
	OperationCompleteV1     OperationV1 = "complete"
	OperationOpenEndpointV1 OperationV1 = "open-endpoint"
)

// RuntimeIdentityV1 records the exact container-local identity selected before
// the session starts.
type RuntimeIdentityV1 = runtimeidentity.IdentityV1

// AuthorizationV1 binds one opaque session handle to one already admitted,
// immutable runtime plan. The plan digests cover all details that are not
// repeated here, including mounts, environment, network, and commands.
//
// Ownership and lifetime are deliberately host runtime state rather than
// transferable fields in this record. The host binds the validated value to
// its LiveRunID, permits exactly one controller connection to claim that lease,
// and ends the lease when that connection is lost or the host cancels it.
type AuthorizationV1 struct {
	Schema              string            `json:"schema"`
	Handle              string            `json:"handle"`
	DeploymentID        string            `json:"deployment_id"`
	GenerationReference string            `json:"generation_reference"`
	BuildIdentity       canonical.Digest  `json:"build_identity"`
	LiveRunID           string            `json:"live_run_id"`
	ApplicationPlan     canonical.Digest  `json:"application_plan"`
	ToolchainPlan       canonical.Digest  `json:"toolchain_plan"`
	RuntimeIdentity     RuntimeIdentityV1 `json:"runtime_identity"`
	Operations          []OperationV1     `json:"operations"`
	EndpointIDs         []string          `json:"endpoint_ids"`
}

var sessionHandlePatternV1 = regexp.MustCompile(`^session-[0-9a-f]{64}$`)
var endpointIDPatternV1 = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

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
	if err := validateSafeTextV1("deployment ID", authorization.DeploymentID); err != nil {
		return err
	}
	if err := validateSafeTextV1("generation reference", authorization.GenerationReference); err != nil {
		return err
	}
	if err := authorization.BuildIdentity.Validate(); err != nil {
		return fmt.Errorf("controlled-session build identity: %w", err)
	}
	if err := deploy.ValidateLiveRunIDV1(authorization.LiveRunID); err != nil {
		return fmt.Errorf("controlled-session live-run ID: %w", err)
	}
	if err := authorization.ApplicationPlan.Validate(); err != nil {
		return fmt.Errorf("controlled-session application plan: %w", err)
	}
	if err := authorization.ToolchainPlan.Validate(); err != nil {
		return fmt.Errorf("controlled-session toolchain plan: %w", err)
	}
	if err := runtimeidentity.ValidateIdentityV1(authorization.RuntimeIdentity); err != nil {
		return fmt.Errorf("controlled-session runtime identity: %w", err)
	}
	if authorization.Operations == nil || authorization.EndpointIDs == nil {
		return fmt.Errorf("controlled-session authorization collections must use arrays")
	}
	for index, operation := range authorization.Operations {
		switch operation {
		case OperationInputV1, OperationResizeV1, OperationTerminateV1, OperationCompleteV1, OperationOpenEndpointV1:
		default:
			return fmt.Errorf("controlled-session operation %q is unsupported", operation)
		}
		if index > 0 && authorization.Operations[index-1] >= operation {
			return fmt.Errorf("controlled-session operations must be unique and sorted")
		}
	}
	for index, endpointID := range authorization.EndpointIDs {
		if !endpointIDPatternV1.MatchString(endpointID) {
			return fmt.Errorf("controlled-session endpoint ID %q is invalid", endpointID)
		}
		if index > 0 && authorization.EndpointIDs[index-1] >= endpointID {
			return fmt.Errorf("controlled-session endpoint IDs must be unique and sorted")
		}
	}
	if len(authorization.EndpointIDs) != 0 && !slices.Contains(authorization.Operations, OperationOpenEndpointV1) {
		return fmt.Errorf("controlled-session endpoint grants require the open-endpoint operation")
	}
	return nil
}

func cloneAuthorizationV1(authorization AuthorizationV1) AuthorizationV1 {
	result := authorization
	result.RuntimeIdentity.SupplementaryGIDs = append([]string{}, authorization.RuntimeIdentity.SupplementaryGIDs...)
	result.Operations = append([]OperationV1{}, authorization.Operations...)
	result.EndpointIDs = append([]string{}, authorization.EndpointIDs...)
	return result
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
