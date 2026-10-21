package dockerdeploy

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

// A portable declaration is intent, including when its leaves are unresolved.
// Later reviewed runtime admission must replace this guard as one complete cut.
func unmappedRuntimeDocumentV1(state deploy.StateV1) (blueprint.Document, error) {
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return blueprint.Document{}, err
	}
	if state.IdentityMapping != nil || document.Environment.Runtime.IdentityMapping != nil {
		return blueprint.Document{}, fmt.Errorf("environment %q: environment.runtime.identity_mapping requires multi-identity runtime admission, which is not yet available; stage and explicit build remain available", document.Environment.ID)
	}
	return document, nil
}

// Queue fixtures and exact-owned cleanup can have no deployment state. Actual
// payload entry points independently require it before reaching admission.
func requireUnmappedDeploymentV1(operation *deploy.OperationLock) error {
	state, found, err := operation.ReadStateV1()
	if err != nil || !found {
		return err
	}
	_, err = unmappedRuntimeDocumentV1(state)
	return err
}

// Staging cleanup owns no mapping reservations and admits no payload.
// B4 must check retained identity authority before this declaration exception.
func requireUnmappedControlDeploymentV1(operation *deploy.OperationLock, kind deploy.ControlOperationV1) error {
	if kind == deploy.ControlOperationStageV1 {
		return operation.RequireNoRetainedIdentityV1()
	}
	return requireUnmappedDeploymentV1(operation)
}
