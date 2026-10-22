package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestPrivateEnvironmentRepeatedUpPreservesRunningWorkload(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, LifecyclePlan{
		Operations: []LifecycleOperation{{Kind: LifecycleStart, Event: "start"}},
	}, &order)
	original := backend.runCommand
	backend.runCommand = func(spec CommandSpec, options RunOptions) error {
		if len(spec.Args) >= 2 && spec.Args[0] == "container" && spec.Args[1] == "inspect" {
			_, err := fmt.Fprintf(options.Stdout, `{"id":%q,"running":true,"started_at":"2026-10-20T00:00:00Z"}`, strings.Repeat("a", 64))
			return err
		}
		return original(spec, options)
	}
	backend.bindCommand = func(_ context.Context, start CommandSpec, _ time.Duration) (CommandSpec, commandRunner, error) {
		return start, backend.runCommand, nil
	}
	injections := 0
	backend.inject = func(context.Context, string, string, ApplicationSandboxPlanV1, privateWorkloadEnvironmentV1, RunOptions, commandRunner) error {
		injections++
		return errors.New("attempted private reinjection into a preexisting process")
	}
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "up",
		Plan:               CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo", PrivateEnvironment: true}},
		PrivateEnvironment: privateWorkloadEnvironmentV1{Present: true, Payload: []byte("REPRO_MARKER=synthetic\n\n")},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	if injections != 0 || containsRuntimeObservationStep(order, "command down", "run compose-down") {
		t.Fatal("repeated up reinjected or removed the existing workload")
	}
}

func TestPrivateEnvironmentStartupOwnsOnlyNewOrStartedContainers(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	running := privateWorkloadContainerV1{ID: a, Running: true, StartedAt: "2026-10-20T00:00:00Z"}
	stopped := running
	stopped.Running = false
	restarted := running
	restarted.StartedAt = "2026-10-21T00:00:00Z"
	replaced := restarted
	replaced.ID = b
	exited := replaced
	exited.Running = false
	for _, test := range []struct {
		name        string
		before      privateWorkloadContainerV1
		after       privateWorkloadContainerV1
		injectError bool
		startError  bool
		cancelStart bool
		wantError   bool
		wantInject  int
		wantRemoval []string
	}{
		{name: "fresh startup", after: running, wantInject: 1},
		{name: "reuse healthy process", before: running, after: running},
		{name: "start stopped container", before: stopped, after: restarted, wantInject: 1},
		{name: "recreate running container", before: running, after: replaced, wantInject: 1},
		{name: "fresh relay failure", after: running, injectError: true, wantError: true, wantInject: 1, wantRemoval: []string{a}},
		{name: "recreation relay failure", before: running, after: replaced, injectError: true, wantError: true, wantInject: 1, wantRemoval: []string{b}},
		{name: "concurrent restart", before: running, after: restarted, wantError: true},
		{name: "existing container stopped", before: running, after: stopped, wantError: true},
		{name: "new container exited", after: exited, wantError: true, wantRemoval: []string{b}},
		{name: "existing process launch failure", before: running, startError: true, wantError: true},
		{name: "fresh launch failure", startError: true, wantError: true},
		{name: "partial fresh launch failure", after: running, startError: true, wantError: true, wantRemoval: []string{a}},
		{name: "partial stopped launch failure", before: stopped, after: restarted, startError: true, wantError: true, wantRemoval: []string{a}},
		{name: "failed launch preserves running process", before: running, after: running, startError: true, wantError: true},
		{name: "cancel immediately after startup", after: running, cancelStart: true, wantError: true, wantRemoval: []string{a}},
		{name: "cancelled partial startup", after: running, startError: true, cancelStart: true, wantError: true, wantRemoval: []string{a}},
		{name: "cancel preserves running process", before: running, after: running, cancelStart: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			inspections, injections := 0, 0
			var removed []string
			run := func(spec CommandSpec, options RunOptions) error {
				if options.Context == nil || options.Stdin != nil {
					t.Fatal("non-relay command inherited private input or lost its context")
				}
				switch {
				case len(spec.Args) > 1 && spec.Args[0] == "container" && spec.Args[1] == "inspect":
					state := test.before
					if inspections != 0 {
						state = test.after
					}
					inspections++
					if inspections > 1 && options.Context.Err() != nil {
						t.Fatal("ownership inspection inherited cancellation")
					}
					if state.ID == "" {
						_, _ = fmt.Fprintln(options.Stderr, "Error response from daemon: No such container: demo")
						return errors.New("container missing")
					}
					return json.NewEncoder(options.Stdout).Encode(map[string]any{
						"id": state.ID, "running": state.Running, "started_at": state.StartedAt,
					})
				case reflect.DeepEqual(spec.Args, []string{"compose", "up"}):
					if test.cancelStart {
						cancel()
					}
					if test.startError {
						return errors.New("network demo not found")
					}
					return nil
				case len(spec.Args) == 4 && reflect.DeepEqual(spec.Args[:3], []string{"container", "rm", "--force"}):
					removed = append(removed, spec.Args[3])
					if options.Context.Err() != nil {
						t.Fatal("cleanup inherited a cancelled context")
					}
					return nil
				default:
					t.Fatal("unexpected or destructive command", spec.Args)
					return nil
				}
			}
			_, err := startAndInjectPrivateWorkloadEnvironmentWithV1(ctx, CommandSpec{Name: "docker", Args: []string{"compose", "up"}},
				"demo", testApplicationSandboxPlanV1(1000, 1000),
				privateWorkloadEnvironmentV1{Present: true, Payload: []byte("REPRO_MARKER=synthetic\n\n")},
				RunOptions{}, run,
				func(_ context.Context, _ string, id string, _ ApplicationSandboxPlanV1, _ privateWorkloadEnvironmentV1, _ RunOptions, _ commandRunner) error {
					injections++
					if id != test.after.ID {
						t.Fatal("relay did not target the inspected immutable container ID")
					}
					if test.injectError {
						return errors.New("relay failed")
					}
					return nil
				})
			if (err != nil) != test.wantError || injections != test.wantInject || !reflect.DeepEqual(removed, test.wantRemoval) {
				t.Fatalf("startup error=%v injections=%d removed=%v", err, injections, removed)
			}
			if test.cancelStart && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost launch cancellation: %v", err)
			}
			if test.before.Running && test.wantError && !errors.Is(err, errPrivateWorkloadWasRunning) {
				t.Fatal("startup lost the guard against destructive stale-network recovery")
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
			_, err := startAndInjectPrivateWorkloadEnvironmentWithV1(t.Context(), CommandSpec{Name: "docker", Args: []string{"compose", "up"}},
				"demo", testApplicationSandboxPlanV1(1000, 1000), privateWorkloadEnvironmentV1{Present: true}, RunOptions{}, run,
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

func TestPrivateEnvironmentCancellationRemovesOnlyOwnedRelayContainer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	id := strings.Repeat("a", 64)
	inspections, removals := 0, 0
	run := func(spec CommandSpec, options RunOptions) error {
		switch {
		case len(spec.Args) > 1 && spec.Args[0] == "container" && spec.Args[1] == "inspect":
			inspections++
			if inspections == 1 {
				_, _ = fmt.Fprintln(options.Stderr, "Error response from daemon: No such container: demo")
				return errors.New("container missing")
			}
			_, err := fmt.Fprintf(options.Stdout, `{"id":%q,"running":true,"started_at":"2026-10-20T00:00:00Z"}`, id)
			return err
		case len(spec.Args) > 0 && spec.Args[0] == "exec":
			deadline, ok := options.Context.Deadline()
			if !ok || time.Until(deadline) > 30*time.Second || options.Stdin == nil || options.Stdout != nil || options.Stderr != nil {
				t.Fatal("relay lost its deadline or private stdin boundary")
			}
			cancel()
			<-options.Context.Done()
			return options.Context.Err()
		case reflect.DeepEqual(spec.Args, []string{"container", "rm", "--force", id}):
			removals++
			if options.Context.Err() != nil || options.Stdin != nil {
				t.Fatal("cleanup used cancelled context or private input")
			}
			return nil
		case reflect.DeepEqual(spec.Args, []string{"compose", "up"}):
			return nil
		default:
			t.Fatal("unexpected destructive command", spec.Args)
			return nil
		}
	}
	err := startAndInjectPrivateWorkloadEnvironmentV1(ctx, CommandSpec{Name: "docker", Args: []string{"compose", "up"}},
		"demo", testApplicationSandboxPlanV1(1000, 1000),
		privateWorkloadEnvironmentV1{Present: true, Payload: []byte("REPRO_MARKER=synthetic\n\n")}, RunOptions{}, run)
	if !errors.Is(err, context.Canceled) || removals != 1 {
		t.Fatalf("cancelled startup: error=%v removals=%d", err, removals)
	}
}
