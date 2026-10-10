package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func frontendFetchFailure(cause string) string {
	return "ERROR: failed to solve: " + MaterializationDockerfileSyntax +
		": failed to resolve source metadata for docker.io/" + MaterializationDockerfileSyntax + ": " + cause
}

func TestDockerFrontendRetryFailureBoundary(t *testing.T) {
	for _, test := range []struct {
		name, output string
		failures     int
		calls        int
	}{
		{"first success", "", 0, 1},
		{"transport recovers", frontendFetchFailure("connection reset by peer"), 1, 2},
		{"WSL recovers", "<3>WSL ERROR: UtilAcceptVsock: accept4 failed 110\n" + frontendFetchFailure("error getting credentials - err: exit status 1, out: ``"), 2, 3},
		{"bounded attempts", frontendFetchFailure("i/o timeout"), 9, 3},
		{"new CLI error prefix", strings.Replace(frontendFetchFailure("unexpected EOF"), "ERROR: failed to solve:", "ERROR: failed to build: failed to solve:", 1), 1, 2},
		{"direct resolution error", strings.Replace(frontendFetchFailure("TLS handshake timeout"), MaterializationDockerfileSyntax+": failed", "failed", 1), 1, 2},
		{"authentication", frontendFetchFailure("unauthorized: authentication required"), 9, 1},
		{"authentication with timeout", frontendFetchFailure("unauthorized: i/o timeout"), 9, 1},
		{"unclassified helper error", frontendFetchFailure("error getting credentials - err: exit status 1, out: ``"), 9, 1},
		{"certificate", frontendFetchFailure("certificate expired; TLS handshake timeout"), 9, 1},
		{"bad frontend", frontendFetchFailure("manifest unknown"), 9, 1},
		{"different digest", strings.ReplaceAll(frontendFetchFailure("i/o timeout"), MaterializationDockerfileSyntax, "docker/dockerfile:1@sha256:"+strings.Repeat("a", 64)), 9, 1},
		{"different image", strings.ReplaceAll(frontendFetchFailure("i/o timeout"), MaterializationDockerfileSyntax, "python:3.11-slim"), 9, 1},
		{"digest suffix", strings.ReplaceAll(frontendFetchFailure("i/o timeout"), MaterializationDockerfileSyntax, MaterializationDockerfileSyntax+"extra"), 9, 1},
		{"warning then RUN failure", "<3>WSL ERROR: UtilAcceptVsock: accept4 failed 110\n#2 resolve image config for docker-image://docker.io/" + MaterializationDockerfileSyntax + "\n#2 DONE\nERROR: failed to solve: process /bin/sh -c pip install did not complete successfully: exit code: 1", 9, 1},
		{"RUN quotes frontend error", "ERROR: failed to solve: process /bin/sh -c echo 'failed to resolve source metadata for docker.io/" + MaterializationDockerfileSyntax + ": i/o timeout' did not complete successfully", 9, 1},
		{"earlier frontend error", frontendFetchFailure("i/o timeout") + "\nERROR: failed to solve: process /bin/sh -c build failed", 9, 1},
		{"frontend error then Docker Desktop summary", frontendFetchFailure("i/o timeout") + "\n\nView build details: docker-desktop://dashboard/build/test", 1, 2},
		{"frontend error then ordinary error and Docker Desktop summary", frontendFetchFailure("i/o timeout") + "\nERROR: failed to solve: process /bin/sh -c build failed\n\nView build details: docker-desktop://dashboard/build/test", 9, 1},
		{"frontend error then ordinary nonsolve error and Docker Desktop summary", frontendFetchFailure("i/o timeout") + "\nERROR: write result.iid: no space left on device\n\nView build details: docker-desktop://dashboard/build/test", 9, 1},
		{"missing terminal solve error", "#2 resolve image config for docker-image://docker.io/" + MaterializationDockerfileSyntax + "\n#2 ERROR: i/o timeout", 9, 1},
	} {
		for _, verbose := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/verbose=%t", test.name, verbose), func(t *testing.T) {
				var stdout, stderr, progress bytes.Buffer
				options := RunOptions{Context: t.Context(), Progress: &progress}
				if verbose {
					options.Stdout, options.Stderr = &stdout, &stderr
				}
				spec := CommandSpec{Name: "docker", Args: []string{"build", "--no-cache", "fixture"}, Env: []string{"DOCKER_BUILDKIT=1"}}
				calls, waits := 0, 0
				cause := errors.New("builder failed")
				run := func(command CommandSpec, got RunOptions) error {
					calls++
					if !reflect.DeepEqual(command, spec) || got.Context != options.Context {
						t.Fatal("retry changed command or context")
					}
					fmt.Fprintln(got.Stdout, "backend stdout")
					if calls <= test.failures {
						fmt.Fprintln(got.Stderr, test.output)
						return cause
					}
					return nil
				}
				wait := func(ctx context.Context, delay time.Duration) error {
					waits++
					if ctx != options.Context || delay != time.Duration(waits)*500*time.Millisecond {
						t.Fatalf("backoff context=%v delay=%v", ctx, delay)
					}
					return nil
				}
				err := runDockerBuildWithFrontendRetryDelay(spec, options, run, wait)
				failed := test.failures >= test.calls
				if calls != test.calls || waits != calls-1 || (err != nil) != failed {
					t.Fatalf("calls=%d waits=%d err=%v", calls, waits, err)
				}
				if failed && (!errors.Is(err, cause) || !strings.Contains(err.Error(), test.output)) {
					t.Fatalf("lost failure diagnostic: %v", err)
				}
				if !verbose && (stdout.Len() != 0 || stderr.Len() != 0) {
					t.Fatal("quiet build leaked backend output")
				}
				if verbose && !strings.Contains(stdout.String(), "backend stdout") {
					t.Fatal("verbose build lost backend output")
				}
				if verbose && test.failures > 0 && !strings.Contains(stderr.String(), test.output) {
					t.Fatal("verbose build lost failure output")
				}
				if waits > 0 && !strings.Contains(progress.String(), "retrying Dockerfile frontend fetch") {
					t.Fatal("missing retry progress")
				}
				if !failed && waits > 0 && !strings.Contains(progress.String(), "fetch recovered") {
					t.Fatal("missing recovery progress")
				}
				if failed && calls == 3 && !strings.Contains(err.Error(), "failed after 3 attempts") {
					t.Fatal("missing exhaustion diagnostic")
				}
			})
		}
	}
}

func TestDockerFrontendRetryUsesContextProgressWhenExplicitWriterAbsent(t *testing.T) {
	for _, test := range []struct {
		name          string
		contextNotice bool
		explicit      bool
		wantProgress  bool
	}{
		{name: "context fallback", contextNotice: true, wantProgress: true},
		{name: "explicit writer wins", contextNotice: true, explicit: true, wantProgress: true},
		{name: "nil stays quiet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var contextProgress, explicitProgress bytes.Buffer
			ctx := t.Context()
			if test.contextNotice {
				ctx = context.WithValue(ctx, dockerPullProgressContextKey{}, &contextProgress)
			}
			options := RunOptions{Context: ctx}
			if test.explicit {
				options.Progress = &explicitProgress
			}
			calls := 0
			run := func(_ CommandSpec, got RunOptions) error {
				calls++
				fmt.Fprintln(got.Stderr, frontendFetchFailure("i/o timeout"))
				if calls == 1 {
					return errors.New("builder failed")
				}
				return nil
			}
			err := runDockerBuildWithFrontendRetryDelay(CommandSpec{}, options, run, func(context.Context, time.Duration) error { return nil })
			if err != nil || calls != 2 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if got := strings.Contains(contextProgress.String(), "retrying Dockerfile frontend fetch") && test.explicit; got {
				t.Fatalf("context progress received output despite explicit writer: %q", contextProgress.String())
			}
			if test.wantProgress {
				progress := contextProgress.String()
				if test.explicit {
					progress = explicitProgress.String()
				}
				if !strings.Contains(progress, "retrying Dockerfile frontend fetch") || !strings.Contains(progress, "fetch recovered") {
					t.Fatalf("progress = %q", progress)
				}
			} else if contextProgress.Len() != 0 || explicitProgress.Len() != 0 {
				t.Fatalf("nil progress was not quiet: context=%q explicit=%q", contextProgress.String(), explicitProgress.String())
			}
		})
	}
}

func TestDockerFrontendRetryReclassifiesEveryAttempt(t *testing.T) {
	calls := 0
	run := func(_ CommandSpec, options RunOptions) error {
		calls++
		if calls == 1 {
			fmt.Fprintln(options.Stderr, frontendFetchFailure("i/o timeout"))
		} else {
			fmt.Fprintln(options.Stderr, "ERROR: failed to solve: process /bin/sh -c build failed")
		}
		return errors.New("failed")
	}
	err := runDockerBuildWithFrontendRetryDelay(CommandSpec{}, RunOptions{Context: t.Context()}, run, func(context.Context, time.Duration) error { return nil })
	if calls != 2 || err == nil || !strings.Contains(err.Error(), "process /bin/sh -c build failed") || strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestDockerFrontendRetryCommandCaptureUsesFinalAttempt(t *testing.T) {
	capture := NewCommandOutputCapture()
	var sink bytes.Buffer
	calls := 0
	run := func(_ CommandSpec, options RunOptions) error {
		calls++
		message := frontendFetchFailure(fmt.Sprintf("i/o timeout (attempt-%d)", calls))
		fmt.Fprintln(options.Stderr, message)
		return errors.New("builder failed")
	}
	err := runDockerBuildWithFrontendRetryDelay(
		CommandSpec{}, RunOptions{Context: t.Context(), Stderr: &sink, Capture: capture}, run,
		func(context.Context, time.Duration) error { return nil },
	)
	if calls != 3 || err == nil {
		t.Fatalf("calls=%d err=%v, want three-attempt failure", calls, err)
	}
	diagnostic := CommandOutputCaptureDiagnostic(err, capture)
	if !strings.Contains(diagnostic, "attempt-3") || strings.Contains(diagnostic, "attempt-1") || strings.Contains(diagnostic, "attempt-2") {
		t.Fatalf("capture diagnostic = %q, want only final attempt", diagnostic)
	}
	if strings.Contains(err.Error(), "attempt-3") {
		t.Fatalf("capture changed rendered error: %q", err)
	}
}

func TestDockerFrontendRetryCommandCaptureReplacesRecoveredFrontendFailure(t *testing.T) {
	capture := NewCommandOutputCapture()
	var sink bytes.Buffer
	calls := 0
	run := func(_ CommandSpec, options RunOptions) error {
		calls++
		if calls == 1 {
			fmt.Fprintln(options.Stderr, frontendFetchFailure("i/o timeout (first-attempt)"))
		} else {
			fmt.Fprintln(options.Stderr, "ERROR: write result.iid: no space left on device")
		}
		return errors.New("builder failed")
	}
	err := runDockerBuildWithFrontendRetryDelay(
		CommandSpec{}, RunOptions{Context: t.Context(), Stderr: &sink, Capture: capture}, run,
		func(context.Context, time.Duration) error { return nil },
	)
	if calls != 2 || err == nil {
		t.Fatalf("calls=%d err=%v, want ordinary second-attempt failure", calls, err)
	}
	diagnostic := CommandOutputCaptureDiagnostic(err, capture)
	if !strings.Contains(diagnostic, "write result.iid: no space left on device") || strings.Contains(diagnostic, "first-attempt-timeout") {
		t.Fatalf("capture diagnostic = %q, want only ordinary final failure", diagnostic)
	}
}

func TestDockerFrontendRetryCancellation(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(fmt.Sprint(alreadyCanceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			calls := 0
			run := func(_ CommandSpec, options RunOptions) error {
				calls++
				fmt.Fprintln(options.Stderr, frontendFetchFailure("i/o timeout"))
				return errors.New("failed")
			}
			wait := func(ctx context.Context, delay time.Duration) error {
				cancel()
				return waitDockerPullRetry(ctx, delay)
			}
			err := runDockerBuildWithFrontendRetryDelay(CommandSpec{}, RunOptions{Context: ctx}, run, wait)
			if !errors.Is(err, context.Canceled) || (alreadyCanceled && calls != 0) || (!alreadyCanceled && calls != 1) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestDockerFrontendRetryRetainsBoundedDiagnosticTail(t *testing.T) {
	cause := errors.New("failed")
	run := func(_ CommandSpec, options RunOptions) error {
		fmt.Fprint(options.Stderr, "discarded beginning", strings.Repeat("x", 100_000), "\nfinal useful diagnostic")
		return cause
	}
	err := runDockerBuildWithFrontendRetryDelay(CommandSpec{}, RunOptions{}, run, func(context.Context, time.Duration) error { t.Fatal("ordinary error retried"); return nil })
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "[last 4000 bytes]") || !strings.Contains(err.Error(), "final useful diagnostic") || strings.Contains(err.Error(), "discarded beginning") || len(err.Error()) > commandOutputErrorLimit+100 {
		t.Fatalf("invalid diagnostic tail: %v", err)
	}
	var tail dockerBuildOutputTail
	var writers sync.WaitGroup
	for i := 0; i < 2; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for j := 0; j < 100; j++ {
				_, _ = io.WriteString(&tail, "concurrent stdout/stderr\n")
			}
		}()
	}
	writers.Wait()
	if len(tail.String()) > commandOutputErrorLimit+30 {
		t.Fatal("concurrent output exceeded diagnostic bound")
	}
}

func TestDockerFrontendRetryPinsEndpointOnceForActualCommands(t *testing.T) {
	dir := t.TempDir()
	marker, endpoints := filepath.Join(dir, "attempt"), filepath.Join(dir, "endpoints")
	t.Setenv("FRONTEND_ATTEMPT_MARKER", marker)
	t.Setenv("FRONTEND_ENDPOINT_LOG", endpoints)
	failure := frontendFetchFailure("i/o timeout")
	writeFakeCommand(t, dir, "docker",
		"#!/bin/sh\nprintf '%s\\n' \"$DOCKER_HOST\" >> \"$FRONTEND_ENDPOINT_LOG\"\nif [ ! -f \"$FRONTEND_ATTEMPT_MARKER\" ]; then\n : > \"$FRONTEND_ATTEMPT_MARKER\"\n printf '%s\\n' '"+failure+"' >&2\n exit 1\nfi\nprintf 'built successfully\\n'\n",
		"@echo off\r\necho %DOCKER_HOST%>> \"%FRONTEND_ENDPOINT_LOG%\"\r\nif exist \"%FRONTEND_ATTEMPT_MARKER%\" goto recovered\r\ntype nul > \"%FRONTEND_ATTEMPT_MARKER%\"\r\necho "+failure+" 1>&2\r\nexit /b 1\r\n:recovered\r\necho built successfully\r\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	const endpoint = "unix:///frontend-test-engine.sock"
	preflights := 0
	restore := stubDockerPreflight(t, func(context.Context, CommandSpec, time.Duration) (string, error) {
		preflights++
		return endpoint, nil
	})
	defer restore()
	var progress bytes.Buffer
	ctx := context.WithValue(t.Context(), dockerPullProgressContextKey{}, &progress)
	if err := runDockerBuildWithFrontendRetry(CommandSpec{Name: "docker", Args: []string{"build", "fixture"}}, RunOptions{Context: ctx}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(content)); !reflect.DeepEqual(got, []string{endpoint, endpoint}) || preflights != 1 || !strings.Contains(progress.String(), "fetch recovered after 2 attempts") {
		t.Fatalf("endpoints=%q preflights=%d progress=%q", content, preflights, progress.String())
	}
}
