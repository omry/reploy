package deploy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestOperationLockRecordsExactControlledSessionOwnership(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	const runID = "run-0000000000000001"
	const generation = "reploy/env/workload:g-current"
	status, err := lock.AdmitLiveRunV1(LiveRunV1{
		ID: runID, Kind: LiveRunKindShellV1, Name: "controlled-session",
		GenerationReference: generation, Exclusive: true,
	}, false)
	if err != nil || status != LiveRunStatusActiveV1 {
		t.Fatalf("admission = %q, %v", status, err)
	}
	ownership := controlledSessionOwnershipFixtureV1(dir, runID, generation)
	planned := ownership
	planned.Controller.ID = ""
	planned.Workload.ID = ""
	missingEndpoint := planned
	missingEndpoint.DockerEndpoint = ""
	if _, err := lock.RecordControlledSessionOwnershipV1(missingEndpoint); err == nil || !strings.Contains(err.Error(), "Docker endpoint must be recorded") {
		t.Fatalf("missing Docker endpoint error = %v", err)
	}
	recorded, err := lock.RecordControlledSessionOwnershipV1(planned)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.BootSession == "" || planned.BootSession != "" || recorded.Controller.ID != "" || recorded.Workload.ID != "" {
		t.Fatalf("boot identity = recorded %q, input %q", recorded.BootSession, ownership.BootSession)
	}
	if err := validateControlledSessionContainerOwnershipV1(recorded.Controller, "controller"); err == nil || !strings.Contains(err.Error(), "container ID") {
		t.Fatalf("complete container validation accepted planned ownership: %v", err)
	}
	workloadFirst := planned
	workloadFirst.Workload.ID = ownership.Workload.ID
	if _, err := lock.RecordControlledSessionOwnershipV1(workloadFirst); err == nil || !strings.Contains(err.Error(), "before the controller") {
		t.Fatalf("workload-first ownership error = %v", err)
	}
	controllerPrepared := ownership
	controllerPrepared.Workload.ID = ""
	recorded, err = lock.RecordControlledSessionOwnershipV1(controllerPrepared)
	if err != nil || recorded.Controller.ID != ownership.Controller.ID || recorded.Workload.ID != "" {
		t.Fatalf("controller ownership = %#v, error=%v", recorded, err)
	}
	recorded, err = lock.RecordControlledSessionOwnershipV1(ownership)
	if err != nil || recorded.Controller.ID != ownership.Controller.ID || recorded.Workload.ID != ownership.Workload.ID {
		t.Fatalf("complete ownership = %#v, error=%v", recorded, err)
	}
	loaded, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found || len(loaded.ControlledSessions) != 1 || loaded.ControlledSessions[0] != recorded {
		t.Fatalf("controlled-session ownership = %#v, found=%t, error=%v", loaded.ControlledSessions, found, err)
	}
	conflict := ownership
	conflict.Controller.ID = strings.Repeat("c", 64)
	if _, err := lock.RecordControlledSessionOwnershipV1(conflict); err == nil || !strings.Contains(err.Error(), "different controlled-session ownership") {
		t.Fatalf("conflicting ownership error = %v", err)
	}
	if completed, err := lock.CompleteControlledSessionV1(runID); err != nil || !completed {
		t.Fatalf("completion = %t, %v", completed, err)
	}
	if _, found, err := lock.ReadLiveRunQueueV1(); err != nil || found {
		t.Fatalf("completed queue found=%t, error=%v", found, err)
	}
}

func TestOperationLockRecordsControlledSessionNetworkBeforeContainers(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	const runID = "run-0000000000000001"
	const generation = "reploy/env/workload:g-current"
	if _, err := lock.AdmitLiveRunV1(LiveRunV1{
		ID: runID, Kind: LiveRunKindShellV1, Name: "controlled-session",
		GenerationReference: generation, Exclusive: true,
	}, false); err != nil {
		t.Fatal(err)
	}
	complete := controlledSessionOwnershipFixtureV1(dir, runID, generation)
	complete.NetworkName = "reploy-session-" + runID
	complete.NetworkID = strings.Repeat("d", 64)
	planned := complete
	planned.NetworkID = ""
	planned.Controller.ID = ""
	planned.Workload.ID = ""
	if _, err := lock.RecordControlledSessionOwnershipV1(planned); err != nil {
		t.Fatal(err)
	}
	containerFirst := planned
	containerFirst.Controller.ID = complete.Controller.ID
	if _, err := lock.RecordControlledSessionOwnershipV1(containerFirst); err == nil || !strings.Contains(err.Error(), "before the network ID") {
		t.Fatalf("container-first ownership error = %v", err)
	}
	networkPrepared := planned
	networkPrepared.NetworkID = complete.NetworkID
	recorded, err := lock.RecordControlledSessionOwnershipV1(networkPrepared)
	if err != nil || recorded.NetworkID != complete.NetworkID || recorded.Controller.ID != "" {
		t.Fatalf("network ownership = %#v, error=%v", recorded, err)
	}
	controllerPrepared := complete
	controllerPrepared.Workload.ID = ""
	recorded, err = lock.RecordControlledSessionOwnershipV1(controllerPrepared)
	if err != nil || recorded.Controller.ID != complete.Controller.ID || recorded.Workload.ID != "" {
		t.Fatalf("controller ownership = %#v, error=%v", recorded, err)
	}
	recorded, err = lock.RecordControlledSessionOwnershipV1(complete)
	if err != nil {
		t.Fatalf("complete network ownership = %#v, error=%v", recorded, err)
	}
	if recorded.NetworkID != complete.NetworkID || recorded.NetworkName != complete.NetworkName || recorded.Workload.ID != complete.Workload.ID {
		t.Fatalf("recorded network ownership = %#v", recorded)
	}
	changedID := complete
	changedID.NetworkID = strings.Repeat("e", 64)
	if _, err := lock.RecordControlledSessionOwnershipV1(changedID); err == nil || !strings.Contains(err.Error(), "network ID changed") {
		t.Fatalf("changed network ID error = %v", err)
	}
	changedName := complete
	changedName.NetworkName = "other-network"
	if _, err := lock.RecordControlledSessionOwnershipV1(changedName); err == nil || !strings.Contains(err.Error(), "immutable resource plan changed") {
		t.Fatalf("changed network name error = %v", err)
	}
}

func TestLiveRunQueueV1DecodesLegacyControlledSessionWithoutDockerEndpoint(t *testing.T) {
	dir := t.TempDir()
	bootSession, err := CurrentBootSessionIDV1()
	if err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFixtureV1(
		dir, "run-0000000000000001", "reploy/env/workload:g-current",
	)
	ownership.BootSession = bootSession
	ownership.DockerEndpoint = ""
	queue := NewLiveRunQueueV1()
	queue.ControlledSessions = []ControlledSessionOwnershipV1{ownership}
	content, err := EncodeLiveRunQueueV1(queue)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte("docker_endpoint")) {
		t.Fatalf("legacy queue unexpectedly gained a Docker endpoint: %s", content)
	}
	decoded, err := DecodeLiveRunQueueV1(content)
	if err != nil || !reflect.DeepEqual(decoded, queue) {
		t.Fatalf("legacy queue = %#v, error=%v", decoded, err)
	}
}

func TestOperationLockControlledSessionOwnershipWriteFailurePreservesQueue(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	const runID = "run-0000000000000001"
	const generation = "reploy/env/workload:g-current"
	if status, err := lock.AdmitLiveRunV1(LiveRunV1{
		ID: runID, Kind: LiveRunKindShellV1, Name: "controlled-session",
		GenerationReference: generation, Exclusive: true,
	}, false); err != nil || status != LiveRunStatusActiveV1 {
		t.Fatalf("admission = %q, %v", status, err)
	}
	planned := controlledSessionOwnershipFixtureV1(dir, runID, generation)
	planned.Controller.ID = ""
	planned.Workload.ID = ""
	if _, err := lock.RecordControlledSessionOwnershipV1(planned); err != nil {
		t.Fatal(err)
	}
	before, _, err := lock.ReadLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	originalReplace := replaceAtomicStateFile
	replaceAtomicStateFile = func(string, string) error { return errors.New("injected ownership replace failure") }
	t.Cleanup(func() { replaceAtomicStateFile = originalReplace })
	controllerPrepared := controlledSessionOwnershipFixtureV1(dir, runID, generation)
	controllerPrepared.Workload.ID = ""
	if _, err := lock.RecordControlledSessionOwnershipV1(controllerPrepared); err == nil || !strings.Contains(err.Error(), "injected ownership replace failure") {
		t.Fatalf("ownership write error = %v", err)
	}
	after, _, err := lock.ReadLiveRunQueueV1()
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed ownership write changed queue: %#v, error=%v", after, err)
	}
}

func TestRecoverLiveRunQueuePreservesControlledSessionOwnership(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	const runID = "run-0000000000000001"
	const generation = "reploy/env/workload:g-current"
	if status, err := lock.AdmitLiveRunV1(LiveRunV1{
		ID: runID, Kind: LiveRunKindShellV1, Name: "controlled-session",
		GenerationReference: generation, Exclusive: true,
	}, false); err != nil || status != LiveRunStatusActiveV1 {
		t.Fatalf("admission = %q, %v", status, err)
	}
	recorded, err := lock.RecordControlledSessionOwnershipV1(controlledSessionOwnershipFixtureV1(dir, runID, generation))
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := lock.RecoverLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Removed) != 1 || recovery.Removed[0].Run.ID != runID ||
		len(recovery.ControlledSessions) != 1 || recovery.ControlledSessions[0] != recorded {
		t.Fatalf("recovery = %#v", recovery)
	}
	queue, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 0 || len(queue.ControlledSessions) != 1 || queue.ControlledSessions[0] != recorded {
		t.Fatalf("ownership after run recovery = %#v, found=%t, error=%v", queue, found, err)
	}
	retry, err := lock.RecoverLiveRunQueueV1()
	if err != nil || len(retry.Removed) != 0 || len(retry.ControlledSessions) != 1 || retry.ControlledSessions[0] != recorded {
		t.Fatalf("retained ownership retry = %#v, error=%v", retry, err)
	}
}

func controlledSessionOwnershipFixtureV1(dir string, runID string, generation string) ControlledSessionOwnershipV1 {
	container := func(role string, id string, environment string, generation string, build string) ControlledSessionContainerOwnershipV1 {
		return ControlledSessionContainerOwnershipV1{
			Role: role, ID: id, Name: "reploy-" + role + "-" + runID,
			DeploymentID: environment, GenerationReference: generation,
			BuildIdentity: "sha256:" + strings.Repeat(build, 64),
		}
	}
	return ControlledSessionOwnershipV1{
		LiveRunID: runID, SessionHandle: "session-" + strings.Repeat("a", 64),
		DockerEndpoint:   "unix:///var/run/docker.sock",
		ChannelDirectory: filepath.Join(dir, ".reploy", "sessions", runID),
		Controller:       container("controller", strings.Repeat("a", 64), "controller", "reploy/env/controller:g-current", "1"),
		Workload:         container("workload", strings.Repeat("b", 64), "workload", generation, "2"),
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
	if err := lock.RecordLiveRunContainerV1(second.ID, "reploy-demo-command-2"); err == nil || !strings.Contains(err.Error(), "unclaimed") {
		t.Fatalf("unclaimed container error = %v", err)
	}
	updated, removed, err := lock.RemoveLiveRunV1(first.ID)
	if err != nil || !removed || len(updated.Runs) != 1 || updated.Runs[0].ID != second.ID || updated.Runs[0].Status != LiveRunStatusReadyV1 {
		t.Fatalf("remove and promote = %#v, removed=%t, error=%v", updated, removed, err)
	}
	if err := lock.ActivateReadyLiveRunV1(second.ID); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := lock.ReadLiveRunQueueV1()
	if err != nil || !found || len(loaded.Runs) != 1 || loaded.Runs[0].ID != second.ID || loaded.Runs[0].Status != LiveRunStatusActiveV1 {
		t.Fatalf("claimed queue = %#v, found=%t, error=%v", loaded, found, err)
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
	waiterLease, err := owner.AcquireLiveRunLeaseV1(waiter.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer waiterLease.Release()
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
	if err != nil || !found || len(queue.Runs) != 1 || queue.Runs[0].ID != waiter.ID || queue.Runs[0].Status != LiveRunStatusReadyV1 {
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
	activeLease, err := operation.AcquireLiveRunLeaseV1(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer activeLease.Release()
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

func TestRecoverLiveRunQueueV1PreservesOwnedSessionAndFlushesUnownedEntries(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	session, err := CurrentBootSessionIDV1()
	if err != nil {
		t.Fatal(err)
	}
	owned := liveRunFixture("run-0000000000000001", false)
	owned.BootSession = session
	owned.Status = LiveRunStatusActiveV1
	abandoned := liveRunFixture("run-0000000000000002", false)
	abandoned.BootSession = session
	abandoned.Status = LiveRunStatusActiveV1
	abandoned.Container = "demo-run-0000000000000002"
	prior := LiveRunV1{
		ID: "control-0000000000000001", Kind: LiveRunKindControlV1,
		Name: string(ControlOperationRestartV1), GenerationReference: owned.GenerationReference,
		BootSession: "prior-session", Status: LiveRunStatusWaitingV1, Exclusive: true,
	}
	legacy := liveRunFixture("run-0000000000000003", false)
	legacy.Status = LiveRunStatusWaitingV1
	queue := NewLiveRunQueueV1()
	queue.Runs = []LiveRunV1{owned, abandoned, prior, legacy}
	path, err := operation.liveRunQueuePathLockedV1()
	if err != nil {
		t.Fatal(err)
	}
	if err := commitLiveRunQueuePathV1(path, queue); err != nil {
		t.Fatal(err)
	}
	lease, err := operation.AcquireLiveRunLeaseV1(owned.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	recovery, err := operation.RecoverLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Removed) != 3 {
		t.Fatalf("recovery = %#v", recovery)
	}
	wantReasons := map[string]LiveRunRecoveryReasonV1{
		abandoned.ID: LiveRunRecoveryAbandonedOwnerV1,
		prior.ID:     LiveRunRecoveryPriorSessionV1,
		legacy.ID:    LiveRunRecoveryLegacyEntryV1,
	}
	for _, removed := range recovery.Removed {
		if removed.Reason != wantReasons[removed.Run.ID] {
			t.Fatalf("removed entry = %#v", removed)
		}
	}
	loaded, found, err := operation.ReadLiveRunQueueV1()
	if err != nil || !found || len(loaded.Runs) != 1 || loaded.Runs[0].ID != owned.ID {
		t.Fatalf("recovered queue = %#v, found=%t, error=%v", loaded, found, err)
	}
	if len(loaded.Cleanup) != 1 || loaded.Cleanup[0].Container != abandoned.Container || loaded.Cleanup[0].Reason != LiveRunRecoveryAbandonedOwnerV1 {
		t.Fatalf("cleanup inventory = %#v", loaded.Cleanup)
	}
	if _, err := operation.RecoverLiveRunQueueV1(); err != nil {
		t.Fatal(err)
	}
	stillLoaded, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(stillLoaded.Runs) != 1 || stillLoaded.Runs[0].ID != owned.ID {
		t.Fatalf("owned entry was not stable across recovery: %#v, %v", stillLoaded, err)
	}
}

func TestRecoverLiveRunQueueV1RemovesEntryAfterOwnerLeaseRelease(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunFixture("run-0000000000000001", false)
	lease, err := operation.AcquireLiveRunLeaseV1(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	if err := lease.abandonForTest(); err != nil {
		t.Fatal(err)
	}
	recovery, err := operation.RecoverLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Removed) != 1 || recovery.Removed[0].Run.ID != run.ID || recovery.Removed[0].Reason != LiveRunRecoveryAbandonedOwnerV1 {
		t.Fatalf("released-owner recovery = %#v", recovery)
	}
	queue, found, err := operation.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.Runs) != 0 {
		t.Fatalf("queue after released owner = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestRecoverLiveRunQueueV1PromotesNextLiveOwnerAfterAbruptExit(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	first := liveRunFixture("run-0000000000000001", true)
	second := liveRunFixture("run-0000000000000002", false)
	firstLease, err := operation.AcquireLiveRunLeaseV1(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondLease, err := operation.AcquireLiveRunLeaseV1(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer secondLease.Release()
	if _, err := operation.AdmitLiveRunV1(first, false); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.AdmitLiveRunV1(second, true); err != nil {
		t.Fatal(err)
	}
	if err := firstLease.abandonForTest(); err != nil {
		t.Fatal(err)
	}
	recovery, err := operation.RecoverLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(recovery.Removed) != 1 || recovery.Removed[0].Run.ID != first.ID ||
		len(queue.Runs) != 1 || queue.Runs[0].ID != second.ID || queue.Runs[0].Status != LiveRunStatusReadyV1 {
		t.Fatalf("promotion after abrupt exit = recovery %#v, queue %#v, error=%v", recovery, queue, err)
	}
}

func TestRecoverLiveRunQueueV1RemovesOrphanedLeaseFiles(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	abandonedID := "run-0000000000000001"
	abandoned, err := operation.AcquireLiveRunLeaseV1(abandonedID)
	if err != nil {
		t.Fatal(err)
	}
	if err := abandoned.abandonForTest(); err != nil {
		t.Fatal(err)
	}
	ownedID := "run-0000000000000002"
	owned, err := operation.AcquireLiveRunLeaseV1(ownedID)
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Release()

	if _, err := operation.RecoverLiveRunQueueV1(); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(dir, ".reploy")
	if _, err := os.Lstat(filepath.Join(directory, abandonedID+controlLeaseSuffixV1)); !os.IsNotExist(err) {
		t.Fatalf("abandoned orphan lease remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(directory, ownedID+controlLeaseSuffixV1)); err != nil {
		t.Fatalf("owned unpublished lease was removed: %v", err)
	}
}

func TestRecoverLiveRunQueueV1AfterOwnerProcessIsKilled(t *testing.T) {
	if os.Getenv("REPLOY_QUEUE_LEASE_HELPER") == "1" {
		dir := os.Getenv("REPLOY_QUEUE_LEASE_DIR")
		operation, err := AcquireOperationLock(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		run := liveRunFixture("run-0000000000000001", false)
		lease, err := operation.AcquireLiveRunLeaseV1(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
			t.Fatal(err)
		}
		if err := operation.Unlock(); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, "ready")
		if err := os.Stdout.Sync(); err != nil {
			t.Fatal(err)
		}
		_ = lease
		time.Sleep(time.Hour)
		return
	}

	dir := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestRecoverLiveRunQueueV1AfterOwnerProcessIsKilled$")
	command.Env = append(
		os.Environ(),
		"REPLOY_QUEUE_LEASE_HELPER=1",
		"REPLOY_QUEUE_LEASE_DIR="+dir,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("lease helper readiness = %q, %v; stderr=%s", line, err, stderr.String())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	waited = true

	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	recovery, err := operation.RecoverLiveRunQueueV1()
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Removed) != 1 ||
		recovery.Removed[0].Run.ID != "run-0000000000000001" ||
		recovery.Removed[0].Reason != LiveRunRecoveryAbandonedOwnerV1 {
		t.Fatalf("killed-owner recovery = %#v", recovery)
	}
	if _, found, err := operation.ReadLiveRunQueueV1(); err != nil || found {
		t.Fatalf("queue after killed-owner recovery: found=%t error=%v", found, err)
	}
}

func TestRecoverLiveRunQueuePathV1RejectsUnknownCurrentSessionWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunFixture("run-0000000000000001", false)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	path, err := operation.liveRunQueuePathLockedV1()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recoverLiveRunQueuePathV1(path, ""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("missing-session error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("failed current-session lookup changed the queue")
	}
}

func TestOperationLockRequiresHeldQueueEntryLeaseForAdmissionOwnership(t *testing.T) {
	operation, err := AcquireOperationLock(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	id := "run-0000000000000001"
	if err := operation.RequireQueueEntryLeaseHeldV1(id); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("missing lease error = %v", err)
	}
	lease, err := operation.AcquireLiveRunLeaseV1(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.RequireQueueEntryLeaseHeldV1(id); err != nil {
		t.Fatalf("held lease rejected: %v", err)
	}
	if err := lease.abandonForTest(); err != nil {
		t.Fatal(err)
	}
	if err := operation.RequireQueueEntryLeaseHeldV1(id); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("released lease accepted: %v", err)
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
