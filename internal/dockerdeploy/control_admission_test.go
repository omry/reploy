package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func controlAdmissionFixtureV1(id string, operation deploy.ControlOperationV1) deploy.ControlMarkerV1 {
	return deploy.ControlMarkerV1{
		ID: id, Operation: operation,
		GenerationReference: "reploy/env/demo:g-current",
	}
}

func holdControlLeaseV1(t *testing.T, operation *deploy.OperationLock, id string) *deploy.ControlLeaseV1 {
	t.Helper()
	lease, err := operation.AcquireControlLeaseV1(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Release(); err != nil {
			t.Errorf("release control lease %q: %v", id, err)
		}
	})
	return lease
}

func TestAwaitControlAdmissionV1ReturnsHeldAfterEarlierRunAndKeepsLaterRunBehind(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	holdLiveRunLeaseV1(t, operation, first.ID)
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	waitStarted := make(chan struct{}, 1)
	continueWait := make(chan struct{})
	backend := controlAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(ctx context.Context) error {
			waitStarted <- struct{}{}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-continueWait:
				return nil
			}
		},
	}
	marker := controlAdmissionFixtureV1("control-0000000000000001", deploy.ControlOperationInstallV1)
	lease, err := operation.AcquireControlLeaseV1(marker.ID)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		operation *deploy.OperationLock
		err       error
	}
	resultChannel := make(chan result, 1)
	go func() {
		admitted, err := awaitControlAdmissionV1(t.Context(), dir, operation, marker, true, nil, backend)
		resultChannel <- result{operation: admitted, err: err}
	}()
	<-waitStarted
	promoter, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	later := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	holdLiveRunLeaseV1(t, promoter, later.ID)
	if status, err := promoter.AdmitLiveRunV1(later, true); err != nil || status != deploy.LiveRunStatusWaitingV1 {
		t.Fatalf("later run admission = %q, %v", status, err)
	}
	queue, removed, err := promoter.RemoveLiveRunV1(first.ID)
	if err != nil || !removed || queue.Runs[0].Kind != deploy.LiveRunKindControlV1 || queue.Runs[0].Status != deploy.LiveRunStatusReadyV1 || queue.Runs[1].Status != deploy.LiveRunStatusWaitingV1 {
		t.Fatalf("control promotion = %#v, removed=%t, error=%v", queue, removed, err)
	}
	if err := promoter.Unlock(); err != nil {
		t.Fatal(err)
	}
	close(continueWait)
	got := <-resultChannel
	if got.err != nil {
		t.Fatal(got.err)
	}
	if err := got.operation.RequireHeld(); err != nil {
		t.Fatalf("returned lock is not held: %v", err)
	}
	if err := CompleteControlAdmissionV1(got.operation, marker.ID, lease); err != nil {
		t.Fatal(err)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 1 || queue.Runs[0].ID != later.ID || queue.Runs[0].Status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("later run promotion = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestAwaitControlAdmissionV1PreflightsRecoveredContainerCleanup(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	abandoned := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	if _, err := operation.AdmitLiveRunV1(abandoned, false); err != nil {
		t.Fatal(err)
	}
	container := "demo-" + abandoned.ID
	if err := operation.RecordLiveRunContainerV1(abandoned.ID, container); err != nil {
		t.Fatal(err)
	}
	marker := controlAdmissionFixtureV1("control-0000000000000001", deploy.ControlOperationInstallV1)
	lease, err := operation.AcquireControlLeaseV1(marker.ID)
	if err != nil {
		t.Fatal(err)
	}
	preflightCalls := 0
	restore := stubDockerPreflight(t, func(context.Context, CommandSpec, time.Duration) (string, error) {
		preflightCalls++
		return "", errors.New("remote Docker endpoint rejected")
	})
	defer restore()

	admitted, err := AwaitControlAdmissionV1(t.Context(), dir, operation, marker, false)
	if err != nil {
		t.Fatal(err)
	}
	if preflightCalls != 1 {
		t.Fatalf("Docker preflight calls = %d, want 1", preflightCalls)
	}
	queue, _, err := admitted.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != marker.ID ||
		len(queue.Cleanup) != 1 || queue.Cleanup[0].Container != container {
		t.Fatalf("queue after rejected remote cleanup = %#v, %v", queue, err)
	}
	if err := CompleteControlAdmissionV1(admitted, marker.ID, lease); err != nil {
		t.Fatal(err)
	}
}

func TestAwaitControlAdmissionV1ExplainsLifecycleWait(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	run := liveRunAdmissionFixtureV1("run-0000000000000001", true)
	run.WritableMount = "data"
	run.WritablePaths = []string{"/data"}
	holdLiveRunLeaseV1(t, operation, run.ID)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var notice bytes.Buffer
	backend := controlAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(context.Context) error {
			cancel()
			return context.Canceled
		},
	}
	marker := controlAdmissionFixtureV1("control-0000000000000001", deploy.ControlOperationInstallV1)
	holdControlLeaseV1(t, operation, marker.ID)
	_, err = awaitControlAdmissionV1(ctx, dir, operation, marker, true, &notice, backend)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v", err)
	}
	want := "Waiting for active app command \"export\" to finish (shared writable mounts: /data).\n" +
		"Ctrl-C cancels this wait without affecting the active command.\n"
	if notice.String() != want {
		t.Fatalf("wait notice = %q, want %q", notice.String(), want)
	}
}

func TestAwaitControlAdmissionV1CancellationRemovesOnlyCaller(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	holdLiveRunLeaseV1(t, operation, first.ID)
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	backend := controlAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		},
	}
	marker := controlAdmissionFixtureV1("control-0000000000000001", deploy.ControlOperationRestartV1)
	lease, err := operation.AcquireControlLeaseV1(marker.ID)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := awaitControlAdmissionV1(ctx, dir, operation, marker, true, nil, backend)
	if leaseErr := lease.Release(); leaseErr != nil {
		t.Fatal(leaseErr)
	}
	if admitted != nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "canceled queued lifecycle operation \"restart\" ("+marker.ID+")") {
		t.Fatalf("canceled control admission = %#v, %v", admitted, err)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 1 || queue.Runs[0].ID != first.ID {
		t.Fatalf("queue after cancellation = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestAwaitControlAdmissionV1ImmediateConflictReleasesLockWithoutMarker(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	holdLiveRunLeaseV1(t, operation, first.ID)
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	marker := controlAdmissionFixtureV1("control-0000000000000001", deploy.ControlOperationStopV1)
	holdControlLeaseV1(t, operation, marker.ID)
	admitted, err := awaitControlAdmissionV1(t.Context(), dir, operation, marker, false, nil, controlAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait:    func(context.Context) error { return nil },
	})
	if admitted != nil || !errors.Is(err, deploy.ErrLiveRunConflict) {
		t.Fatalf("conflicting control admission = %#v, %v", admitted, err)
	}
	if err := operation.RequireHeld(); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("conflicting control admission retained lock: %v", err)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), filepath.Clean(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 1 || queue.Runs[0].ID != first.ID || len(deploy.ControlMarkersV1(queue)) != 0 {
		t.Fatalf("queue after conflict = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestAwaitControlAdmissionV1RejectsForeignOperationLock(t *testing.T) {
	operation, err := deploy.AcquireOperationLock(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	marker := controlAdmissionFixtureV1("control-0000000000000001", deploy.ControlOperationUpV1)
	holdControlLeaseV1(t, operation, marker.ID)
	admitted, err := awaitControlAdmissionV1(t.Context(), t.TempDir(), operation, marker, false, nil, controlAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait:    func(context.Context) error { return nil },
	})
	if admitted != nil || err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign control admission = %#v, %v", admitted, err)
	}
	if err := operation.RequireHeld(); err == nil {
		t.Fatal("foreign operation lock was not released")
	}
}
