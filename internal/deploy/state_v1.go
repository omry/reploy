package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const StateSchemaV1 = "state-v1"

var ErrLegacyStateUnsupported = errors.New("state.legacy_unsupported")

type StateV1 struct {
	Schema     string                       `json:"schema"`
	Blueprint  blueprint.ResolvedDocumentV1 `json:"blueprint"`
	Platform   blueprint.Platform           `json:"platform"`
	Overlay    RequestOverlayV1             `json:"overlay"`
	Current    *EnvironmentGenerationState  `json:"current"`
	Deployment *DeploymentStateV1           `json:"deployment"`
}

func ValidateStateV1(state StateV1) error {
	if state.Schema != StateSchemaV1 {
		return fmt.Errorf("state schema must be %q", StateSchemaV1)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return fmt.Errorf("state blueprint: %w", err)
	}
	if err := blueprint.ValidateSelectedPlatform(document, state.Platform); err != nil {
		return fmt.Errorf("state platform: %w", err)
	}
	if _, err := RequestOverlayDigestV1(state.Overlay); err != nil {
		return fmt.Errorf("state overlay: %w", err)
	}
	if state.Current != nil {
		if err := ValidateEnvironmentGenerationState(*state.Current); err != nil {
			return fmt.Errorf("state current generation: %w", err)
		}
	}
	if state.Deployment != nil {
		if err := ValidateDeploymentStateV1(*state.Deployment); err != nil {
			return fmt.Errorf("state deployment: %w", err)
		}
	}
	return nil
}

func EncodeStateV1(state StateV1) ([]byte, error) {
	if err := ValidateStateV1(state); err != nil {
		return nil, fmt.Errorf("encode state: %w", err)
	}
	content, err := canonical.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode state: %w", err)
	}
	return content, nil
}

func DecodeStateV1(content []byte) (StateV1, error) {
	var envelope struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		return StateV1{}, fmt.Errorf("decode state envelope: %w", err)
	}
	if envelope.Schema == "" {
		return StateV1{}, fmt.Errorf("%w: deployment state is not %s; recreate the deployment", ErrLegacyStateUnsupported, StateSchemaV1)
	}
	if envelope.Schema != StateSchemaV1 {
		return StateV1{}, fmt.Errorf("state schema %q is unsupported; expected %q", envelope.Schema, StateSchemaV1)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var state StateV1
	if err := decoder.Decode(&state); err != nil {
		return StateV1{}, fmt.Errorf("decode state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return StateV1{}, fmt.Errorf("state contains trailing JSON")
		}
		return StateV1{}, fmt.Errorf("decode state trailer: %w", err)
	}
	if err := ValidateStateV1(state); err != nil {
		return StateV1{}, fmt.Errorf("validate state: %w", err)
	}
	canonicalContent, err := canonical.Marshal(state)
	if err != nil {
		return StateV1{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return StateV1{}, fmt.Errorf("state is not canonical JSON")
	}
	return state, nil
}
