package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestAdmitControlOperationV1DrainCancelsWaitersBeforeQueuingMarker(t *testing.T) {
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
	pauseCalls := 0
	result, err := admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationInstallV1, GenerationReference: active.GenerationReference,
		Mode: ControlAdmissionDrainV1,
	}, controlOperationAdmissionBackendV1{
		newID: func() (string, error) { return "control-0000000000000001", nil },
		pause: func(context.Context, time.Duration) error { pauseCalls++; return nil },
		await: func(_ context.Context, _ string, held *deploy.OperationLock, marker deploy.ControlMarkerV1, wait bool, _ io.Writer) (*deploy.OperationLock, error) {
			queue, _, err := held.ReadLiveRunQueueV1()
			if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != active.ID || !wait {
				t.Fatalf("queue before drain marker = %#v, wait=%t, error=%v", queue, wait, err)
			}
			status, err := held.AdmitControlMarkerV1(marker, true)
			if err != nil || status != deploy.LiveRunStatusWaitingV1 {
				t.Fatalf("drain marker admission = %q, %v", status, err)
			}
			return held, nil
		},
		removeContainer: func(CommandSpec, RunOptions) error { return nil },
	})
	if err != nil || len(result.CanceledRuns) != 1 || result.CanceledRuns[0].ID != waiting.ID || len(result.StoppedRuns) != 0 {
		t.Fatalf("drain result = %#v, %v", result, err)
	}
	if pauseCalls != 0 {
		t.Fatalf("graceful admission paused %d times", pauseCalls)
	}
	queue, _, err := result.Operation.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 2 || queue.Runs[1].Kind != deploy.LiveRunKindControlV1 {
		t.Fatalf("drain queue = %#v, %v", queue, err)
	}
	if _, removed, err := result.Operation.RemoveControlMarkerV1(result.Marker.ID); err != nil || !removed {
		t.Fatalf("remove test marker = %t, %v", removed, err)
	}
	if err := result.Lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := result.Operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestAdmitControlOperationV1ForceStopsActiveContainersBeforeMarker(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	second := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	waiting := liveRunAdmissionFixtureV1("run-0000000000000003", true)
	for _, run := range []deploy.LiveRunV1{first, second} {
		holdLiveRunLeaseV1(t, operation, run.ID)
		if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
			t.Fatal(err)
		}
		if err := operation.RecordLiveRunContainerV1(run.ID, "demo-"+run.ID); err != nil {
			t.Fatal(err)
		}
	}
	holdLiveRunLeaseV1(t, operation, waiting.ID)
	if _, err := operation.AdmitLiveRunV1(waiting, true); err != nil {
		t.Fatal(err)
	}
	calls := []CommandSpec{}
	var notice bytes.Buffer
	pauseCalls := 0
	result, err := admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationRestartV1, GenerationReference: first.GenerationReference,
		Mode: ControlAdmissionForceV1, DockerPreflightTimeout: 8 * time.Second, Notice: &notice,
	}, controlOperationAdmissionBackendV1{
		newID: func() (string, error) { return "control-0000000000000001", nil },
		pause: func(_ context.Context, delay time.Duration) error {
			pauseCalls++
			if delay != 3*time.Second {
				t.Fatalf("disruption delay = %s", delay)
			}
			return nil
		},
		await: AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(spec CommandSpec, options RunOptions) error {
			if options.DockerPreflightTimeout != 8*time.Second {
				t.Fatalf("Docker timeout = %s", options.DockerPreflightTimeout)
			}
			calls = append(calls, spec)
			return nil
		},
	})
	if err != nil || len(result.CanceledRuns) != 1 || result.CanceledRuns[0].ID != waiting.ID || len(result.StoppedRuns) != 2 {
		t.Fatalf("force result = %#v, %v", result, err)
	}
	wantCalls := []CommandSpec{
		TemporaryContainerStopCommand("demo-" + first.ID),
		TemporaryContainerStopCommand("demo-" + second.ID),
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("force container calls = %#v", calls)
	}
	for _, want := range []string{"restart will stop 2 active jobs", "cancel 1 waiting job", "3 seconds", "Ctrl-C", "use `--wait` to let active jobs finish"} {
		if !strings.Contains(notice.String(), want) {
			t.Fatalf("disruption notice missing %q: %q", want, notice.String())
		}
	}
	if strings.Contains(notice.String(), "reploy restart --wait") {
		t.Fatalf("disruption notice must not prescribe a deployment-ambiguous command: %q", notice.String())
	}
	if pauseCalls != 1 {
		t.Fatalf("disruption pause calls = %d", pauseCalls)
	}
	queue, _, err := result.Operation.ReadLiveRunQueueV1()
	markers := deploy.ControlMarkersV1(queue)
	if err != nil || len(queue.Runs) != 1 || len(markers) != 1 || markers[0].Status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("force queue = %#v, markers=%#v, error=%v", queue, markers, err)
	}
	if err := CompleteControlAdmissionV1(result.Operation, result.Marker.ID, result.Lease); err != nil {
		t.Fatal(err)
	}
}

func TestAdmitControlOperationV1ForceStopsControlledSessionContainersAndRetainsOwnership(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	dir := plan.Workload.DeploymentDirectory
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	run := liveRunAdmissionFixtureV1(plan.LiveRunID, false)
	run.Kind = deploy.LiveRunKindShellV1
	run.GenerationReference = plan.Workload.GenerationReference
	holdLiveRunLeaseV1(t, operation, run.ID)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	ownership, err := operation.RecordControlledSessionOwnershipV1(controlledSessionOwnershipFromPlanV1(
		plan, dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1,
	))
	if err != nil {
		t.Fatal(err)
	}

	calls := []CommandSpec{}
	result, err := admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationStopV1, GenerationReference: run.GenerationReference,
		Mode: ControlAdmissionForceV1,
	}, controlOperationAdmissionBackendV1{
		newID: func() (string, error) { return "control-0000000000000001", nil },
		pause: func(context.Context, time.Duration) error { return nil },
		await: AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(spec CommandSpec, _ RunOptions) error {
			calls = append(calls, spec)
			return nil
		},
	})
	if err != nil || len(result.StoppedRuns) != 1 || result.StoppedRuns[0].ID != run.ID {
		t.Fatalf("controlled-session force result = %#v, %v", result, err)
	}
	wantCalls := []CommandSpec{
		TemporaryContainerStopCommand(dockerWorkloadTestContainerIDV1),
		TemporaryContainerStopCommand(dockerControllerTestContainerIDV1),
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("controlled-session force calls = %#v", calls)
	}
	queue, _, err := result.Operation.ReadLiveRunQueueV1()
	if err != nil || len(queue.ControlledSessions) != 1 || queue.ControlledSessions[0] != ownership {
		t.Fatalf("retained controlled-session ownership = %#v, error=%v", queue.ControlledSessions, err)
	}
	if len(queue.Runs) != 1 || queue.Runs[0].Kind != deploy.LiveRunKindControlV1 {
		t.Fatalf("controlled-session force queue = %#v", queue)
	}
	if err := CompleteControlAdmissionV1(result.Operation, result.Marker.ID, result.Lease); err != nil {
		t.Fatal(err)
	}
}

func TestAdmitControlOperationV1ForcePreservesControlledSessionOnPartialStopFailure(t *testing.T) {
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	dir := plan.Workload.DeploymentDirectory
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	run := liveRunAdmissionFixtureV1(plan.LiveRunID, false)
	run.Kind = deploy.LiveRunKindShellV1
	run.GenerationReference = plan.Workload.GenerationReference
	holdLiveRunLeaseV1(t, operation, run.ID)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	ownership, err := operation.RecordControlledSessionOwnershipV1(controlledSessionOwnershipFromPlanV1(
		plan, dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1,
	))
	if err != nil {
		t.Fatal(err)
	}

	want := errors.New("controller stop failed")
	calls := []CommandSpec{}
	result, err := admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationStopV1, GenerationReference: run.GenerationReference,
		Mode: ControlAdmissionForceV1,
	}, controlOperationAdmissionBackendV1{
		newID: func() (string, error) { return "control-0000000000000001", nil },
		pause: func(context.Context, time.Duration) error { return nil },
		await: AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(spec CommandSpec, _ RunOptions) error {
			calls = append(calls, spec)
			if len(calls) == 2 {
				return want
			}
			return nil
		},
	})
	if !errors.Is(err, want) || len(result.StoppedRuns) != 0 {
		t.Fatalf("partial controlled-session stop result = %#v, %v", result, err)
	}
	wantCalls := []CommandSpec{
		TemporaryContainerStopCommand(dockerWorkloadTestContainerIDV1),
		TemporaryContainerStopCommand(dockerControllerTestContainerIDV1),
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("partial controlled-session stop calls = %#v", calls)
	}
	check, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Unlock()
	queue, _, err := check.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != run.ID ||
		len(queue.ControlledSessions) != 1 || queue.ControlledSessions[0] != ownership ||
		len(deploy.ControlMarkersV1(queue)) != 0 {
		t.Fatalf("queue after partial controlled-session stop = %#v, error=%v", queue, err)
	}
}

func TestAdmitControlOperationV1ForceFailurePreservesFailedAndLaterActiveRuns(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	second := liveRunAdmissionFixtureV1("run-0000000000000002", false)
	for _, run := range []deploy.LiveRunV1{first, second} {
		holdLiveRunLeaseV1(t, operation, run.ID)
		if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
			t.Fatal(err)
		}
		if err := operation.RecordLiveRunContainerV1(run.ID, "demo-"+run.ID); err != nil {
			t.Fatal(err)
		}
	}
	want := errors.New("daemon refused removal")
	calls := 0
	result, err := admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationStopV1, GenerationReference: first.GenerationReference,
		Mode: ControlAdmissionForceV1,
	}, controlOperationAdmissionBackendV1{
		newID: func() (string, error) { return "control-0000000000000001", nil },
		pause: func(context.Context, time.Duration) error { return nil },
		await: AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(CommandSpec, RunOptions) error {
			calls++
			if calls == 2 {
				return want
			}
			return nil
		},
	})
	if !errors.Is(err, want) || len(result.StoppedRuns) != 1 {
		t.Fatalf("failed force result = %#v, %v", result, err)
	}
	if err := operation.RequireHeld(); err == nil {
		t.Fatal("failed force retained operation lock")
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, _, err := inspection.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != second.ID || len(deploy.ControlMarkersV1(queue)) != 0 {
		t.Fatalf("queue after failed force = %#v, %v", queue, err)
	}
}

func TestAdmitControlOperationV1RejectsInvalidModeBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationUpV1, GenerationReference: "g-current", Mode: "later",
	}, controlOperationAdmissionBackendV1{
		newID:           func() (string, error) { return "control-0000000000000001", nil },
		pause:           func(context.Context, time.Duration) error { return nil },
		await:           AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(CommandSpec, RunOptions) error { return nil },
	})
	if err == nil {
		t.Fatal("invalid mode was accepted")
	}
	if heldErr := operation.RequireHeld(); heldErr == nil {
		t.Fatal("invalid mode retained operation lock")
	}
}

func TestAdmitControlOperationV1RejectsInvalidMarkerBeforeForceMutation(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	active := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	holdLiveRunLeaseV1(t, operation, active.ID)
	if _, err := operation.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	dockerCalls := 0
	_, err = admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: "remove-everything", GenerationReference: active.GenerationReference,
		Mode: ControlAdmissionForceV1,
	}, controlOperationAdmissionBackendV1{
		newID: func() (string, error) { return "control-0000000000000001", nil },
		pause: func(context.Context, time.Duration) error { return nil },
		await: AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(CommandSpec, RunOptions) error {
			dockerCalls++
			return nil
		},
	})
	if err == nil || dockerCalls != 0 {
		t.Fatalf("invalid marker error=%v Docker calls=%d", err, dockerCalls)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, _, err := inspection.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != active.ID {
		t.Fatalf("invalid marker changed queue = %#v, %v", queue, err)
	}
}

func TestAdmitControlOperationV1InterruptedPauseChangesNothing(t *testing.T) {
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
	if err := operation.RecordLiveRunContainerV1(active.ID, "demo-"+active.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(waiting, true); err != nil {
		t.Fatal(err)
	}
	want := errors.New("interrupted")
	dockerCalls := 0
	_, err = admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationStopV1, GenerationReference: active.GenerationReference,
		Mode: ControlAdmissionForceV1, Notice: &bytes.Buffer{},
	}, controlOperationAdmissionBackendV1{
		newID: func() (string, error) { return "control-0000000000000001", nil },
		pause: func(context.Context, time.Duration) error { return want },
		await: AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(CommandSpec, RunOptions) error {
			dockerCalls++
			return nil
		},
	})
	if !errors.Is(err, want) || dockerCalls != 0 {
		t.Fatalf("interrupted admission error=%v Docker calls=%d", err, dockerCalls)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, _, err := inspection.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 2 || queue.Runs[0].ID != active.ID || queue.Runs[1].ID != waiting.ID {
		t.Fatalf("queue after interrupted warning = %#v, %v", queue, err)
	}
}

func TestAdmitControlOperationV1ForceDoesNotOvertakeLifecycleOperation(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	marker := deploy.ControlMarkerV1{
		ID: "control-0000000000000001", Operation: deploy.ControlOperationInstallV1,
		GenerationReference: "g-current",
	}
	lease, err := operation.AcquireControlLeaseV1(marker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitControlMarkerV1(marker, false); err != nil {
		t.Fatal(err)
	}
	pauseCalls := 0
	_, err = admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationRestartV1, GenerationReference: "g-current", Mode: ControlAdmissionForceV1,
	}, controlOperationAdmissionBackendV1{
		newID:           func() (string, error) { return "control-0000000000000002", nil },
		pause:           func(context.Context, time.Duration) error { pauseCalls++; return nil },
		await:           AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(CommandSpec, RunOptions) error { return nil },
	})
	if !errors.Is(err, deploy.ErrLiveRunConflict) || pauseCalls != 0 {
		t.Fatalf("lifecycle conflict error=%v pause calls=%d", err, pauseCalls)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	queue, _, readErr := inspection.ReadLiveRunQueueV1()
	if readErr != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != marker.ID {
		t.Fatalf("lifecycle conflict queue = %#v, %v", queue, readErr)
	}
	if _, removed, removeErr := inspection.RemoveControlMarkerV1(marker.ID); removeErr != nil || !removed {
		t.Fatalf("remove lifecycle marker = %t, %v", removed, removeErr)
	}
	if err := inspection.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAdmitControlOperationV1DoesNotPauseWhenNoJobsAreAffected(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	pauseCalls := 0
	var notice bytes.Buffer
	result, err := admitControlOperationV1(t.Context(), dir, operation, ControlAdmissionInputV1{
		Operation: deploy.ControlOperationRestartV1, GenerationReference: "g-current",
		Mode: ControlAdmissionForceV1, Notice: &notice,
	}, controlOperationAdmissionBackendV1{
		newID:           func() (string, error) { return "control-0000000000000001", nil },
		pause:           func(context.Context, time.Duration) error { pauseCalls++; return nil },
		await:           AwaitControlAdmissionWithNoticeV1,
		removeContainer: func(CommandSpec, RunOptions) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if pauseCalls != 0 || notice.Len() != 0 {
		t.Fatalf("empty disruption pause=%d notice=%q", pauseCalls, notice.String())
	}
	if err := CompleteControlAdmissionV1(result.Operation, result.Marker.ID, result.Lease); err != nil {
		t.Fatal(err)
	}
}
