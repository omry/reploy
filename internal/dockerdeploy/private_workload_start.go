package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var errPrivateWorkloadWasRunning = errors.New("private workload was already running; use explicit restart to replace it")

type privateWorkloadContainerV1 struct {
	ID        string
	Running   bool
	StartedAt string
}

type privateWorkloadStartResultV1 struct {
	ContainerID string
	Owned       bool
}

func inspectPrivateWorkloadContainerV1(ctx context.Context, start CommandSpec, name string, options RunOptions, run commandRunner) (privateWorkloadContainerV1, error) {
	inspectCtx, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
	defer cancel()
	var output, diagnostics bytes.Buffer
	command := start
	command.Args = []string{"container", "inspect", "--format", `{"id":{{json .Id}},"running":{{json .State.Running}},"started_at":{{json .State.StartedAt}}}`, name}
	options.Context, options.Stdin = inspectCtx, nil
	options.Stdout, options.Stderr = &output, &diagnostics
	if err := run(command, options); err != nil {
		message := strings.ToLower(strings.TrimSpace(diagnostics.String()))
		if inspectCtx.Err() == nil && privateWorkloadContainerMissingV1(message) {
			return privateWorkloadContainerV1{}, nil
		}
		return privateWorkloadContainerV1{}, fmt.Errorf("inspect private workload container: %w", err)
	}
	var value struct {
		ID        *string `json:"id"`
		Running   *bool   `json:"running"`
		StartedAt *string `json:"started_at"`
	}
	decoder := json.NewDecoder(&output)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || value.ID == nil || value.Running == nil || value.StartedAt == nil {
		return privateWorkloadContainerV1{}, fmt.Errorf("private workload inspection returned incomplete identity")
	}
	if decoder.Decode(new(any)) != io.EOF || len(*value.ID) != 64 || *value.ID != strings.ToLower(*value.ID) {
		return privateWorkloadContainerV1{}, fmt.Errorf("private workload inspection returned invalid identity")
	}
	if _, err := hex.DecodeString(*value.ID); err != nil {
		return privateWorkloadContainerV1{}, fmt.Errorf("private workload inspection returned invalid container ID")
	}
	if *value.Running {
		started, err := time.Parse(time.RFC3339Nano, *value.StartedAt)
		if err != nil || started.IsZero() {
			return privateWorkloadContainerV1{}, fmt.Errorf("private workload inspection returned invalid start time")
		}
	}
	return privateWorkloadContainerV1{ID: *value.ID, Running: *value.Running, StartedAt: *value.StartedAt}, nil
}

func privateWorkloadContainerMissingV1(message string) bool {
	for _, prefix := range []string{"error response from daemon:", "error:"} {
		if body, found := strings.CutPrefix(message, prefix); found {
			body = strings.TrimSpace(body)
			return strings.HasPrefix(body, "no such container:") || strings.HasPrefix(body, "no such object:") ||
				strings.HasPrefix(body, "no container with name or id ") && strings.HasSuffix(body, ": no such container")
		}
	}
	return false
}

// The caller supplies its operation-locked, endpoint-bound command runner.
// Container IDs, rather than a mutable name or Compose down, own cleanup.
func startAndInjectPrivateWorkloadEnvironmentWithV1(
	ctx context.Context, start CommandSpec, containerName string,
	sandbox ApplicationSandboxPlanV1, environment privateWorkloadEnvironmentV1,
	options RunOptions, run commandRunner, inject privateWorkloadEnvironmentInjectorV1,
) (result privateWorkloadStartResultV1, err error) {
	if ctx == nil || start.Name == "" || containerName == "" || run == nil || inject == nil {
		return result, fmt.Errorf("private workload startup requires a context and complete runner")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	startOptions := options
	startOptions.Context, startOptions.Stdin = ctx, nil
	if !environment.Present {
		return result, run(start, startOptions)
	}
	before, err := inspectPrivateWorkloadContainerV1(ctx, start, containerName, options, run)
	if err != nil {
		return result, err
	}
	if before.Running {
		defer func() {
			if err != nil {
				err = errors.Join(err, errPrivateWorkloadWasRunning)
			}
		}()
	}
	startErr := run(start, startOptions)
	// A failed or cancelled Compose command can still have started its
	// container. The bounded ownership inspection must survive cancellation.
	after, err := inspectPrivateWorkloadContainerV1(context.WithoutCancel(ctx), start, containerName, options, run)
	if err != nil {
		return result, errors.Join(startErr, ctx.Err(), err)
	}
	result = privateWorkloadStartResultV1{
		ContainerID: after.ID,
		Owned:       after.ID != "" && (after.ID != before.ID || !before.Running && after.StartedAt != before.StartedAt),
	}
	cleanup := func(cause error) error {
		if cause == nil {
			return cause
		}
		return errors.Join(cause, cleanupPrivateWorkloadStartV1(ctx, start, result, options, run))
	}
	if startErr != nil || ctx.Err() != nil {
		return result, cleanup(errors.Join(startErr, ctx.Err()))
	}
	if !after.Running {
		return result, cleanup(fmt.Errorf("private workload exited before environment injection; inspect workload logs"))
	}
	if before.Running && before.ID == after.ID {
		if before.StartedAt != after.StartedAt {
			return result, fmt.Errorf("private workload restarted concurrently; retry the command")
		}
		return result, nil
	}
	return result, cleanup(inject(ctx, start.Name, after.ID, sandbox, environment, options, run))
}

func cleanupPrivateWorkloadStartV1(ctx context.Context, start CommandSpec, result privateWorkloadStartResultV1, options RunOptions, run commandRunner) error {
	if !result.Owned {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), privateWorkloadEnvironmentTimeoutV1)
	defer cancel()
	options.Context, options.Stdin = cleanupCtx, nil
	options.Stdout, options.Stderr = nil, nil
	start.Args = []string{"container", "rm", "--force", result.ContainerID}
	return cleanupPrivateWorkloadContainerV1(start, options, run)
}
