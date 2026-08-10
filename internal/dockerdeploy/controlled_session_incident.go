package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
)

// ListControlledSessionIncidentReceiptsV1 is the read-only retrieval surface
// for durable post-crash watchdog evidence. It does not create deployment
// state or contact Docker.
func ListControlledSessionIncidentReceiptsV1(
	ctx context.Context,
	deploymentDirectory string,
) ([]deploy.ControlledSessionIncidentReceiptV1, error) {
	operation, err := deploy.AcquireExistingOperationLock(ctx, deploymentDirectory)
	if err != nil {
		return nil, fmt.Errorf("open controlled-session incident receipts: %w", err)
	}
	receipts, readErr := operation.ReadControlledSessionIncidentReceiptsV1()
	unlockErr := operation.Unlock()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("release controlled-session incident receipt lock: %w", unlockErr)
	}
	return receipts, errors.Join(readErr, unlockErr)
}

// AcknowledgeControlledSessionIncidentReceiptV1 removes only one validated
// receipt after its consumer has handled it. Missing receipts are idempotent.
func AcknowledgeControlledSessionIncidentReceiptV1(
	ctx context.Context,
	deploymentDirectory string,
	liveRunID string,
) (bool, error) {
	operation, err := deploy.AcquireExistingOperationLock(ctx, deploymentDirectory)
	if err != nil {
		return false, fmt.Errorf("open controlled-session incident receipt: %w", err)
	}
	removed, acknowledgeErr := operation.AcknowledgeControlledSessionIncidentReceiptV1(liveRunID)
	unlockErr := operation.Unlock()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("release controlled-session incident receipt lock: %w", unlockErr)
	}
	return removed, errors.Join(acknowledgeErr, unlockErr)
}
