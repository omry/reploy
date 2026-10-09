package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDockerPullRetryRecoversOnlyTransientFailures(t *testing.T) {
	for _, test := range []struct {
		name, failure    string
		failCount, calls int
	}{
		{"WSL recovers", "WSL UtilAcceptVsock: accept4 failed 110; error getting credentials - err: exit status 1, out: ``", 2, 3},
		{"network recovers", "connection reset by peer", 1, 2},
		{"bounded attempts", "i/o timeout", 9, 3},
		{"authentication", "unauthorized: authentication required", 9, 1},
		{"authentication with timeout", "unauthorized: i/o timeout", 9, 1},
		{"helper configuration", "error getting credentials - err: exit status 1, out: ``", 9, 1},
		{"bad reference", "manifest unknown", 9, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls, waits := 0, 0
			run := func(_ context.Context, args ...string) (string, error) {
				calls++
				if strings.Join(args, " ") != "pull --platform linux/amd64 python:3.11-slim" {
					t.Fatalf("args=%v", args)
				}
				if calls <= test.failCount {
					return "", fmt.Errorf("%s", test.failure)
				}
				return "Digest: sha256:verified", nil
			}
			wait := func(_ context.Context, delay time.Duration) error {
				waits++
				if delay != time.Duration(waits)*500*time.Millisecond {
					t.Fatalf("delay=%v", delay)
				}
				return nil
			}
			var progress bytes.Buffer
			ctx := context.WithValue(t.Context(), dockerPullProgressContextKey{}, &progress)
			output, err := pullDockerBaseWithRetryDelay(ctx, run, "linux/amd64", "python:3.11-slim", wait)
			if calls != test.calls || waits != calls-1 || (err != nil) != (test.failCount >= test.calls) {
				t.Fatalf("calls=%d waits=%d output=%q err=%v", calls, waits, output, err)
			}
			if err != nil && !strings.Contains(err.Error(), test.failure) {
				t.Fatalf("lost diagnostic: %v", err)
			}
			if waits > 0 && !strings.Contains(progress.String(), "retrying base image fetch") {
				t.Fatal("missing retry progress")
			}
			if err == nil && waits > 0 && !strings.Contains(progress.String(), "fetch recovered") {
				t.Fatal("missing recovery progress")
			}
		})
	}
}

func TestDockerPullRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	run := func(context.Context, ...string) (string, error) { calls++; return "", errors.New("i/o timeout") }
	wait := func(ctx context.Context, delay time.Duration) error { cancel(); return waitDockerPullRetry(ctx, delay) }
	_, err := pullDockerBaseWithRetryDelay(ctx, run, "linux/amd64", "python:3.11-slim", wait)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
