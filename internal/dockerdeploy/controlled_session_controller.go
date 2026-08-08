package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/omry/reploy/internal/controlledsession"
)

type dockerControllerBackendV1 struct {
	run                 commandRunner
	observe             func(context.Context, CommandSpec, string) (int, error)
	requireReadyChannel func(ControlledSessionContainerPlanV1) error
}

type dockerControllerWaitResultV1 struct {
	status controlledsession.ProcessStatusV1
	err    error
}

// DockerControllerV1 is the narrow Docker boundary for the trusted controller
// container. It can act only on the exact container named by the immutable
// controlled-session plan.
type DockerControllerV1 struct {
	plan    ControlledSessionContainerPlanV1
	backend dockerControllerBackendV1

	operationMu sync.Mutex
	stateMu     sync.Mutex
	started     bool
	startTried  bool
	cleaned     bool
	waitDone    chan struct{}
	waitResult  dockerControllerWaitResultV1
}

// PrepareDockerControllerV1 verifies that the private channel is ready and
// creates the exact controller container without starting it.
func PrepareDockerControllerV1(
	ctx context.Context,
	plan ControlledSessionContainerPlanV1,
) (*DockerControllerV1, error) {
	return prepareDockerControllerV1(ctx, plan, dockerControllerBackendV1{
		run:                 runDockerCommand,
		observe:             observeDockerContainerExitV1,
		requireReadyChannel: requirePreparedControlledSessionControllerChannelV1,
	})
}

func prepareDockerControllerV1(
	ctx context.Context,
	plan ControlledSessionContainerPlanV1,
	backend dockerControllerBackendV1,
) (*DockerControllerV1, error) {
	plan = cloneControlledSessionContainerPlanV1(plan)
	if err := ValidateControlledSessionContainerPlanV1(plan); err != nil {
		return nil, fmt.Errorf("prepare controlled-session controller: %w", err)
	}
	if plan.Role != ControlledSessionRoleControllerV1 {
		return nil, fmt.Errorf("prepare controlled-session controller: container role must be %q", ControlledSessionRoleControllerV1)
	}
	if backend.run == nil || backend.observe == nil || backend.requireReadyChannel == nil {
		return nil, fmt.Errorf("prepare controlled-session controller: backend is incomplete")
	}
	if err := backend.requireReadyChannel(plan); err != nil {
		return nil, fmt.Errorf("prepare controlled-session controller channel: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := backend.run(controlledSessionCommandSpecV1(plan.Create), RunOptions{Context: ctx}); err != nil {
		createErr := fmt.Errorf("create controlled-session controller container %q: %w", plan.Container, err)
		cleanupErr := rollbackAmbiguousControlledSessionControllerCreateV1(backend, plan)
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("reconcile controlled-session controller container %q after ambiguous create failure: %w", plan.Container, cleanupErr)
		}
		return nil, errors.Join(createErr, cleanupErr)
	}
	return &DockerControllerV1{
		plan: plan, backend: backend, waitDone: make(chan struct{}),
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
	if err := controller.backend.run(controlledSessionCommandSpecV1(controller.plan.Start), RunOptions{Context: ctx}); err != nil {
		startErr := fmt.Errorf("start controlled-session controller container %q: %w", controller.plan.Container, err)
		cleanupErr := rollbackControlledSessionControllerContainerV1(controller.backend, controller.plan)
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
	command := CommandSpec{Name: controller.plan.Start.Name, Args: []string{
		"kill", "--signal", signal, controller.plan.Container,
	}}
	if err := controller.backend.run(command, RunOptions{Context: ctx}); err != nil {
		return fmt.Errorf("%s controlled-session controller container %q: %w", action, controller.plan.Container, err)
	}
	return nil
}

// Cleanup force-removes the exact planned controller container. Successful
// cleanup is idempotent within this adapter; a failed attempt may be retried.
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
	if err := controller.backend.run(controlledSessionCommandSpecV1(controller.plan.Cleanup), RunOptions{Context: ctx}); err != nil {
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
		controlledSessionCommandSpecV1(controller.plan.Start),
		controller.plan.Container,
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
) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultDockerPreflightTimeout)
	defer cancel()
	return backend.run(controlledSessionCommandSpecV1(plan.Cleanup), RunOptions{Context: cleanupCtx})
}

func rollbackAmbiguousControlledSessionControllerCreateV1(
	backend dockerControllerBackendV1,
	plan ControlledSessionContainerPlanV1,
) error {
	inspectCtx, cancel := context.WithTimeout(context.Background(), defaultDockerPreflightTimeout)
	defer cancel()
	var output bytes.Buffer
	inspect := CommandSpec{Name: plan.Create.Name, Args: []string{
		"container", "inspect", "--format", "{{json .Config.Labels}}", plan.Container,
	}}
	if err := backend.run(inspect, RunOptions{Context: inspectCtx, Stdout: &output, Stderr: &output}); err != nil {
		return fmt.Errorf("inspect possible controller container before removal: %w", err)
	}
	labels := map[string]string{}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &labels); err != nil {
		return fmt.Errorf("decode possible controller container labels: %w", err)
	}
	for _, expected := range plan.Labels {
		if labels[expected.Name] != expected.Value {
			return fmt.Errorf("refuse to remove container because its controlled-session ownership labels do not match the immutable plan")
		}
	}
	return rollbackControlledSessionControllerContainerV1(backend, plan)
}
