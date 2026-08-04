package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func liveRunAdmissionFixtureV1(id string, exclusive bool) deploy.LiveRunV1 {
	return deploy.LiveRunV1{
		ID: id, Kind: deploy.LiveRunKindAppV1, Name: "export",
		GenerationReference: "reploy/env/demo:g-current", Exclusive: exclusive,
	}
}

func holdLiveRunLeaseV1(t *testing.T, operation *deploy.OperationLock, id string) *deploy.QueueEntryLeaseV1 {
	t.Helper()
	lease, err := operation.AcquireLiveRunLeaseV1(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Release(); err != nil {
			t.Errorf("release live-run lease %q: %v", id, err)
		}
	})
	return lease
}

func TestAwaitLiveRunAdmissionV1ReturnsWithLockHeldAfterFairPromotion(t *testing.T) {
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
	backend := liveRunAdmissionBackendV1{
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
	second := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	holdLiveRunLeaseV1(t, operation, second.ID)
	type admissionResult struct {
		operation *deploy.OperationLock
		err       error
	}
	resultChannel := make(chan admissionResult, 1)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		admitted, err := awaitLiveRunAdmissionV1(ctx, dir, operation, second, true, nil, backend)
		resultChannel <- admissionResult{operation: admitted, err: err}
	}()
	<-waitStarted
	promoter, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	queue, removed, err := promoter.RemoveLiveRunV1(first.ID)
	if err != nil || !removed || len(queue.Runs) != 1 || queue.Runs[0].ID != second.ID || queue.Runs[0].Status != deploy.LiveRunStatusReadyV1 {
		t.Fatalf("promotion = %#v, removed=%t, error=%v", queue, removed, err)
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
	cancel()
	queue, _, err = got.operation.ReadLiveRunQueueV1()
	if err != nil || queue.Runs[0].Status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("claimed admission = %#v, error=%v", queue, err)
	}
	if _, removed, err := got.operation.RemoveLiveRunV1(second.ID); err != nil || !removed {
		t.Fatalf("remove admitted run = %t, %v", removed, err)
	}
	if err := got.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestAwaitLiveRunAdmissionV1CancellationWinsAfterReadyReservation(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", true)
	holdLiveRunLeaseV1(t, operation, first.ID)
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	second := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	holdLiveRunLeaseV1(t, operation, second.ID)
	waitStarted := make(chan struct{})
	allowWait := make(chan struct{})
	lockAcquired := make(chan struct{})
	allowAcquire := make(chan struct{})
	backend := liveRunAdmissionBackendV1{
		acquire: func(ctx context.Context, deploymentDir string) (*deploy.OperationLock, error) {
			lock, err := deploy.AcquireOperationLock(ctx, deploymentDir)
			if err != nil {
				return nil, err
			}
			close(lockAcquired)
			<-allowAcquire
			return lock, nil
		},
		wait: func(context.Context) error {
			close(waitStarted)
			<-allowWait
			return nil
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := awaitLiveRunAdmissionV1(ctx, dir, operation, second, true, nil, backend)
		result <- err
	}()
	<-waitStarted
	promoter, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	queue, removed, err := promoter.RemoveLiveRunV1(first.ID)
	if err != nil || !removed || len(queue.Runs) != 1 || queue.Runs[0].Status != deploy.LiveRunStatusReadyV1 {
		t.Fatalf("ready reservation = %#v, removed=%t, error=%v", queue, removed, err)
	}
	if err := promoter.Unlock(); err != nil {
		t.Fatal(err)
	}
	close(allowWait)
	<-lockAcquired
	cancel()
	close(allowAcquire)
	if err := <-result; !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "canceled queued app command") {
		t.Fatalf("cancellation result = %v", err)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.Runs) != 0 {
		t.Fatalf("queue after cancellation = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestAwaitLiveRunAdmissionV1ExplainsWaitBeforePolling(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", true)
	first.Kind = deploy.LiveRunKindShellV1
	first.Name = "shell"
	first.WritableMount = "config"
	first.WritablePaths = []string{"/conf", "/data"}
	holdLiveRunLeaseV1(t, operation, first.ID)
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var notice bytes.Buffer
	backend := liveRunAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(context.Context) error {
			cancel()
			return context.Canceled
		},
	}
	second := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	holdLiveRunLeaseV1(t, operation, second.ID)
	_, err = awaitLiveRunAdmissionV1(ctx, dir, operation, second, true, &notice, backend)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v", err)
	}
	want := "Waiting for active shell to finish (shared writable mounts: /conf, /data).\n" +
		"Ctrl-C cancels this wait without affecting the active command.\n"
	if notice.String() != want {
		t.Fatalf("wait notice = %q, want %q", notice.String(), want)
	}
}

func TestAwaitLiveRunAdmissionV1ExplainsReadyPredecessorWithoutCallingItActive(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", true)
	ready := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	holdLiveRunLeaseV1(t, operation, first.ID)
	holdLiveRunLeaseV1(t, operation, ready.ID)
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(ready, true); err != nil {
		t.Fatal(err)
	}
	queue, removed, err := operation.RemoveLiveRunV1(first.ID)
	if err != nil || !removed || len(queue.Runs) != 1 || queue.Runs[0].Status != deploy.LiveRunStatusReadyV1 {
		t.Fatalf("ready predecessor = %#v, removed=%t, error=%v", queue, removed, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var notice bytes.Buffer
	candidate := liveRunAdmissionFixtureV1("run-0000000000000003", false)
	holdLiveRunLeaseV1(t, operation, candidate.ID)
	_, err = awaitLiveRunAdmissionV1(ctx, dir, operation, candidate, true, &notice, liveRunAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(context.Context) error {
			cancel()
			return context.Canceled
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v", err)
	}
	want := "Waiting for queued app command \"export\" to start.\n" +
		"Ctrl-C cancels this wait without affecting earlier operations.\n"
	if notice.String() != want {
		t.Fatalf("wait notice = %q, want %q", notice.String(), want)
	}
}

func TestAwaitLiveRunAdmissionV1ExplainsReadyPredecessorBeforeCompatibleActiveRun(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	active := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	exclusiveWaiter := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	ready := liveRunAdmissionFixtureV1("run-0000000000000003", false)
	ready.Name = "generate"
	for _, run := range []deploy.LiveRunV1{active, exclusiveWaiter, ready} {
		holdLiveRunLeaseV1(t, operation, run.ID)
	}
	if _, err := operation.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(exclusiveWaiter, true); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(ready, true); err != nil {
		t.Fatal(err)
	}
	queue, removed, err := operation.RemoveLiveRunV1(exclusiveWaiter.ID)
	if err != nil || !removed || len(queue.Runs) != 2 || queue.Runs[1].Status != deploy.LiveRunStatusReadyV1 {
		t.Fatalf("ready predecessor with active run = %#v, removed=%t, error=%v", queue, removed, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var notice bytes.Buffer
	candidate := liveRunAdmissionFixtureV1("run-0000000000000004", false)
	holdLiveRunLeaseV1(t, operation, candidate.ID)
	_, err = awaitLiveRunAdmissionV1(ctx, dir, operation, candidate, true, &notice, liveRunAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(context.Context) error {
			cancel()
			return context.Canceled
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v", err)
	}
	want := "Waiting behind 2 operations; queued app command \"generate\" is next to start.\n" +
		"Ctrl-C cancels this wait without affecting earlier operations.\n"
	if notice.String() != want {
		t.Fatalf("wait notice = %q, want %q", notice.String(), want)
	}
}

func TestAwaitLiveRunAdmissionV1ReportsQueueDepthForMultipleWaiters(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", true)
	holdLiveRunLeaseV1(t, operation, first.ID)
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	second := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	holdLiveRunLeaseV1(t, operation, second.ID)
	if _, err := operation.AdmitLiveRunV1(second, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var notice bytes.Buffer
	third := liveRunAdmissionFixtureV1("run-0000000000000003", false)
	holdLiveRunLeaseV1(t, operation, third.ID)
	_, err = awaitLiveRunAdmissionV1(ctx, dir, operation, third, true, &notice, liveRunAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(context.Context) error {
			cancel()
			return context.Canceled
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v", err)
	}
	if !strings.HasPrefix(notice.String(), "Waiting behind 2 operations; active app command \"export\" is blocking this command.") {
		t.Fatalf("multi-waiter notice = %q", notice.String())
	}
}

func TestAwaitLiveRunAdmissionV1CancellationRemovesOnlyCaller(t *testing.T) {
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
	backend := liveRunAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait: func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		},
	}
	second := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	holdLiveRunLeaseV1(t, operation, second.ID)
	admitted, err := awaitLiveRunAdmissionV1(ctx, dir, operation, second, true, nil, backend)
	if admitted != nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "canceled queued app command \"export\" ("+second.ID+")") {
		t.Fatalf("canceled admission = %#v, %v", admitted, err)
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

func TestAwaitLiveRunAdmissionV1ImmediateConflictReleasesLockWithoutQueueChange(t *testing.T) {
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
	second := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	holdLiveRunLeaseV1(t, operation, second.ID)
	admitted, err := awaitLiveRunAdmissionV1(t.Context(), dir, operation, second, false, nil, liveRunAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait:    func(context.Context) error { return nil },
	})
	if admitted != nil || !errors.Is(err, deploy.ErrLiveRunConflict) {
		t.Fatalf("conflicting admission = %#v, %v", admitted, err)
	}
	if err := operation.RequireHeld(); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("conflicting admission retained lock: %v", err)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), filepath.Clean(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 1 || queue.Runs[0].ID != first.ID {
		t.Fatalf("queue after conflict = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestAwaitLiveRunAdmissionV1RejectsForeignOperationLock(t *testing.T) {
	operation, err := deploy.AcquireOperationLock(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	candidate := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	holdLiveRunLeaseV1(t, operation, candidate.ID)
	admitted, err := awaitLiveRunAdmissionV1(t.Context(), t.TempDir(), operation, candidate, false, nil, liveRunAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait:    func(context.Context) error { return nil },
	})
	if admitted != nil || err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign operation admission = %#v, %v", admitted, err)
	}
	if err := operation.RequireHeld(); err == nil {
		t.Fatal("foreign operation lock was not released")
	}
}

func TestAwaitLiveRunAdmissionV1RecoversAbandonedControlMarker(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitControlMarkerV1(controlAdmissionFixtureV1(
		"control-0000000000000001", deploy.ControlOperationRestartV1,
	), false); err != nil {
		t.Fatal(err)
	}
	candidate := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	holdLiveRunLeaseV1(t, operation, candidate.ID)
	admitted, err := awaitLiveRunAdmissionV1(t.Context(), dir, operation, candidate, false, nil, liveRunAdmissionBackendV1{
		acquire: deploy.AcquireOperationLock,
		wait:    func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	queue, _, err := admitted.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != candidate.ID || len(deploy.ControlMarkersV1(queue)) != 0 {
		t.Fatalf("queue after admission recovery = %#v, %v", queue, err)
	}
	if _, _, err := admitted.RemoveLiveRunV1(candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err := admitted.Unlock(); err != nil {
		t.Fatal(err)
	}
}
