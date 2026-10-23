package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/omry/reploy/internal/deploy"
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
	StartedAt   string
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
	if environment.Operation == nil || environment.RuntimeBinding == "" {
		return result, fmt.Errorf("workload startup requires controller-owned lifecycle evidence")
	}
	if err := environment.Operation.RequireWritable(); err != nil {
		return result, err
	}
	record, err := privateWorkloadRecordV1(start, environment.RuntimeBinding)
	if err != nil {
		return result, err
	}
	previous, found, err := environment.Operation.ReadWorkloadLifecycleV1()
	if err != nil {
		return result, err
	}
	before, err := inspectPrivateWorkloadContainerV1(ctx, start, containerName, options, run)
	if err != nil {
		return result, err
	}
	if before.Running {
		result.ContainerID, result.StartedAt = before.ID, before.StartedAt
		if found && previous.Endpoint == record.Endpoint && previous.Runtime == record.Runtime &&
			previous.ContainerID == before.ID && previous.StartedAt == before.StartedAt && previous.State == "up" &&
			previous.StartupComplete && (!previous.TransferClaimed || previous.HandoffComplete) {
			return result, nil
		}
		return result, errors.Join(errPrivateWorkloadWasRunning, fmt.Errorf("workload startup is unconfirmed or its inputs changed; use `reploy restart`"))
	}
	if found && previous.Endpoint == record.Endpoint && previous.State == "down" && before.ID != "" &&
		previous.ContainerID == before.ID && previous.StartedAt == before.StartedAt {
		// A new requested startup replaces a retained failed attempt. The
		// non-force removal cannot kill a concurrently restarted process.
		remove := start
		remove.Args = []string{"container", "rm", before.ID}
		if err := run(remove, startOptions); err != nil {
			return result, fmt.Errorf("replace retained failed startup: %w", err)
		}
		before = privateWorkloadContainerV1{}
	}
	if err := environment.Operation.WriteWorkloadLifecycleV1(record); err != nil {
		return result, err
	}
	startErr := run(start, startOptions)
	// A failed or cancelled Compose command can still have started its
	// container. The bounded ownership inspection must survive cancellation.
	after, err := inspectPrivateWorkloadContainerV1(context.WithoutCancel(ctx), start, containerName, options, run)
	if err != nil {
		return result, errors.Join(startErr, ctx.Err(), err)
	}
	result = privateWorkloadStartResultV1{
		ContainerID: after.ID, StartedAt: after.StartedAt,
		Owned: after.ID != "" && (after.ID != before.ID || !before.Running && after.StartedAt != before.StartedAt),
	}
	cleanup := func(cause error) error {
		if cause == nil {
			return cause
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), privateWorkloadEnvironmentTimeoutV1)
		defer cancel()
		return errors.Join(cause, stopFailedPrivateWorkloadStartV1(cleanupCtx, start, result, environment.Operation, record, options, run))
	}
	if startErr != nil || ctx.Err() != nil {
		return result, cleanup(errors.Join(startErr, ctx.Err()))
	}
	if !after.Running {
		return result, cleanup(fmt.Errorf("private workload exited before environment injection; inspect workload logs"))
	}
	if !result.Owned {
		return result, fmt.Errorf("workload startup process identity is unconfirmed; use `reploy restart`")
	}
	record.ContainerID, record.StartedAt = after.ID, after.StartedAt
	if environment.Present {
		// The durable claim precedes any secret transfer. On interruption,
		// an existing process never receives a second transfer.
		record.TransferClaimed = true
		if err := environment.Operation.WriteWorkloadLifecycleV1(record); err != nil {
			return result, cleanup(err)
		}
		if err := inject(ctx, start.Name, after.ID, sandbox, environment, options, run); err != nil {
			return result, cleanup(err)
		}
		current, err := inspectPrivateWorkloadContainerV1(ctx, start, after.ID, options, run)
		if err != nil {
			return result, cleanup(err)
		}
		if !current.Running || current.ID != after.ID || current.StartedAt != after.StartedAt {
			return result, cleanup(fmt.Errorf("workload process changed during private startup"))
		}
		record.HandoffComplete = true
	} else if err := confirmApplicationLaunchV1(ctx, start, after, options, run); err != nil {
		return result, cleanup(err)
	}
	if err := environment.Operation.WriteWorkloadLifecycleV1(record); err != nil {
		return result, cleanup(err)
	}
	return result, nil
}

func privateWorkloadRecordV1(start CommandSpec, runtimeBinding string) (deploy.WorkloadLifecycleV1, error) {
	endpoint := commandEnvironmentValueV1(start, "DOCKER_HOST")
	if !localDockerEndpointV1(endpoint) {
		return deploy.WorkloadLifecycleV1{}, fmt.Errorf("workload lifecycle requires a pinned local engine endpoint")
	}
	record := deploy.WorkloadLifecycleV1{Schema: "workload-lifecycle-v1", State: "starting", Endpoint: fmt.Sprintf("%x", sha256.Sum256([]byte(endpoint))), Runtime: runtimeBinding}
	return record, record.Validate()
}

func stopFailedPrivateWorkloadStartV1(ctx context.Context, start CommandSpec, result privateWorkloadStartResultV1, operation *deploy.OperationLock, record deploy.WorkloadLifecycleV1, options RunOptions, run commandRunner) error {
	if !result.Owned {
		return nil
	}
	if err := operation.RequireWritable(); err != nil {
		return fmt.Errorf("failed startup cleanup lost operation authority: %w", err)
	}
	record.ContainerID, record.StartedAt, record.State = result.ContainerID, result.StartedAt, "stopping"
	// A metadata write failure must not prevent bounded shutdown of a new,
	// exactly owned process. Preserve both errors for the operator.
	if instant, err := time.Parse(time.RFC3339Nano, record.StartedAt); err != nil || instant.IsZero() {
		record.ContainerID, record.StartedAt = "", ""
		record.TransferClaimed, record.HandoffComplete = false, false
	}
	writeErr := operation.WriteWorkloadLifecycleV1(record)
	if err := operation.RequireWritable(); err != nil {
		return errors.Join(writeErr, fmt.Errorf("failed startup cleanup lost operation authority: %w", err))
	}
	confirmed, stopErr := cleanupPrivateWorkloadStartV1(ctx, start, result, options, run)
	if !confirmed {
		return errors.Join(writeErr, stopErr)
	}
	record.State = "down"
	return errors.Join(writeErr, stopErr, operation.WriteWorkloadLifecycleV1(record))
}

func cleanupPrivateWorkloadStartV1(ctx context.Context, start CommandSpec, result privateWorkloadStartResultV1, options RunOptions, run commandRunner) (bool, error) {
	if !result.Owned {
		return true, nil
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, privateWorkloadEnvironmentTimeoutV1)
	defer cancel()
	options.Context, options.Stdin = cleanupCtx, nil
	options.Stdout, options.Stderr = nil, nil
	// Failure cleanup retains the stopped container for diagnosis. Inspect
	// the exact process before stopping; never stop a concurrently restarted
	// process merely because its mutable name or container ID is the same.
	current, err := inspectPrivateWorkloadContainerV1(cleanupCtx, start, result.ContainerID, options, run)
	if err != nil {
		return false, err
	}
	if current.ID == "" || !current.Running {
		return true, nil
	}
	if current.ID != result.ContainerID || current.StartedAt != result.StartedAt {
		return false, fmt.Errorf("failed startup process changed before shutdown; inspect workload state")
	}
	start.Args = []string{"container", "stop", result.ContainerID}
	stopErr := run(start, options)
	current, inspectErr := inspectPrivateWorkloadContainerV1(cleanupCtx, start, result.ContainerID, options, run)
	if inspectErr != nil {
		return false, errors.Join(stopErr, inspectErr)
	}
	if current.Running {
		return false, errors.Join(stopErr, fmt.Errorf("failed startup container is still stopping"))
	}
	return true, stopErr
}
