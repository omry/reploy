package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestInstalledServiceStartupTimeoutReportsHeavyCustomEventsWithoutArguments(t *testing.T) {
	var output bytes.Buffer
	ctx, timing, finish := newInstalledServiceStartupBudgetV1(t.Context(), 5*time.Second, 75*time.Second, 30*time.Millisecond, &output)
	defer finish()
	plan := LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleCommand, Event: "before_start", Command: &ResolvedEnvironmentCommand{Name: "prepare-data", Argv: []string{"private-argument-value"}}},
		{Kind: LifecycleStart, Event: "start"},
	}}
	starts := 0
	err := ExecuteLifecycle(ctx, plan, LifecycleExecutor{
		beforeOperation: timing.lifecycleStep,
		RunCommand:      func(ctx context.Context, _ ResolvedEnvironmentCommand) error { <-ctx.Done(); return ctx.Err() },
		Start:           func(context.Context) error { starts++; return nil },
	})
	err = timing.timeoutError(ctx, err)
	finish() // Joins the diagnostic callback before reading the writer.
	if !errors.Is(err, context.DeadlineExceeded) || starts != 0 {
		t.Fatalf("starts=%d error=%v", starts, err)
	}
	for _, value := range []string{"preparation=1m20s (limit 1m30s)", "lock/setup=5s", "engine wait=1m15s", "application startup=", `before_start[1] command "prepare-data"`, "30ms"} {
		if !strings.Contains(err.Error(), value) || !strings.Contains(output.String(), value) {
			t.Fatalf("missing %q: error=%v output=%q", value, err, output.String())
		}
	}
	if strings.Contains(err.Error()+output.String(), "private-argument-value") {
		t.Fatal("startup timing exposed a command argument")
	}
}

func TestInstalledServiceStartupBudgetIsAggregateAndChecksCallbacksIgnoringCancellation(t *testing.T) {
	ctx, timing, finish := newInstalledServiceStartupBudgetV1(t.Context(), 0, 80*time.Second, 40*time.Millisecond, nil)
	defer finish()
	plan := LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleCommand, Event: "before_start", Command: &ResolvedEnvironmentCommand{Name: "first"}},
		{Kind: LifecycleStart, Event: "start"},
		{Kind: LifecycleCommand, Event: "after_start", Command: &ResolvedEnvironmentCommand{Name: "last"}},
	}}
	commands, starts := 0, 0
	err := ExecuteLifecycle(ctx, plan, LifecycleExecutor{
		beforeOperation: timing.lifecycleStep,
		RunCommand:      func(context.Context, ResolvedEnvironmentCommand) error { commands++; return nil },
		Start:           func(ctx context.Context) error { starts++; <-ctx.Done(); return nil },
	})
	if !errors.Is(err, context.DeadlineExceeded) || commands != 1 || starts != 1 {
		t.Fatalf("commands=%d starts=%d error=%v", commands, starts, err)
	}
	if !strings.Contains(timing.timeoutError(ctx, err).Error(), "start[2] start") {
		t.Fatal("timing lost the active startup step")
	}
}

func TestInstalledServiceStartupCompletionDoesNotCancelServiceMonitoring(t *testing.T) {
	parent := t.Context()
	var output bytes.Buffer
	ctx, timing, finish := newInstalledServiceStartupBudgetV1(parent, 0, 89*time.Second, time.Second, &output)
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < 500*time.Millisecond {
		t.Fatal("engine delay consumed the independent startup allowance")
	}
	timing.mu.Lock()
	timing.ready = true
	timing.mu.Unlock()
	finish()
	finish() // Explicit completion plus deferred completion must be safe.
	if ctx.Err() != context.Canceled || parent.Err() != nil || output.Len() != 0 {
		t.Fatalf("startup=%v service=%v output=%q", ctx.Err(), parent.Err(), output.String())
	}
	cause := errors.New("container later exited")
	if got := timing.timeoutError(ctx, cause); got != cause {
		t.Fatal("monitoring failure was mislabeled as startup timeout")
	}
}

func TestInstalledServiceStartupParentCancellationIsNotTimeout(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	var output bytes.Buffer
	ctx, timing, finish := newInstalledServiceStartupBudgetV1(parent, 0, 0, time.Second, &output)
	cancel()
	finish()
	if err := timing.timeoutError(ctx, context.Canceled); err != context.Canceled || output.Len() != 0 {
		t.Fatalf("error=%v output=%q", err, output.String())
	}
}

func TestInstalledServiceStartupAfterStartHookUsesSameDeadlineAndReportsEarlierWork(t *testing.T) {
	ctx, timing, finish := newInstalledServiceStartupBudgetV1(t.Context(), 0, 70*time.Second, 50*time.Millisecond, nil)
	defer finish()
	deadline, _ := ctx.Deadline()
	plan := LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleCommand, Event: "before_start", Command: &ResolvedEnvironmentCommand{Name: "prepare"}},
		{Kind: LifecycleStart, Event: "start"},
		{Kind: LifecycleCommand, Event: "after_start", Command: &ResolvedEnvironmentCommand{Name: "migrate"}},
		{Kind: LifecycleReadiness, Event: "after_start", Endpoint: &EndpointExecutionPlan{}},
	}}
	commands, probes := 0, 0
	err := ExecuteLifecycle(ctx, plan, LifecycleExecutor{
		beforeOperation: timing.lifecycleStep,
		RunCommand: func(ctx context.Context, command ResolvedEnvironmentCommand) error {
			commands++
			if got, _ := ctx.Deadline(); got != deadline {
				t.Fatal("custom event received a renewed startup budget")
			}
			if command.Name == "migrate" {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		},
		Start: func(ctx context.Context) error {
			if got, _ := ctx.Deadline(); got != deadline {
				t.Fatal("container startup received a renewed budget")
			}
			return nil
		},
		Readiness: func(context.Context, EndpointExecutionPlan) error { probes++; return nil },
	})
	message := timing.timeoutError(ctx, err).Error()
	if !errors.Is(err, context.DeadlineExceeded) || commands != 2 || probes != 0 {
		t.Fatalf("commands=%d probes=%d error=%v", commands, probes, err)
	}
	for _, want := range []string{`before_start[1] command "prepare"=`, "start[2] start=", `after_start[3] command "migrate"=`} {
		if !strings.Contains(message, want) {
			t.Fatalf("missing %q: %s", want, message)
		}
	}
}

func TestInstalledServiceStartupEarlyFailureStopsClockBeforeCleanup(t *testing.T) {
	var output bytes.Buffer
	ctx, timing, stop := newInstalledServiceStartupBudgetV1(t.Context(), 0, 0, 30*time.Millisecond, &output)
	timing.step("after_start command")
	cause := errors.New("custom startup event failed")
	cleanup := errors.New("cleanup evidence")
	err := timing.finish(ctx, cause, stop, func(err error) error {
		if ctx.Err() != context.Canceled {
			t.Error("cleanup began while the startup timer was still active")
		}
		time.Sleep(60 * time.Millisecond) // Cleanup can outlast the unused startup budget.
		return errors.Join(err, cleanup)
	})
	if !errors.Is(err, cause) || !errors.Is(err, cleanup) || errors.Is(err, context.DeadlineExceeded) || output.Len() != 0 {
		t.Fatalf("error=%v output=%q", err, output.String())
	}
}

func TestInstalledServiceCustomEventCleanupIsBoundedAndRollbackRemainsReachable(t *testing.T) {
	var output bytes.Buffer
	startupCtx, timing, stop := newInstalledServiceStartupBudgetV1(t.Context(), 0, 0, 25*time.Millisecond, &output)
	defer stop()
	fixture := newWorkloadLifecycleFixtureV1(t)
	fixture.prime("starting")
	order := []string{}
	rollbackDeadline := time.Time{}
	lifecycle := ownedWorkloadLifecycleForInstalledCleanupTest(t, fixture, &order, &rollbackDeadline)
	cleanupDeadline := time.Time{}
	createdName, removedName := "", ""
	var deferredCleanup func(context.Context) error
	originalRuntimeCommand := runRuntimeCommand
	t.Cleanup(func() { runRuntimeCommand = originalRuntimeCommand })
	runRuntimeCommand = func(spec CommandSpec, options RunOptions) error {
		switch {
		case len(spec.Args) > 0 && spec.Args[0] == "run":
			order = append(order, "transient-run")
			createdName = namedTransientContainerV1(spec.Args)
			<-options.Context.Done()
			return options.Context.Err()
		case len(spec.Args) > 1 && spec.Args[0] == "container" && spec.Args[1] == "rm":
			order = append(order, "transient-cleanup")
			removedName = spec.Args[len(spec.Args)-1]
			var ok bool
			cleanupDeadline, ok = options.Context.Deadline()
			if !ok {
				t.Fatal("transient cleanup had no deadline")
			}
			if removedName != createdName {
				return errors.New("Error response from daemon: No such container: " + removedName)
			}
			return nil
		default:
			t.Fatalf("unexpected runtime command: %#v", spec)
			return nil
		}
	}
	executor, command := installedCustomEventExecutorForTest(t, &output, installedServiceCustomEventCleanupHookV1(startupCtx, timing, stop, &deferredCleanup))
	err := executor.RunCommand(startupCtx, command)
	if deferredCleanup == nil {
		t.Fatal("installed executor did not take ownership of transient cleanup")
	}
	err = timing.finish(startupCtx, err, stop, func(cause error) error {
		return cleanupInstalledServiceStartupFailureV1(t.Context(), lifecycle, deferredCleanup, nil, nil, cause)
	})
	if !errors.Is(err, context.DeadlineExceeded) || cleanupDeadline.IsZero() || rollbackDeadline.IsZero() || !cleanupDeadline.Equal(rollbackDeadline) || createdName == "" || removedName != createdName {
		t.Fatalf("error=%v created=%q removed=%q cleanup deadline=%v rollback deadline=%v order=%v output=%q", err, createdName, removedName, cleanupDeadline, rollbackDeadline, order, output.String())
	}
	if len(order) != 3 || order[0] != "transient-run" || order[1] != "rollback-stop" || order[2] != "transient-cleanup" {
		t.Fatalf("cleanup order = %v", order)
	}
}

func TestInstalledServiceCustomEventFailureKeepsOriginalClassificationWhenCleanupOutlastsStartupDeadline(t *testing.T) {
	var output bytes.Buffer
	fixture := newWorkloadLifecycleFixtureV1(t)
	fixture.prime("starting")
	order := []string{}
	rollbackDeadline := time.Time{}
	lifecycle := ownedWorkloadLifecycleForInstalledCleanupTest(t, fixture, &order, &rollbackDeadline)
	startupCtx, timing, stop := newInstalledServiceStartupBudgetV1(t.Context(), 0, 0, 100*time.Millisecond, &output)
	defer stop()
	appDeadline, ok := startupCtx.Deadline()
	if !ok {
		t.Fatal("startup context has no deadline")
	}
	cleanupDeadline := time.Time{}
	cleanupFinished := time.Time{}
	createdName, removedName := "", ""
	cause := errors.New("custom event failed")
	originalRuntimeCommand := runRuntimeCommand
	t.Cleanup(func() { runRuntimeCommand = originalRuntimeCommand })
	runRuntimeCommand = func(spec CommandSpec, options RunOptions) error {
		switch {
		case len(spec.Args) > 0 && spec.Args[0] == "run":
			order = append(order, "transient-run")
			createdName = namedTransientContainerV1(spec.Args)
			return cause
		case len(spec.Args) > 1 && spec.Args[0] == "container" && spec.Args[1] == "rm":
			order = append(order, "transient-cleanup")
			removedName = spec.Args[len(spec.Args)-1]
			var deadlineOK bool
			cleanupDeadline, deadlineOK = options.Context.Deadline()
			if !deadlineOK {
				t.Fatal("transient cleanup had no deadline")
			}
			if removedName != createdName {
				return errors.New("Error response from daemon: No such container: " + removedName)
			}
			for !time.Now().After(appDeadline) {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(5 * time.Millisecond)
			cleanupFinished = time.Now()
			return nil
		default:
			t.Fatalf("unexpected runtime command: %#v", spec)
			return nil
		}
	}
	var deferredCleanup func(context.Context) error
	executor, command := installedCustomEventExecutorForTest(t, &output, installedServiceCustomEventCleanupHookV1(startupCtx, timing, stop, &deferredCleanup))
	err := executor.RunCommand(startupCtx, command)
	if deferredCleanup == nil {
		t.Fatal("installed executor did not take ownership of transient cleanup")
	}
	err = timing.finish(startupCtx, err, stop, func(cause error) error {
		return cleanupInstalledServiceStartupFailureV1(t.Context(), lifecycle, deferredCleanup, nil, nil, cause)
	})
	if !errors.Is(err, cause) || errors.Is(err, context.DeadlineExceeded) || cleanupDeadline.IsZero() || rollbackDeadline.IsZero() || !cleanupFinished.After(appDeadline) || createdName == "" || removedName != createdName {
		t.Fatalf("error=%v created=%q removed=%q cleanup deadline=%v rollback deadline=%v cleanup finished=%v app deadline=%v order=%v", err, createdName, removedName, cleanupDeadline, rollbackDeadline, cleanupFinished, appDeadline, order)
	}
	if strings.Contains(output.String(), "installed service startup timed out") {
		t.Fatalf("ordinary failure emitted startup timeout diagnostic: %q", output.String())
	}
}

func namedTransientContainerV1(args []string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--name" {
			return args[index+1]
		}
	}
	return ""
}

func TestInstalledServicePrivateStartFailureUsesOneCleanupDeadline(t *testing.T) {
	fixture := newWorkloadLifecycleFixtureV1(t)
	composeFailure := errors.New("compose cancelled after creating the workload")
	fixture.startError = composeFailure
	fixture.keepRunning = true
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(t.Context()), privateWorkloadEnvironmentTimeoutV1)
	defer cleanupCancel()
	cleanupDeadline, ok := cleanupCtx.Deadline()
	if !ok {
		t.Fatal("shared cleanup context has no deadline")
	}
	var inspectionDeadlines []time.Time
	hookCalled := false
	run := func(command CommandSpec, options RunOptions) error {
		if len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "inspect" && fixture.starts > 0 {
			deadline, ok := options.Context.Deadline()
			if !ok {
				t.Fatal("cancellation-surviving ownership inspection has no deadline")
			}
			if !hookCalled {
				t.Fatal("ownership inspection started before failure classification was frozen")
			}
			inspectionDeadlines = append(inspectionDeadlines, deadline)
		}
		return fixture.run(command, options)
	}
	started, startErr := startAndInjectPrivateWorkloadEnvironmentWithFailureCleanupHookV1(
		t.Context(), fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment,
		RunOptions{}, run, fixture.inject,
		func(cause error) context.Context {
			if !errors.Is(cause, composeFailure) {
				t.Fatalf("failure hook observed %v, want %v", cause, composeFailure)
			}
			hookCalled = true
			return cleanupCtx
		},
	)
	if !errors.Is(startErr, composeFailure) || !started.Owned {
		t.Fatalf("started=%+v error=%v", started, startErr)
	}
	binding, err := privateWorkloadRecordV1(fixture.command, fixture.environment.RuntimeBinding)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := &workloadLifecycleV1{
		command: fixture.command, containerName: "demo", options: RunOptions{}, operation: func(_ context.Context, action func(*deploy.OperationLock) error) error {
			return action(fixture.operation)
		},
		binding: binding, started: started, run: run,
	}
	rollbackDeadline := time.Time{}
	fixture.onStop = func() {
		rollbackDeadline, ok = cleanupCtx.Deadline()
		if !ok {
			t.Fatal("rollback lost the shared cleanup deadline")
		}
	}
	cleanupErr := cleanupInstalledServiceStartupFailureV1(t.Context(), lifecycle, nil, cleanupCtx, cleanupCancel, startErr)
	if !errors.Is(cleanupErr, composeFailure) || fixture.stops != 1 || rollbackDeadline.IsZero() || !rollbackDeadline.Equal(cleanupDeadline) {
		t.Fatalf("error=%v stops=%d cleanup deadline=%v rollback deadline=%v inspections=%v", cleanupErr, fixture.stops, cleanupDeadline, rollbackDeadline, inspectionDeadlines)
	}
	if len(inspectionDeadlines) == 0 {
		t.Fatal("missing cancellation-surviving ownership inspection")
	}
	for _, deadline := range inspectionDeadlines {
		if deadline.After(cleanupDeadline) {
			t.Fatalf("ownership inspection renewed cleanup deadline: inspection=%v cleanup=%v", deadline, cleanupDeadline)
		}
	}
}

func TestInstalledServicePrivateStartFailuresFreezeClassificationBeforeCleanup(t *testing.T) {
	tests := []struct {
		name       string
		composeErr error
		injectErr  error
		timeout    bool
	}{
		{name: "compose failure", composeErr: errors.New("compose failed before startup deadline")},
		{name: "private injection failure", injectErr: errors.New("private environment injection failed")},
		{name: "true application timeout", timeout: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			fixture := newWorkloadLifecycleFixtureV1(t)
			fixture.startError, fixture.injectError = test.composeErr, test.injectErr
			startupCtx, timing, finish := newInstalledServiceStartupBudgetV1(t.Context(), 0, 0, 100*time.Millisecond, &output)
			defer finish()
			appDeadline, ok := startupCtx.Deadline()
			if !ok {
				t.Fatal("startup context has no deadline")
			}
			if !test.timeout {
				fixture.onStop = func() {
					for time.Now().Before(appDeadline) {
						time.Sleep(time.Millisecond)
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			var failureCleanupContext context.Context
			var failureCleanupCancel context.CancelFunc
			baseHook := installedServiceFailureCleanupHookV1(t.Context(), startupCtx, timing, finish, &failureCleanupContext, &failureCleanupCancel)
			failureHook := func(cause error) context.Context {
				if test.timeout {
					if !errors.Is(cause, context.DeadlineExceeded) {
						t.Fatalf("timeout failure hook observed %v", cause)
					}
				} else {
					want := test.composeErr
					if want == nil {
						want = test.injectErr
					}
					if !errors.Is(cause, want) {
						t.Fatalf("ordinary failure hook observed %v, want %v", cause, want)
					}
				}
				return baseHook(cause)
			}
			run := func(command CommandSpec, options RunOptions) error {
				if test.timeout && len(command.Args) == 2 && command.Args[0] == "compose" && command.Args[1] == "up" {
					if err := fixture.run(command, options); err != nil {
						return err
					}
					<-options.Context.Done()
					return options.Context.Err()
				}
				return fixture.run(command, options)
			}
			started, startErr := startAndInjectPrivateWorkloadEnvironmentWithFailureCleanupHookV1(
				startupCtx, fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment,
				RunOptions{}, run, fixture.inject, failureHook,
			)
			binding, err := privateWorkloadRecordV1(fixture.command, fixture.environment.RuntimeBinding)
			if err != nil {
				t.Fatal(err)
			}
			lifecycle := &workloadLifecycleV1{
				command: fixture.command, containerName: "demo", options: RunOptions{}, operation: func(_ context.Context, action func(*deploy.OperationLock) error) error {
					return action(fixture.operation)
				},
				binding: binding, started: started, run: run,
			}
			finalErr := timing.finish(startupCtx, startErr, finish, func(cause error) error {
				return cleanupInstalledServiceStartupFailureV1(t.Context(), lifecycle, nil, failureCleanupContext, failureCleanupCancel, cause)
			})
			if test.timeout {
				if !errors.Is(finalErr, context.DeadlineExceeded) || !strings.Contains(finalErr.Error(), "installed service application startup timed out") {
					t.Fatalf("true timeout was reclassified: %v", finalErr)
				}
				return
			}
			want := test.composeErr
			if want == nil {
				want = test.injectErr
			}
			if !errors.Is(finalErr, want) || errors.Is(finalErr, context.DeadlineExceeded) || strings.Contains(output.String(), "installed service startup timed out") {
				t.Fatalf("ordinary failure classification changed: error=%v output=%q", finalErr, output.String())
			}
			if fixture.stops != 1 {
				t.Fatalf("ordinary failure cleanup ran %d stops", fixture.stops)
			}
		})
	}
}

func TestPrivateStartInspectionCancellationUsesOneCleanupAllowance(t *testing.T) {
	t.Run("installed deadline uses original application deadline", func(t *testing.T) {
		fixture := newWorkloadLifecycleFixtureV1(t)
		startupCtx, timing, finish := newInstalledServiceStartupBudgetV1(t.Context(), 0, 0, 30*time.Millisecond, nil)
		appDeadline, ok := startupCtx.Deadline()
		if !ok {
			t.Fatal("startup context has no deadline")
		}
		var inspectionDeadline time.Time
		run := func(command CommandSpec, options RunOptions) error {
			if len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "inspect" && fixture.starts > 0 {
				inspectionDeadline, _ = options.Context.Deadline()
				time.Sleep(50 * time.Millisecond)
			}
			return fixture.run(command, options)
		}
		var failureCleanupContext context.Context
		var failureCleanupCancel context.CancelFunc
		hook := installedServiceFailureCleanupHookV1(t.Context(), startupCtx, timing, finish, &failureCleanupContext, &failureCleanupCancel)
		started, startErr := startAndInjectPrivateWorkloadEnvironmentWithFailureCleanupHookV1(
			startupCtx, fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment,
			RunOptions{DockerPreflightTimeout: 45 * time.Second}, run, fixture.inject, hook,
		)
		if !errors.Is(startErr, context.DeadlineExceeded) || !started.Owned {
			t.Fatalf("started=%+v error=%v", started, startErr)
		}
		if inspectionDeadline.IsZero() || inspectionDeadline.After(appDeadline.Add(privateWorkloadEnvironmentTimeoutV1)) {
			t.Fatalf("inspection deadline=%v application deadline=%v", inspectionDeadline, appDeadline)
		}
		order := []string{}
		rollbackDeadline := time.Time{}
		lifecycle := ownedWorkloadLifecycleForInstalledCleanupTest(t, fixture, &order, &rollbackDeadline)
		finalErr := timing.finish(startupCtx, startErr, finish, func(cause error) error {
			return cleanupInstalledServiceStartupFailureV1(t.Context(), lifecycle, nil, failureCleanupContext, failureCleanupCancel, cause)
		})
		wantDeadline := appDeadline.Add(privateWorkloadEnvironmentTimeoutV1)
		if !errors.Is(finalErr, context.DeadlineExceeded) || !rollbackDeadline.Equal(wantDeadline) || fixture.stops != 1 {
			t.Fatalf("error=%v stops=%d rollback deadline=%v want=%v", finalErr, fixture.stops, rollbackDeadline, wantDeadline)
		}
		record, found, err := fixture.operation.ReadWorkloadLifecycleV1()
		if err != nil || !found || record.TransferClaimed || fixture.injections != 0 {
			t.Fatalf("record=%+v found=%t err=%v injections=%d", record, found, err, fixture.injections)
		}
	})

	t.Run("installed cancellation before deadline clamps cleanup to original bound", func(t *testing.T) {
		// Exercise cancellation before expiry and cleanup after expiry in virtual time.
		synctest.Test(t, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture := newWorkloadLifecycleFixtureV1(t)
			startupCtx, timing, finish := newInstalledServiceStartupBudgetV1(parent, 0, 0, 40*time.Millisecond, nil)
			appDeadline, ok := startupCtx.Deadline()
			if !ok {
				t.Fatal("startup context has no deadline")
			}
			canceledAt := time.Time{}
			var inspectionDeadline time.Time
			run := func(command CommandSpec, options RunOptions) error {
				if len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "inspect" && fixture.starts > 0 {
					inspectionDeadline, _ = options.Context.Deadline()
					time.Sleep(10 * time.Millisecond)
					canceledAt = time.Now()
					cancel()
					time.Sleep(60 * time.Millisecond)
				}
				return fixture.run(command, options)
			}
			var failureCleanupContext context.Context
			var failureCleanupCancel context.CancelFunc
			hook := installedServiceFailureCleanupHookV1(parent, startupCtx, timing, finish, &failureCleanupContext, &failureCleanupCancel)
			started, startErr := startAndInjectPrivateWorkloadEnvironmentWithFailureCleanupHookV1(
				startupCtx, fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment,
				RunOptions{DockerPreflightTimeout: 45 * time.Second}, run, fixture.inject, hook,
			)
			if !errors.Is(startErr, context.Canceled) || !started.Owned || canceledAt.IsZero() {
				t.Fatalf("started=%+v error=%v canceled=%v", started, startErr, canceledAt)
			}
			if !canceledAt.Before(appDeadline) || time.Now().Before(appDeadline) {
				t.Fatalf("cancellation=%v deadline=%v cleanup=%v", canceledAt, appDeadline, time.Now())
			}
			if inspectionDeadline.After(appDeadline.Add(privateWorkloadEnvironmentTimeoutV1)) {
				t.Fatalf("inspection deadline=%v exceeds original cleanup bound=%v", inspectionDeadline, appDeadline.Add(privateWorkloadEnvironmentTimeoutV1))
			}
			order := []string{}
			rollbackDeadline := time.Time{}
			lifecycle := ownedWorkloadLifecycleForInstalledCleanupTest(t, fixture, &order, &rollbackDeadline)
			finalErr := timing.finish(startupCtx, startErr, finish, func(cause error) error {
				return cleanupInstalledServiceStartupFailureV1(parent, lifecycle, nil, failureCleanupContext, failureCleanupCancel, cause)
			})
			if !errors.Is(finalErr, context.Canceled) || !rollbackDeadline.Equal(appDeadline.Add(privateWorkloadEnvironmentTimeoutV1)) || fixture.stops != 1 {
				t.Fatalf("error=%v stops=%d canceled=%v rollback deadline=%v want=%v", finalErr, fixture.stops, canceledAt, rollbackDeadline, appDeadline.Add(privateWorkloadEnvironmentTimeoutV1))
			}
			record, found, err := fixture.operation.ReadWorkloadLifecycleV1()
			if err != nil || !found || record.TransferClaimed || fixture.injections != 0 {
				t.Fatalf("record=%+v found=%t err=%v injections=%d", record, found, err, fixture.injections)
			}
		})
	})

	t.Run("installed caller cancellation starts one allowance", func(t *testing.T) {
		parent, cancel := context.WithCancel(t.Context())
		defer cancel()
		fixture := newWorkloadLifecycleFixtureV1(t)
		startupCtx, timing, finish := newInstalledServiceStartupBudgetV1(parent, 0, 0, time.Second, nil)
		canceledAt := time.Time{}
		run := func(command CommandSpec, options RunOptions) error {
			if len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "inspect" && fixture.starts > 0 {
				time.Sleep(10 * time.Millisecond)
				canceledAt = time.Now()
				cancel()
				time.Sleep(20 * time.Millisecond)
			}
			return fixture.run(command, options)
		}
		var failureCleanupContext context.Context
		var failureCleanupCancel context.CancelFunc
		hook := installedServiceFailureCleanupHookV1(parent, startupCtx, timing, finish, &failureCleanupContext, &failureCleanupCancel)
		started, startErr := startAndInjectPrivateWorkloadEnvironmentWithFailureCleanupHookV1(
			startupCtx, fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment,
			RunOptions{}, run, fixture.inject, hook,
		)
		if !errors.Is(startErr, context.Canceled) || !started.Owned || canceledAt.IsZero() {
			t.Fatalf("started=%+v error=%v canceled=%v", started, startErr, canceledAt)
		}
		order := []string{}
		rollbackDeadline := time.Time{}
		lifecycle := ownedWorkloadLifecycleForInstalledCleanupTest(t, fixture, &order, &rollbackDeadline)
		finalErr := timing.finish(startupCtx, startErr, finish, func(cause error) error {
			return cleanupInstalledServiceStartupFailureV1(parent, lifecycle, nil, failureCleanupContext, failureCleanupCancel, cause)
		})
		if !errors.Is(finalErr, context.Canceled) || fixture.stops != 1 || rollbackDeadline.Before(canceledAt) || rollbackDeadline.After(canceledAt.Add(privateWorkloadEnvironmentTimeoutV1+200*time.Millisecond)) {
			t.Fatalf("error=%v stops=%d canceled=%v rollback deadline=%v", finalErr, fixture.stops, canceledAt, rollbackDeadline)
		}
		record, found, err := fixture.operation.ReadWorkloadLifecycleV1()
		if err != nil || !found || record.TransferClaimed || fixture.injections != 0 {
			t.Fatalf("record=%+v found=%t err=%v injections=%d", record, found, err, fixture.injections)
		}
	})

	t.Run("generic caller cancellation keeps the barrier", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		fixture := newWorkloadLifecycleFixtureV1(t)
		run := func(command CommandSpec, options RunOptions) error {
			if len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "inspect" && fixture.starts > 0 {
				time.Sleep(10 * time.Millisecond)
				cancel()
				time.Sleep(20 * time.Millisecond)
			}
			return fixture.run(command, options)
		}
		started, startErr := startAndInjectPrivateWorkloadEnvironmentWithV1(
			ctx, fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment,
			RunOptions{}, run, fixture.inject,
		)
		if !errors.Is(startErr, context.Canceled) || !started.Owned || fixture.stops != 1 || fixture.injections != 0 {
			t.Fatalf("started=%+v error=%v stops=%d injections=%d", started, startErr, fixture.stops, fixture.injections)
		}
		record, found, err := fixture.operation.ReadWorkloadLifecycleV1()
		if err != nil || !found || record.TransferClaimed {
			t.Fatalf("record=%+v found=%t err=%v", record, found, err)
		}
	})

	t.Run("generic deadline keeps the barrier", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		fixture := newWorkloadLifecycleFixtureV1(t)
		run := func(command CommandSpec, options RunOptions) error {
			if len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "inspect" && fixture.starts > 0 {
				time.Sleep(50 * time.Millisecond)
			}
			return fixture.run(command, options)
		}
		started, startErr := startAndInjectPrivateWorkloadEnvironmentWithV1(
			ctx, fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment,
			RunOptions{}, run, fixture.inject,
		)
		if !errors.Is(startErr, context.DeadlineExceeded) || !started.Owned || fixture.stops != 1 || fixture.injections != 0 {
			t.Fatalf("started=%+v error=%v stops=%d injections=%d", started, startErr, fixture.stops, fixture.injections)
		}
		record, found, err := fixture.operation.ReadWorkloadLifecycleV1()
		if err != nil || !found || record.TransferClaimed {
			t.Fatalf("record=%+v found=%t err=%v", record, found, err)
		}
	})

	t.Run("generic early Compose failure retains later cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		fixture := newWorkloadLifecycleFixtureV1(t)
		composeFailure := errors.New("compose failed after creating the workload")
		fixture.startError = composeFailure
		run := func(command CommandSpec, options RunOptions) error {
			if len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "inspect" && fixture.starts > 0 {
				time.Sleep(10 * time.Millisecond)
				cancel()
				time.Sleep(20 * time.Millisecond)
			}
			return fixture.run(command, options)
		}
		started, startErr := startAndInjectPrivateWorkloadEnvironmentWithV1(
			ctx, fixture.command, "demo", testApplicationSandboxPlanV1(1000, 1000), fixture.environment,
			RunOptions{}, run, fixture.inject,
		)
		if !errors.Is(startErr, composeFailure) || !errors.Is(startErr, context.Canceled) || !started.Owned || fixture.stops != 1 || fixture.injections != 0 {
			t.Fatalf("started=%+v error=%v stops=%d injections=%d", started, startErr, fixture.stops, fixture.injections)
		}
		record, found, err := fixture.operation.ReadWorkloadLifecycleV1()
		if err != nil || !found || record.TransferClaimed {
			t.Fatalf("record=%+v found=%t err=%v", record, found, err)
		}
	})
}

func installedCustomEventExecutorForTest(t *testing.T, output io.Writer, beforeCleanup func(error, func(context.Context) error)) (LifecycleExecutor, ResolvedEnvironmentCommand) {
	t.Helper()
	document := blueprint.Document{Environment: blueprint.Environment{Commands: map[string]blueprint.Command{"check": {}}}}
	plan := DockerExecutionPlan{
		DeploymentDir: t.TempDir(), Image: "reploy/test:staging", ContainerName: "demo",
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 1000, GID: 1000, DockerUser: "1000:1000"}),
	}
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{Destination: environmentTemporaryHome, SourceKind: deploy.RuntimeMountSourceGenerated}})
	return environmentLifecycleExecutorWithCleanupHookV1(
		RuntimeOptions{Dir: plan.DeploymentDir}, document, plan, policy, output, output, beforeCleanup,
	), ResolvedEnvironmentCommand{Name: "check", Argv: []string{"/bin/true"}}
}

func ownedWorkloadLifecycleForInstalledCleanupTest(t *testing.T, fixture *workloadLifecycleFixtureV1, order *[]string, rollbackDeadline *time.Time) *workloadLifecycleV1 {
	t.Helper()
	binding, err := privateWorkloadRecordV1(fixture.command, fixture.environment.RuntimeBinding)
	if err != nil {
		t.Fatal(err)
	}
	return &workloadLifecycleV1{
		command: fixture.command, containerName: "demo", options: RunOptions{}, binding: binding,
		started: privateWorkloadStartResultV1{ContainerID: fixture.state.ID, StartedAt: fixture.state.StartedAt, Owned: true},
		run: func(command CommandSpec, options RunOptions) error {
			if len(command.Args) > 1 && command.Args[0] == "container" && command.Args[1] == "stop" {
				var ok bool
				*rollbackDeadline, ok = options.Context.Deadline()
				if !ok {
					t.Fatal("owned rollback had no deadline")
				}
				*order = append(*order, "rollback-stop")
			}
			return fixture.run(command, options)
		},
		operation: func(_ context.Context, action func(*deploy.OperationLock) error) error {
			return action(fixture.operation)
		},
	}
}

func TestInstalledServiceStartupReadinessBoundsPinnedContainerInspection(t *testing.T) {
	fixture := newWorkloadLifecycleFixtureV1(t)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	inspections := 0
	lifecycle := &workloadLifecycleV1{command: fixture.command, containerName: "demo", options: RunOptions{},
		run: func(command CommandSpec, options RunOptions) error {
			inspections++
			if !pinnedDockerEndpointV1(command) || options.Context == nil {
				t.Fatal("readiness lost endpoint pinning or cancellation")
			}
			if deadline, ok := options.Context.Deadline(); !ok || deadline.After(wantDeadline) {
				t.Fatal("container inspection outlived startup deadline")
			}
			<-options.Context.Done()
			return options.Context.Err()
		}}
	err := lifecycle.waitInstalledServiceReadiness(ctx, EndpointExecutionPlan{Scheme: "http", ProbeHost: "127.0.0.1", Readiness: &blueprint.Readiness{Timeout: time.Second, Interval: time.Millisecond}})
	if !errors.Is(err, context.DeadlineExceeded) || inspections != 1 {
		t.Fatalf("inspections=%d error=%v", inspections, err)
	}
}

func TestInstalledServiceStartupReadinessRejectsStoppedWorkload(t *testing.T) {
	fixture := newWorkloadLifecycleFixtureV1(t)
	fixture.prime("up")
	fixture.state.Running = false
	lifecycle := &workloadLifecycleV1{command: fixture.command, containerName: "demo", run: fixture.run}
	err := lifecycle.waitInstalledServiceReadiness(t.Context(), EndpointExecutionPlan{Scheme: "http", ProbeHost: "127.0.0.1", Readiness: &blueprint.Readiness{Timeout: time.Second, Interval: time.Millisecond}})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stopped workload readiness=%v", err)
	}
}
