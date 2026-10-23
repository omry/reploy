package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	installedServiceEngineTimeoutV1  = 90 * time.Second
	installedServiceStartupTimeoutV1 = 60 * time.Second
)

// Keep preparation and engine waiting outside the application's startup budget.
// Only names and durations belong in diagnostics, never arguments or private values.
type installedServiceStartupTimingsV1 struct {
	mu              sync.Mutex
	setup           time.Duration
	engine          time.Duration
	started         time.Time
	deadline        time.Time
	limit           time.Duration
	steps           []installedServiceStartupStepV1
	ready           bool
	failureObserved bool
	failureTimedOut bool
}

type installedServiceStartupStepV1 struct {
	name    string
	started time.Time
}

func newInstalledServiceStartupBudgetV1(parent context.Context, setup, engine time.Duration, timeout time.Duration, stderr io.Writer) (context.Context, *installedServiceStartupTimingsV1, func()) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	deadline, _ := ctx.Deadline()
	timing := &installedServiceStartupTimingsV1{setup: setup, engine: engine, started: time.Now(), deadline: deadline, limit: timeout}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && stderr != nil {
			timing.mu.Lock()
			defer timing.mu.Unlock()
			if !timing.ready {
				fmt.Fprintf(stderr, "installed service startup timed out; stopping failed startup; %s\n", timing.breakdownLocked(time.Now()))
			}
		}
	})
	var once sync.Once
	return ctx, timing, func() {
		once.Do(func() {
			if !stop() {
				<-done
			}
			cancel()
		})
	}
}

func (timing *installedServiceStartupTimingsV1) step(name string) {
	timing.mu.Lock()
	defer timing.mu.Unlock()
	timing.steps = append(timing.steps, installedServiceStartupStepV1{name: name, started: time.Now()})
}

func (timing *installedServiceStartupTimingsV1) lifecycleStep(index int, operation LifecycleOperation) {
	name := fmt.Sprintf("%s[%d] %s", operation.Event, index+1, operation.Kind)
	if operation.Command != nil {
		name += fmt.Sprintf(" %q", operation.Command.Name)
	}
	timing.step(name)
}

func (timing *installedServiceStartupTimingsV1) timeoutError(ctx context.Context, err error) error {
	timing.mu.Lock()
	defer timing.mu.Unlock()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if timing.failureObserved {
		timedOut = timing.failureTimedOut
	}
	if timing.ready || err == nil || !timedOut {
		return err
	}
	timeoutCause := ctx.Err()
	if timing.failureObserved && timing.failureTimedOut {
		timeoutCause = context.DeadlineExceeded
	}
	return fmt.Errorf("installed service application startup timed out; %s: %w", timing.breakdownLocked(time.Now()), errors.Join(timeoutCause, err))
}

func (timing *installedServiceStartupTimingsV1) failBeforeCleanup(ctx context.Context, err error, stop func()) {
	timing.mu.Lock()
	if err != nil && !timing.ready && !timing.failureObserved {
		timing.failureObserved = true
		timing.failureTimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
	timing.mu.Unlock()
	stop()
}

// Stop the startup clock before ownership-checked cleanup. A normal failure
// must not become an application timeout while cleanup is running.
func (timing *installedServiceStartupTimingsV1) finish(ctx context.Context, err error, stop func(), rollback func(error) error) error {
	err = timing.timeoutError(ctx, err)
	stop()
	if err != nil && !timing.ready {
		return rollback(err)
	}
	return err
}

func (lifecycle *workloadLifecycleV1) waitInstalledServiceReadiness(ctx context.Context, endpoint EndpointExecutionPlan) error {
	return WaitForHTTPReadinessWithServiceCheck(ctx, endpoint, func(checkCtx context.Context) error {
		container, err := lifecycle.inspect(checkCtx)
		if err != nil {
			return err
		}
		if !container.Running {
			return fmt.Errorf("installed workload is not running during startup readiness")
		}
		return nil
	})
}

func (timing *installedServiceStartupTimingsV1) breakdownLocked(now time.Time) string {
	end := now
	if end.After(timing.deadline) {
		end = timing.deadline
	}
	parts := []string{fmt.Sprintf("preparation=%s (limit %s)", (timing.setup + timing.engine).Round(time.Millisecond), installedServiceEngineTimeoutV1), fmt.Sprintf("lock/setup=%s", timing.setup.Round(time.Millisecond)), fmt.Sprintf("engine wait=%s", timing.engine.Round(time.Millisecond)), fmt.Sprintf("application startup=%s (limit %s)", max(0, end.Sub(timing.started)).Round(time.Millisecond), timing.limit)}
	for i, step := range timing.steps {
		next := end
		if i+1 < len(timing.steps) && timing.steps[i+1].started.Before(next) {
			next = timing.steps[i+1].started
		}
		if next.Before(step.started) {
			next = step.started
		}
		parts = append(parts, fmt.Sprintf("%s=%s", step.name, next.Sub(step.started).Round(time.Millisecond)))
	}
	if now.After(timing.deadline) {
		parts = append(parts, fmt.Sprintf("after deadline=%s", now.Sub(timing.deadline).Round(time.Millisecond)))
	}
	return strings.Join(parts, "; ")
}
