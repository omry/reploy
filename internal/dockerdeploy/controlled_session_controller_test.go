package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestDockerControllerV1OrdersChannelCreateStartAndExactLifecycle(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	var actionsMu sync.Mutex
	actions := []string{}
	record := func(value string) {
		actionsMu.Lock()
		defer actionsMu.Unlock()
		actions = append(actions, value)
	}
	exit := make(chan int, 1)
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error {
			record("channel ready")
			return nil
		},
		run: func(spec CommandSpec, _ RunOptions) error {
			record(strings.Join(spec.Args, " "))
			return nil
		},
		observe: func(_ context.Context, _ CommandSpec, container string) (int, error) {
			record("observe " + container)
			return <-exit, nil
		},
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Wait(t.Context()); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("pre-start wait error = %v", err)
	}
	if err := controller.RequestGracefulStop(t.Context()); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("pre-start stop error = %v", err)
	}
	if err := controller.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "already attempted") {
		t.Fatalf("second start error = %v", err)
	}
	if err := controller.RequestGracefulStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.ForceStop(t.Context()); err != nil {
		t.Fatal(err)
	}
	exit <- 42
	status, err := controller.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Kind != controlledsession.ProcessStatusExitedV1 || status.Code == nil || *status.Code != 42 {
		t.Fatalf("wait status = %#v", status)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatalf("repeated cleanup = %v", err)
	}

	actionsMu.Lock()
	got := append([]string(nil), actions...)
	actionsMu.Unlock()
	wantPrefix := []string{
		"channel ready",
		strings.Join(plan.Create.Args, " "),
		strings.Join(plan.Start.Args, " "),
	}
	if len(got) < len(wantPrefix) || !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("initial actions = %#v, want prefix %#v", got, wantPrefix)
	}
	for _, want := range []string{
		"observe " + plan.Container,
		"kill --signal TERM " + plan.Container,
		"kill --signal KILL " + plan.Container,
		strings.Join(plan.Cleanup.Args, " "),
	} {
		if !slicesContainStringV1(got, want) {
			t.Fatalf("actions = %#v, missing %q", got, want)
		}
	}
	if countStringsV1(got, strings.Join(plan.Cleanup.Args, " ")) != 1 {
		t.Fatalf("cleanup actions = %#v", got)
	}
}

func TestPrepareDockerControllerV1RequiresReadyChannelBeforeCreate(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	run := false
	_, err := prepareDockerControllerV1(t.Context(), plan, dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error {
			return errors.New("channel socket missing")
		},
		run: func(CommandSpec, RunOptions) error {
			run = true
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "channel socket missing") || run {
		t.Fatalf("prepare error = %v, Docker invoked = %t", err, run)
	}
}

func TestPrepareDockerControllerV1RejectsWorkloadAndIncompleteBackend(t *testing.T) {
	workload := controlledSessionWorkloadPlanFixtureV1(t)
	complete := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run:                 func(CommandSpec, RunOptions) error { return nil },
		observe:             func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	if _, err := prepareDockerControllerV1(t.Context(), workload, complete); err == nil || !strings.Contains(err.Error(), "container role") {
		t.Fatalf("workload role error = %v", err)
	}
	if _, err := prepareDockerControllerV1(t.Context(), controlledSessionControllerPlanFixtureV1(t), dockerControllerBackendV1{}); err == nil || !strings.Contains(err.Error(), "backend is incomplete") {
		t.Fatalf("incomplete backend error = %v", err)
	}
}

func TestDockerControllerV1RollsBackAmbiguousStartFailure(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	runs := []CommandSpec{}
	observed := false
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, _ RunOptions) error {
			runs = append(runs, spec)
			if reflect.DeepEqual(spec.Args, plan.Start.Args) {
				return errors.New("start response was lost")
			}
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) {
			observed = true
			return 0, nil
		},
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	err = controller.Start(t.Context())
	if err == nil || !strings.Contains(err.Error(), "start response was lost") {
		t.Fatalf("ambiguous start error = %v", err)
	}
	if len(runs) != 3 || !reflect.DeepEqual(runs[0].Args, plan.Create.Args) ||
		!reflect.DeepEqual(runs[1].Args, plan.Start.Args) || !reflect.DeepEqual(runs[2].Args, plan.Cleanup.Args) {
		t.Fatalf("start rollback commands = %#v", runs)
	}
	if observed {
		t.Fatal("ambiguous start failure launched an exit observer")
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatalf("cleanup after successful rollback = %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("successful rollback was not remembered: %#v", runs)
	}
}

func TestDockerControllerV1ReportsObservationLossAndCallerWaitCancellation(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	release := make(chan struct{})
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run:                 func(CommandSpec, RunOptions) error { return nil },
		observe: func(context.Context, CommandSpec, string) (int, error) {
			<-release
			return 0, errors.New("daemon observation ended")
		},
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, err := controller.Wait(waitCtx); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("canceled caller wait error = %v", err)
	}
	close(release)
	status, err := controller.Wait(t.Context())
	if err == nil || !strings.Contains(err.Error(), "daemon observation ended") {
		t.Fatalf("observation error = %v", err)
	}
	if status.Kind != controlledsession.ProcessStatusUnavailableV1 || status.Reason != "Docker controller observation was lost" {
		t.Fatalf("observation-loss status = %#v", status)
	}
}

func TestDockerControllerV1RetriesFailedCleanup(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	cleanupAttempts := 0
	backend := dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, _ RunOptions) error {
			if reflect.DeepEqual(spec.Args, plan.Cleanup.Args) {
				cleanupAttempts++
				if cleanupAttempts == 1 {
					return errors.New("daemon unavailable")
				}
			}
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) { return 0, nil },
	}
	controller, err := prepareDockerControllerV1(t.Context(), plan, backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Cleanup(t.Context()); err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("first cleanup error = %v", err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatalf("retry cleanup = %v", err)
	}
	if cleanupAttempts != 2 {
		t.Fatalf("cleanup attempts = %d, want 2", cleanupAttempts)
	}
}

func TestDockerControllerV1FreezesCallerOwnedPlanSlices(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	wantStart := append([]string(nil), plan.Start.Args...)
	wantCleanup := append([]string(nil), plan.Cleanup.Args...)
	runs := []CommandSpec{}
	exit := make(chan int, 1)
	controller, err := prepareDockerControllerV1(t.Context(), plan, dockerControllerBackendV1{
		requireReadyChannel: func(ControlledSessionContainerPlanV1) error { return nil },
		run: func(spec CommandSpec, _ RunOptions) error {
			runs = append(runs, spec)
			return nil
		},
		observe: func(context.Context, CommandSpec, string) (int, error) { return <-exit, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	plan.Start.Args[0] = "tampered-start"
	plan.Cleanup.Args[0] = "tampered-cleanup"
	if err := controller.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	exit <- 0
	if _, err := controller.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || !reflect.DeepEqual(runs[1].Args, wantStart) || !reflect.DeepEqual(runs[2].Args, wantCleanup) {
		t.Fatalf("frozen lifecycle commands = %#v, want start %#v cleanup %#v", runs, wantStart, wantCleanup)
	}
}

func controlledSessionControllerPlanFixtureV1(t *testing.T) ControlledSessionContainerPlanV1 {
	t.Helper()
	input, backend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Controller
}

func countStringsV1(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}
