package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type EmbeddedControlMetadataV1 struct {
	EnvironmentID string
	ControlScript string
	SystemdUnit   string
}

// LoadEmbeddedControlMetadataV1 reads only the installed state needed by the
// self-contained control wrapper. A false result means the deployment does not
// use state-v1.
func LoadEmbeddedControlMetadataV1(ctx context.Context, dir string) (metadata EmbeddedControlMetadataV1, found bool, err error) {
	if ctx == nil {
		return EmbeddedControlMetadataV1{}, false, fmt.Errorf("load embedded control metadata requires a context")
	}
	if err := ctx.Err(); err != nil {
		return EmbeddedControlMetadataV1{}, false, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return EmbeddedControlMetadataV1{}, false, fmt.Errorf("resolve embedded control deployment directory: %w", err)
	}
	schema, err := runtimeStateSchema(dir)
	if err != nil {
		return EmbeddedControlMetadataV1{}, false, err
	}
	if schema != deploy.StateSchemaV1 {
		return EmbeddedControlMetadataV1{}, false, nil
	}
	operation, err := deploy.AcquireOperationLock(ctx, dir)
	if err != nil {
		return EmbeddedControlMetadataV1{}, false, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return EmbeddedControlMetadataV1{}, false, err
	}
	if !found || state.Deployment == nil {
		return EmbeddedControlMetadataV1{}, false, fmt.Errorf("embedded control requires an installed state-v1 deployment")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return EmbeddedControlMetadataV1{}, false, fmt.Errorf("embedded control blueprint: %w", err)
	}
	metadata = EmbeddedControlMetadataV1{
		EnvironmentID: document.Environment.ID,
		ControlScript: document.Environment.ControlScript,
	}
	installation := state.Deployment.Installation
	if strings.TrimSpace(installation.UnitPath) != "" {
		metadata.SystemdUnit = strings.TrimSpace(installation.Service)
		if !strings.HasSuffix(metadata.SystemdUnit, ".service") {
			metadata.SystemdUnit += ".service"
		}
	}
	return metadata, true, nil
}
