package dockerdeploy

import (
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

// Entry mode describes the operation's required execution context. It is
// selected by trusted entry code, never by a public qualification flag. Do not
// replace occupied entry with a fresh instance when its mode is unsupported.
type mappedEntryModeV1 uint8

const (
	mappedEntryFreshInstanceV1 mappedEntryModeV1 = iota + 1
	mappedEntryOccupiedExecV1
	mappedEntryHostNativeV1
)

var (
	errMappedOccupiedEntryV1   = errors.New("mapped occupied-container entry is unsupported: no qualified exec transition")
	errMappedHostNativeEntryV1 = errors.New("mapped host-native entry is unsupported")
)

// runtimeDocumentForEntryModeV1 consumes the existing owner's locked state.
// Declaration alone counts as intent, including unresolved mapping leaves;
// retained mapping authority still counts if the declaration was removed.
//
// This is a pure refusal boundary, not admission. Fresh instance classification
// grants no permission to create, release or attach. Public inhibition and
// fresh mapped admission remain separate mandatory checks. In particular,
// caller capability lists, UID strings and expected-state IDs are not inputs.
func runtimeDocumentForEntryModeV1(state deploy.StateV1, mode mappedEntryModeV1) (blueprint.Document, error) {
	switch mode {
	case mappedEntryFreshInstanceV1, mappedEntryOccupiedExecV1, mappedEntryHostNativeV1:
	default:
		return blueprint.Document{}, fmt.Errorf("unsupported runtime entry mode %d", mode)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return blueprint.Document{}, err
	}
	mapped := state.IdentityMapping != nil || document.Environment.Runtime.IdentityMapping != nil || document.Environment.Runtime.IdentityPolicy != nil
	if mapped {
		switch mode {
		case mappedEntryOccupiedExecV1:
			return blueprint.Document{}, fmt.Errorf("environment %q: %w", document.Environment.ID, errMappedOccupiedEntryV1)
		case mappedEntryHostNativeV1:
			return blueprint.Document{}, fmt.Errorf("environment %q: %w", document.Environment.ID, errMappedHostNativeEntryV1)
		}
	}
	return document, nil
}
