package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type installedServiceEngineWaitV1 struct {
	bind     func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error)
	timeout  time.Duration
	interval time.Duration
}

// The generated unit pins its original local endpoint for start and stop.
// Wait before lifecycle initialization or any private-environment transfer.
// This changes engine startup readiness, not application restart policy.
func waitInstalledServiceEngineV1(ctx context.Context, spec CommandSpec, options RunOptions, backend installedServiceEngineWaitV1) (CommandSpec, commandRunner, error) {
	if ctx == nil || backend.bind == nil || backend.timeout <= 0 || backend.interval <= 0 {
		return CommandSpec{}, nil, fmt.Errorf("installed service engine readiness requires a context and complete backend")
	}
	waitCtx, cancel := context.WithTimeout(ctx, backend.timeout)
	defer cancel()
	reported := false
	var lastError error
	for {
		if err := waitCtx.Err(); err != nil {
			return CommandSpec{}, nil, fmt.Errorf("configured container engine readiness was interrupted: %w", errors.Join(err, lastError))
		}
		pinned, run, err := backend.bind(waitCtx, spec, options.DockerPreflightTimeout)
		if canceled := waitCtx.Err(); canceled != nil {
			return CommandSpec{}, nil, fmt.Errorf("configured container engine readiness was interrupted: %w", errors.Join(canceled, err))
		}
		if err == nil {
			return pinned, run, nil
		}
		lastError = err
		if !reported && options.Stderr != nil {
			fmt.Fprintln(options.Stderr, "waiting for the configured local container engine before service startup")
			reported = true
		}
		timer := time.NewTimer(backend.interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return CommandSpec{}, nil, fmt.Errorf("configured container engine was not ready before service startup: %w", errors.Join(waitCtx.Err(), err))
		case <-timer.C:
		}
	}
}
