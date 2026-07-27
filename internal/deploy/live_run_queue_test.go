package deploy

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func liveRunFixture(id string, exclusive bool) LiveRunV1 {
	return LiveRunV1{
		ID: id, Kind: LiveRunKindAppV1, Name: "export",
		GenerationReference: "reploy/env/demo:g-current", Exclusive: exclusive,
	}
}

func TestNewLiveRunIDV1UsesOpaqueRandomBytes(t *testing.T) {
	id, err := newLiveRunIDV1(bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 255}))
	if err != nil {
		t.Fatal(err)
	}
	if id != "run-00010203040506ff" {
		t.Fatalf("run ID = %q", id)
	}
	if _, err := newLiveRunIDV1(bytes.NewReader(nil)); err == nil {
		t.Fatal("short randomness was accepted")
	}
}

func TestNewControlMarkerIDV1UsesSeparateOpaqueNamespace(t *testing.T) {
	id, err := newControlMarkerIDV1(bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 255}))
	if err != nil {
		t.Fatal(err)
	}
	if id != "control-00010203040506ff" {
		t.Fatalf("control marker ID = %q", id)
	}
	if err := ValidateLiveRunIDV1(id); err == nil {
		t.Fatal("internal control marker was accepted as a public run ID")
	}
	if _, err := newControlMarkerIDV1(bytes.NewReader(nil)); err == nil {
		t.Fatal("short randomness was accepted")
	}
}

func TestAdmitLiveRunV1AllowsConcurrentRunsAndRejectsImmediateExclusiveConflict(t *testing.T) {
	queue := NewLiveRunQueueV1()
	first := liveRunFixture("run-0000000000000001", false)
	second := liveRunFixture("run-0000000000000002", false)
	var err error
	queue, status, err := AdmitLiveRunV1(queue, first, false)
	if err != nil || status != LiveRunStatusActiveV1 {
		t.Fatalf("first admission = %#v, %v", status, err)
	}
	queue, status, err = AdmitLiveRunV1(queue, second, false)
	if err != nil || status != LiveRunStatusActiveV1 || len(queue.Runs) != 2 {
		t.Fatalf("second admission = %#v, %#v, %v", queue, status, err)
	}
	before := queue
	_, status, err = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000003", true), false)
	if !errors.Is(err, ErrLiveRunConflict) || status != "" || !reflect.DeepEqual(queue, before) {
		t.Fatalf("exclusive conflict = %#v, %q, %v", queue, status, err)
	}
}

func TestAdmitLiveRunV1RejectsUnsafeDisplayName(t *testing.T) {
	bad := liveRunFixture("run-0000000000000001", false)
	bad.Name = "export\x1b[2J"
	if _, _, err := AdmitLiveRunV1(NewLiveRunQueueV1(), bad, false); err == nil || !strings.Contains(err.Error(), "safe text") {
		t.Fatalf("unsafe live run name error = %v", err)
	}
}

func TestLiveRunQueueV1PreservesFIFOAcrossExclusiveAndConcurrentWaiters(t *testing.T) {
	queue := NewLiveRunQueueV1()
	queue, _, _ = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000001", false), false)
	queue, status, err := AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000002", true), true)
	if err != nil || status != LiveRunStatusWaitingV1 {
		t.Fatalf("exclusive waiter = %q, %v", status, err)
	}
	queue, status, err = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000003", false), true)
	if err != nil || status != LiveRunStatusWaitingV1 {
		t.Fatalf("concurrent waiter = %q, %v", status, err)
	}
	queue, removed, err := RemoveLiveRunV1(queue, "run-0000000000000001")
	if err != nil || !removed || queue.Runs[0].ID != "run-0000000000000002" || queue.Runs[0].Status != LiveRunStatusActiveV1 || queue.Runs[1].Status != LiveRunStatusWaitingV1 {
		t.Fatalf("exclusive promotion = %#v, removed=%t, err=%v", queue, removed, err)
	}
	queue, removed, err = RemoveLiveRunV1(queue, "run-0000000000000002")
	if err != nil || !removed || len(queue.Runs) != 1 || queue.Runs[0].ID != "run-0000000000000003" || queue.Runs[0].Status != LiveRunStatusActiveV1 {
		t.Fatalf("concurrent promotion = %#v, removed=%t, err=%v", queue, removed, err)
	}
}

func TestLiveRunQueueV1PreservesFIFOAcrossHiddenControlMarker(t *testing.T) {
	queue := NewLiveRunQueueV1()
	queue, _, _ = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000001", false), false)
	marker := ControlMarkerV1{
		ID: "control-0000000000000001", Operation: ControlOperationInstallV1,
		GenerationReference: "reploy/env/demo:g-current",
	}
	queue, status, err := AdmitControlMarkerV1(queue, marker, true)
	if err != nil || status != LiveRunStatusWaitingV1 {
		t.Fatalf("control admission = %q, %v", status, err)
	}
	queue, status, err = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000002", false), true)
	if err != nil || status != LiveRunStatusWaitingV1 {
		t.Fatalf("run behind control = %q, %v", status, err)
	}
	queue, removed, err := RemoveLiveRunV1(queue, "run-0000000000000001")
	if err != nil || !removed || queue.Runs[0].Kind != LiveRunKindControlV1 || queue.Runs[0].Status != LiveRunStatusReadyV1 || queue.Runs[1].Status != LiveRunStatusWaitingV1 {
		t.Fatalf("control promotion = %#v, removed=%t, err=%v", queue, removed, err)
	}
	markers := ControlMarkersV1(queue)
	if len(markers) != 1 || markers[0].ID != marker.ID || markers[0].Operation != marker.Operation || markers[0].Status != LiveRunStatusReadyV1 {
		t.Fatalf("control markers = %#v", markers)
	}
	queue, removed, err = RemoveControlMarkerV1(queue, marker.ID)
	if err != nil || !removed || len(queue.Runs) != 1 || queue.Runs[0].ID != "run-0000000000000002" || queue.Runs[0].Status != LiveRunStatusActiveV1 {
		t.Fatalf("run promotion = %#v, removed=%t, err=%v", queue, removed, err)
	}
}

func TestControlMarkerV1RejectsImmediateConflictAndInvalidPublicShapes(t *testing.T) {
	queue := NewLiveRunQueueV1()
	queue, _, _ = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000001", false), false)
	marker := ControlMarkerV1{
		ID: "control-0000000000000001", Operation: ControlOperationStopV1,
		GenerationReference: "reploy/env/demo:g-current",
	}
	before := cloneLiveRunQueueV1(queue)
	if _, _, err := AdmitControlMarkerV1(queue, marker, false); !errors.Is(err, ErrLiveRunConflict) || !reflect.DeepEqual(queue, before) {
		t.Fatalf("immediate control conflict changed queue: %#v, %v", queue, err)
	}
	for _, candidate := range []ControlMarkerV1{
		{ID: "run-0000000000000002", Operation: ControlOperationStopV1, GenerationReference: "g-current"},
		{ID: "control-0000000000000002", Operation: "down", GenerationReference: "g-current"},
		{ID: "control-0000000000000003", Operation: ControlOperationStopV1},
	} {
		if _, _, err := AdmitControlMarkerV1(NewLiveRunQueueV1(), candidate, true); err == nil {
			t.Fatalf("invalid control marker accepted: %#v", candidate)
		}
	}
}

func TestCancelWaitingLiveRunsV1PreservesActiveRunsAndControlOrder(t *testing.T) {
	queue := NewLiveRunQueueV1()
	active := liveRunFixture("run-0000000000000001", false)
	firstWaiter := liveRunFixture("run-0000000000000002", true)
	laterWaiter := liveRunFixture("run-0000000000000003", false)
	queue, _, _ = AdmitLiveRunV1(queue, active, false)
	queue, _, _ = AdmitLiveRunV1(queue, firstWaiter, true)
	marker := ControlMarkerV1{
		ID: "control-0000000000000001", Operation: ControlOperationInstallV1,
		GenerationReference: active.GenerationReference,
	}
	queue, _, _ = AdmitControlMarkerV1(queue, marker, true)
	queue, _, _ = AdmitLiveRunV1(queue, laterWaiter, true)
	updated, canceled, err := CancelWaitingLiveRunsV1(queue)
	if err != nil {
		t.Fatal(err)
	}
	if len(canceled) != 2 || canceled[0].ID != firstWaiter.ID || canceled[1].ID != laterWaiter.ID {
		t.Fatalf("canceled runs = %#v", canceled)
	}
	if len(updated.Runs) != 2 || updated.Runs[0].ID != active.ID || updated.Runs[0].Status != LiveRunStatusActiveV1 || updated.Runs[1].ID != marker.ID || updated.Runs[1].Status != LiveRunStatusWaitingV1 {
		t.Fatalf("retained queue = %#v", updated)
	}
	updated, removed, err := RemoveLiveRunV1(updated, active.ID)
	if err != nil || !removed || updated.Runs[0].ID != marker.ID || updated.Runs[0].Status != LiveRunStatusReadyV1 {
		t.Fatalf("control promotion = %#v, removed=%t, error=%v", updated, removed, err)
	}
}

func TestRemoveLiveRunV1PromotesConcurrentBatchAndCancellationDoesNotLeaveGap(t *testing.T) {
	queue := NewLiveRunQueueV1()
	queue, _, _ = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000001", false), false)
	queue, _, _ = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000002", true), true)
	queue, _, _ = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000003", false), true)
	queue, _, _ = AdmitLiveRunV1(queue, liveRunFixture("run-0000000000000004", false), true)
	queue, removed, err := RemoveLiveRunV1(queue, "run-0000000000000002")
	if err != nil || !removed || len(queue.Runs) != 3 {
		t.Fatalf("cancel = %#v, removed=%t, err=%v", queue, removed, err)
	}
	for _, run := range queue.Runs {
		if run.Status != LiveRunStatusActiveV1 {
			t.Fatalf("run was not promoted: %#v", queue)
		}
	}
}

func TestRemoveLiveRunV1DistinguishesInvalidAndAbsentIDsWithoutHistory(t *testing.T) {
	queue := NewLiveRunQueueV1()
	if _, _, err := RemoveLiveRunV1(queue, "invalid"); err == nil || !strings.Contains(err.Error(), "run ID") {
		t.Fatalf("invalid ID error = %v", err)
	}
	result, found, err := RemoveLiveRunV1(queue, "run-0000000000000001")
	if err != nil || found || !reflect.DeepEqual(result, queue) {
		t.Fatalf("absent removal = %#v, found=%t, err=%v", result, found, err)
	}
}

func TestValidateLiveRunQueueV1RejectsNoncanonicalOrUnsafeState(t *testing.T) {
	valid := NewLiveRunQueueV1()
	valid.Runs = []LiveRunV1{{
		ID: "run-0000000000000001", Kind: LiveRunKindShellV1, Name: "shell",
		GenerationReference: "reploy/env/demo:g-current",
		Status:              LiveRunStatusActiveV1,
		Exclusive:           true,
		WritableMount:       "data",
		Container:           "reploy-demo-command-1",
	}}
	if err := ValidateLiveRunQueueV1(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*LiveRunQueueV1){
		func(value *LiveRunQueueV1) { value.Schema = "old" },
		func(value *LiveRunQueueV1) { value.Runs = nil },
		func(value *LiveRunQueueV1) { value.Runs = append(value.Runs, value.Runs[0]) },
		func(value *LiveRunQueueV1) { value.Runs[0].Status = "done" },
		func(value *LiveRunQueueV1) { value.Runs[0].Exclusive = false },
		func(value *LiveRunQueueV1) { value.Runs[0].GenerationReference = "" },
		func(value *LiveRunQueueV1) { value.Runs[0].Status = LiveRunStatusWaitingV1 },
	} {
		value := cloneLiveRunQueueV1(valid)
		mutate(&value)
		if err := ValidateLiveRunQueueV1(value); err == nil {
			t.Fatalf("invalid queue accepted: %#v", value)
		}
	}
}
