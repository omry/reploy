package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

const liveRunRecoveryCleanupTimeoutV1 = 2 * time.Second

func recoverLiveRunQueueV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	notice io.Writer,
	removeContainer commandRunner,
) (deploy.LiveRunRecoveryV1, error) {
	return recoverLiveRunQueueWithinV1(
		ctx, operation, notice, removeContainer, liveRunRecoveryCleanupTimeoutV1,
	)
}

func recoverLiveRunQueueWithinV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	notice io.Writer,
	removeContainer commandRunner,
	cleanupTimeout time.Duration,
) (deploy.LiveRunRecoveryV1, error) {
	recovery, err := operation.RecoverLiveRunQueueV1()
	if err != nil {
		return deploy.LiveRunRecoveryV1{}, err
	}
	writeLiveRunRecoveryNoticeV1(notice, recovery)
	if removeContainer == nil {
		writeScheduledLiveRunCleanupNoticeV1(notice, recovery)
		return recovery, nil
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		return deploy.LiveRunRecoveryV1{}, err
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	for _, cleanup := range queue.Cleanup {
		if err := cleanupContext.Err(); err != nil {
			if notice != nil {
				fmt.Fprintf(notice, "warning: deferred remaining recovered container cleanup: %v\n", err)
			}
			break
		}
		removeErr := removeContainer(
			TemporaryContainerCleanupCommand(cleanup.Container),
			RunOptions{Context: cleanupContext},
		)
		if removeErr != nil && !isMissingContainerCleanupError(removeErr) {
			if notice != nil {
				fmt.Fprintf(notice,
					"warning: deferred cleanup of recovered %s %q container %q: %v\n",
					cleanup.Kind, cleanup.Name, cleanup.Container, removeErr,
				)
			}
			continue
		}
		removed, err := operation.CompleteLiveRunContainerCleanupV1(cleanup.Container)
		if err != nil {
			return deploy.LiveRunRecoveryV1{}, err
		}
		if !removed {
			return deploy.LiveRunRecoveryV1{}, fmt.Errorf(
				"recovered container cleanup %q disappeared while the operation lock was held",
				cleanup.Container,
			)
		}
	}
	return recovery, nil
}

func writeLiveRunRecoveryNoticeV1(output io.Writer, recovery deploy.LiveRunRecoveryV1) {
	if output == nil {
		return
	}
	for _, recovered := range recovery.Removed {
		entry := recovered.Run
		switch recovered.Reason {
		case deploy.LiveRunRecoveryAbandonedOwnerV1:
			fmt.Fprintf(output,
				"warning: skipped abandoned %s %q (%s): its owning Reploy client exited\n",
				admissionRecoveryKindV1(entry), entry.Name, entry.ID,
			)
		case deploy.LiveRunRecoveryPriorSessionV1:
			fmt.Fprintf(output,
				"warning: skipped prior-session %s %q (%s) after a host restart\n",
				admissionRecoveryKindV1(entry), entry.Name, entry.ID,
			)
		case deploy.LiveRunRecoveryLegacyEntryV1:
			fmt.Fprintf(output,
				"warning: skipped legacy %s %q (%s): its owner cannot be verified\n",
				admissionRecoveryKindV1(entry), entry.Name, entry.ID,
			)
		}
	}
}

func writeScheduledLiveRunCleanupNoticeV1(output io.Writer, recovery deploy.LiveRunRecoveryV1) {
	if output == nil {
		return
	}
	for _, recovered := range recovery.Removed {
		if recovered.Run.Container != "" {
			fmt.Fprintf(output, "warning: scheduled cleanup for transient container %q\n", recovered.Run.Container)
		}
	}
}

// WriteLiveRunRecoveryNoticeV1 reports successful automatic recovery without
// exposing the queue's internal control-marker representation.
func WriteLiveRunRecoveryNoticeV1(output io.Writer, recovery deploy.LiveRunRecoveryV1) {
	writeLiveRunRecoveryNoticeV1(output, recovery)
}

func admissionRecoveryKindV1(entry deploy.LiveRunV1) string {
	switch entry.Kind {
	case deploy.LiveRunKindAppV1:
		return "app command"
	case deploy.LiveRunKindShellV1:
		return "shell"
	case deploy.LiveRunKindControlV1:
		return "lifecycle operation"
	default:
		return "queued operation"
	}
}
