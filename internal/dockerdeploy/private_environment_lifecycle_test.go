package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type workloadLifecycleFixtureV1 struct {
	t                                   *testing.T
	operation                           *deploy.OperationLock
	command                             CommandSpec
	environment                         privateWorkloadEnvironmentV1
	state                               privateWorkloadContainerV1
	starts, stops, injections, removals int
	startError, injectError, stopError  error
	keepRunning                         bool
	launchProof                         bool
	launchProofCalls                    int
	afterStart                          func()
	onStop                              func()
}

func newWorkloadLifecycleFixtureV1(t *testing.T) *workloadLifecycleFixtureV1 {
	t.Helper()
	operation, err := deploy.AcquireOperationLock(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	return &workloadLifecycleFixtureV1{t: t, operation: operation,
		command:     pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"compose", "up"}}, "unix:///fixture.sock"),
		environment: privateWorkloadEnvironmentV1{Operation: operation, RuntimeBinding: strings.Repeat("b", 64), Present: true, Payload: []byte("TOKEN=synthetic\n\n")}}
}

func (fixture *workloadLifecycleFixtureV1) prime(state string) {
	fixture.t.Helper()
	fixture.state = privateWorkloadContainerV1{ID: strings.Repeat("a", 64), Running: true, StartedAt: "2026-10-22T00:00:00Z"}
	record, err := privateWorkloadRecordV1(fixture.command, fixture.environment.RuntimeBinding)
	if err != nil {
		fixture.t.Fatal(err)
	}
	record.ContainerID, record.StartedAt, record.State = fixture.state.ID, fixture.state.StartedAt, state
	record.TransferClaimed, record.HandoffComplete, record.StartupComplete = true, true, state == "up"
	if err := fixture.operation.WriteWorkloadLifecycleV1(record); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *workloadLifecycleFixtureV1) run(command CommandSpec, options RunOptions) error {
	fixture.t.Helper()
	if options.Context == nil || options.Stdin != nil {
		fixture.t.Fatal("non-relay command lost its context or inherited private input")
	}
	switch {
	case len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "inspect":
		if fixture.state.ID == "" {
			_, _ = fmt.Fprintln(options.Stderr, "Error response from daemon: No such container: demo")
			return errors.New("missing container")
		}
		return json.NewEncoder(options.Stdout).Encode(map[string]any{"id": fixture.state.ID, "running": fixture.state.Running, "started_at": fixture.state.StartedAt})
	case len(command.Args) == 4 && command.Args[0] == "exec" && command.Args[3] == "application-launch-proof-v1":
		if command.Args[1] != fixture.state.ID {
			fixture.t.Fatal("launch proof lost its exact container ID")
		}
		deadline, ok := options.Context.Deadline()
		if !ok || time.Until(deadline) > privateWorkloadEnvironmentTimeoutV1 {
			fixture.t.Fatal("launch proof lacks its independent deadline")
		}
		fixture.launchProofCalls++
		if !fixture.launchProof {
			return errors.New("application is still under the trusted startup verifier")
		}
		return nil
	case reflect.DeepEqual(command.Args, []string{"compose", "up"}):
		fixture.starts++
		fixture.state = privateWorkloadContainerV1{ID: strings.Repeat("a", 64), Running: true, StartedAt: fmt.Sprintf("2026-10-23T00:00:%02dZ", fixture.starts)}
		if fixture.afterStart != nil {
			fixture.afterStart()
		}
		return fixture.startError
	case len(command.Args) == 3 && reflect.DeepEqual(command.Args[:2], []string{"container", "stop"}):
		fixture.stops++
		if command.Args[2] != fixture.state.ID || options.Context.Err() != nil {
			fixture.t.Fatal("cleanup lost its exact identity or inherited cancellation")
		}
		if fixture.onStop != nil {
			fixture.onStop()
		}
		if !fixture.keepRunning {
			fixture.state.Running = false
		}
		return fixture.stopError
	case len(command.Args) == 3 && reflect.DeepEqual(command.Args[:2], []string{"container", "rm"}):
		if fixture.state.Running || command.Args[2] != fixture.state.ID {
			fixture.t.Fatal("unsafe removal of retained attempt")
		}
		fixture.removals++
		fixture.state = privateWorkloadContainerV1{}
		return nil
	case reflect.DeepEqual(command.Args, []string{"compose", "down"}):
		fixture.removals++
		fixture.state = privateWorkloadContainerV1{}
		return nil
	default:
		fixture.t.Fatalf("unexpected/destructive engine command: %v", command.Args)
		return nil
	}
}

func (fixture *workloadLifecycleFixtureV1) inject(ctx context.Context, _ string, id string, _ ApplicationSandboxPlanV1, _ privateWorkloadEnvironmentV1, _ RunOptions, _ commandRunner) error {
	fixture.injections++
	if id != fixture.state.ID {
		fixture.t.Fatal("private transfer lost its exact container ID")
	}
	record, found, err := fixture.operation.ReadWorkloadLifecycleV1()
	if err != nil || !found || !record.TransferClaimed || record.State != "starting" || record.ContainerID != id || record.StartedAt != fixture.state.StartedAt {
		fixture.t.Fatal("transfer preceded its durable claim", record, err)
	}
	return fixture.injectError
}

func (fixture *workloadLifecycleFixtureV1) start(ctx context.Context) error {
	_, err := startAndInjectPrivateWorkloadEnvironmentWithV1(ctx, fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment, RunOptions{}, fixture.run, fixture.inject)
	return err
}

func TestPrivateEnvironmentStartupRequiresControllerProofV1(t *testing.T) {
	for _, state := range []string{"up", "starting", "stopping", "down", "missing"} {
		t.Run(state, func(t *testing.T) {
			fixture := newWorkloadLifecycleFixtureV1(t)
			if state == "missing" {
				fixture.state = privateWorkloadContainerV1{ID: strings.Repeat("a", 64), Running: true, StartedAt: "2026-10-22T00:00:00Z"}
			} else {
				fixture.prime(state)
			}
			err := fixture.start(t.Context())
			if (err == nil) != (state == "up") || fixture.starts != 0 || fixture.injections != 0 || fixture.stops != 0 {
				t.Fatalf("state=%s error=%v starts=%d relay=%d stops=%d", state, err, fixture.starts, fixture.injections, fixture.stops)
			}
			if err != nil && (!errors.Is(err, errPrivateWorkloadWasRunning) || !strings.Contains(err.Error(), "reploy restart")) {
				t.Fatal("missing restart guidance", err)
			}
		})
	}
	for _, change := range []string{"endpoint", "runtime", "process"} {
		t.Run(change, func(t *testing.T) {
			fixture := newWorkloadLifecycleFixtureV1(t)
			fixture.prime("up")
			switch change {
			case "endpoint":
				fixture.command = pinDockerEndpointV1(fixture.command, "unix:///other.sock")
			case "runtime":
				fixture.environment.RuntimeBinding = strings.Repeat("c", 64)
			case "process":
				fixture.state.StartedAt = "2026-10-23T00:00:00Z"
			}
			if err := fixture.start(t.Context()); err == nil || fixture.injections != 0 || fixture.stops != 0 {
				t.Fatal("unbound proof reused", err)
			}
		})
	}
}

func TestPrivateEnvironmentFailureStopsAndRetainsOwnedAttemptV1(t *testing.T) {
	for _, kind := range []string{"launch", "relay", "cancel", "stop-unconfirmed"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newWorkloadLifecycleFixtureV1(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch kind {
			case "launch":
				fixture.startError = errors.New("partial compose failure")
			case "relay":
				fixture.injectError = errors.New("private exec failed")
			case "cancel":
				fixture.afterStart = cancel
			case "stop-unconfirmed":
				fixture.injectError = errors.New("private exec failed")
				fixture.keepRunning = true
			}
			fixture.onStop = func() {
				record, _, err := fixture.operation.ReadWorkloadLifecycleV1()
				if err != nil || record.State != "stopping" {
					t.Fatal("shutdown did not expose stopping", record, err)
				}
			}
			if err := fixture.start(ctx); err == nil {
				t.Fatal("failed startup succeeded")
			}
			record, found, err := fixture.operation.ReadWorkloadLifecycleV1()
			expected := "down"
			if fixture.keepRunning {
				expected = "stopping"
			}
			if err != nil || !found || record.State != expected || fixture.stops != 1 || fixture.removals != 0 || fixture.state.ID == "" {
				t.Fatal("failed startup lost diagnosis or state", record, err)
			}
			previous := fixture.injections
			if fixture.state.Running {
				if err := fixture.start(t.Context()); err == nil || fixture.injections != previous {
					t.Fatal("uncertain startup received another transfer", err)
				}
			}
		})
	}
}

func TestPrivateEnvironmentFreshStartupUsesNewProcessClaimV1(t *testing.T) {
	fixture := newWorkloadLifecycleFixtureV1(t)
	if err := fixture.start(t.Context()); err != nil {
		t.Fatal(err)
	}
	record, _, err := fixture.operation.ReadWorkloadLifecycleV1()
	if err != nil || !record.HandoffComplete || record.State != "starting" {
		t.Fatal("handoff incorrectly claimed full readiness", record, err)
	}
	record.State, record.StartupComplete = "up", true
	if err := fixture.operation.WriteWorkloadLifecycleV1(record); err != nil {
		t.Fatal(err)
	}
	fixture.environment.Payload = []byte("TOKEN=rotated\n\n")
	if err := fixture.start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fixture.injections != 1 || fixture.starts != 1 {
		t.Fatal("rotation or repeated up resent private values")
	}
	fixture.state.Running = false
	if err := fixture.start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fixture.injections != 2 {
		t.Fatal("fresh startup did not receive its first transfer")
	}
}

func TestNonPrivateStartupRequiresApplicationLaunchProofV1(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(fmt.Sprint(ready), func(t *testing.T) {
			fixture := newWorkloadLifecycleFixtureV1(t)
			fixture.environment.Present = false
			fixture.launchProof = ready
			err := fixture.start(t.Context())
			if (err == nil) != ready {
				t.Fatalf("launch ready=%t error=%v", ready, err)
			}
			if fixture.injections != 0 || fixture.launchProofCalls != 1 {
				t.Fatal("non-private startup replayed secret transfer or skipped launch proof")
			}
			if !ready && (fixture.state.Running || fixture.stops != 1 || fixture.removals != 0) {
				t.Fatal("failed launch did not retain its stopped owned attempt")
			}
		})
	}
}

func TestNonPrivateCompletedRecordDoesNotReplayLaunchProofV1(t *testing.T) {
	fixture := newWorkloadLifecycleFixtureV1(t)
	fixture.environment.Present = false
	fixture.prime("up")
	lifecycle := &workloadLifecycleV1{command: fixture.command, containerName: "demo", run: fixture.run, operation: func(_ context.Context, action func(*deploy.OperationLock) error) error {
		return action(fixture.operation)
	}}
	reused, changed, err := lifecycle.initialize(t.Context(), fixture.environment.RuntimeBinding, false)
	if err != nil || !reused || changed || fixture.launchProofCalls != 0 {
		t.Fatal("completed process needed another launch proof", reused, changed, err)
	}
	// An interrupted launch must still be observable so down/restart can recover.
	fixture.prime("starting")
	reused, changed, err = lifecycle.initialize(t.Context(), fixture.environment.RuntimeBinding, false)
	if err != nil || reused || changed || fixture.launchProofCalls != 0 {
		t.Fatal("incomplete startup blocked recovery", err)
	}
}

func TestConfirmedWorkloadSurvivesPrivateEnvironmentPresenceTransitionsV1(t *testing.T) {
	for _, original := range []bool{false, true} {
		for _, current := range []bool{false, true} {
			t.Run(fmt.Sprintf("%t-to-%t", original, current), func(t *testing.T) {
				fixture := newWorkloadLifecycleFixtureV1(t)
				fixture.environment.Present = original
				fixture.prime("up")
				if !original {
					record, _, err := fixture.operation.ReadWorkloadLifecycleV1()
					if err != nil {
						t.Fatal(err)
					}
					record.TransferClaimed, record.HandoffComplete = false, false
					if err := fixture.operation.WriteWorkloadLifecycleV1(record); err != nil {
						t.Fatal(err)
					}
				}
				lifecycle := &workloadLifecycleV1{
					command: fixture.command, containerName: "demo", run: fixture.run,
					operation: func(_ context.Context, action func(*deploy.OperationLock) error) error {
						return action(fixture.operation)
					},
				}
				reused, changed, err := lifecycle.initialize(t.Context(), fixture.environment.RuntimeBinding, current)
				if err != nil || !reused || changed || fixture.starts != 0 || fixture.injections != 0 {
					t.Fatalf("presence transition did not preserve confirmed process: reused=%t changed=%t starts=%d relays=%d err=%v", reused, changed, fixture.starts, fixture.injections, err)
				}
				fixture.environment.Present = current
				if err := fixture.start(t.Context()); err != nil || fixture.starts != 0 || fixture.injections != 0 {
					t.Fatalf("direct startup path changed confirmed process: starts=%d relays=%d err=%v", fixture.starts, fixture.injections, err)
				}
			})
		}
	}
}

func TestWorkloadLifecycleHooksStateAndFailureV1(t *testing.T) {
	for _, failure := range []string{"", "before_stop", "after_stop", "after_start", "readiness", "partial-network"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newWorkloadLifecycleFixtureV1(t)
			if failure == "partial-network" {
				fixture.startError = errors.New("network demo not found")
			}
			order := []string{}
			command := ResolvedEnvironmentCommand{Name: "check", Argv: []string{"/bin/true"}}
			lifecycle := LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleCommand, Event: "before_start", Command: &command}, {Kind: LifecycleStart, Event: "start"}, {Kind: LifecycleReadiness, Event: "after_start", Endpoint: &EndpointExecutionPlan{}}, {Kind: LifecycleCommand, Event: "after_start", Command: &command}}}
			backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
			backend.command = func(_ string, action string) (CommandSpec, error) {
				result := fixture.command
				result.Args = []string{"compose", action}
				return result, nil
			}
			backend.bindCommand = func(_ context.Context, spec CommandSpec, _ time.Duration) (CommandSpec, commandRunner, error) {
				return spec, fixture.run, nil
			}
			backend.inject = fixture.inject
			input := CurrentWorkloadLifecycleInputV1{Operation: fixture.operation, DeploymentDir: filepath.Dir(filepath.Dir(fixture.operation.Path())), Environment: "demo", Action: "up", PrivateEnvironment: fixture.environment, Plan: CurrentRuntimePlanV1{Document: blueprint.Document{Environment: blueprint.Environment{Commands: map[string]blueprint.Command{"check": {}}}}, Docker: DockerExecutionPlan{DeploymentDir: filepath.Dir(filepath.Dir(fixture.operation.Path())), ContainerName: "demo", Sandbox: testApplicationSandboxPlanV1(1000, 1000), PrivateEnvironment: true, Workload: &WorkloadExecutionPlan{Argv: []string{"/bin/true"}}}}}
			backend.runTemporary = func(temporaryCommandRunner, CommandSpec, CommandSpec, RunOptions) error {
				order = append(order, "hook")
				if failure == "after_start" && fixture.starts > 0 {
					return errors.New("after_start failure")
				}
				return nil
			}
			backend.readiness = func(context.Context, EndpointExecutionPlan, func(context.Context) error) error {
				if failure == "readiness" {
					return errors.New("readiness failure")
				}
				return nil
			}
			err := runCurrentWorkloadLifecycleV1(t.Context(), input, backend)
			if failure == "after_start" || failure == "readiness" || failure == "partial-network" {
				record, _, readErr := fixture.operation.ReadWorkloadLifecycleV1()
				if err == nil || readErr != nil || record.State != "down" || fixture.state.Running || fixture.state.ID == "" || fixture.removals != 0 {
					t.Fatal("failed checks left an up workload or removed logs", err, record, readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			before := len(order)
			input.Plan.Docker.Sandbox.RuntimeUser.Warnings = []string{"revised presentation only"}
			if err := runCurrentWorkloadLifecycleV1(t.Context(), input, backend); err != nil {
				t.Fatal(err)
			}
			if fixture.injections != 1 || fixture.starts != 1 || len(order) != before+1 {
				t.Fatal("repeated up replayed hooks, Compose, or secrets", order)
			}
			backend.planStop = func(CurrentRuntimePlanV1, CurrentBuild) (LifecyclePlan, error) {
				return LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleCommand, Event: "before_stop", Command: &command}, {Kind: LifecycleStop, Event: "stop"}, {Kind: LifecycleCommand, Event: "after_stop", Command: &command}}}, nil
			}
			hooks := 0
			backend.runTemporary = func(temporaryCommandRunner, CommandSpec, CommandSpec, RunOptions) error {
				hooks++
				if failure == "before_stop" && hooks == 1 || failure == "after_stop" && hooks == 2 {
					return errors.New("stop hook veto/error")
				}
				return nil
			}
			input.Action = "down"
			err = runCurrentWorkloadLifecycleV1(t.Context(), input, backend)
			record, _, readErr := fixture.operation.ReadWorkloadLifecycleV1()
			expected := "down"
			if failure == "before_stop" {
				expected = "up"
			}
			if (err != nil) != (failure != "") || readErr != nil || record.State != expected {
				t.Fatal("stop hook changed confirmed state", err, record, readErr)
			}
			if failure != "before_stop" {
				before = hooks
				if err := runCurrentWorkloadLifecycleV1(t.Context(), input, backend); err != nil || hooks != before {
					t.Fatal("already-down operation replayed hooks", err)
				}
			}
		})
	}
}

func TestPrivateEnvironmentStartupRejectsUnprovedContainerIdentity(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, test := range []struct {
		name       string
		output     string
		diagnostic string
	}{
		{name: "missing fields", output: `{}`},
		{name: "missing running flag", output: fmt.Sprintf(`{"id":%q,"started_at":""}`, id)},
		{name: "null running flag", output: fmt.Sprintf(`{"id":%q,"running":null,"started_at":""}`, id)},
		{name: "invalid ID", output: `{"id":"invalid","running":false,"started_at":""}`},
		{name: "noncanonical ID", output: fmt.Sprintf(`{"id":%q,"running":false,"started_at":""}`, strings.ToUpper(id))},
		{name: "invalid start time", output: fmt.Sprintf(`{"id":%q,"running":true,"started_at":"synthetic-private-value"}`, id)},
		{name: "unknown fields", output: fmt.Sprintf(`{"id":%q,"running":false,"started_at":"","extra":true}`, id)},
		{name: "trailing object", output: fmt.Sprintf(`{"id":%q,"running":false,"started_at":""}{}`, id)},
		{name: "permission error", diagnostic: "Error response from daemon: permission denied"},
		{name: "connection error", diagnostic: "Error response from daemon: cannot connect to /no such container.sock"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutations := 0
			run := func(spec CommandSpec, options RunOptions) error {
				if len(spec.Args) > 1 && spec.Args[0] == "container" && spec.Args[1] == "inspect" {
					if test.diagnostic != "" {
						_, _ = fmt.Fprintln(options.Stderr, test.diagnostic)
						return errors.New("inspection failed")
					}
					_, err := fmt.Fprint(options.Stdout, test.output)
					return err
				}
				mutations++
				return nil
			}
			fixture := newWorkloadLifecycleFixtureV1(t)
			_, err := startAndInjectPrivateWorkloadEnvironmentWithV1(t.Context(), fixture.command,
				"demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment, RunOptions{}, run,
				func(context.Context, string, string, ApplicationSandboxPlanV1, privateWorkloadEnvironmentV1, RunOptions, commandRunner) error {
					mutations++
					return nil
				})
			if err == nil || mutations != 0 || strings.Contains(err.Error(), "synthetic-private-value") {
				t.Fatalf("unproved identity: error=%v mutations=%d", err, mutations)
			}
		})
	}
}

func TestFailedStartupCleanupRequiresHeldOperationAuthorityV1(t *testing.T) {
	fixture := newWorkloadLifecycleFixtureV1(t)
	fixture.prime("starting")
	record, _, err := fixture.operation.ReadWorkloadLifecycleV1()
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	calls := 0
	run := func(CommandSpec, RunOptions) error { calls++; return nil }
	result := privateWorkloadStartResultV1{ContainerID: fixture.state.ID, StartedAt: fixture.state.StartedAt, Owned: true}
	err = stopFailedPrivateWorkloadStartV1(t.Context(), fixture.command, result, fixture.operation, record, RunOptions{}, run)
	if err == nil || calls != 0 {
		t.Fatal("lost operation authority permitted an engine mutation", err, calls)
	}
}

func TestStartupRollbackBoundsLockAcquisitionIndependentlyOfCallerV1(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cause := errors.New("startup checks failed")
	calls := 0
	lifecycle := &workloadLifecycleV1{started: privateWorkloadStartResultV1{Owned: true}, operation: func(cleanup context.Context, _ func(*deploy.OperationLock) error) error {
		calls++
		deadline, ok := cleanup.Deadline()
		if cleanup.Err() != nil || !ok || time.Until(deadline) > privateWorkloadEnvironmentTimeoutV1 {
			t.Fatal("rollback lock wait inherited cancellation or lost its deadline")
		}
		return context.DeadlineExceeded
	}}
	err := lifecycle.rollback(ctx, cause)
	if calls != 1 || !errors.Is(err, cause) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("rollback lost failure or cleanup evidence", err)
	}
}

func TestStartupCleanupCannotExtendAnExistingDeadlineV1(t *testing.T) {
	fixture := newWorkloadLifecycleFixtureV1(t)
	fixture.prime("starting")
	record, _, err := fixture.operation.ReadWorkloadLifecycleV1()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	run := func(command CommandSpec, options RunOptions) error {
		observed, ok := options.Context.Deadline()
		if !ok || observed.After(deadline) {
			t.Fatal("engine cleanup extended the existing deadline")
		}
		return fixture.run(command, options)
	}
	result := privateWorkloadStartResultV1{ContainerID: fixture.state.ID, StartedAt: fixture.state.StartedAt, Owned: true}
	if err := stopFailedPrivateWorkloadStartV1(ctx, fixture.command, result, fixture.operation, record, RunOptions{}, run); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryStopRequiresObservedShutdownV1(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, name := range []string{"absent", "stopped", "duplicate", "running", "recorded-running", "observe-error", "invalid-id"} {
		t.Run(name, func(t *testing.T) {
			fixture := newWorkloadLifecycleFixtureV1(t)
			output, recorded := "", ""
			if name != "absent" {
				fixture.state = privateWorkloadContainerV1{ID: id, StartedAt: "2026-10-22T00:00:00Z"}
				output = id + "\n"
			}
			if name == "duplicate" {
				output += id + "\n"
			}
			if name == "running" || name == "recorded-running" {
				fixture.state.Running = true
			}
			if name == "recorded-running" {
				output = ""
				recorded = id
			}
			if name == "invalid-id" {
				output = "--invalid-identity\n"
			}
			inspections := 0
			run := func(command CommandSpec, options RunOptions) error {
				if reflect.DeepEqual(command.Args, []string{"compose", "ps", "--all", "--quiet"}) {
					_, err := fmt.Fprint(options.Stdout, output)
					return err
				}
				inspections++
				if name == "observe-error" {
					return errors.New("engine observation failed")
				}
				return fixture.run(command, options)
			}
			command := fixture.command
			command.Args = []string{"compose", "ps", "--all", "--quiet"}
			err := confirmRecordedWorkloadStoppedV1(t.Context(), command, recorded, RunOptions{}, run)
			bad := name == "running" || name == "recorded-running" || name == "observe-error" || name == "invalid-id"
			if (err != nil) != bad {
				t.Fatal("command success replaced shutdown evidence", name, err)
			}
			if name == "duplicate" && inspections != 1 {
				t.Fatal("duplicate identity was inspected as distinct ownership")
			}
		})
	}
}

func TestInterruptedStartupAndStopResumeRespectHookPrerequisitesV1(t *testing.T) {
	for _, kind := range []string{"unfinished-start", "unfinished-stop", "already-down"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newWorkloadLifecycleFixtureV1(t)
			fixture.prime("up")
			record, _, err := fixture.operation.ReadWorkloadLifecycleV1()
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unfinished-start":
				record.State = "starting"
				record.StartupComplete = false
			case "unfinished-stop":
				record.State = "stopping"
			case "already-down":
				record.State = "down"
				fixture.state.Running = false
			}
			if err := fixture.operation.WriteWorkloadLifecycleV1(record); err != nil {
				t.Fatal(err)
			}
			before := ResolvedEnvironmentCommand{Name: "before", Argv: []string{"/bin/true"}}
			after := ResolvedEnvironmentCommand{Name: "after", Argv: []string{"/bin/true"}}
			plan := LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleCommand, Event: "before_stop", Command: &before}, {Kind: LifecycleStop, Event: "stop"}, {Kind: LifecycleCommand, Event: "after_stop", Command: &after}}}
			order := []string{}
			backend := currentWorkloadLifecycleTestBackend(t, plan, &order)
			backend.command = func(_ string, action string) (CommandSpec, error) {
				command := fixture.command
				command.Args = []string{"compose", action}
				return command, nil
			}
			backend.bindCommand = func(_ context.Context, spec CommandSpec, _ time.Duration) (CommandSpec, commandRunner, error) {
				return spec, fixture.run, nil
			}
			hooks := []string{}
			backend.transient = func(_ DockerExecutionPlan, command ResolvedEnvironmentCommand, _ *transientOutputMount, _ bool, _ bool) (CommandSpec, error) {
				return CommandSpec{Name: command.Name}, nil
			}
			backend.runTemporary = func(_ temporaryCommandRunner, command CommandSpec, _ CommandSpec, _ RunOptions) error {
				hooks = append(hooks, command.Name)
				return nil
			}
			input := CurrentWorkloadLifecycleInputV1{Operation: fixture.operation, DeploymentDir: filepath.Dir(filepath.Dir(fixture.operation.Path())), Environment: "demo", Action: "down", Plan: CurrentRuntimePlanV1{Document: blueprint.Document{Environment: blueprint.Environment{Commands: map[string]blueprint.Command{"before": {}, "after": {}}}}, Docker: DockerExecutionPlan{DeploymentDir: filepath.Dir(filepath.Dir(fixture.operation.Path())), ContainerName: "demo", Sandbox: testApplicationSandboxPlanV1(1000, 1000), Workload: &WorkloadExecutionPlan{Argv: []string{"/bin/true"}}}}}
			if err := runCurrentWorkloadLifecycleV1(t.Context(), input, backend); err != nil {
				t.Fatal(err)
			}
			wanted := []string{}
			if kind == "unfinished-stop" {
				wanted = []string{"after"}
			}
			if !reflect.DeepEqual(hooks, wanted) {
				t.Fatal("recovery replayed a completed hook or ran hooks without startup", hooks)
			}
		})
	}
}

func TestFailedStartupStillStopsWhenJournalIOFailsV1(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unprivileged symlink fixture requires Unix")
	}
	fixture := newWorkloadLifecycleFixtureV1(t)
	fixture.prime("starting")
	lifecycle := &workloadLifecycleV1{command: fixture.command, containerName: "demo", run: fixture.run, operation: func(_ context.Context, action func(*deploy.OperationLock) error) error {
		return action(fixture.operation)
	}}
	if _, _, err := lifecycle.initialize(t.Context(), fixture.environment.RuntimeBinding, true); err != nil {
		t.Fatal(err)
	}
	lifecycle.started = privateWorkloadStartResultV1{ContainerID: fixture.state.ID, StartedAt: fixture.state.StartedAt, Owned: true}
	path := filepath.Join(filepath.Dir(fixture.operation.Path()), "workload-lifecycle.json")
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("after_start failed")
	err := lifecycle.rollback(t.Context(), cause)
	if !errors.Is(err, cause) || fixture.stops != 1 || fixture.state.Running || fixture.removals != 0 {
		t.Fatal("metadata error abandoned an owned failed startup", err)
	}
	content, readErr := os.ReadFile(target)
	if readErr != nil || string(content) != "unchanged" {
		t.Fatal("cleanup followed an unsafe metadata path")
	}
}

func TestWorkloadRuntimeBindingTracksEffectiveInputsV1(t *testing.T) {
	plan := DockerExecutionPlan{DeploymentDir: t.TempDir(), ContainerName: "demo", Sandbox: testApplicationSandboxPlanV1(1000, 1000), Workload: &WorkloadExecutionPlan{Argv: []string{"/bin/true"}}}
	initial, err := workloadRuntimeBindingV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Sandbox.RuntimeUser.Warnings = []string{"presentation only"}
	unchanged, err := workloadRuntimeBindingV1(plan)
	if err != nil || unchanged != initial {
		t.Fatal("presentation changed runtime identity", err)
	}
	plan.PrivateEnvironment = true
	privateMode, err := workloadRuntimeBindingV1(plan)
	if err != nil || privateMode != initial {
		t.Fatal("private environment presence changed runtime identity", err)
	}
	plan.Workload.Argv = []string{"/bin/false"}
	changed, err := workloadRuntimeBindingV1(plan)
	if err != nil || changed == initial {
		t.Fatal("changed application retained runtime identity", err)
	}
}
