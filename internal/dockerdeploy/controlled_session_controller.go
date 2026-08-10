package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

type dockerControllerBackendV1 struct {
	bind                func(context.Context, CommandSpec, time.Duration) (CommandSpec, commandRunner, error)
	run                 commandRunner
	observe             func(context.Context, CommandSpec, string) (int, error)
	requireReadyChannel func(ControlledSessionContainerPlanV1) error
	recordNoContainer   func()
	peerAddresses       []string
}

type dockerControllerWaitResultV1 struct {
	status controlledsession.ProcessStatusV1
	err    error
}

// DockerControllerV1 is the narrow Docker boundary for the trusted controller
// container. Creation uses the immutable planned name; every later operation
// uses the exact full container ID returned by that successful create.
type DockerControllerV1 struct {
	plan        ControlledSessionContainerPlanV1
	docker      CommandSpec
	containerID string
	backend     dockerControllerBackendV1

	operationMu sync.Mutex
	stateMu     sync.Mutex
	started     bool
	startTried  bool
	cleaned     bool
	waitDone    chan struct{}
	waitResult  dockerControllerWaitResultV1
}

func (controller *DockerControllerV1) ContainerID() string {
	return controller.containerID
}

// PrepareDockerControllerV1 verifies that the private channel is ready and
// creates the exact controller container without starting it.
func PrepareDockerControllerV1(
	ctx context.Context,
	plan ControlledSessionContainerPlanV1,
) (*DockerControllerV1, error) {
	return prepareDockerControllerWithCleanupVerificationV1(ctx, plan, nil)
}

func prepareDockerControllerWithCleanupVerificationV1(
	ctx context.Context,
	plan ControlledSessionContainerPlanV1,
	recordNoContainer func(),
) (*DockerControllerV1, error) {
	return prepareDockerControllerV1(ctx, plan, dockerControllerBackendV1{
		bind:                bindPinnedDockerCommandRunnerV1,
		observe:             observeDockerContainerExitV1,
		requireReadyChannel: requirePreparedControlledSessionControllerChannelV1,
		recordNoContainer:   recordNoContainer,
	})
}

func prepareDockerControllerV1(
	ctx context.Context,
	plan ControlledSessionContainerPlanV1,
	backend dockerControllerBackendV1,
) (*DockerControllerV1, error) {
	failBeforeCreate := func(err error) (*DockerControllerV1, error) {
		if backend.recordNoContainer != nil {
			backend.recordNoContainer()
		}
		return nil, err
	}
	plan = cloneControlledSessionContainerPlanV1(plan)
	if err := ValidateControlledSessionContainerPlanV1(plan); err != nil {
		return failBeforeCreate(fmt.Errorf("prepare controlled-session controller: %w", err))
	}
	if plan.Role != ControlledSessionRoleControllerV1 {
		return failBeforeCreate(fmt.Errorf("prepare controlled-session controller: container role must be %q", ControlledSessionRoleControllerV1))
	}
	if (backend.bind == nil && backend.run == nil) || backend.observe == nil || backend.requireReadyChannel == nil {
		return failBeforeCreate(fmt.Errorf("prepare controlled-session controller: backend is incomplete"))
	}
	if err := backend.requireReadyChannel(plan); err != nil {
		return failBeforeCreate(fmt.Errorf("prepare controlled-session controller channel: %w", err))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	docker, err := controlledSessionCreateWithPeerHostsV1(
		controlledSessionCommandSpecV1(plan.Create), plan, backend.peerAddresses,
	)
	if err != nil {
		return failBeforeCreate(fmt.Errorf("prepare controlled-session controller network realization: %w", err))
	}
	if backend.bind != nil {
		var err error
		docker, backend.run, err = backend.bind(ctx, docker, defaultDockerPreflightTimeout)
		if err != nil {
			return failBeforeCreate(fmt.Errorf("bind controlled-session controller Docker endpoint: %w", err))
		}
		if backend.run == nil {
			return failBeforeCreate(fmt.Errorf("prepare controlled-session controller: Docker endpoint binder returned no command runner"))
		}
	}
	var createOutput bytes.Buffer
	var createErrorOutput bytes.Buffer
	if err := backend.run(docker, RunOptions{
		Context: ctx, Stdout: &createOutput, Stderr: &createErrorOutput,
	}); err != nil {
		if output := trimmedCommandOutput(createErrorOutput.String()); output != "" {
			err = fmt.Errorf("%w\ncommand output:\n%s", err, output)
		}
		return nil, fmt.Errorf("create controlled-session controller container %q: %w; refusing cleanup because the creating attempt did not return an exact container ID", plan.Container, err)
	}
	containerID, err := parseDockerContainerIDV1(createOutput.String())
	if err != nil {
		return nil, fmt.Errorf("create controlled-session controller container %q: %w; refusing name-based cleanup because the created container identity is unknown", plan.Container, err)
	}
	return &DockerControllerV1{
		plan: plan, docker: docker, containerID: containerID, backend: backend, waitDone: make(chan struct{}),
	}, nil
}

// Start starts the inert controller and begins independent Docker exit
// observation. A failed start is terminal because Docker may have started the
// process before its response was lost; the exact container is force-removed.
func (controller *DockerControllerV1) Start(ctx context.Context) error {
	controller.operationMu.Lock()
	defer controller.operationMu.Unlock()

	controller.stateMu.Lock()
	if controller.cleaned {
		controller.stateMu.Unlock()
		return fmt.Errorf("controlled-session controller container %q is already cleaned", controller.plan.Container)
	}
	if controller.startTried {
		controller.stateMu.Unlock()
		return fmt.Errorf("controlled-session controller container %q start was already attempted", controller.plan.Container)
	}
	controller.startTried = true
	controller.stateMu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	start := controller.commandV1("start", controller.containerID)
	if err := controller.backend.run(start, RunOptions{Context: ctx}); err != nil {
		startErr := fmt.Errorf("start controlled-session controller container %q: %w", controller.plan.Container, err)
		cleanupErr := rollbackControlledSessionControllerContainerV1(controller.backend, controller.plan, controller.containerID)
		if cleanupErr == nil {
			controller.stateMu.Lock()
			controller.cleaned = true
			controller.stateMu.Unlock()
		} else {
			cleanupErr = fmt.Errorf("remove controlled-session controller container %q after ambiguous start failure: %w", controller.plan.Container, cleanupErr)
		}
		return errors.Join(startErr, cleanupErr)
	}

	controller.stateMu.Lock()
	controller.started = true
	controller.stateMu.Unlock()
	go controller.observeExit()
	return nil
}

// Wait returns the immutable Docker-observed controller exit status. Caller
// cancellation stops only this wait; independent host observation continues.
func (controller *DockerControllerV1) Wait(ctx context.Context) (controlledsession.ProcessStatusV1, error) {
	controller.stateMu.Lock()
	started := controller.started
	controller.stateMu.Unlock()
	if !started {
		return controlledsession.ProcessStatusV1{}, fmt.Errorf("controlled-session controller container %q is not started", controller.plan.Container)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-controller.waitDone:
		return controller.waitResult.status, controller.waitResult.err
	case <-ctx.Done():
		return controlledsession.ProcessStatusV1{}, fmt.Errorf("wait for controlled-session controller container %q: %w", controller.plan.Container, ctx.Err())
	}
}

// RequestGracefulStop delivers SIGTERM to the controller. The lifecycle
// supervisor owns the grace deadline and follows with ForceStop when needed.
func (controller *DockerControllerV1) RequestGracefulStop(ctx context.Context) error {
	return controller.signal(ctx, "TERM", "request graceful stop for")
}

// ForceStop delivers SIGKILL to the controller.
func (controller *DockerControllerV1) ForceStop(ctx context.Context) error {
	return controller.signal(ctx, "KILL", "force stop")
}

func (controller *DockerControllerV1) signal(ctx context.Context, signal string, action string) error {
	controller.operationMu.Lock()
	defer controller.operationMu.Unlock()
	if err := controller.requireStarted(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	command := controller.commandV1(
		"kill", "--signal", signal, controller.containerID,
	)
	if err := controller.backend.run(command, RunOptions{Context: ctx}); err != nil {
		return fmt.Errorf("%s controlled-session controller container %q: %w", action, controller.plan.Container, err)
	}
	return nil
}

// Cleanup force-removes the exact created controller by its full container ID.
// Successful cleanup is idempotent within this adapter; a failed attempt may
// be retried.
func (controller *DockerControllerV1) Cleanup(ctx context.Context) error {
	controller.operationMu.Lock()
	defer controller.operationMu.Unlock()

	controller.stateMu.Lock()
	if controller.cleaned {
		controller.stateMu.Unlock()
		return nil
	}
	controller.stateMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	cleanup := controller.commandV1("container", "rm", "--force", controller.containerID)
	if err := controller.backend.run(cleanup, RunOptions{Context: ctx}); err != nil && !isMissingContainerCleanupError(err) {
		return fmt.Errorf("remove controlled-session controller container %q: %w", controller.plan.Container, err)
	}
	controller.stateMu.Lock()
	controller.cleaned = true
	controller.stateMu.Unlock()
	return nil
}

func (controller *DockerControllerV1) observeExit() {
	code, err := controller.backend.observe(
		context.Background(),
		controller.docker,
		controller.containerID,
	)
	if err != nil {
		controller.waitResult = dockerControllerWaitResultV1{
			status: controlledsession.ProcessStatusV1{
				Kind: controlledsession.ProcessStatusUnavailableV1, Reason: "Docker controller observation was lost",
			},
			err: fmt.Errorf("observe controlled-session controller container %q exit: %w", controller.plan.Container, err),
		}
	} else {
		exitCode := code
		controller.waitResult = dockerControllerWaitResultV1{
			status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: &exitCode},
		}
	}
	close(controller.waitDone)
}

func (controller *DockerControllerV1) commandV1(args ...string) CommandSpec {
	command := controller.docker
	command.Args = append([]string(nil), args...)
	return command
}

func (controller *DockerControllerV1) requireStarted() error {
	controller.stateMu.Lock()
	defer controller.stateMu.Unlock()
	if !controller.started {
		return fmt.Errorf("controlled-session controller container %q is not started", controller.plan.Container)
	}
	if controller.cleaned {
		return fmt.Errorf("controlled-session controller container %q is already cleaned", controller.plan.Container)
	}
	return nil
}

func rollbackControlledSessionControllerContainerV1(
	backend dockerControllerBackendV1,
	plan ControlledSessionContainerPlanV1,
	containerID string,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultDockerPreflightTimeout)
	defer cancel()
	cleanup := CommandSpec{Name: plan.Cleanup.Name, Args: []string{"container", "rm", "--force", containerID}}
	return backend.run(cleanup, RunOptions{Context: cleanupCtx})
}

func parseDockerContainerIDV1(output string) (string, error) {
	containerID := string(bytes.TrimSpace([]byte(output)))
	if len(containerID) != 64 {
		return "", fmt.Errorf("Docker create returned invalid full container ID %q", containerID)
	}
	if _, err := hex.DecodeString(containerID); err != nil {
		return "", fmt.Errorf("Docker create returned invalid full container ID %q", containerID)
	}
	return containerID, nil
}
