package deploy

import (
	"context"
	"fmt"
	"os"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// TransferBuildLockStoreClosure copies exactly one build lock's verified
// provider-store closure between two locked deployments. Callers acquire the
// source operation lock before the destination lock and retain both until the
// installed state commit that follows this transfer.
func (sourceLock *OperationLock) TransferBuildLockStoreClosure(
	ctx context.Context,
	destinationLock *OperationLock,
	sourceStore providerstore.Store,
	destinationStore providerstore.Store,
	build BuildLockV1,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) ([]providerstore.StoreObjectRef, error) {
	if ctx == nil {
		return nil, fmt.Errorf("transfer build objects requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sourceLock == nil || destinationLock == nil {
		return nil, fmt.Errorf("transfer build objects requires source and destination operation locks")
	}
	if sourceLock == destinationLock {
		return nil, fmt.Errorf("transfer build objects requires distinct source and destination operation locks")
	}

	sourceLock.mutex.Lock()
	defer sourceLock.mutex.Unlock()
	destinationLock.mutex.Lock()
	defer destinationLock.mutex.Unlock()
	if err := sourceLock.validateProviderStoreLocked(sourceStore); err != nil {
		return nil, fmt.Errorf("source provider store: %w", err)
	}
	if err := destinationLock.validateProviderStoreLocked(destinationStore); err != nil {
		return nil, fmt.Errorf("destination provider store: %w", err)
	}
	if sourceStore.Root() == destinationStore.Root() {
		return nil, fmt.Errorf("transfer build objects requires distinct source and destination provider stores")
	}

	plan, err := loadBuildLockStoreClosure(build, sourceStore, validateProfileOwner, validateBundleOwner)
	if err != nil {
		return nil, fmt.Errorf("validate source build objects: %w", err)
	}

	for _, reference := range plan.References {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch reference.Kind {
		case providerstore.BlobKind:
			descriptor, ok := plan.Artifacts[reference]
			if !ok {
				return nil, fmt.Errorf("transfer source blob %s has no locked artifact descriptor", reference.Digest)
			}
			if err := transferBuildArtifact(ctx, sourceStore, destinationStore, descriptor); err != nil {
				return nil, err
			}
		case providerstore.BundleManifestKind:
			content, ok := plan.Manifests[reference]
			if !ok {
				return nil, fmt.Errorf("transfer source manifest %s is not referenced by a locked node", reference.Digest)
			}
			if err := destinationStore.PublishManifest(ctx, reference, content); err != nil {
				return nil, fmt.Errorf("publish destination bundle manifest %s: %w", reference.Digest, err)
			}
		case providerstore.ValidationRecordKind:
			if reference != build.ValidationRecord {
				return nil, fmt.Errorf("transfer source validation record %s is not selected by the build lock", reference.Digest)
			}
			if err := destinationStore.PublishValidationRecord(ctx, reference, plan.Validation); err != nil {
				return nil, fmt.Errorf("publish destination validation record %s: %w", reference.Digest, err)
			}
		default:
			return nil, fmt.Errorf("transfer source object kind %q is unsupported", reference.Kind)
		}
	}
	return plan.References, nil
}

func transferBuildArtifact(
	ctx context.Context,
	source providerstore.Store,
	destination providerstore.Store,
	descriptor providerstore.ArtifactDescriptor,
) (err error) {
	path, err := source.InspectArtifactPath(descriptor)
	if err != nil {
		return fmt.Errorf("inspect transfer source artifact %q: %w", descriptor.LogicalPath, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open transfer source artifact %q: %w", descriptor.LogicalPath, err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close transfer source artifact %q: %w", descriptor.LogicalPath, closeErr)
		}
	}()
	if _, err := destination.PublishExpected(ctx, descriptor, file); err != nil {
		return fmt.Errorf("publish destination artifact %q: %w", descriptor.LogicalPath, err)
	}
	return nil
}
