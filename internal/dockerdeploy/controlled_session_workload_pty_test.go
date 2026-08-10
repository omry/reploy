package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

const dockerWorkloadTestContainerIDV1 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type fakeDockerPTYAttachmentV1 struct {
	mu     sync.Mutex
	input  []byte
	closed bool
}

func (attachment *fakeDockerPTYAttachmentV1) Read([]byte) (int, error) {
	attachment.mu.Lock()
	defer attachment.mu.Unlock()
	if attachment.closed {
		return 0, io.EOF
	}
	return 0, nil
}

func (attachment *fakeDockerPTYAttachmentV1) WriteContext(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	attachment.mu.Lock()
	defer attachment.mu.Unlock()
	if attachment.closed {
		return io.ErrClosedPipe
	}
	attachment.input = append(attachment.input, data...)
	return nil
}

func (attachment *fakeDockerPTYAttachmentV1) Close() error {
	attachment.mu.Lock()
	defer attachment.mu.Unlock()
	attachment.closed = true
	return nil
}

func TestDockerWorkloadPTYV1OrdersAttachStartResizeAndExactOperations(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	attachment := &fakeDockerPTYAttachmentV1{}
	var actionsMu sync.Mutex
	actions := []string{}
	record := func(value string) {
		actionsMu.Lock()
		defer actionsMu.Unlock()
		actions = append(actions, value)
	}
	exit := make(chan int, 1)
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			record(strings.Join(spec.Args, " "))
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			return nil
		},
		attach: func(_ context.Context, _ CommandSpec, container string, _ time.Duration) (dockerPTYAttachmentV1, error) {
			record("attach " + container)
			return attachment, nil
		},
		resize: func(_ context.Context, _ CommandSpec, container string, columns uint32, rows uint32, _ time.Duration) error {
			record(fmt.Sprintf("resize %dx%d %s", columns, rows, container))
			return nil
		},
		observe: func(_ context.Context, _ CommandSpec, container string) (int, error) {
			record("observe " + container)
			return <-exit, nil
		},
	}
	workload, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := workload.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "output must be claimed") {
		t.Fatalf("start without output claim error = %v", err)
	}
	if _, err := workload.Output(); err != nil {
		t.Fatal(err)
	}
	if _, err := workload.Output(); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("second output claim error = %v", err)
	}
	if err := workload.WriteInput(t.Context(), []byte("early")); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("pre-start input error = %v", err)
	}
	if err := workload.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !workload.Started() {
		t.Fatal("successful workload start was not recorded")
	}
	if err := workload.WriteInput(t.Context(), []byte{0, 1, 2, 0xff}); err != nil {
		t.Fatal(err)
	}
	if err := workload.Resize(t.Context(), 132, 43); err != nil {
		t.Fatal(err)
	}
	if err := workload.RequestGracefulStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := workload.ForceStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	exit <- 42
	status, err := workload.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Kind != controlledsession.ProcessStatusExitedV1 || status.Code == nil || *status.Code != 42 {
		t.Fatalf("wait status = %#v", status)
	}
	if err := workload.Close(); err != nil {
		t.Fatal(err)
	}
	if err := workload.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := workload.Cleanup(t.Context()); err != nil {
		t.Fatalf("idempotent cleanup = %v", err)
	}
	if !reflect.DeepEqual(attachment.input, []byte{0, 1, 2, 0xff}) {
		t.Fatalf("PTY input = %v", attachment.input)
	}

	deadline := time.Now().Add(time.Second)
	for {
		actionsMu.Lock()
		got := append([]string(nil), actions...)
		actionsMu.Unlock()
		if len(got) >= 8 {
			wantPrefix := []string{
				strings.Join(plan.Create.Args, " "),
				"attach " + dockerWorkloadTestContainerIDV1,
				"start " + dockerWorkloadTestContainerIDV1,
			}
			if !reflect.DeepEqual(got[:3], wantPrefix) {
				t.Fatalf("initial actions = %#v, want prefix %#v", got, wantPrefix)
			}
			if !slicesContainStringV1(got, "resize 80x24 "+dockerWorkloadTestContainerIDV1) ||
				!slicesContainStringV1(got, "resize 132x43 "+dockerWorkloadTestContainerIDV1) ||
				!slicesContainStringV1(got, "kill --signal TERM "+dockerWorkloadTestContainerIDV1) ||
				!slicesContainStringV1(got, "kill --signal KILL "+dockerWorkloadTestContainerIDV1) ||
				!slicesContainStringV1(got, "container rm --force "+dockerWorkloadTestContainerIDV1) {
				t.Fatalf("lifecycle actions = %#v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for exit observer, actions = %#v", got)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDockerWorkloadPTYV1PinsOneDockerEndpointForItsLifetime(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	const endpoint = "unix:///session-engine.sock"
	var mu sync.Mutex
	var commands []CommandSpec
	record := func(spec CommandSpec) {
		mu.Lock()
		defer mu.Unlock()
		commands = append(commands, spec)
	}
	binds := 0
	backend := dockerWorkloadPTYBackendV1{
		bind: func(_ context.Context, spec CommandSpec, _ time.Duration) (CommandSpec, commandRunner, error) {
			binds++
			return pinDockerEndpointV1(spec, endpoint), func(command CommandSpec, options RunOptions) error {
				command = pinDockerEndpointV1(command, endpoint)
				record(command)
				writeDockerWorkloadCreateIDV1(command, options, plan)
				return nil
			}, nil
		},
		attach: func(_ context.Context, docker CommandSpec, _ string, _ time.Duration) (dockerPTYAttachmentV1, error) {
			record(docker)
			return &fakeDockerPTYAttachmentV1{}, nil
		},
		resize: func(_ context.Context, docker CommandSpec, _ string, _, _ uint32, _ time.Duration) error {
			record(docker)
			return nil
		},
		observe: func(_ context.Context, docker CommandSpec, _ string) (int, error) {
			record(docker)
			return 0, nil
		},
	}
	workload, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workload.Output(); err != nil {
		t.Fatal(err)
	}
	if err := workload.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := workload.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := workload.RequestGracefulStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := workload.ForceStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := workload.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]CommandSpec(nil), commands...)
	mu.Unlock()
	if binds != 1 || len(got) < 8 {
		t.Fatalf("endpoint binds=%d commands=%#v", binds, got)
	}
	for _, command := range got {
		if host, found := commandSpecEnvironmentValueV1(command, "DOCKER_HOST"); !found || host != endpoint {
			t.Fatalf("command %#v used Docker host %q, found=%t", command.Args, host, found)
		}
		if contextName, found := commandSpecEnvironmentValueV1(command, "DOCKER_CONTEXT"); !found || contextName != "" {
			t.Fatalf("command %#v retained Docker context %q, found=%t", command.Args, contextName, found)
		}
	}
}

func TestDockerWorkloadPTYV1TreatsMissingContainerAsCleaned(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	cleanupAttempts := 0
	workload, err := prepareDockerWorkloadPTYV1(t.Context(), plan, dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			if reflect.DeepEqual(spec.Args, []string{"container", "rm", "--force", dockerWorkloadTestContainerIDV1}) {
				cleanupAttempts++
				return errors.New("Error response from daemon: No such container: " + dockerWorkloadTestContainerIDV1)
			}
			return nil
		},
		attach: func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error) {
			return &fakeDockerPTYAttachmentV1{}, nil
		},
		resize:  func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error { return nil },
		observe: func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := workload.Cleanup(t.Context()); err != nil {
		t.Fatalf("missing-container cleanup = %v", err)
	}
	if err := workload.Cleanup(t.Context()); err != nil {
		t.Fatalf("repeated cleanup = %v", err)
	}
	if cleanupAttempts != 1 {
		t.Fatalf("cleanup attempts = %d, want 1", cleanupAttempts)
	}
}

func TestPrepareDockerWorkloadPTYV1RollsBackInertContainerAfterAttachFailure(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	runs := []CommandSpec{}
	rollbackVerified := false
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			runs = append(runs, spec)
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			return nil
		},
		attach: func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error) {
			return nil, errors.New("attach refused")
		},
		recordRollbackVerified: func() { rollbackVerified = true },
		resize:                 func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error { return nil },
		observe:                func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	_, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if err == nil || !strings.Contains(err.Error(), "before start") || !strings.Contains(err.Error(), "attach refused") {
		t.Fatalf("attach error = %v", err)
	}
	if len(runs) != 2 || !reflect.DeepEqual(runs[0].Args, plan.Create.Args) ||
		!reflect.DeepEqual(runs[1].Args, []string{"container", "rm", "--force", dockerWorkloadTestContainerIDV1}) {
		t.Fatalf("rollback commands = %#v", runs)
	}
	if !rollbackVerified {
		t.Fatal("successful rollback was not reported")
	}
}

func TestPrepareDockerWorkloadPTYV1RecordsExactIDBeforeAttachFailure(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	actions := []string{}
	rollbackVerified := false
	cleanupErr := errors.New("cleanup unavailable")
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			actions = append(actions, strings.Join(spec.Args, " "))
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			if reflect.DeepEqual(spec.Args, []string{"container", "rm", "--force", dockerWorkloadTestContainerIDV1}) {
				return cleanupErr
			}
			return nil
		},
		recordContainerID: func(containerID string) error {
			actions = append(actions, "record "+containerID)
			return nil
		},
		recordRollbackVerified: func() { rollbackVerified = true },
		attach: func(_ context.Context, _ CommandSpec, containerID string, _ time.Duration) (dockerPTYAttachmentV1, error) {
			actions = append(actions, "attach "+containerID)
			return nil, errors.New("attach refused")
		},
		resize:  func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error { return nil },
		observe: func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	_, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if err == nil || !strings.Contains(err.Error(), "attach refused") || !errors.Is(err, cleanupErr) {
		t.Fatalf("attach and rollback error = %v", err)
	}
	want := []string{
		strings.Join(plan.Create.Args, " "),
		"record " + dockerWorkloadTestContainerIDV1,
		"attach " + dockerWorkloadTestContainerIDV1,
		"container rm --force " + dockerWorkloadTestContainerIDV1,
	}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("partial preparation actions = %#v, want %#v", actions, want)
	}
	if rollbackVerified {
		t.Fatal("failed rollback was reported as verified")
	}
}

func TestPrepareDockerWorkloadPTYV1RetriesExactIDAfterRollbackFailure(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	actions := []string{}
	recordErr := errors.New("injected workload ownership failure")
	cleanupErr := errors.New("injected workload rollback failure")
	recordCalls := 0
	rollbackVerified := false
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			actions = append(actions, strings.Join(spec.Args, " "))
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			if reflect.DeepEqual(spec.Args, []string{"container", "rm", "--force", dockerWorkloadTestContainerIDV1}) {
				return cleanupErr
			}
			return nil
		},
		recordContainerID: func(containerID string) error {
			recordCalls++
			actions = append(actions, "record "+containerID)
			if recordCalls == 1 {
				return recordErr
			}
			return nil
		},
		recordRollbackVerified: func() { rollbackVerified = true },
		attach: func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error) {
			t.Fatal("attachment continued after ownership recording failed")
			return nil, nil
		},
		resize:  func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error { return nil },
		observe: func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	_, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if !errors.Is(err, recordErr) || !errors.Is(err, cleanupErr) || recordCalls != 2 {
		t.Fatalf("workload preparation error = %v, record calls = %d", err, recordCalls)
	}
	want := []string{
		strings.Join(plan.Create.Args, " "),
		"record " + dockerWorkloadTestContainerIDV1,
		"container rm --force " + dockerWorkloadTestContainerIDV1,
		"record " + dockerWorkloadTestContainerIDV1,
	}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("partial preparation actions = %#v, want %#v", actions, want)
	}
	if rollbackVerified {
		t.Fatal("failed rollback was reported as verified")
	}
}

func TestDockerWorkloadPTYV1RollsBackAmbiguousStartFailure(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	attachment := &fakeDockerPTYAttachmentV1{}
	runs := []CommandSpec{}
	observed := false
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			runs = append(runs, spec)
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			if reflect.DeepEqual(spec.Args, []string{"start", dockerWorkloadTestContainerIDV1}) {
				return errors.New("start response was lost")
			}
			return nil
		},
		attach: func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error) {
			return attachment, nil
		},
		resize: func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error { return nil },
		observe: func(context.Context, CommandSpec, string) (int, error) {
			observed = true
			return 0, nil
		},
	}
	workload, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workload.Output(); err != nil {
		t.Fatal(err)
	}
	err = workload.Start(t.Context())
	if err == nil || !strings.Contains(err.Error(), "start response was lost") {
		t.Fatalf("ambiguous start error = %v", err)
	}
	if len(runs) != 3 || !reflect.DeepEqual(runs[0].Args, plan.Create.Args) ||
		!reflect.DeepEqual(runs[1].Args, []string{"start", dockerWorkloadTestContainerIDV1}) ||
		!reflect.DeepEqual(runs[2].Args, []string{"container", "rm", "--force", dockerWorkloadTestContainerIDV1}) {
		t.Fatalf("start rollback commands = %#v", runs)
	}
	attachment.mu.Lock()
	closed := attachment.closed
	attachment.mu.Unlock()
	if !closed || observed {
		t.Fatalf("start rollback closed=%t observed=%t", closed, observed)
	}
	if workload.Started() {
		t.Fatal("failed Docker start was recorded as started")
	}
}

func TestDockerWorkloadPTYV1ReportsPartialStartAfterInitialResizeFailure(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	releaseObserver := make(chan struct{})
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			return nil
		},
		attach: func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error) {
			return &fakeDockerPTYAttachmentV1{}, nil
		},
		resize: func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error {
			return errors.New("resize unavailable")
		},
		observe: func(context.Context, CommandSpec, string) (int, error) {
			<-releaseObserver
			return 143, nil
		},
	}
	workload, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workload.Output(); err != nil {
		t.Fatal(err)
	}
	if err := workload.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "resize unavailable") {
		t.Fatalf("initial resize error = %v", err)
	}
	if !workload.Started() {
		t.Fatal("Docker-started workload was hidden by the later resize failure")
	}
	close(releaseObserver)
}

func TestDockerWorkloadPTYV1ReportsObservationLossAndCallerWaitCancellation(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	release := make(chan struct{})
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			return nil
		},
		attach: func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error) {
			return &fakeDockerPTYAttachmentV1{}, nil
		},
		resize: func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error { return nil },
		observe: func(context.Context, CommandSpec, string) (int, error) {
			<-release
			return 0, errors.New("daemon observation ended")
		},
	}
	workload, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workload.Output(); err != nil {
		t.Fatal(err)
	}
	if err := workload.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, err := workload.Wait(waitCtx); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("canceled caller wait error = %v", err)
	}
	close(release)
	status, err := workload.Wait(t.Context())
	if err == nil || !strings.Contains(err.Error(), "daemon observation ended") {
		t.Fatalf("observation error = %v", err)
	}
	if status.Kind != controlledsession.ProcessStatusUnavailableV1 || status.Reason != "Docker workload observation was lost" {
		t.Fatalf("observation-loss status = %#v", status)
	}
}

func TestDockerWorkloadPTYV1FreezesCallerOwnedPlanSlices(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	wantStart := append([]string(nil), plan.Start.Args...)
	runs := []CommandSpec{}
	exit := make(chan int, 1)
	workload, err := prepareDockerWorkloadPTYV1(t.Context(), plan, dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, options RunOptions) error {
			runs = append(runs, spec)
			writeDockerWorkloadCreateIDV1(spec, options, plan)
			return nil
		},
		attach: func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error) {
			return &fakeDockerPTYAttachmentV1{}, nil
		},
		resize:  func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error { return nil },
		observe: func(context.Context, CommandSpec, string) (int, error) { return <-exit, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workload.Output(); err != nil {
		t.Fatal(err)
	}
	plan.Start.Args[0] = "tampered-start"
	if err := workload.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	exit <- 0
	if _, err := workload.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || !reflect.DeepEqual(runs[1].Args, []string{"start", dockerWorkloadTestContainerIDV1}) {
		t.Fatalf("frozen start command = %#v, original planned start %#v", runs, wantStart)
	}
}

func writeDockerWorkloadCreateIDV1(spec CommandSpec, options RunOptions, plan ControlledSessionContainerPlanV1) {
	if reflect.DeepEqual(spec.Args, plan.Create.Args) && options.Stdout != nil {
		_, _ = fmt.Fprintln(options.Stdout, dockerWorkloadTestContainerIDV1)
	}
}

func TestObserveDockerContainerExitV1RejectsMalformedStatus(t *testing.T) {
	dir := t.TempDir()
	docker := writeFakeCommand(
		t, dir, "docker-wait",
		"#!/bin/sh\nprintf '256\\n'\n",
		"@echo off\r\necho 256\r\n",
	)
	_, err := observeDockerContainerExitV1(t.Context(), CommandSpec{Name: docker}, "workload")
	if err == nil || !strings.Contains(err.Error(), "invalid exit code") {
		t.Fatalf("malformed wait error = %v", err)
	}
}

func TestDockerHijackedPTYV1WritesAllBytesAndHonorsCancellation(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	attachment := &dockerHijackedPTYV1{connection: client, reader: nil}
	defer attachment.Close()
	want := bytes.Repeat([]byte{0, 1, 2, 0xff}, 1024)
	received := make(chan []byte, 1)
	go func() {
		buffer := make([]byte, len(want))
		_, _ = io.ReadFull(server, buffer)
		received <- buffer
	}()
	if err := attachment.WriteContext(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	if got := <-received; !bytes.Equal(got, want) {
		t.Fatalf("written bytes differ: got %d bytes", len(got))
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := attachment.WriteContext(canceled, []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write error = %v", err)
	}
}

func controlledSessionWorkloadPlanFixtureV1(t *testing.T) ControlledSessionContainerPlanV1 {
	t.Helper()
	input, backend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Workload
}

func slicesContainStringV1(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
