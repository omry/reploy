package dockerdeploy

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
)

// This private metadata seam does not allocate IDs or admit a workload.
// B5 will bind its candidate to the selected generation and mapping revision.
func loadStagedIdentityMappingV1(operation *deploy.OperationLock, dir string) (*identitymapping.Geometry, error) {
	if operation == nil {
		return nil, fmt.Errorf("mapping input requires a deployment operation lock")
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return nil, err
	}
	if !found || state.Deployment != nil || state.Staging == nil {
		return nil, fmt.Errorf("mapping input requires selected staging state")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return nil, err
	}
	overrides, _, _, err := LoadStagedPackageOverridesV1(operation, dir, document)
	if err != nil {
		return nil, err
	}
	return deploy.ResolveIdentityMappingGeometryV1(document, overrides.IdentityMappingOverride())
}
