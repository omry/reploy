package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
)

// This private metadata seam does not allocate IDs or admit a workload.
func loadStagedIdentityMappingV1(operation *deploy.OperationLock, dir string) (*identitymapping.Geometry, error) {
	_, candidate, err := loadSelectedIdentityMappingV1(operation, dir, "")
	return candidate, err
}

func loadSelectedIdentityMappingV1(operation *deploy.OperationLock, dir, overrideFile string) (deploy.StateV1, *identitymapping.Geometry, error) {
	if operation == nil {
		return deploy.StateV1{}, nil, fmt.Errorf("mapping input requires a deployment operation lock")
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return state, nil, err
	}
	if !found || state.Deployment != nil || state.Staging == nil {
		return state, nil, fmt.Errorf("mapping input requires selected staging state")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil || operation.Path() != filepath.Join(absolute, ".reploy", "operation.lock") {
		return state, nil, fmt.Errorf("mapping input lock does not belong to the selected deployment")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return state, nil, err
	}
	var overrides deploy.PackageOverridesV1
	if overrideFile == "" {
		overrides, _, _, err = LoadStagedPackageOverridesV1(operation, dir, document)
	} else {
		overrides, found, err = deploy.ReadPackageOverridesFileV1(overrideFile)
		if err == nil && !found {
			err = fmt.Errorf("selected mapping override file does not exist")
		}
		if err == nil && overrides.Environment.ID != document.Environment.ID {
			err = fmt.Errorf("selected mapping override targets a different environment")
		}
		if err == nil {
			err = overrides.CheckIdentityMappingDeclaration(document)
		}
	}
	if err != nil {
		return state, nil, err
	}
	candidate, err := deploy.ResolveIdentityMappingGeometryV1(document, overrides.IdentityMappingOverride())
	return state, candidate, err
}

func configureStagedIdentityMappingV1(ctx context.Context, operation *deploy.OperationLock, dir, overrideFile, operationID string, evidence deploy.IdentityMappingEvidenceV1) (deploy.StateV1, error) {
	state, candidate, err := loadSelectedIdentityMappingV1(operation, dir, overrideFile)
	if err != nil || candidate == nil {
		return state, fmt.Errorf("mapping configuration requires a complete declared mapping: %v", err)
	}
	return (deploy.IdentityMappingOperationsV1{Operation: operation, Evidence: evidence}).Configure(ctx, state, *candidate, operationID)
}
