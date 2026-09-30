package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

// removeRecordedGenerationReferencesV1 removes every generation-lifetime tag
// selected by a validated lock. If removal of the final image tag fails, the
// portable tag is restored before the caller can restore its retained state.
func removeRecordedGenerationReferencesV1(
	ctx context.Context,
	lock deploy.BuildLockV1,
	generation, environment, deploymentDir string,
	removeFinal func(context.Context, providers.RealizedImageV1, string, string, string) error,
	removePortable func(context.Context, providers.RealizedImageV1, string, string, string) error,
	createPortable func(context.Context, providers.RealizedImageV1, string, string, string) error,
) error {
	if err := validateRecordedGenerationReferenceCleanupV1(lock, removeFinal, removePortable, createPortable); err != nil {
		return err
	}
	if lock.PortableRuntimeLayer != nil {
		if err := removePortable(ctx, lock.PortableRuntimeLayer.Result, generation, environment, deploymentDir); err != nil {
			return fmt.Errorf("remove portable runtime layer reference: %w", err)
		}
	}
	if err := removeFinal(ctx, lock.FinalImage, generation, environment, deploymentDir); err != nil {
		if lock.PortableRuntimeLayer == nil {
			return fmt.Errorf("remove final image reference: %w", err)
		}
		restoreErr := createPortable(context.WithoutCancel(ctx), lock.PortableRuntimeLayer.Result, generation, environment, deploymentDir)
		return errors.Join(fmt.Errorf("remove final image reference: %w", err),
			wrapPortableReferenceRestoreErrorV1(restoreErr))
	}
	return nil
}

func validateRecordedGenerationReferenceCleanupV1(
	lock deploy.BuildLockV1,
	removeFinal func(context.Context, providers.RealizedImageV1, string, string, string) error,
	removePortable func(context.Context, providers.RealizedImageV1, string, string, string) error,
	createPortable func(context.Context, providers.RealizedImageV1, string, string, string) error,
) error {
	if removeFinal == nil {
		return fmt.Errorf("remove recorded generation requires final-image reference support")
	}
	if lock.PortableRuntimeLayer != nil {
		if removePortable == nil || createPortable == nil {
			return fmt.Errorf("remove recorded generation requires portable-layer reference support")
		}
	}
	return nil
}

func wrapPortableReferenceRestoreErrorV1(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("restore portable runtime layer reference: %w", err)
}
