package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
)

const controlledSessionOutputFinalizationTimeoutV1 = time.Duration(controlledsession.DefaultOutputFinalizationTimeoutMillisecondsV1) * time.Millisecond

type ControlledSessionRunOptionsV1 struct {
	StartupTimeout                time.Duration
	TerminationGrace              time.Duration
	ControllerFinalizationTimeout time.Duration
	ResultAcknowledgementTimeout  time.Duration
	CleanupTimeout                time.Duration
}

// ControlledSessionRunResultV1 separates the authoritative session result
// delivered over the private channel from delivery-tail facts that can exist
// only after that channel and the controller have been removed.
type ControlledSessionRunResultV1 struct {
	SessionResult              controlledsession.ResultV1
	ResultDelivered            bool
	ResultAcknowledged         bool
	ControllerStatus           controlledsession.ProcessStatusV1
	DeliveryTailCleanupStatus  controlledsession.CleanupStatusV1
	DeliveryTailRecoveryAction controlledsession.RecoveryActionV1
}

type controlledSessionControllerRuntimeV1 interface {
	ContainerID() string
	Start(context.Context) error
	Wait(context.Context) (controlledsession.ProcessStatusV1, error)
	RequestGracefulStop(context.Context) error
	ForceStop(context.Context) error
	Cleanup(context.Context) error
}

type controlledSessionWorkloadRuntimeV1 interface {
	controlledsession.WorkloadPTYControlV1
	ContainerID() string
	Output() (io.ReadCloser, error)
	Start(context.Context) error
	Started() bool
	Wait(context.Context) (controlledsession.ProcessStatusV1, error)
	RequestGracefulStop(context.Context) error
	ForceStop(context.Context) error
	Close() error
	Cleanup(context.Context) error
}

type controlledSessionChannelRuntimeV1 interface {
	Claim(context.Context) (controlledsession.ControllerTransportV1, error)
	Close() error
}

type privateControlledSessionChannelRuntimeV1 struct {
	channel *controlledsession.PrivateChannelV1
}

func (runtime *privateControlledSessionChannelRuntimeV1) Claim(ctx context.Context) (controlledsession.ControllerTransportV1, error) {
	return runtime.channel.Claim(ctx)
}

func (runtime *privateControlledSessionChannelRuntimeV1) Close() error {
	return runtime.channel.Close()
}

type controlledSessionSupervisorBackendV1 struct {
	prepareChannel            func(ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error)
	prepareController         func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error)
	prepareWorkload           func(context.Context, ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error)
	recordPlannedOwnership    func() error
	recordControllerOwnership func(string) error
	recordOwnership           func(string, string) error
	now                       func() time.Time
}

type controlledSessionProcessResultV1 struct {
	status controlledsession.ProcessStatusV1
	err    error
}

type controlledSessionSupervisorV1 struct {
	plan    ControlledSessionExecutionPlanV1
	options ControlledSessionRunOptionsV1
	backend controlledSessionSupervisorBackendV1
	machine *controlledsession.MachineV1

	channel    controlledSessionChannelRuntimeV1
	controller controlledSessionControllerRuntimeV1
	workload   controlledSessionWorkloadRuntimeV1
	bridge     *controlledsession.SessionIOBridgeV1

	startupResolved              chan struct{}
	resolveOnce                  sync.Once
	stateChanged                 chan struct{}
	resultDeliveryStarted        chan struct{}
	resultDeliveryResolved       chan struct{}
	startDeliveryOnce            sync.Once
	resolveDeliveryOnce          sync.Once
	outputPublicationStarted     chan struct{}
	outputPublicationResolved    chan struct{}
	startOutputPublicationOnce   sync.Once
	resolveOutputPublicationOnce sync.Once
	outputPublicationSucceeded   bool
	terminationMu                sync.Mutex
	terminationAt                time.Time

	workloadResult     <-chan controlledSessionProcessResultV1
	controllerResult   <-chan controlledSessionProcessResultV1
	workloadObserved   *controlledSessionProcessResultV1
	controllerObserved *controlledSessionProcessResultV1
	workloadRecorded   bool
	controllerStarted  bool
	workloadStarted    bool

	transportHealthy bool
	diagnosticErr    error
}

// RunControlledSessionV1 takes ownership of the admitted workload operation
// lock. The caller must retain the live-run queue-entry lease until this call
// returns. The supervisor durably records both exact inert containers and the
// private channel before releasing the lock and starting either process.
func RunControlledSessionV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	plan ControlledSessionExecutionPlanV1,
	options ControlledSessionRunOptionsV1,
) (ControlledSessionRunResultV1, error) {
	if operation == nil {
		return ControlledSessionRunResultV1{}, fmt.Errorf("run controlled session requires an admitted operation lock")
	}
	if err := operation.RequireHeld(); err != nil {
		return ControlledSessionRunResultV1{}, err
	}
	absoluteDir, err := filepath.Abs(plan.Workload.DeploymentDirectory)
	if err != nil {
		return ControlledSessionRunResultV1{}, releaseControlledSessionOperationV1(operation, fmt.Errorf("resolve controlled-session workload deployment directory: %w", err))
	}
	if filepath.Dir(filepath.Dir(operation.Path())) != absoluteDir {
		return ControlledSessionRunResultV1{}, removeUnstartedControlledSessionV1(operation, plan.LiveRunID, fmt.Errorf("controlled-session operation lock does not belong to workload deployment %q", absoluteDir))
	}
	if ctx == nil || ctx.Done() == nil {
		return ControlledSessionRunResultV1{}, removeUnstartedControlledSessionV1(operation, plan.LiveRunID, fmt.Errorf("run controlled session: cancelable host context is required"))
	}
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		return ControlledSessionRunResultV1{}, removeUnstartedControlledSessionV1(operation, plan.LiveRunID, fmt.Errorf("run controlled session plan: %w", err))
	}
	if err := validateControlledSessionRunOptionsV1(options); err != nil {
		return ControlledSessionRunResultV1{}, removeUnstartedControlledSessionV1(operation, plan.LiveRunID, err)
	}
	if err := operation.RequireQueueEntryLeaseHeldV1(plan.LiveRunID); err != nil {
		return ControlledSessionRunResultV1{}, removeUnstartedControlledSessionV1(operation, plan.LiveRunID, fmt.Errorf("controlled-session admission ownership: %w", err))
	}
	released := false
	ownershipRecorded := false
	partialPreparationCleanupVerified := false
	controllerID := ""
	persistOwnership := func(controllerID string, workloadID string) error {
		ownership := controlledSessionOwnershipFromPlanV1(plan, controllerID, workloadID)
		if _, err := operation.RecordControlledSessionOwnershipV1(ownership); err != nil {
			return fmt.Errorf("persist controlled-session ownership: %w", err)
		}
		ownershipRecorded = true
		return nil
	}
	result, runErr := runControlledSessionV1(ctx, plan, options, controlledSessionSupervisorBackendV1{
		prepareChannel: func(plan ControlledSessionExecutionPlanV1) (controlledSessionChannelRuntimeV1, error) {
			channel, err := PrepareControlledSessionChannelV1(plan)
			if err != nil {
				partialPreparationCleanupVerified = controlledSessionChannelAbsentV1(plan.Channel.HostDirectory)
				return nil, err
			}
			return &privateControlledSessionChannelRuntimeV1{channel: channel}, nil
		},
		prepareController: func(ctx context.Context, plan ControlledSessionContainerPlanV1) (controlledSessionControllerRuntimeV1, error) {
			return prepareDockerControllerWithCleanupVerificationV1(ctx, plan, func() {
				partialPreparationCleanupVerified = true
			})
		},
		prepareWorkload: func(ctx context.Context, plan ControlledSessionContainerPlanV1) (controlledSessionWorkloadRuntimeV1, error) {
			return prepareDockerWorkloadPTYWithContainerIDV1(ctx, plan, func(workloadID string) error {
				return persistOwnership(controllerID, workloadID)
			}, func() {
				partialPreparationCleanupVerified = true
			})
		},
		recordPlannedOwnership: func() error {
			return persistOwnership("", "")
		},
		recordControllerOwnership: func(exactControllerID string) error {
			err := persistOwnership(exactControllerID, "")
			if err == nil {
				controllerID = exactControllerID
			}
			return err
		},
		recordOwnership: func(controllerID string, workloadID string) error {
			if err := persistOwnership(controllerID, workloadID); err != nil {
				return err
			}
			if err := operation.Unlock(); err != nil {
				return fmt.Errorf("release operation lock before controlled-session startup: %w", err)
			}
			released = true
			return nil
		},
		now: time.Now,
	})
	cleaned := result.SessionResult.CleanupStatus.Kind == controlledsession.CleanupStatusSucceededV1 &&
		result.DeliveryTailCleanupStatus.Kind == controlledsession.CleanupStatusSucceededV1
	cleaned = controlledSessionPreparationCanCompleteV1(cleaned, ownershipRecorded, released, partialPreparationCleanupVerified)
	completionErr := finishControlledSessionOwnershipV1(context.WithoutCancel(ctx), absoluteDir, operation, released, plan.LiveRunID, cleaned)
	return result, errors.Join(runErr, completionErr)
}

func controlledSessionChannelAbsentV1(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

func controlledSessionPreparationCanCompleteV1(cleaned bool, ownershipRecorded bool, released bool, partialCleanupVerified bool) bool {
	return cleaned && (!ownershipRecorded || released || partialCleanupVerified)
}

func controlledSessionOwnershipFromPlanV1(plan ControlledSessionExecutionPlanV1, controllerID string, workloadID string) deploy.ControlledSessionOwnershipV1 {
	container := func(plan ControlledSessionContainerPlanV1, id string) deploy.ControlledSessionContainerOwnershipV1 {
		return deploy.ControlledSessionContainerOwnershipV1{
			Role: string(plan.Role), ID: id, Name: plan.Container, DeploymentID: plan.DeploymentID,
			GenerationReference: plan.GenerationReference, BuildIdentity: string(plan.BuildIdentity),
		}
	}
	return deploy.ControlledSessionOwnershipV1{
		LiveRunID: plan.LiveRunID, SessionHandle: plan.Authorization.Handle,
		ChannelDirectory: plan.Channel.HostDirectory,
		Controller:       container(plan.Controller, controllerID), Workload: container(plan.Workload, workloadID),
	}
}

func finishControlledSessionOwnershipV1(
	ctx context.Context,
	deploymentDir string,
	operation *deploy.OperationLock,
	released bool,
	runID string,
	cleaned bool,
) error {
	if released {
		var err error
		operation, err = deploy.AcquireOperationLock(ctx, deploymentDir)
		if err != nil {
			return fmt.Errorf("reacquire operation lock after controlled session: %w", err)
		}
	}
	var completionErr error
	if cleaned {
		_, completionErr = operation.CompleteControlledSessionV1(runID)
		if completionErr != nil {
			completionErr = fmt.Errorf("remove verified-clean controlled-session ownership: %w", completionErr)
		}
	}
	unlockErr := operation.Unlock()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("release controlled-session operation lock: %w", unlockErr)
	}
	return errors.Join(completionErr, unlockErr)
}

func removeUnstartedControlledSessionV1(operation *deploy.OperationLock, runID string, cause error) error {
	var removeErr error
	if deploy.ValidateLiveRunIDV1(runID) == nil {
		_, _, removeErr = operation.RemoveLiveRunV1(runID)
	}
	return releaseControlledSessionOperationV1(operation, errors.Join(cause, removeErr))
}

func releaseControlledSessionOperationV1(operation *deploy.OperationLock, cause error) error {
	if err := operation.Unlock(); err != nil {
		return errors.Join(cause, fmt.Errorf("release controlled-session operation lock: %w", err))
	}
	return cause
}

func runControlledSessionV1(
	ctx context.Context,
	plan ControlledSessionExecutionPlanV1,
	options ControlledSessionRunOptionsV1,
	backend controlledSessionSupervisorBackendV1,
) (ControlledSessionRunResultV1, error) {
	if ctx == nil || ctx.Done() == nil {
		return ControlledSessionRunResultV1{}, fmt.Errorf("run controlled session: cancelable host context is required")
	}
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		return ControlledSessionRunResultV1{}, fmt.Errorf("run controlled session plan: %w", err)
	}
	if err := validateControlledSessionRunOptionsV1(options); err != nil {
		return ControlledSessionRunResultV1{}, err
	}
	if backend.prepareChannel == nil || backend.prepareController == nil || backend.prepareWorkload == nil || backend.now == nil {
		return ControlledSessionRunResultV1{}, fmt.Errorf("run controlled session: supervisor backend is incomplete")
	}
	ownershipCallbacksEnabled := backend.recordPlannedOwnership != nil ||
		backend.recordControllerOwnership != nil || backend.recordOwnership != nil
	if ownershipCallbacksEnabled && (backend.recordPlannedOwnership == nil ||
		backend.recordControllerOwnership == nil || backend.recordOwnership == nil) {
		return ControlledSessionRunResultV1{}, fmt.Errorf("run controlled session: ownership backend is incomplete")
	}
	machine, err := controlledsession.NewMachineV1(plan.Authorization)
	if err != nil {
		return ControlledSessionRunResultV1{}, fmt.Errorf("run controlled session lifecycle: %w", err)
	}
	supervisor := &controlledSessionSupervisorV1{
		plan: plan, options: options, backend: backend, machine: machine,
		startupResolved: make(chan struct{}), stateChanged: make(chan struct{}, 1),
		resultDeliveryStarted: make(chan struct{}), resultDeliveryResolved: make(chan struct{}),
		outputPublicationStarted: make(chan struct{}), outputPublicationResolved: make(chan struct{}),
		transportHealthy: true,
	}
	return supervisor.run(ctx)
}

func validateControlledSessionRunOptionsV1(options ControlledSessionRunOptionsV1) error {
	for _, value := range []struct {
		name  string
		value time.Duration
	}{
		{name: "startup timeout", value: options.StartupTimeout},
		{name: "termination grace", value: options.TerminationGrace},
		{name: "controller finalization timeout", value: options.ControllerFinalizationTimeout},
		{name: "result acknowledgement timeout", value: options.ResultAcknowledgementTimeout},
		{name: "cleanup timeout", value: options.CleanupTimeout},
	} {
		if value.value <= 0 {
			return fmt.Errorf("run controlled session: %s must be finite and positive", value.name)
		}
	}
	return nil
}

func (supervisor *controlledSessionSupervisorV1) run(ctx context.Context) (ControlledSessionRunResultV1, error) {
	startupCtx, cancelStartup := context.WithTimeout(ctx, supervisor.options.StartupTimeout)
	defer cancelStartup()
	if err := supervisor.prepare(startupCtx); err != nil {
		if ctx.Err() != nil {
			_, observeErr := supervisor.observe(controlledsession.ObservationV1{
				Kind: controlledsession.ObservationHostCancelV1, Reason: "host operation was canceled during startup",
			})
			if observeErr != nil {
				supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("record controlled-session startup cancellation: %w", observeErr))
			}
		}
		return supervisor.finishStartupFailure(err)
	}

	if _, err := supervisor.observe(controlledsession.ObservationV1{Kind: controlledsession.ObservationActivatedV1}); err != nil {
		return supervisor.finishStartupFailure(fmt.Errorf("activate controlled-session lifecycle: %w", err))
	}
	supervisor.resolveStartup()

	supervisor.waitForTermination(ctx)
	supervisor.bridge.CancelActiveRequest()
	supervisor.sendTerminating()
	supervisor.stopAndObserveWorkload()
	supervisor.finalizeWorkloadOutput()
	supervisor.waitForControllerFinalization()

	preDeliveryCleanup, preDeliveryRecovery, cleanupErr := supervisor.cleanupWorkload()
	finish := supervisor.finishStatus(preDeliveryCleanup, preDeliveryRecovery)
	transition, finishErr := supervisor.observe(controlledsession.ObservationV1{Kind: controlledsession.ObservationFinishedV1, Finish: &finish})
	if finishErr != nil {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("finish controlled-session lifecycle: %w", finishErr))
	}
	result := transition.Result
	if result == nil {
		result = supervisor.machine.Snapshot().Result
	}
	invocation := supervisor.deliverTerminalResult(result)

	invocation.ControllerStatus, invocation.DeliveryTailCleanupStatus,
		invocation.DeliveryTailRecoveryAction = supervisor.cleanupDeliveryTail()
	return invocation, errors.Join(supervisor.diagnosticErr, cleanupErr)
}

func (supervisor *controlledSessionSupervisorV1) prepare(ctx context.Context) error {
	if supervisor.backend.recordPlannedOwnership != nil {
		if err := supervisor.backend.recordPlannedOwnership(); err != nil {
			return err
		}
	}
	channel, err := supervisor.backend.prepareChannel(supervisor.plan)
	if err != nil {
		return fmt.Errorf("prepare controlled-session channel: %w", err)
	}
	supervisor.channel = channel

	controller, err := supervisor.backend.prepareController(ctx, supervisor.plan.Controller)
	if err != nil {
		return fmt.Errorf("prepare controlled-session controller: %w", err)
	}
	supervisor.controller = controller
	if supervisor.backend.recordControllerOwnership != nil {
		if err := supervisor.backend.recordControllerOwnership(controller.ContainerID()); err != nil {
			return err
		}
	}
	workload, err := supervisor.backend.prepareWorkload(ctx, supervisor.plan.Workload)
	if err != nil {
		return fmt.Errorf("prepare controlled-session workload: %w", err)
	}
	supervisor.workload = workload
	output, err := workload.Output()
	if err != nil {
		return fmt.Errorf("claim controlled-session workload output: %w", err)
	}
	if supervisor.backend.recordOwnership != nil {
		if err := supervisor.backend.recordOwnership(controller.ContainerID(), workload.ContainerID()); err != nil {
			return err
		}
	}
	if err := controller.Start(ctx); err != nil {
		return fmt.Errorf("start controlled-session controller: %w", err)
	}
	supervisor.controllerStarted = true
	supervisor.controllerResult = observeControlledSessionProcessV1(controller.Wait)
	transport, err := channel.Claim(ctx)
	if err != nil {
		return fmt.Errorf("claim controlled-session controller channel: %w", err)
	}
	bridge, err := controlledsession.StartSessionIOBridgeV1(transport, output, supervisor.handleRequest)
	if err != nil {
		return fmt.Errorf("start controlled-session I/O bridge: %w", err)
	}
	supervisor.bridge = bridge
	if err := workload.Start(ctx); err != nil {
		supervisor.workloadStarted = workload.Started()
		if supervisor.workloadStarted {
			supervisor.workloadResult = observeControlledSessionProcessV1(workload.Wait)
		}
		return fmt.Errorf("start controlled-session workload: %w", err)
	}
	supervisor.workloadStarted = true
	supervisor.workloadResult = observeControlledSessionProcessV1(workload.Wait)
	return nil
}

func observeControlledSessionProcessV1(
	wait func(context.Context) (controlledsession.ProcessStatusV1, error),
) <-chan controlledSessionProcessResultV1 {
	result := make(chan controlledSessionProcessResultV1, 1)
	go func() {
		status, err := wait(context.Background())
		result <- controlledSessionProcessResultV1{status: status, err: err}
	}()
	return result
}

func (supervisor *controlledSessionSupervisorV1) handleRequest(ctx context.Context, request controlledsession.RequestV1) error {
	select {
	case <-supervisor.startupResolved:
	case <-ctx.Done():
		return ctx.Err()
	}
	if request.Kind == controlledsession.RequestAcknowledgeTerminatedV1 {
		select {
		case <-supervisor.resultDeliveryStarted:
			select {
			case <-supervisor.resultDeliveryResolved:
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
		}
	}
	if request.Kind == controlledsession.RequestCompleteV1 {
		select {
		case <-supervisor.outputPublicationStarted:
			select {
			case <-supervisor.outputPublicationResolved:
			case <-ctx.Done():
				return ctx.Err()
			}
			if !supervisor.outputPublicationSucceeded {
				return fmt.Errorf("controlled-session workload output finalization event was not published")
			}
		default:
		}
	}
	transition, err := supervisor.machine.ApplyRequest(request)
	if err != nil {
		return err
	}
	supervisor.recordTransition(transition)
	if _, err := controlledsession.ApplyAcceptedWorkloadPTYRequestV1(ctx, supervisor.workload, request); err != nil {
		return err
	}
	supervisor.notifyStateChanged()
	return nil
}

func (supervisor *controlledSessionSupervisorV1) waitForTermination(ctx context.Context) {
	outputDone := supervisor.bridge.OutputDone()
	for supervisor.machine.Snapshot().State == controlledsession.StateActiveV1 {
		select {
		case result := <-supervisor.workloadResult:
			supervisor.workloadObserved = &result
			supervisor.observeWorkloadResult(result)
		case result := <-supervisor.controllerResult:
			supervisor.controllerObserved = &result
			supervisor.observeControllerLoss("controller process exited", result.err)
		case <-supervisor.bridge.RequestsDone():
			supervisor.observeRequestFailure()
		case <-outputDone:
			outputDone = nil
			supervisor.observeOutputTermination()
		case <-supervisor.stateChanged:
		case <-ctx.Done():
			_, err := supervisor.observe(controlledsession.ObservationV1{
				Kind: controlledsession.ObservationHostCancelV1, Reason: "host operation was canceled",
			})
			if err != nil {
				supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("record controlled-session host cancellation: %w", err))
			}
		}
	}
}

func (supervisor *controlledSessionSupervisorV1) observeOutputTermination() {
	result, stopped := supervisor.bridge.OutputTerminalResult()
	if !stopped {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("controlled-session output completion was signaled without a terminal result"))
		return
	}
	supervisor.observeOutputFailure(result)
}

func (supervisor *controlledSessionSupervisorV1) observeOutputFailure(result controlledsession.PTYOutputFinalizationV1) {
	if result.Status.Kind != controlledsession.WorkloadOutputFinalizationFailedV1 {
		return
	}
	if controlledsession.IsPTYOutputDeliveryFailureV1(result) {
		if supervisor.machine.Snapshot().ControllerFinalizationStatus.Kind == controlledsession.ControllerFinalizationLostV1 {
			return
		}
		supervisor.transportHealthy = false
		supervisor.observeControllerLoss("controller event transport was lost", nil)
		return
	}
	if !controlledsession.IsPTYOutputObservationFailureV1(result) ||
		supervisor.machine.Snapshot().RuntimeObservationStatus.Kind == controlledsession.RuntimeObservationLostV1 {
		return
	}
	_, err := supervisor.observe(controlledsession.ObservationV1{
		Kind:   controlledsession.ObservationRuntimeObservationLostV1,
		Reason: "workload output observation was lost",
	})
	if err != nil {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("record controlled-session output observation loss: %w", err))
	}
}

func (supervisor *controlledSessionSupervisorV1) observeWorkloadResult(result controlledSessionProcessResultV1) {
	if supervisor.workloadRecorded {
		return
	}
	supervisor.workloadRecorded = true
	if result.err != nil {
		_, err := supervisor.observe(controlledsession.ObservationV1{
			Kind: controlledsession.ObservationRuntimeObservationLostV1, Reason: "workload runtime observation was lost",
		})
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, result.err, err)
		return
	}
	_, err := supervisor.observe(controlledsession.ObservationV1{
		Kind: controlledsession.ObservationWorkloadExitV1, WorkloadStatus: &result.status,
	})
	if err != nil {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("record controlled-session workload exit: %w", err))
		return
	}
	if supervisor.transportHealthy {
		if err := supervisor.sendPreFinalizationLifecycleEvent(controlledsession.EventV1{
			Kind:         controlledsession.EventWorkloadExitV1,
			WorkloadExit: &controlledsession.WorkloadExitV1{Status: result.status},
		}); err != nil {
			supervisor.loseTransport("send workload exit", err)
		}
	}
}

func (supervisor *controlledSessionSupervisorV1) observeControllerLoss(reason string, detail error) {
	snapshot := supervisor.machine.Snapshot()
	if snapshot.State == controlledsession.StateTerminatedV1 || snapshot.ControllerFinalizationStatus.Kind == controlledsession.ControllerFinalizationLostV1 {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, detail)
		return
	}
	_, err := supervisor.observe(controlledsession.ObservationV1{
		Kind: controlledsession.ObservationControllerLostV1, Reason: reason,
	})
	supervisor.transportHealthy = false
	supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, detail, err)
}

func (supervisor *controlledSessionSupervisorV1) observeRequestFailure() {
	waitCtx, cancel := context.WithTimeout(context.Background(), supervisor.options.CleanupTimeout)
	defer cancel()
	err := supervisor.bridge.WaitRequests(waitCtx)
	if err == nil {
		err = fmt.Errorf("controller request stream stopped before host teardown")
	}
	supervisor.observeControllerLoss("controller request stream was lost", err)
}

func (supervisor *controlledSessionSupervisorV1) sendTerminating() {
	if !supervisor.transportHealthy || supervisor.bridge == nil {
		return
	}
	cause := supervisor.machine.Snapshot().Cause
	if err := supervisor.sendPreFinalizationLifecycleEvent(controlledsession.EventV1{
		Kind: controlledsession.EventTerminatingV1, Terminating: &controlledsession.TerminatingV1{Cause: cause},
	}); err != nil {
		supervisor.loseTransport("send terminating event", err)
	}
}

func (supervisor *controlledSessionSupervisorV1) stopAndObserveWorkload() {
	if !supervisor.workloadStarted {
		return
	}
	if supervisor.workloadObserved != nil && supervisor.workloadObserved.err == nil {
		supervisor.observeWorkloadResult(*supervisor.workloadObserved)
		return
	}

	finalizationDeadline := supervisor.terminationDeadline()
	graceDeadline := earlierControlledSessionDeadlineV1(
		time.Now().Add(supervisor.options.TerminationGrace),
		finalizationDeadline,
	)
	stopCtx, cancel := context.WithDeadline(context.Background(), graceDeadline)
	stopErr := supervisor.workload.RequestGracefulStop(stopCtx)
	cancel()
	if supervisor.workloadObserved == nil {
		timer := time.NewTimer(time.Until(graceDeadline))
		select {
		case result := <-supervisor.workloadResult:
			timer.Stop()
			supervisor.workloadObserved = &result
			if result.err == nil {
				supervisor.observeWorkloadResult(result)
				return
			}
			supervisor.observeWorkloadResult(result)
		case <-timer.C:
		}
	}

	forceCtx, forceCancel := context.WithDeadline(context.Background(), finalizationDeadline)
	forceErr := supervisor.workload.ForceStop(forceCtx)
	forceCancel()
	if supervisor.workloadObserved == nil {
		waitTimer := time.NewTimer(time.Until(finalizationDeadline))
		select {
		case result := <-supervisor.workloadResult:
			supervisor.workloadObserved = &result
			waitTimer.Stop()
			if result.err == nil {
				supervisor.observeWorkloadResult(result)
				return
			}
			supervisor.observeWorkloadResult(result)
		case <-waitTimer.C:
			result := controlledSessionProcessResultV1{
				status: controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusUnavailableV1, Reason: "workload runtime observation was lost"},
				err:    fmt.Errorf("timed out observing controlled-session workload after forced stop"),
			}
			supervisor.workloadObserved = &result
			supervisor.observeWorkloadResult(result)
		}
	}
	if supervisor.workloadObserved == nil || supervisor.workloadObserved.err != nil {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, stopErr, forceErr)
	}
}

func (supervisor *controlledSessionSupervisorV1) finalizeWorkloadOutput() {
	deadline := supervisor.terminationDeadline()
	finalization, err := supervisor.bridge.FinalizeOutput(deadline)
	if err != nil {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, err)
		return
	}
	supervisor.observeOutputFailure(finalization)
	if supervisor.machine.Snapshot().RuntimeObservationStatus.Kind == controlledsession.RuntimeObservationLostV1 &&
		finalization.Status.Kind == controlledsession.WorkloadOutputFinalizationDrainedV1 {
		finalization.Status = controlledsession.WorkloadOutputFinalizationStatusV1{
			Kind:   controlledsession.WorkloadOutputFinalizationFailedV1,
			Reason: "workload runtime observation was lost before output finalization completed",
		}
	}
	supervisor.startOutputPublicationOnce.Do(func() { close(supervisor.outputPublicationStarted) })
	defer supervisor.resolveOutputPublication()
	_, observeErr := supervisor.observe(controlledsession.ObservationV1{
		Kind:                             controlledsession.ObservationWorkloadOutputsFinalizedV1,
		WorkloadOutputFinalizationStatus: &finalization.Status,
	})
	supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, finalization.Err, observeErr)
	if observeErr != nil {
		return
	}
	if !supervisor.transportHealthy {
		return
	}
	if err := supervisor.sendLifecycleEvent(controlledsession.EventV1{
		Kind: controlledsession.EventWorkloadOutputsFinalizedV1,
		WorkloadOutputsFinalized: &controlledsession.WorkloadOutputsFinalizedV1{
			Status: finalization.Status.Kind, Reason: finalization.Status.Reason,
		},
	}); err != nil {
		supervisor.loseTransport("send workload output finalization", err)
		return
	}
	if supervisor.machine.Snapshot().AwaitingWorkloadOutputPublication {
		if _, err := supervisor.observe(controlledsession.ObservationV1{Kind: controlledsession.ObservationWorkloadOutputsPublishedV1}); err != nil {
			supervisor.observeControllerLoss("workload output finalization publication could not be recorded", err)
			return
		}
	}
	supervisor.outputPublicationSucceeded = true
	supervisor.resolveOutputPublication()
}

func (supervisor *controlledSessionSupervisorV1) resolveOutputPublication() {
	supervisor.resolveOutputPublicationOnce.Do(func() { close(supervisor.outputPublicationResolved) })
}

func (supervisor *controlledSessionSupervisorV1) waitForControllerFinalization() {
	if !supervisor.machine.Snapshot().AwaitingControllerFinalization {
		return
	}
	timer := time.NewTimer(supervisor.options.ControllerFinalizationTimeout)
	defer timer.Stop()
	for supervisor.machine.Snapshot().AwaitingControllerFinalization {
		select {
		case <-supervisor.stateChanged:
		case result := <-supervisor.controllerResult:
			supervisor.controllerObserved = &result
			supervisor.observeControllerLoss("controller process exited during finalization", result.err)
		case <-supervisor.bridge.RequestsDone():
			supervisor.observeRequestFailure()
		case <-timer.C:
			_, err := supervisor.observe(controlledsession.ObservationV1{Kind: controlledsession.ObservationControllerFinalizationExpiredV1})
			if err != nil && supervisor.machine.Snapshot().AwaitingControllerFinalization {
				supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("expire controlled-session controller finalization: %w", err))
			}
			return
		}
	}
}

func (supervisor *controlledSessionSupervisorV1) cleanupWorkload() (
	controlledsession.CleanupStatusV1,
	controlledsession.RecoveryActionV1,
	error,
) {
	closeErr := supervisor.workload.Close()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), supervisor.options.CleanupTimeout)
	cleanupErr := supervisor.workload.Cleanup(cleanupCtx)
	cancel()
	err := errors.Join(closeErr, cleanupErr)
	if err == nil {
		return controlledsession.CleanupStatusV1{Kind: controlledsession.CleanupStatusSucceededV1}, controlledsession.RecoveryNoneV1, nil
	}
	return controlledsession.CleanupStatusV1{
		Kind: controlledsession.CleanupStatusFailedV1, Message: "controlled-session workload cleanup failed",
	}, controlledsession.RecoveryRetryCleanupV1, err
}

func (supervisor *controlledSessionSupervisorV1) finishStatus(
	cleanup controlledsession.CleanupStatusV1,
	recovery controlledsession.RecoveryActionV1,
) controlledsession.FinishV1 {
	snapshot := supervisor.machine.Snapshot()
	controller := snapshot.ControllerFinalizationStatus
	if controller.Kind == controlledsession.ControllerFinalizationActiveV1 || controller.Kind == controlledsession.ControllerFinalizationUnknownV1 {
		controller = controlledsession.ControllerFinalizationStatusV1{Kind: controlledsession.ControllerFinalizationNotCompletedV1}
	}
	workload := snapshot.WorkloadStatus
	if workload.Kind == controlledsession.ProcessStatusUnknownV1 && snapshot.RuntimeObservationStatus.Kind == controlledsession.RuntimeObservationLostV1 {
		workload = controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusUnavailableV1, Reason: "workload runtime observation was lost"}
	}
	return controlledsession.FinishV1{
		WorkloadStatus:                   workload,
		WorkloadOutputFinalizationStatus: snapshot.WorkloadOutputFinalizationStatus,
		ControllerFinalizationStatus:     controller,
		CleanupStatus:                    cleanup,
		RecoveryAction:                   recovery,
	}
}

func (supervisor *controlledSessionSupervisorV1) waitForResultAcknowledgement() bool {
	timer := time.NewTimer(supervisor.options.ResultAcknowledgementTimeout)
	defer timer.Stop()
	for supervisor.machine.Snapshot().AwaitingResultAcknowledgement {
		select {
		case <-supervisor.stateChanged:
		case <-supervisor.bridge.RequestsDone():
			return supervisor.machine.Snapshot().ResultAcknowledged
		case result := <-supervisor.controllerResult:
			supervisor.controllerObserved = &result
		case <-timer.C:
			return supervisor.machine.Snapshot().ResultAcknowledged
		}
	}
	return supervisor.machine.Snapshot().ResultAcknowledged
}

func (supervisor *controlledSessionSupervisorV1) cleanupDeliveryTail() (
	controlledsession.ProcessStatusV1,
	controlledsession.CleanupStatusV1,
	controlledsession.RecoveryActionV1,
) {
	if supervisor.bridge != nil {
		supervisor.bridge.StopRequests()
	}
	var cleanupErr error
	if supervisor.channel != nil {
		cleanupErr = errors.Join(cleanupErr, supervisor.channel.Close())
	}
	if supervisor.controller != nil {
		if supervisor.controllerStarted && supervisor.controllerObserved == nil {
			select {
			case result := <-supervisor.controllerResult:
				supervisor.controllerObserved = &result
			default:
			}
		}
		if supervisor.controllerStarted && supervisor.controllerObserved == nil {
			graceDeadline := time.Now().Add(supervisor.options.TerminationGrace)
			stopCtx, cancel := context.WithDeadline(context.Background(), graceDeadline)
			stopErr := supervisor.controller.RequestGracefulStop(stopCtx)
			cancel()
			timer := time.NewTimer(time.Until(graceDeadline))
			select {
			case result := <-supervisor.controllerResult:
				supervisor.controllerObserved = &result
				timer.Stop()
				if result.err != nil {
					cleanupErr = errors.Join(cleanupErr, stopErr)
				}
			case <-timer.C:
				forceCtx, forceCancel := context.WithTimeout(context.Background(), supervisor.options.CleanupTimeout)
				forceErr := supervisor.controller.ForceStop(forceCtx)
				forceCancel()
				waitTimer := time.NewTimer(supervisor.options.CleanupTimeout)
				select {
				case result := <-supervisor.controllerResult:
					supervisor.controllerObserved = &result
					waitTimer.Stop()
					if result.err != nil {
						cleanupErr = errors.Join(cleanupErr, stopErr, forceErr)
					}
				case <-waitTimer.C:
					cleanupErr = errors.Join(cleanupErr, stopErr, forceErr, fmt.Errorf("timed out observing controlled-session controller after forced stop"))
				}
			}
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), supervisor.options.CleanupTimeout)
		cleanupErr = errors.Join(cleanupErr, supervisor.controller.Cleanup(cleanupCtx))
		cleanupCancel()
	}
	status := controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusUnknownV1}
	if supervisor.controllerObserved != nil {
		status = supervisor.controllerObserved.status
		cleanupErr = errors.Join(cleanupErr, supervisor.controllerObserved.err)
	}
	if cleanupErr != nil {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, cleanupErr)
		return status, controlledsession.CleanupStatusV1{
			Kind: controlledsession.CleanupStatusFailedV1, Message: "controlled-session delivery-tail cleanup failed",
		}, controlledsession.RecoveryRetryCleanupV1
	}
	return status, controlledsession.CleanupStatusV1{Kind: controlledsession.CleanupStatusSucceededV1}, controlledsession.RecoveryNoneV1
}

func (supervisor *controlledSessionSupervisorV1) finishStartupFailure(cause error) (ControlledSessionRunResultV1, error) {
	var finalizedOutput *controlledsession.WorkloadOutputFinalizationStatusV1
	if !supervisor.workloadStarted && supervisor.bridge != nil && supervisor.workload != nil {
		closeErr := supervisor.workload.Close()
		finalization, finalizationErr := supervisor.bridge.FinalizeOutput(
			time.Now().Add(supervisor.options.CleanupTimeout),
		)
		finalizedOutput = &finalization.Status
		supervisor.diagnosticErr = errors.Join(
			supervisor.diagnosticErr,
			closeErr,
			finalization.Err,
			finalizationErr,
		)
	}
	if supervisor.machine.Snapshot().ControllerFinalizationStatus.Kind == controlledsession.ControllerFinalizationUnknownV1 {
		_, observeErr := supervisor.observe(controlledsession.ObservationV1{
			Kind:                             controlledsession.ObservationStartupFailureV1,
			Reason:                           "controlled-session startup failed",
			WorkloadOutputPending:            supervisor.workloadStarted,
			WorkloadOutputFinalizationStatus: finalizedOutput,
		})
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, observeErr)
	}
	supervisor.resolveStartup()
	if supervisor.workloadStarted {
		supervisor.sendTerminating()
		supervisor.stopAndObserveWorkload()
		supervisor.finalizeWorkloadOutput()
	}
	preCleanup, recovery, cleanupErr := controlledsession.CleanupStatusV1{Kind: controlledsession.CleanupStatusSucceededV1}, controlledsession.RecoveryNoneV1, error(nil)
	if supervisor.workload != nil {
		preCleanup, recovery, cleanupErr = supervisor.cleanupWorkload()
	}
	finish := supervisor.finishStatus(preCleanup, recovery)
	transition, finishErr := supervisor.observe(controlledsession.ObservationV1{Kind: controlledsession.ObservationFinishedV1, Finish: &finish})
	invocation := supervisor.deliverTerminalResult(transition.Result)
	invocation.ControllerStatus, invocation.DeliveryTailCleanupStatus,
		invocation.DeliveryTailRecoveryAction = supervisor.cleanupDeliveryTail()
	return invocation, errors.Join(cause, supervisor.diagnosticErr, cleanupErr, finishErr)
}

func (supervisor *controlledSessionSupervisorV1) deliverTerminalResult(result *controlledsession.ResultV1) ControlledSessionRunResultV1 {
	invocation := ControlledSessionRunResultV1{}
	if result == nil {
		return invocation
	}
	invocation.SessionResult = *result
	if !supervisor.transportHealthy || supervisor.bridge == nil {
		return invocation
	}
	supervisor.startDeliveryOnce.Do(func() { close(supervisor.resultDeliveryStarted) })
	defer supervisor.resolveDelivery()
	if err := supervisor.sendLifecycleEvent(controlledsession.EventV1{
		Kind: controlledsession.EventTerminatedV1, Terminated: result,
	}); err != nil {
		supervisor.loseTransport("deliver terminal result", err)
		return invocation
	}
	invocation.ResultDelivered = true
	if _, err := supervisor.observe(controlledsession.ObservationV1{Kind: controlledsession.ObservationResultDeliveredV1}); err != nil {
		supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("record controlled-session result delivery: %w", err))
		return invocation
	}
	supervisor.resolveDelivery()
	invocation.ResultAcknowledged = supervisor.waitForResultAcknowledgement()
	return invocation
}

func (supervisor *controlledSessionSupervisorV1) resolveDelivery() {
	supervisor.resolveDeliveryOnce.Do(func() { close(supervisor.resultDeliveryResolved) })
}

func (supervisor *controlledSessionSupervisorV1) observe(observation controlledsession.ObservationV1) (controlledsession.TransitionV1, error) {
	transition, err := supervisor.machine.Observe(observation)
	if err == nil {
		supervisor.recordTransition(transition)
		supervisor.notifyStateChanged()
	}
	return transition, err
}

func (supervisor *controlledSessionSupervisorV1) recordTransition(transition controlledsession.TransitionV1) {
	if !transition.BeginTermination {
		return
	}
	supervisor.terminationMu.Lock()
	if supervisor.terminationAt.IsZero() {
		supervisor.terminationAt = supervisor.backend.now()
	}
	supervisor.terminationMu.Unlock()
}

func (supervisor *controlledSessionSupervisorV1) notifyStateChanged() {
	select {
	case supervisor.stateChanged <- struct{}{}:
	default:
	}
}

func (supervisor *controlledSessionSupervisorV1) resolveStartup() {
	supervisor.resolveOnce.Do(func() { close(supervisor.startupResolved) })
}

func (supervisor *controlledSessionSupervisorV1) terminationDeadline() time.Time {
	supervisor.terminationMu.Lock()
	defer supervisor.terminationMu.Unlock()
	if supervisor.terminationAt.IsZero() {
		supervisor.terminationAt = supervisor.backend.now()
	}
	return supervisor.terminationAt.Add(controlledSessionOutputFinalizationTimeoutV1)
}

func (supervisor *controlledSessionSupervisorV1) sendLifecycleEvent(event controlledsession.EventV1) error {
	ctx, cancel := context.WithTimeout(context.Background(), supervisor.options.CleanupTimeout)
	defer cancel()
	return supervisor.bridge.SendLifecycleEvent(ctx, event)
}

func (supervisor *controlledSessionSupervisorV1) sendPreFinalizationLifecycleEvent(event controlledsession.EventV1) error {
	deadline := earlierControlledSessionDeadlineV1(
		time.Now().Add(supervisor.options.CleanupTimeout),
		supervisor.terminationDeadline(),
	)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	return supervisor.bridge.SendLifecycleEvent(ctx, event)
}

func earlierControlledSessionDeadlineV1(left time.Time, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func (supervisor *controlledSessionSupervisorV1) loseTransport(action string, err error) {
	supervisor.transportHealthy = false
	supervisor.diagnosticErr = errors.Join(supervisor.diagnosticErr, fmt.Errorf("%s: %w", action, err))
	supervisor.observeControllerLoss("controller event transport was lost", nil)
}
