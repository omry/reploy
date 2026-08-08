package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

type dockerPTYAttachmentV1 interface {
	io.ReadCloser
	WriteContext(context.Context, []byte) error
}

type dockerWorkloadPTYBackendV1 struct {
	run     commandRunner
	attach  func(context.Context, CommandSpec, string, time.Duration) (dockerPTYAttachmentV1, error)
	resize  func(context.Context, CommandSpec, string, uint32, uint32, time.Duration) error
	observe func(context.Context, CommandSpec, string) (int, error)
}

type dockerWorkloadPTYWaitResultV1 struct {
	status controlledsession.ProcessStatusV1
	err    error
}

// DockerWorkloadPTYV1 is the narrow Docker boundary owned by the attached
// controlled-session host operation. Closing it closes only the PTY
// attachment; container cleanup remains the lifecycle supervisor's job.
type DockerWorkloadPTYV1 struct {
	plan       ControlledSessionContainerPlanV1
	backend    dockerWorkloadPTYBackendV1
	attachment dockerPTYAttachmentV1

	operationMu sync.Mutex
	stateMu     sync.Mutex
	started     bool
	outputTaken bool
	waitDone    chan struct{}
	waitResult  dockerWorkloadPTYWaitResultV1

	closeOnce sync.Once
	closeErr  error
}

// PrepareDockerWorkloadPTYV1 creates the exact workload container without
// starting it and establishes the Docker PTY attachment before returning.
func PrepareDockerWorkloadPTYV1(
	ctx context.Context,
	plan ControlledSessionContainerPlanV1,
) (*DockerWorkloadPTYV1, error) {
	return prepareDockerWorkloadPTYV1(ctx, plan, dockerWorkloadPTYBackendV1{
		run:     runDockerCommand,
		attach:  attachDockerContainerPTYV1,
		resize:  resizeDockerContainerPTYV1,
		observe: observeDockerContainerExitV1,
	})
}

func prepareDockerWorkloadPTYV1(
	ctx context.Context,
	plan ControlledSessionContainerPlanV1,
	backend dockerWorkloadPTYBackendV1,
) (*DockerWorkloadPTYV1, error) {
	plan = cloneControlledSessionContainerPlanV1(plan)
	if err := ValidateControlledSessionContainerPlanV1(plan); err != nil {
		return nil, fmt.Errorf("prepare controlled-session workload PTY: %w", err)
	}
	if plan.Role != ControlledSessionRoleWorkloadV1 {
		return nil, fmt.Errorf("prepare controlled-session workload PTY: container role must be %q", ControlledSessionRoleWorkloadV1)
	}
	if backend.run == nil || backend.attach == nil || backend.resize == nil || backend.observe == nil {
		return nil, fmt.Errorf("prepare controlled-session workload PTY: backend is incomplete")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	create := controlledSessionCommandSpecV1(plan.Create)
	if err := backend.run(create, RunOptions{Context: ctx}); err != nil {
		return nil, fmt.Errorf("create controlled-session workload container %q: %w", plan.Container, err)
	}
	attachment, err := backend.attach(ctx, create, plan.Container, defaultDockerPreflightTimeout)
	if err != nil {
		attachErr := fmt.Errorf("attach controlled-session workload PTY for container %q before start: %w", plan.Container, err)
		if cleanupErr := rollbackControlledSessionWorkloadContainerV1(backend, plan); cleanupErr != nil {
			return nil, errors.Join(attachErr, fmt.Errorf("remove inert controlled-session workload container %q after attach failure: %w", plan.Container, cleanupErr))
		}
		return nil, attachErr
	}
	return &DockerWorkloadPTYV1{
		plan: plan, backend: backend, attachment: attachment, waitDone: make(chan struct{}),
	}, nil
}

// Output returns the one ordered raw PTY output source. It may be claimed once
// before Start so the output pump is ready before workload code can execute.
func (workload *DockerWorkloadPTYV1) Output() (io.ReadCloser, error) {
	workload.stateMu.Lock()
	defer workload.stateMu.Unlock()
	if workload.outputTaken {
		return nil, fmt.Errorf("controlled-session workload PTY output is already claimed")
	}
	workload.outputTaken = true
	return workload.attachment, nil
}

// Start starts the already-attached inert container, begins independent exit
// observation, and applies the immutable initial terminal dimensions before
// returning success. Docker does not permit resizing a created container, so
// the initial resize is the first post-start operation and activation must not
// be declared until Start returns successfully.
func (workload *DockerWorkloadPTYV1) Start(ctx context.Context) error {
	workload.operationMu.Lock()
	defer workload.operationMu.Unlock()

	workload.stateMu.Lock()
	if workload.started {
		workload.stateMu.Unlock()
		return fmt.Errorf("controlled-session workload container %q is already started", workload.plan.Container)
	}
	if !workload.outputTaken {
		workload.stateMu.Unlock()
		return fmt.Errorf("controlled-session workload PTY output must be claimed before container start")
	}
	workload.stateMu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := workload.backend.run(controlledSessionCommandSpecV1(workload.plan.Start), RunOptions{Context: ctx}); err != nil {
		startErr := fmt.Errorf("start attached controlled-session workload container %q: %w", workload.plan.Container, err)
		closeErr := workload.Close()
		cleanupErr := rollbackControlledSessionWorkloadContainerV1(workload.backend, workload.plan)
		if closeErr != nil {
			closeErr = fmt.Errorf("close controlled-session workload PTY after start failure: %w", closeErr)
		}
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("remove controlled-session workload container %q after ambiguous start failure: %w", workload.plan.Container, cleanupErr)
		}
		return errors.Join(startErr, closeErr, cleanupErr)
	}

	workload.stateMu.Lock()
	workload.started = true
	workload.stateMu.Unlock()
	go workload.observeExit()

	columns, _ := strconv.ParseUint(workload.plan.InitialColumns, 10, 16)
	rows, _ := strconv.ParseUint(workload.plan.InitialRows, 10, 16)
	if err := workload.resizeLocked(ctx, uint32(columns), uint32(rows)); err != nil {
		return fmt.Errorf("apply initial controlled-session workload PTY dimensions: %w", err)
	}
	return nil
}

// WriteInput writes all bytes exactly as supplied to the Docker PTY.
func (workload *DockerWorkloadPTYV1) WriteInput(ctx context.Context, data []byte) error {
	workload.operationMu.Lock()
	defer workload.operationMu.Unlock()
	if err := workload.requireStarted(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := workload.attachment.WriteContext(ctx, data); err != nil {
		return fmt.Errorf("write controlled-session workload PTY input: %w", err)
	}
	return nil
}

// Resize applies one terminal window size through Docker.
func (workload *DockerWorkloadPTYV1) Resize(ctx context.Context, columns uint32, rows uint32) error {
	workload.operationMu.Lock()
	defer workload.operationMu.Unlock()
	if err := workload.requireStarted(); err != nil {
		return err
	}
	return workload.resizeLocked(ctx, columns, rows)
}

func (workload *DockerWorkloadPTYV1) resizeLocked(ctx context.Context, columns uint32, rows uint32) error {
	if columns == 0 || columns > 65535 || rows == 0 || rows > 65535 {
		return fmt.Errorf("controlled-session workload PTY dimensions must be between 1 and 65535")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := workload.backend.resize(
		ctx,
		controlledSessionCommandSpecV1(workload.plan.Start),
		workload.plan.Container,
		columns,
		rows,
		defaultDockerPreflightTimeout,
	); err != nil {
		return fmt.Errorf("resize controlled-session workload PTY to %dx%d: %w", columns, rows, err)
	}
	return nil
}

// RequestGracefulStop delivers SIGTERM to the workload container. The caller
// owns the grace deadline and must follow with ForceStop if exit is not
// observed in time.
func (workload *DockerWorkloadPTYV1) RequestGracefulStop(ctx context.Context) error {
	return workload.signal(ctx, "TERM", "request graceful stop for")
}

// ForceStop delivers SIGKILL to the workload container.
func (workload *DockerWorkloadPTYV1) ForceStop(ctx context.Context) error {
	return workload.signal(ctx, "KILL", "force stop")
}

func (workload *DockerWorkloadPTYV1) signal(ctx context.Context, signal string, action string) error {
	workload.operationMu.Lock()
	defer workload.operationMu.Unlock()
	if err := workload.requireStarted(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	command := CommandSpec{Name: workload.plan.Start.Name, Args: []string{
		"kill", "--signal", signal, workload.plan.Container,
	}}
	if err := workload.backend.run(command, RunOptions{Context: ctx}); err != nil {
		return fmt.Errorf("%s controlled-session workload container %q: %w", action, workload.plan.Container, err)
	}
	return nil
}

// Wait returns the immutable Docker-observed exit status. Caller cancellation
// stops only that wait; the independent host observation continues.
func (workload *DockerWorkloadPTYV1) Wait(ctx context.Context) (controlledsession.ProcessStatusV1, error) {
	workload.stateMu.Lock()
	started := workload.started
	workload.stateMu.Unlock()
	if !started {
		return controlledsession.ProcessStatusV1{}, fmt.Errorf("controlled-session workload container %q is not started", workload.plan.Container)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-workload.waitDone:
		return workload.waitResult.status, workload.waitResult.err
	case <-ctx.Done():
		return controlledsession.ProcessStatusV1{}, fmt.Errorf("wait for controlled-session workload container %q: %w", workload.plan.Container, ctx.Err())
	}
}

func (workload *DockerWorkloadPTYV1) observeExit() {
	code, err := workload.backend.observe(context.Background(), controlledSessionCommandSpecV1(workload.plan.Start), workload.plan.Container)
	if err != nil {
		workload.waitResult = dockerWorkloadPTYWaitResultV1{
			status: controlledsession.ProcessStatusV1{
				Kind: controlledsession.ProcessStatusUnavailableV1, Reason: "Docker workload observation was lost",
			},
			err: fmt.Errorf("observe controlled-session workload container %q exit: %w", workload.plan.Container, err),
		}
	} else {
		exitCode := code
		workload.waitResult = dockerWorkloadPTYWaitResultV1{
			status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &exitCode},
		}
	}
	close(workload.waitDone)
}

func (workload *DockerWorkloadPTYV1) requireStarted() error {
	workload.stateMu.Lock()
	defer workload.stateMu.Unlock()
	if !workload.started {
		return fmt.Errorf("controlled-session workload container %q is not started", workload.plan.Container)
	}
	return nil
}

// Close closes only the PTY attachment and permanently unblocks its reader.
func (workload *DockerWorkloadPTYV1) Close() error {
	workload.closeOnce.Do(func() {
		workload.closeErr = workload.attachment.Close()
	})
	return workload.closeErr
}

func controlledSessionCommandSpecV1(command ControlledSessionDockerCommandV1) CommandSpec {
	return CommandSpec{Name: command.Name, Args: append([]string(nil), command.Args...)}
}

func rollbackControlledSessionWorkloadContainerV1(
	backend dockerWorkloadPTYBackendV1,
	plan ControlledSessionContainerPlanV1,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultDockerPreflightTimeout)
	defer cancel()
	return backend.run(controlledSessionCommandSpecV1(plan.Cleanup), RunOptions{Context: cleanupCtx})
}

func observeDockerContainerExitV1(ctx context.Context, docker CommandSpec, container string) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.CommandContext(ctx, docker.Name, "wait", container)
	command.Dir = docker.Dir
	if len(docker.Env) > 0 {
		command.Env = append(os.Environ(), docker.Env...)
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if diagnostic := trimmedCommandOutput(output.String()); diagnostic != "" {
			return 0, fmt.Errorf("docker wait failed: %w\ncommand output:\n%s", err, diagnostic)
		}
		return 0, fmt.Errorf("docker wait failed: %w", err)
	}
	value := strings.TrimSpace(output.String())
	code, err := strconv.ParseUint(value, 10, 8)
	if err != nil || strconv.FormatUint(code, 10) != value {
		return 0, fmt.Errorf("docker wait returned invalid exit code %q", value)
	}
	return int(code), nil
}
