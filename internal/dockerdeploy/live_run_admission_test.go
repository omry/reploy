package dockerdeploy

import (
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

func TestAwaitLiveRunAdmissionV1ReturnsWithLockHeldAfterFairPromotion(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", false)
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
	type admissionResult struct {
		operation *deploy.OperationLock
		err       error
	}
	resultChannel := make(chan admissionResult, 1)
	go func() {
		admitted, err := awaitLiveRunAdmissionV1(t.Context(), dir, operation, second, true, backend)
		resultChannel <- admissionResult{operation: admitted, err: err}
	}()
	<-waitStarted
	promoter, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	queue, removed, err := promoter.RemoveLiveRunV1(first.ID)
	if err != nil || !removed || len(queue.Runs) != 1 || queue.Runs[0].ID != second.ID || queue.Runs[0].Status != deploy.LiveRunStatusActiveV1 {
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
	if _, removed, err := got.operation.RemoveLiveRunV1(second.ID); err != nil || !removed {
		t.Fatalf("remove admitted run = %t, %v", removed, err)
	}
	if err := got.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestAwaitLiveRunAdmissionV1CancellationRemovesOnlyCaller(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", false)
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
	admitted, err := awaitLiveRunAdmissionV1(ctx, dir, operation, second, true, backend)
	if admitted != nil || !errors.Is(err, context.Canceled) {
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
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	second := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	admitted, err := awaitLiveRunAdmissionV1(t.Context(), dir, operation, second, false, liveRunAdmissionBackendV1{
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
	admitted, err := awaitLiveRunAdmissionV1(t.Context(), t.TempDir(), operation, candidate, false, liveRunAdmissionBackendV1{
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
	admitted, err := awaitLiveRunAdmissionV1(t.Context(), dir, operation, candidate, false, liveRunAdmissionBackendV1{
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
