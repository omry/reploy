package dockerdeploy

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func admittedTransientFixtureV1(t *testing.T, dir string) (*deploy.OperationLock, deploy.LiveRunV1, TransientContainerExecutionV1) {
	t.Helper()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	run := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	if _, err := operation.AdmitLiveRunV1(run, false); err != nil {
		t.Fatal(err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	workspace := testPreparedProbeWorkspace(t, platform, t.TempDir())
	execution, err := PlanTransientContainerExecutionV1(
		DockerExecutionPlan{ContainerName: "demo", Image: "demo:image", RuntimeUser: RuntimeUserPlan{UID: 1000, GID: 1000, DockerUser: "1000:1000"}},
		ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}}, workspace, nil, run.ID, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	return operation, run, execution
}

func TestRunAdmittedTransientContainerV1ReleasesLockForExecutionAndCompletesQueue(t *testing.T) {
	dir := t.TempDir()
	operation, run, execution := admittedTransientFixtureV1(t, dir)
	waiter := liveRunAdmissionFixtureV1("run-0000000000000002", true)
	if status, err := operation.AdmitLiveRunV1(waiter, true); err != nil || status != deploy.LiveRunStatusWaitingV1 {
		t.Fatalf("waiter admission = %q, %v", status, err)
	}
	order := []string{}
	backend := admittedTransientContainerBackendV1{
		acquire: deploy.AcquireOperationLock,
		create: func(spec CommandSpec, options RunOptions) error {
			order = append(order, "create")
			if err := operation.RequireHeld(); err != nil {
				t.Fatalf("create lock = %v", err)
			}
			if !reflect.DeepEqual(spec, execution.Create) || options.Stdin != nil || options.Stdout != nil || options.Stderr != nil {
				t.Fatalf("create input = %#v, %#v", spec, options)
			}
			return nil
		},
		followup: func(CommandSpec, RunOptions) error { return nil },
		runTemporary: func(run temporaryCommandRunner, start CommandSpec, cleanup CommandSpec, options RunOptions) error {
			order = append(order, "execute")
			if err := operation.RequireHeld(); err == nil {
				t.Fatal("operation lock remained held during execution")
			}
			inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			queue, found, err := inspection.ReadLiveRunQueueV1()
			if err != nil || !found || len(queue.Runs) != 2 || queue.Runs[0].Container != execution.Container || queue.Runs[1].Status != deploy.LiveRunStatusWaitingV1 {
				t.Fatalf("recorded queue = %#v, found=%t, error=%v", queue, found, err)
			}
			if err := inspection.Unlock(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(start, execution.Start) || !reflect.DeepEqual(cleanup, execution.Cleanup) || options.Stdin == nil {
				t.Fatalf("execution input = %#v, %#v, %#v", start, cleanup, options)
			}
			return nil
		},
	}
	err := runAdmittedTransientContainerV1(t.Context(), dir, operation, run.ID, execution, RunOptions{Stdin: strings.NewReader("input"), Stdout: io.Discard}, backend)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"create", "execute"}) {
		t.Fatalf("order = %v", order)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 1 || queue.Runs[0].ID != waiter.ID || queue.Runs[0].Status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("promoted queue = %#v, found=%t, error=%v", queue, found, err)
	}
	if _, removed, err := inspection.RemoveLiveRunV1(waiter.ID); err != nil || !removed {
		t.Fatalf("remove promoted waiter = %t, %v", removed, err)
	}
}

func TestRunAdmittedTransientContainerV1CreateFailureCleansQueueAndLock(t *testing.T) {
	dir := t.TempDir()
	operation, run, execution := admittedTransientFixtureV1(t, dir)
	want := errors.New("create failed")
	cleanupCalls := 0
	backend := admittedTransientContainerBackendV1{
		acquire: deploy.AcquireOperationLock,
		create:  func(CommandSpec, RunOptions) error { return want },
		followup: func(spec CommandSpec, options RunOptions) error {
			cleanupCalls++
			if !reflect.DeepEqual(spec, execution.Cleanup) || options.Context == nil || options.Context.Err() != nil {
				t.Fatalf("cleanup input = %#v, %#v", spec, options)
			}
			return errors.New("No such container")
		},
		runTemporary: runTemporaryContainerCommand,
	}
	err := runAdmittedTransientContainerV1(t.Context(), dir, operation, run.ID, execution, RunOptions{}, backend)
	if !errors.Is(err, want) || cleanupCalls != 1 {
		t.Fatalf("create failure = %v, cleanup calls=%d", err, cleanupCalls)
	}
	if err := operation.RequireHeld(); err == nil {
		t.Fatal("create failure retained operation lock")
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.Runs) != 0 {
		t.Fatalf("queue after create failure = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestRunAdmittedTransientContainerV1ExecutionFailureStillCompletesQueue(t *testing.T) {
	dir := t.TempDir()
	operation, run, execution := admittedTransientFixtureV1(t, dir)
	want := errors.New("start failed")
	backend := admittedTransientContainerBackendV1{
		acquire:  deploy.AcquireOperationLock,
		create:   func(CommandSpec, RunOptions) error { return nil },
		followup: func(CommandSpec, RunOptions) error { return nil },
		runTemporary: func(temporaryCommandRunner, CommandSpec, CommandSpec, RunOptions) error {
			return want
		},
	}
	err := runAdmittedTransientContainerV1(t.Context(), dir, operation, run.ID, execution, RunOptions{}, backend)
	if !errors.Is(err, want) {
		t.Fatalf("execution failure = %v", err)
	}
	inspection, err := deploy.AcquireOperationLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.Runs) != 0 {
		t.Fatalf("queue after execution failure = %#v, found=%t, error=%v", queue, found, err)
	}
}

func TestRunAdmittedTransientContainerV1CanceledBeforeCreateRemovesAdmission(t *testing.T) {
	dir := t.TempDir()
	operation, run, execution := admittedTransientFixtureV1(t, dir)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := runAdmittedTransientContainerV1(ctx, dir, operation, run.ID, execution, RunOptions{}, admittedTransientContainerBackendV1{
		acquire:      deploy.AcquireOperationLock,
		create:       func(CommandSpec, RunOptions) error { t.Fatal("canceled run reached create"); return nil },
		followup:     func(CommandSpec, RunOptions) error { t.Fatal("canceled run reached Docker cleanup"); return nil },
		runTemporary: runTemporaryContainerCommand,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run error = %v", err)
	}
	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || found || len(queue.Runs) != 0 {
		t.Fatalf("queue after pre-create cancellation = %#v, found=%t, error=%v", queue, found, err)
	}
}
