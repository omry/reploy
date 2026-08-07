package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestListLiveRunsV1ReturnsOnlyPersistedOutstandingRuns(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	active := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	waiting := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	holdLiveRunLeaseV1(t, operation, active.ID)
	holdLiveRunLeaseV1(t, operation, waiting.ID)
	if _, err := operation.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(waiting, true); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitControlMarkerV1(deploy.ControlMarkerV1{
		ID: "control-0000000000000001", Operation: deploy.ControlOperationInstallV1,
		GenerationReference: active.GenerationReference,
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	runs, err := ListLiveRunsV1(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != active.ID || runs[0].Status != deploy.LiveRunStatusActiveV1 || runs[1].ID != waiting.ID || runs[1].Status != deploy.LiveRunStatusWaitingV1 {
		t.Fatalf("runs = %#v", runs)
	}
	runs[0].Name = "mutated"
	again, err := ListLiveRunsV1(t.Context(), dir)
	if err != nil || again[0].Name == "mutated" {
		t.Fatalf("list exposed queue storage: %#v, %v", again, err)
	}
}

func TestStopLiveRunV1CancelsWaiterWithoutDockerAndPromotesFairly(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	active := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	firstWaiter := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	secondWaiter := liveRunAdmissionFixtureV1("run-0000000000000003", false)
	holdLiveRunLeaseV1(t, operation, active.ID)
	holdLiveRunLeaseV1(t, operation, firstWaiter.ID)
	holdLiveRunLeaseV1(t, operation, secondWaiter.ID)
	for _, admission := range []struct {
		run  deploy.LiveRunV1
		wait bool
	}{{active, false}, {firstWaiter, true}, {secondWaiter, true}} {
		if _, err := operation.AdmitLiveRunV1(admission.run, admission.wait); err != nil {
			t.Fatal(err)
		}
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	dockerCalls := 0
	result, err := stopLiveRunV1(t.Context(), dir, firstWaiter.ID, 0, liveRunsBackendV1{
		acquire: deploy.AcquireOperationLock,
		removeContainer: func(CommandSpec, RunOptions) error {
			dockerCalls++
			return nil
		},
	})
	if err != nil || !result.Found || result.Run.Status != deploy.LiveRunStatusWaitingV1 || dockerCalls != 0 {
		t.Fatalf("waiter stop = %#v, calls=%d, error=%v", result, dockerCalls, err)
	}
	runs, err := ListLiveRunsV1(t.Context(), dir)
	if err != nil || len(runs) != 2 || runs[0].ID != active.ID || runs[1].ID != secondWaiter.ID || runs[1].Status != deploy.LiveRunStatusWaitingV1 {
		t.Fatalf("queue after waiter stop = %#v, %v", runs, err)
	}
}

func TestStopLiveRunV1RemovesActiveContainerBeforePromotingWaiter(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	active := liveRunAdmissionFixtureV1("run-0000000000000001", true)
	waiter := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	holdLiveRunLeaseV1(t, operation, active.ID)
	holdLiveRunLeaseV1(t, operation, waiter.ID)
	if _, err := operation.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	if err := operation.RecordLiveRunContainerV1(active.ID, "demo-run-0000000000000001"); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(waiter, true); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	result, err := stopLiveRunV1(t.Context(), dir, active.ID, 7*time.Second, liveRunsBackendV1{
		acquire: deploy.AcquireOperationLock,
		removeContainer: func(spec CommandSpec, options RunOptions) error {
			order = append(order, "docker")
			want := TemporaryContainerCleanupCommand("demo-run-0000000000000001")
			if !reflect.DeepEqual(spec, want) || options.DockerPreflightTimeout != 7*time.Second {
				t.Fatalf("container removal = %#v, %#v", spec, options)
			}
			return nil
		},
	})
	if err != nil || !result.Found || result.Run.Container == "" || !reflect.DeepEqual(order, []string{"docker"}) {
		t.Fatalf("active stop = %#v, order=%v, error=%v", result, order, err)
	}
	runs, err := ListLiveRunsV1(t.Context(), dir)
	if err != nil || len(runs) != 1 || runs[0].ID != waiter.ID || runs[0].Status != deploy.LiveRunStatusWaitingV1 {
		t.Fatalf("promoted waiter = %#v, %v", runs, err)
	}
}

func TestStopLiveRunV1ReportsReadyReservationAsWaitingWithoutDocker(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	active := liveRunAdmissionFixtureV1("run-0000000000000001", true)
	ready := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	holdLiveRunLeaseV1(t, operation, active.ID)
	holdLiveRunLeaseV1(t, operation, ready.ID)
	if _, err := operation.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(ready, true); err != nil {
		t.Fatal(err)
	}
	queue, removed, err := operation.RemoveLiveRunV1(active.ID)
	if err != nil || !removed || len(queue.Runs) != 1 || queue.Runs[0].Status != deploy.LiveRunStatusReadyV1 {
		t.Fatalf("ready reservation = %#v, removed=%t, error=%v", queue, removed, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	dockerCalls := 0
	result, err := stopLiveRunV1(t.Context(), dir, ready.ID, 0, liveRunsBackendV1{
		acquire: deploy.AcquireOperationLock,
		removeContainer: func(CommandSpec, RunOptions) error {
			dockerCalls++
			return nil
		},
	})
	if err != nil || !result.Found || result.Run.Status != deploy.LiveRunStatusWaitingV1 || dockerCalls != 0 {
		t.Fatalf("ready stop = %#v, calls=%d, error=%v", result, dockerCalls, err)
	}
	runs, err := ListLiveRunsV1(t.Context(), dir)
	if err != nil || len(runs) != 0 {
		t.Fatalf("queue after ready stop = %#v, error=%v", runs, err)
	}
}

func TestStopLiveRunV1IsIdempotentForAbsentRunAndPreservesQueueOnDockerFailure(t *testing.T) {
	dir := t.TempDir()
	result, err := StopLiveRunV1(t.Context(), dir, "run-0000000000000099", 0)
	if err != nil || result.Found {
		t.Fatalf("absent stop = %#v, %v", result, err)
	}
	if _, err := StopLiveRunV1(t.Context(), dir, "invalid", 0); err == nil || !strings.Contains(err.Error(), "run ID") {
		t.Fatalf("invalid ID error = %v", err)
	}

	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	active := liveRunAdmissionFixtureV1("run-0000000000000001", true)
	holdLiveRunLeaseV1(t, operation, active.ID)
	if _, err := operation.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	if err := operation.RecordLiveRunContainerV1(active.ID, "demo-run-0000000000000001"); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	want := errors.New("daemon denied removal")
	result, err = stopLiveRunV1(context.Background(), dir, active.ID, 0, liveRunsBackendV1{
		acquire:         deploy.AcquireOperationLock,
		removeContainer: func(CommandSpec, RunOptions) error { return want },
	})
	if !errors.Is(err, want) || !result.Found {
		t.Fatalf("failed active stop = %#v, %v", result, err)
	}
	runs, listErr := ListLiveRunsV1(t.Context(), dir)
	if listErr != nil || len(runs) != 1 || runs[0].ID != active.ID {
		t.Fatalf("failed stop changed queue = %#v, %v", runs, listErr)
	}
}

func TestStopLiveRunV1PreservesAutomaticRecoveryReport(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	abandoned := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	target := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	targetLease := holdLiveRunLeaseV1(t, operation, target.ID)
	if targetLease == nil {
		t.Fatal("target lease is missing")
	}
	if _, err := operation.AdmitLiveRunV1(abandoned, false); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(target, false); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	result, err := StopLiveRunV1(t.Context(), dir, target.ID, 0)
	if err != nil || !result.Found || result.Run.ID != target.ID ||
		len(result.Recovery.Removed) != 1 || result.Recovery.Removed[0].Run.ID != abandoned.ID {
		t.Fatalf("stop recovery result = %#v, %v", result, err)
	}
}
