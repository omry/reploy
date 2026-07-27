package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOperationLockLiveRunQueueFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	queue, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || found || !reflect.DeepEqual(queue, NewLiveRunQueueV1()) {
		t.Fatalf("missing queue = %#v, found=%t, error=%v", queue, found, err)
	}
	queue.Runs = []LiveRunV1{{
		ID: "run-0000000000000001", Kind: LiveRunKindAppV1, Name: "export",
		GenerationReference: "reploy/env/demo:g-current",
		Status:              LiveRunStatusActiveV1,
		Exclusive:           true,
		WritableMount:       "output",
	}}
	if err := lock.CommitLiveRunQueueV1(queue); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", liveRunQueueFilenameV1)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o600 {
		t.Fatalf("live run queue mode = %v", info.Mode())
	}
	loaded, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, queue) {
		t.Fatalf("loaded queue = %#v, found=%t, error=%v", loaded, found, err)
	}
	if err := lock.CommitLiveRunQueueV1(NewLiveRunQueueV1()); err != nil {
		t.Fatal(err)
	}
	if err := lock.CommitLiveRunQueueV1(NewLiveRunQueueV1()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("live run queue remains: %v", err)
	}
}

func TestOperationLockLiveRunQueueReplaceFailurePreservesQueue(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	queue := NewLiveRunQueueV1()
	queue.Runs = []LiveRunV1{{
		ID: "run-0000000000000001", Kind: LiveRunKindShellV1, Name: "shell",
		GenerationReference: "reploy/env/demo:g-current",
		Status:              LiveRunStatusActiveV1,
	}}
	if err := lock.CommitLiveRunQueueV1(queue); err != nil {
		t.Fatal(err)
	}
	replacement := cloneLiveRunQueueV1(queue)
	replacement.Runs[0].Name = "replacement"
	originalReplace := replaceAtomicStateFile
	replaceAtomicStateFile = func(string, string) error { return errors.New("injected replace failure") }
	t.Cleanup(func() { replaceAtomicStateFile = originalReplace })
	if err := lock.CommitLiveRunQueueV1(replacement); err == nil || !strings.Contains(err.Error(), "injected replace failure") {
		t.Fatalf("error = %v", err)
	}
	loaded, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, queue) {
		t.Fatalf("failed replace changed queue: %#v, found=%t, error=%v", loaded, found, err)
	}
}

func TestOperationLockLiveRunQueueTransitionsAreAtomicAndPersistent(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	first := liveRunFixture("run-0000000000000001", false)
	status, err := lock.AdmitLiveRunV1(first, false)
	if err != nil || status != LiveRunStatusActiveV1 {
		t.Fatalf("first admission = %q, %v", status, err)
	}
	second := liveRunFixture("run-0000000000000002", true)
	status, err = lock.AdmitLiveRunV1(second, true)
	if err != nil || status != LiveRunStatusWaitingV1 {
		t.Fatalf("second admission = %q, %v", status, err)
	}
	before, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found {
		t.Fatalf("read before conflict = %#v, found=%t, error=%v", before, found, err)
	}
	if _, err := lock.AdmitLiveRunV1(liveRunFixture("run-0000000000000003", false), false); !errors.Is(err, ErrLiveRunConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	after, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found || !reflect.DeepEqual(after, before) {
		t.Fatalf("conflict changed queue = %#v, found=%t, error=%v", after, found, err)
	}
	if err := lock.RecordLiveRunContainerV1(first.ID, "reploy-demo-command-1"); err != nil {
		t.Fatal(err)
	}
	if err := lock.RecordLiveRunContainerV1(second.ID, "reploy-demo-command-2"); err == nil || !strings.Contains(err.Error(), "waiting") {
		t.Fatalf("waiting container error = %v", err)
	}
	updated, removed, err := lock.RemoveLiveRunV1(first.ID)
	if err != nil || !removed || len(updated.Runs) != 1 || updated.Runs[0].ID != second.ID || updated.Runs[0].Status != LiveRunStatusActiveV1 {
		t.Fatalf("remove and promote = %#v, removed=%t, error=%v", updated, removed, err)
	}
	loaded, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, updated) {
		t.Fatalf("promoted queue = %#v, found=%t, error=%v", loaded, found, err)
	}
	if _, removed, err := lock.RemoveLiveRunV1("run-0000000000000099"); err != nil || removed {
		t.Fatalf("absent removal = %t, %v", removed, err)
	}
	if _, removed, err := lock.RemoveLiveRunV1(second.ID); err != nil || !removed {
		t.Fatalf("final removal = %t, %v", removed, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".reploy", liveRunQueueFilenameV1)); !os.IsNotExist(err) {
		t.Fatalf("empty queue file remains: %v", err)
	}
}

func TestOperationLockPersistsHiddenControlMarkerTransitions(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	active := liveRunFixture("run-0000000000000001", false)
	if _, err := lock.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	marker := ControlMarkerV1{
		ID: "control-0000000000000001", Operation: ControlOperationRestartV1,
		GenerationReference: active.GenerationReference,
	}
	if status, err := lock.AdmitControlMarkerV1(marker, true); err != nil || status != LiveRunStatusWaitingV1 {
		t.Fatalf("control admission = %q, %v", status, err)
	}
	if _, removed, err := lock.RemoveLiveRunV1(active.ID); err != nil || !removed {
		t.Fatalf("active removal = %t, %v", removed, err)
	}
	queue, found, err := lock.ReadLiveRunQueueV1()
	markers := ControlMarkersV1(queue)
	if err != nil || !found || len(markers) != 1 || markers[0].Status != LiveRunStatusReadyV1 {
		t.Fatalf("persisted control = %#v, found=%t, error=%v", queue, found, err)
	}
	if _, removed, err := lock.RemoveControlMarkerV1(marker.ID); err != nil || !removed {
		t.Fatalf("control removal = %t, %v", removed, err)
	}
	if _, found, err := lock.ReadLiveRunQueueV1(); err != nil || found {
		t.Fatalf("empty queue persisted: found=%t, error=%v", found, err)
	}
}

func TestOperationLockAtomicallyCancelsOnlyWaitingLiveRuns(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	active := liveRunFixture("run-0000000000000001", false)
	waiting := liveRunFixture("run-0000000000000002", true)
	if _, err := lock.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AdmitLiveRunV1(waiting, true); err != nil {
		t.Fatal(err)
	}
	marker := ControlMarkerV1{
		ID: "control-0000000000000001", Operation: ControlOperationStopV1,
		GenerationReference: active.GenerationReference,
	}
	if _, err := lock.AdmitControlMarkerV1(marker, true); err != nil {
		t.Fatal(err)
	}
	updated, canceled, err := lock.CancelWaitingLiveRunsV1()
	if err != nil || len(canceled) != 1 || canceled[0].ID != waiting.ID || len(updated.Runs) != 2 {
		t.Fatalf("cancel transition = %#v, canceled=%#v, error=%v", updated, canceled, err)
	}
	loaded, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, updated) {
		t.Fatalf("persisted cancel transition = %#v, found=%t, error=%v", loaded, found, err)
	}
	before := cloneLiveRunQueueV1(loaded)
	unchanged, canceled, err := lock.CancelWaitingLiveRunsV1()
	if err != nil || len(canceled) != 0 || !reflect.DeepEqual(unchanged, before) {
		t.Fatalf("idempotent cancel = %#v, canceled=%#v, error=%v", unchanged, canceled, err)
	}
}

func TestOperationLockRecoversAbandonedActiveControlAndPromotesNextRun(t *testing.T) {
	dir := t.TempDir()
	owner, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	marker := ControlMarkerV1{
		ID: "control-0000000000000001", Operation: ControlOperationRestartV1,
		GenerationReference: "reploy/env/demo:g-current",
	}
	if status, err := owner.AdmitControlMarkerV1(marker, false); err != nil || status != LiveRunStatusActiveV1 {
		t.Fatalf("active marker = %q, %v", status, err)
	}
	waiter := liveRunFixture("run-0000000000000001", false)
	if status, err := owner.AdmitLiveRunV1(waiter, true); err != nil || status != LiveRunStatusWaitingV1 {
		t.Fatalf("waiter = %q, %v", status, err)
	}
	if err := owner.Unlock(); err != nil {
		t.Fatal(err)
	}

	recovery, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Unlock()
	recovered, found, err := recovery.RecoverAbandonedControlMarkerV1()
	if err != nil || !found || recovered.ID != marker.ID {
		t.Fatalf("recovered marker = %#v, found=%t, error=%v", recovered, found, err)
	}
	queue, found, err := recovery.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 1 || queue.Runs[0].ID != waiter.ID || queue.Runs[0].Status != LiveRunStatusActiveV1 {
		t.Fatalf("queue after recovery = %#v, found=%t, error=%v", queue, found, err)
	}
	if _, found, err := recovery.RecoverAbandonedControlMarkerV1(); err != nil || found {
		t.Fatalf("idempotent recovery found=%t error=%v", found, err)
	}
}

func TestOperationLockKeepsOwnedControlAndRecoversAbandonedReadyControl(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	active := liveRunFixture("run-0000000000000001", false)
	if _, err := operation.AdmitLiveRunV1(active, false); err != nil {
		t.Fatal(err)
	}
	marker := ControlMarkerV1{
		ID: "control-0000000000000001", Operation: ControlOperationRestartV1,
		GenerationReference: active.GenerationReference,
	}
	lease, err := operation.AcquireControlLeaseV1(marker.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if status, err := operation.AdmitControlMarkerV1(marker, true); err != nil || status != LiveRunStatusWaitingV1 {
		t.Fatalf("control admission = %q, %v", status, err)
	}
	if _, found, err := operation.RecoverAbandonedControlMarkerV1(); err != nil || found {
		t.Fatalf("owned waiting control recovered: found=%t error=%v", found, err)
	}
	if _, removed, err := operation.RemoveLiveRunV1(active.ID); err != nil || !removed {
		t.Fatalf("active removal = %t, %v", removed, err)
	}
	if _, found, err := operation.RecoverAbandonedControlMarkerV1(); err != nil || found {
		t.Fatalf("owned ready control recovered: found=%t error=%v", found, err)
	}
	if err := lease.abandonForTest(); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := operation.RecoverAbandonedControlMarkerV1()
	if err != nil || !found || recovered.ID != marker.ID || recovered.Status != LiveRunStatusReadyV1 {
		t.Fatalf("abandoned ready control recovery = %#v, found=%t error=%v", recovered, found, err)
	}
	if _, found, err := operation.ReadLiveRunQueueV1(); err != nil || found {
		t.Fatalf("recovered queue remains: found=%t error=%v", found, err)
	}
}

func (lease *ControlLeaseV1) abandonForTest() error {
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	if lease.released {
		return nil
	}
	lease.released = true
	if err := unlockOperationFile(lease.file); err != nil {
		return err
	}
	return lease.file.Close()
}

func TestOperationLockLiveRunQueueRejectsInvalidFilesAndUseAfterUnlock(t *testing.T) {
	for name, content := range map[string]string{
		"unknown field": `{"runs":[],"schema":"live-run-queue-v1","unknown":true}`,
		"noncanonical":  `{"schema":"live-run-queue-v1","runs":[]}`,
		"trailing JSON": `{"runs":[],"schema":"live-run-queue-v1"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			lock, err := AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Unlock()
			path := filepath.Join(dir, ".reploy", liveRunQueueFilenameV1)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := lock.ReadLiveRunQueueV1(); err == nil {
				t.Fatal("invalid queue file was accepted")
			}
			preserved, err := os.ReadFile(path)
			if err != nil || string(preserved) != content {
				t.Fatalf("invalid queue file changed: %q, %v", preserved, err)
			}
		})
	}

	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", liveRunQueueFilenameV1)
	if err := os.Symlink(filepath.Join(dir, "outside"), path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lock.ReadLiveRunQueueV1(); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink read error = %v", err)
	}
	if err := lock.CommitLiveRunQueueV1(NewLiveRunQueueV1()); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink remove error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lock.ReadLiveRunQueueV1(); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("read after unlock error = %v", err)
	}
	if err := lock.CommitLiveRunQueueV1(NewLiveRunQueueV1()); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("commit after unlock error = %v", err)
	}
}
