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
		run: func(spec CommandSpec, _ RunOptions) error {
			record(strings.Join(spec.Args, " "))
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
	if !reflect.DeepEqual(attachment.input, []byte{0, 1, 2, 0xff}) {
		t.Fatalf("PTY input = %v", attachment.input)
	}

	deadline := time.Now().Add(time.Second)
	for {
		actionsMu.Lock()
		got := append([]string(nil), actions...)
		actionsMu.Unlock()
		if len(got) >= 7 {
			wantPrefix := []string{
				strings.Join(plan.Create.Args, " "),
				"attach " + plan.Container,
				strings.Join(plan.Start.Args, " "),
			}
			if !reflect.DeepEqual(got[:3], wantPrefix) {
				t.Fatalf("initial actions = %#v, want prefix %#v", got, wantPrefix)
			}
			if !slicesContainStringV1(got, "resize 80x24 "+plan.Container) ||
				!slicesContainStringV1(got, "resize 132x43 "+plan.Container) ||
				!slicesContainStringV1(got, "kill --signal TERM "+plan.Container) ||
				!slicesContainStringV1(got, "kill --signal KILL "+plan.Container) {
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

func TestPrepareDockerWorkloadPTYV1RollsBackInertContainerAfterAttachFailure(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	runs := []CommandSpec{}
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, _ RunOptions) error {
			runs = append(runs, spec)
			return nil
		},
		attach: func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error) {
			return nil, errors.New("attach refused")
		},
		resize:  func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error { return nil },
		observe: func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	_, err := prepareDockerWorkloadPTYV1(t.Context(), plan, backend)
	if err == nil || !strings.Contains(err.Error(), "before start") || !strings.Contains(err.Error(), "attach refused") {
		t.Fatalf("attach error = %v", err)
	}
	if len(runs) != 2 || !reflect.DeepEqual(runs[0].Args, plan.Create.Args) || !reflect.DeepEqual(runs[1].Args, plan.Cleanup.Args) {
		t.Fatalf("rollback commands = %#v", runs)
	}
}

func TestDockerWorkloadPTYV1RollsBackAmbiguousStartFailure(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	attachment := &fakeDockerPTYAttachmentV1{}
	runs := []CommandSpec{}
	observed := false
	backend := dockerWorkloadPTYBackendV1{
		run: func(spec CommandSpec, _ RunOptions) error {
			runs = append(runs, spec)
			if reflect.DeepEqual(spec.Args, plan.Start.Args) {
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
		!reflect.DeepEqual(runs[1].Args, plan.Start.Args) || !reflect.DeepEqual(runs[2].Args, plan.Cleanup.Args) {
		t.Fatalf("start rollback commands = %#v", runs)
	}
	attachment.mu.Lock()
	closed := attachment.closed
	attachment.mu.Unlock()
	if !closed || observed {
		t.Fatalf("start rollback closed=%t observed=%t", closed, observed)
	}
}

func TestDockerWorkloadPTYV1ReportsObservationLossAndCallerWaitCancellation(t *testing.T) {
	plan := controlledSessionWorkloadPlanFixtureV1(t)
	release := make(chan struct{})
	backend := dockerWorkloadPTYBackendV1{
		run: func(CommandSpec, RunOptions) error { return nil },
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
		run: func(spec CommandSpec, _ RunOptions) error {
			runs = append(runs, spec)
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
	if len(runs) != 2 || !reflect.DeepEqual(runs[1].Args, wantStart) {
		t.Fatalf("frozen start command = %#v, want %#v", runs, wantStart)
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
