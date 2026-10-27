package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"github.com/omry/reploy/internal/providerstore"
)

func validateStagedIdentityPolicyCoverageV1(ctx context.Context, operation *deploy.OperationLock, dir string, state deploy.StateV1, candidate identitymapping.Geometry) error {
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil || document.Environment.Runtime.IdentityPolicy == nil {
		return err
	}
	reserved, err := deploy.RetainIdentityMappingGeometryV1(state, candidate)
	if err != nil {
		return err
	}
	store, err := providerstore.NewStore(dir)
	if err != nil {
		return err
	}
	return validateLockedIdentityCoverageV1(ctx, operation, store, document.Environment.ID, dir, &reserved)
}

// The existing operation lock binds selected state, evidence and reservations.
// Loading recorded evidence here must not inspect or execute the image.
func validateLockedIdentityCoverageV1(ctx context.Context, operation *deploy.OperationLock, store providerstore.Store, environment, dir string, reserved *identitymapping.Geometry) error {
	current, found, err := LoadRecordedCurrentBuildV1(ctx, operation, store, environment, dir)
	if err != nil {
		return err
	}
	if !found || current.Lock.IdentityPolicy == nil {
		return fmt.Errorf("identity policy coverage requires selected locked image evidence")
	}
	if reserved == nil {
		return fmt.Errorf("identity policy coverage for generation %s requires retained reservations", current.Generation.BuildLockDigest)
	}
	return deploy.ValidateIdentityPolicyCoverageV1(current.Lock.IdentityPolicy, *reserved, current.Generation.BuildLockDigest)
}
