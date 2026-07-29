package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/omry/reploy/internal/providerstore"
)

var runAbandonedBuildReferenceCleanupDocker = runDockerOutput

// removeAbandonedBuildReferences derives exact temporary image references from
// surviving build workspaces in the current deployment provider store.
// Callers hold the deployment operation lock, so no matching workspace can
// belong to a live Reploy operation.
func removeAbandonedBuildReferences(ctx context.Context, store providerstore.Store) error {
	if ctx == nil {
		return fmt.Errorf("abandoned build reference cleanup requires a context")
	}
	temporaryRoot := filepath.Join(store.Root(), "tmp")
	entries, err := os.ReadDir(temporaryRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read provider store temporary directory for build-reference recovery: %w", err)
	}
	references := []string{}
	for _, entry := range entries {
		if !temporaryWorkspaceName(entry.Name(), "build-") &&
			!temporaryWorkspaceName(entry.Name(), "finalize-") {
			continue
		}
		workspace := filepath.Join(temporaryRoot, entry.Name())
		baseReference, err := temporaryBuildBaseReference(store.Root(), workspace)
		if err != nil {
			return fmt.Errorf("derive abandoned build base reference: %w", err)
		}
		outputReference, err := temporaryBuildOutputReference(store.Root(), workspace)
		if err != nil {
			return fmt.Errorf("derive abandoned build output reference: %w", err)
		}
		references = append(references, baseReference, outputReference)
	}
	sort.Strings(references)
	for _, reference := range references {
		if err := removeTemporaryBuildReference(
			ctx, reference, "", runAbandonedBuildReferenceCleanupDocker,
		); err != nil {
			return fmt.Errorf("remove abandoned build reference %q: %w", reference, err)
		}
	}
	return nil
}
