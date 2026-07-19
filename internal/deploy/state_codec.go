package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func ParseDeploymentState(content []byte) (DeploymentState, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var state DeploymentState
	if err := decoder.Decode(&state); err != nil {
		return DeploymentState{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return DeploymentState{}, fmt.Errorf("deployment state contains multiple JSON values")
		}
		return DeploymentState{}, err
	}
	if state.Overlay.Schema == "" && state.Overlay.SelectedOptions == nil && state.Overlay.DirectPackages == nil {
		state.Overlay = EmptyRequestOverlayV1()
	}
	if _, err := RequestOverlayDigestV1(state.Overlay); err != nil {
		return DeploymentState{}, fmt.Errorf("deployment state overlay: %w", err)
	}
	return state, nil
}

func MarshalDeploymentState(state DeploymentState) ([]byte, error) {
	if state.Overlay.Schema == "" && state.Overlay.SelectedOptions == nil && state.Overlay.DirectPackages == nil {
		state.Overlay = EmptyRequestOverlayV1()
	}
	if _, err := RequestOverlayDigestV1(state.Overlay); err != nil {
		return nil, fmt.Errorf("deployment state overlay: %w", err)
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}
