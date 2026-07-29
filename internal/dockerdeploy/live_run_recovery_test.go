package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestRecoverLiveRunQueueV1DefersAndRetriesExactContainerCleanup(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	run := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	container := "demo-" + run.ID
	if err := operation.RecordLiveRunContainerV1(run.ID, container); err != nil {
		t.Fatal(err)
	}
	want := errors.New("Docker daemon is restarting")
	var notice bytes.Buffer
	recovery, err := recoverLiveRunQueueV1(t.Context(), operation, &notice, func(spec CommandSpec, _ RunOptions) error {
		if !reflect.DeepEqual(spec, TemporaryContainerCleanupCommand(container)) {
			t.Fatalf("cleanup command = %#v", spec)
		}
		return want
	})
	if err != nil || len(recovery.Removed) != 1 {
		t.Fatalf("first recovery = %#v, %v", recovery, err)
	}
	for _, text := range []string{
		"skipped abandoned app command \"export\" (" + run.ID + ")",
		"deferred cleanup of recovered app \"export\" container \"" + container + "\"",
	} {
		if !strings.Contains(notice.String(), text) {
			t.Fatalf("recovery notice missing %q: %q", text, notice.String())
		}
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 0 || len(queue.Cleanup) != 1 || queue.Cleanup[0].Container != container {
		t.Fatalf("deferred cleanup queue = %#v, %v", queue, err)
	}
	calls := 0
	if _, err := recoverLiveRunQueueV1(t.Context(), operation, nil, func(spec CommandSpec, _ RunOptions) error {
		calls++
		if !reflect.DeepEqual(spec, TemporaryContainerCleanupCommand(container)) {
			t.Fatalf("retry command = %#v", spec)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	queue, found, err := operation.ReadLiveRunQueueV1()
	if err != nil || found || calls != 1 || len(queue.Cleanup) != 0 {
		t.Fatalf("cleanup retry = %#v, found=%t calls=%d error=%v", queue, found, calls, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverLiveRunQueueV1BoundsCleanupAcrossInventory(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	queue := deploy.NewLiveRunQueueV1()
	queue.Cleanup = []deploy.LiveRunContainerCleanupV1{
		{
			Container: "demo-run-0000000000000001", RunID: "run-0000000000000001",
			Kind: deploy.LiveRunKindAppV1, Name: "first", Reason: deploy.LiveRunRecoveryCleanupFailedV1,
		},
		{
			Container: "demo-run-0000000000000002", RunID: "run-0000000000000002",
			Kind: deploy.LiveRunKindAppV1, Name: "second", Reason: deploy.LiveRunRecoveryCleanupFailedV1,
		},
	}
	if err := operation.CommitLiveRunQueueV1(queue); err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	calls := 0
	started := time.Now()
	_, err = recoverLiveRunQueueWithinV1(
		t.Context(),
		operation,
		&notice,
		func(_ CommandSpec, options RunOptions) error {
			calls++
			<-options.Context.Done()
			return options.Context.Err()
		},
		20*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded cleanup took %s", elapsed)
	}
	if calls != 1 || !strings.Contains(notice.String(), "deferred remaining recovered container cleanup") {
		t.Fatalf("bounded cleanup calls=%d notice=%q", calls, notice.String())
	}
	loaded, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(loaded.Cleanup) != 2 {
		t.Fatalf("bounded cleanup inventory = %#v, %v", loaded.Cleanup, err)
	}
}

func TestRecoverLiveRunQueueV1ReportsScheduledCleanupWhenDockerIsNotQueried(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	container := "demo-" + run.ID
	if err := operation.RecordLiveRunContainerV1(run.ID, container); err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	if _, err := recoverLiveRunQueueWithinV1(
		context.Background(), operation, &notice, nil, time.Second,
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice.String(), "scheduled cleanup for transient container \""+container+"\"") {
		t.Fatalf("scheduled cleanup notice = %q", notice.String())
	}
}

func TestRecoverLiveRunQueueV1PreservesLiveOwnerAcrossDockerInterruption(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	run := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	lease := holdLiveRunLeaseV1(t, operation, run.ID)
	if lease == nil {
		t.Fatal("live-run lease is missing")
	}
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	dockerCalls := 0
	recovery, err := recoverLiveRunQueueV1(t.Context(), operation, nil, func(CommandSpec, RunOptions) error {
		dockerCalls++
		return errors.New("Docker daemon unavailable")
	})
	if err != nil || len(recovery.Removed) != 0 || dockerCalls != 0 {
		t.Fatalf("live-owner recovery = %#v, Docker calls=%d, error=%v", recovery, dockerCalls, err)
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil || len(queue.Runs) != 1 || queue.Runs[0].ID != run.ID {
		t.Fatalf("preserved queue = %#v, %v", queue, err)
	}
}
