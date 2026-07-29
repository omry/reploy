package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/dockerdeploy"
)

func TestRunsHelpAndTopLevelHelp(t *testing.T) {
	code, stdout, stderr := runCLI("runs", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("runs help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{"runs list", "runs stop RUN_ID", "completed runs do not appear"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("runs help missing %q:\n%s", want, stdout)
		}
	}
	code, stdout, stderr = runCLI("--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "runs         List or stop outstanding app commands and shell sessions") {
		t.Fatalf("top-level help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunsListUsesResolvedDeploymentAndPrintsOutstandingRuns(t *testing.T) {
	old := dockerListLiveRuns
	t.Cleanup(func() { dockerListLiveRuns = old })
	var gotDir string
	dockerListLiveRuns = func(_ context.Context, dir string, _ io.Writer) ([]deploy.LiveRunV1, error) {
		gotDir = dir
		return []deploy.LiveRunV1{
			{ID: "run-0000000000000001", Status: deploy.LiveRunStatusActiveV1, Kind: deploy.LiveRunKindAppV1, Name: "export-data"},
			{ID: "run-0000000000000002", Status: deploy.LiveRunStatusWaitingV1, Kind: deploy.LiveRunKindShellV1, Name: "/bin/sh"},
		}, nil
	}
	code, stdout, stderr := runCLI("runs", "list", "--dir", "deployment")
	if code != 0 || stderr != "" || gotDir != "deployment" {
		t.Fatalf("runs list: code=%d dir=%q stdout=%q stderr=%q", code, gotDir, stdout, stderr)
	}
	for _, want := range []string{"ID", "STATUS", "KIND", "NAME", "run-0000000000000001  active", "run-0000000000000002  waiting", "export-data", "/bin/sh"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("runs list missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunsListReportsEmptyQueue(t *testing.T) {
	old := dockerListLiveRuns
	t.Cleanup(func() { dockerListLiveRuns = old })
	dockerListLiveRuns = func(context.Context, string, io.Writer) ([]deploy.LiveRunV1, error) { return nil, nil }
	code, stdout, stderr := runCLI("runs", "list", "--dir", t.TempDir())
	if code != 0 || stdout != "No active or waiting runs.\n" || stderr != "" {
		t.Fatalf("empty runs list: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunsStopForwardsTimeoutAndReportsFoundOrAbsent(t *testing.T) {
	old := dockerStopLiveRun
	t.Cleanup(func() { dockerStopLiveRun = old })
	var gotDir string
	var gotID string
	var gotTimeout time.Duration
	dockerStopLiveRun = func(_ context.Context, dir string, id string, timeout time.Duration) (dockerdeploy.LiveRunStopResultV1, error) {
		gotDir, gotID, gotTimeout = dir, id, timeout
		return dockerdeploy.LiveRunStopResultV1{Found: true, Run: deploy.LiveRunV1{ID: id, Status: deploy.LiveRunStatusWaitingV1, Kind: deploy.LiveRunKindShellV1}}, nil
	}
	code, stdout, stderr := runCLI("--docker-timeout", "9s", "runs", "stop", "run-0000000000000001", "--dir=deployment")
	if code != 0 || stderr != "" || gotDir != "deployment" || gotID != "run-0000000000000001" || gotTimeout != 9*time.Second || stdout != "Canceled waiting shell run-0000000000000001.\n" {
		t.Fatalf("found stop: code=%d dir=%q id=%q timeout=%s stdout=%q stderr=%q", code, gotDir, gotID, gotTimeout, stdout, stderr)
	}
	dockerStopLiveRun = func(_ context.Context, _ string, id string, _ time.Duration) (dockerdeploy.LiveRunStopResultV1, error) {
		return dockerdeploy.LiveRunStopResultV1{Found: true, Run: deploy.LiveRunV1{ID: id, Status: deploy.LiveRunStatusActiveV1, Kind: deploy.LiveRunKindAppV1}}, nil
	}
	code, stdout, stderr = runCLI("runs", "stop", "run-0000000000000002", "--dir", "deployment")
	if code != 0 || stderr != "" || stdout != "Stopped active app run-0000000000000002.\n" {
		t.Fatalf("active stop: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	dockerStopLiveRun = func(context.Context, string, string, time.Duration) (dockerdeploy.LiveRunStopResultV1, error) {
		return dockerdeploy.LiveRunStopResultV1{}, nil
	}
	code, stdout, stderr = runCLI("runs", "stop", "run-0000000000000003", "--dir", "deployment")
	if code != 0 || stderr != "" || stdout != "No active run found for run-0000000000000003; it may have already finished.\n" {
		t.Fatalf("absent stop: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunsStopReportsRecoveryBeforeStopError(t *testing.T) {
	old := dockerStopLiveRun
	t.Cleanup(func() { dockerStopLiveRun = old })
	dockerStopLiveRun = func(_ context.Context, _ string, _ string, _ time.Duration) (dockerdeploy.LiveRunStopResultV1, error) {
		return dockerdeploy.LiveRunStopResultV1{
			Recovery: deploy.LiveRunRecoveryV1{Removed: []deploy.RecoveredLiveRunV1{{
				Run: deploy.LiveRunV1{
					ID: "run-0000000000000002", Kind: deploy.LiveRunKindShellV1,
					Name: "shell",
				},
				Reason: deploy.LiveRunRecoveryAbandonedOwnerV1,
			}}},
		}, errors.New("target container removal failed")
	}
	code, stdout, stderr := runCLI(
		"runs", "stop", "run-0000000000000001", "--dir", "deployment",
	)
	if code != 1 || stdout != "" ||
		!strings.Contains(stderr, "skipped abandoned shell \"shell\" (run-0000000000000002)") ||
		!strings.Contains(stderr, "reploy runs stop error: target container removal failed") {
		t.Fatalf("stop recovery error: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunsRejectsInvalidUsageBeforeBackend(t *testing.T) {
	oldList := dockerListLiveRuns
	oldStop := dockerStopLiveRun
	t.Cleanup(func() { dockerListLiveRuns, dockerStopLiveRun = oldList, oldStop })
	dockerListLiveRuns = func(context.Context, string, io.Writer) ([]deploy.LiveRunV1, error) { panic("backend called") }
	dockerStopLiveRun = func(context.Context, string, string, time.Duration) (dockerdeploy.LiveRunStopResultV1, error) {
		panic("backend called")
	}
	for _, args := range [][]string{
		{"runs"},
		{"runs", "unknown"},
		{"runs", "list", "extra"},
		{"runs", "stop"},
		{"runs", "stop", "invalid"},
		{"runs", "stop", "run-0000000000000001", "run-0000000000000002"},
		{"runs", "list", "--dir="},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "reploy runs") {
			t.Fatalf("invalid args %v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}
