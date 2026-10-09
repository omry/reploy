package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

type dockerPullProgressContextKey struct{}

// Retry only failed pulls with recognizable transport failures. Authentication,
// invalid references and unclassified credential-helper errors fail immediately.
// Successful output still passes the normal immutable-digest validation.
func pullDockerBaseWithRetry(ctx context.Context, run dockerOutputRunner, platform, reference string) (string, error) {
	return pullDockerBaseWithRetryDelay(ctx, run, platform, reference, waitDockerPullRetry)
}

func pullDockerBaseWithRetryDelay(ctx context.Context, run dockerOutputRunner, platform, reference string, wait func(context.Context, time.Duration) error) (string, error) {
	progress, _ := ctx.Value(dockerPullProgressContextKey{}).(io.Writer)
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		output, err := run(ctx, "pull", "--platform", platform, reference)
		if err == nil {
			if attempt > 1 {
				writeProviderBuildProgress(progress, "base image fetch recovered after %d attempts", attempt)
			}
			return output, nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		if attempt == 3 || !transientDockerPullFailure(output+"\n"+err.Error()) {
			return "", fmt.Errorf("Docker pull failed after %d attempt(s): %w", attempt, err)
		}
		if err := wait(ctx, time.Duration(attempt)*500*time.Millisecond); err != nil {
			return "", err
		}
		writeProviderBuildProgress(progress, "retrying base image fetch (attempt %d/3)", attempt+1)
	}
}

func waitDockerPullRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func transientDockerPullFailure(message string) bool {
	message = strings.ToLower(message)
	for _, permanent := range []string{"unauthorized", "authentication required", "denied", "incorrect username", "invalid reference", "manifest unknown", "no matching manifest", "no such host", "certificate", "too many requests"} {
		if strings.Contains(message, permanent) {
			return false
		}
	}
	for _, transient := range []string{"utilacceptvsock", "connection reset by peer", "connection refused", "i/o timeout", "tls handshake timeout", "context deadline exceeded", "unexpected eof", "temporary failure in name resolution"} {
		if strings.Contains(message, transient) {
			return true
		}
	}
	return false
}
