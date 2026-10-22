package dockerdeploy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
)

type workloadLifecycleV1 struct {
	command       CommandSpec
	containerName string
	run           commandRunner
	options       RunOptions
	operation     func(context.Context, func(*deploy.OperationLock) error) error
	record        deploy.WorkloadLifecycleV1
	binding       deploy.WorkloadLifecycleV1
	initial       privateWorkloadContainerV1
	started       privateWorkloadStartResultV1
	private       bool
}

func preserveConfirmedWorkloadRuntimeInputsV1(
	operation *deploy.OperationLock,
	deploymentDir string,
	plan CurrentRuntimePlanV1,
	record deploy.WorkloadLifecycleV1,
	publish func(*deploy.OperationLock, string, CurrentRuntimePlanV1) (bool, error),
) error {
	if publish == nil || plan.Docker.PrivateEnvironment == record.HandoffComplete {
		return nil
	}
	preserved := plan
	preserved.Docker.PrivateEnvironment = record.HandoffComplete
	if _, err := publish(operation, deploymentDir, preserved); err != nil {
		return fmt.Errorf("preserve confirmed workload runtime inputs: %w", err)
	}
	return nil
}

func workloadRuntimeBindingV1(plan DockerExecutionPlan) (string, error) {
	// Bind exactly the rendered workload inputs, excluding presentation-only
	// plan metadata, deployment-local private environment values, and the
	// presence-only private launcher mode. A confirmed process retains the
	// launcher mode that created it until an explicit fresh startup.
	plan.PrivateEnvironment = false
	rendered, err := RenderDockerInputs(plan, "workload-lifecycle")
	if err != nil {
		return "", fmt.Errorf("bind workload runtime: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(rendered.Compose)), nil
}

func (lifecycle *workloadLifecycleV1) inspect(ctx context.Context) (privateWorkloadContainerV1, error) {
	return inspectPrivateWorkloadContainerV1(ctx, lifecycle.command, lifecycle.containerName, lifecycle.options, lifecycle.run)
}

func (lifecycle *workloadLifecycleV1) initialize(ctx context.Context, runtime string, private bool) (bool, bool, error) {
	expected, err := privateWorkloadRecordV1(lifecycle.command, runtime)
	if err != nil {
		return false, false, err
	}
	lifecycle.private = private
	lifecycle.binding = expected
	var previous deploy.WorkloadLifecycleV1
	var found bool
	err = lifecycle.operation(ctx, func(operation *deploy.OperationLock) error {
		var err error
		previous, found, err = operation.ReadWorkloadLifecycleV1()
		if err != nil {
			return err
		}
		lifecycle.initial, err = lifecycle.inspect(ctx)
		return err
	})
	if err != nil {
		return false, false, err
	}
	lifecycle.record = expected
	same := found && previous.Endpoint == expected.Endpoint && previous.ContainerID == lifecycle.initial.ID && previous.StartedAt == lifecycle.initial.StartedAt
	if same {
		lifecycle.record = previous
	}
	// The current .env presence describes a future startup. A confirmed
	// process is complete according to its own durable startup evidence, even
	// when the current file presence differs from the mode it used. A claimed
	// transfer still requires a completed handoff.
	completed := same && previous.State == "up" && previous.StartupComplete &&
		(!previous.TransferClaimed || previous.HandoffComplete)
	return completed && previous.Runtime == expected.Runtime && lifecycle.initial.Running, completed && previous.Runtime != expected.Runtime && lifecycle.initial.Running, nil
}

func (lifecycle *workloadLifecycleV1) write(ctx context.Context, state string) error {
	lifecycle.record.State = state
	if state == "up" {
		lifecycle.record.StartupComplete = true
	}
	return lifecycle.operation(ctx, func(operation *deploy.OperationLock) error {
		return operation.WriteWorkloadLifecycleV1(lifecycle.record)
	})
}

func (lifecycle *workloadLifecycleV1) refresh(ctx context.Context) error {
	return lifecycle.operation(ctx, func(operation *deploy.OperationLock) error {
		record, found, err := operation.ReadWorkloadLifecycleV1()
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("workload startup evidence is missing; use `reploy restart`")
		}
		lifecycle.record = record
		return nil
	})
}

func (lifecycle *workloadLifecycleV1) confirmApplicationLaunch(ctx context.Context, current privateWorkloadContainerV1) error {
	return confirmApplicationLaunchV1(ctx, lifecycle.command, current, lifecycle.options, lifecycle.run)
}

func confirmApplicationLaunchV1(ctx context.Context, command CommandSpec, current privateWorkloadContainerV1, options RunOptions, run commandRunner) error {
	proofCtx, cancel := context.WithTimeout(ctx, privateWorkloadEnvironmentTimeoutV1)
	defer cancel()
	options.Context, options.Stdin = proofCtx, nil
	proof := command
	proof.Args = []string{"exec", current.ID, deploy.ApplicationStartupVerifierPathV1, "application-launch-proof-v1"}
	if err := run(proof, options); err != nil {
		return fmt.Errorf("workload application launch is unconfirmed; use `reploy restart`: %w", err)
	}
	return nil
}

func (lifecycle *workloadLifecycleV1) complete(ctx context.Context) error {
	if err := lifecycle.refresh(ctx); err != nil {
		return err
	}
	current, err := lifecycle.inspect(ctx)
	if err != nil {
		return err
	}
	if lifecycle.record.Endpoint != lifecycle.binding.Endpoint || lifecycle.record.Runtime != lifecycle.binding.Runtime || !current.Running || current.ID != lifecycle.record.ContainerID || current.StartedAt != lifecycle.record.StartedAt || lifecycle.private && !lifecycle.record.HandoffComplete {
		return fmt.Errorf("workload startup completion is unconfirmed; use `reploy restart`")
	}
	if !lifecycle.private {
		if err := lifecycle.confirmApplicationLaunch(ctx, current); err != nil {
			return err
		}
		confirmed, err := lifecycle.inspect(ctx)
		if err != nil {
			return err
		}
		if !confirmed.Running || confirmed.ID != current.ID || confirmed.StartedAt != current.StartedAt {
			return fmt.Errorf("workload startup completion is unconfirmed; use `reploy restart`")
		}
	}
	return lifecycle.write(ctx, "up")
}

func (lifecycle *workloadLifecycleV1) stop(ctx context.Context, stop func() error) error {
	current, err := lifecycle.inspect(ctx)
	if err != nil {
		return err
	}
	if current.Running {
		// The admitted stop targets this deployment. Retain its observed
		// process identity while shutdown is in progress.
		lifecycle.record.ContainerID, lifecycle.record.StartedAt = current.ID, current.StartedAt
	}
	if err := lifecycle.write(ctx, "stopping"); err != nil {
		return err
	}
	stopErr := stop()
	current, inspectErr := lifecycle.inspect(context.WithoutCancel(ctx))
	if inspectErr != nil {
		return errors.Join(stopErr, inspectErr)
	}
	if current.Running {
		return errors.Join(stopErr, fmt.Errorf("workload shutdown is not confirmed; status is stopping"))
	}
	return errors.Join(stopErr, lifecycle.write(context.WithoutCancel(ctx), "down"))
}

func (lifecycle *workloadLifecycleV1) rollback(ctx context.Context, cause error) error {
	if !lifecycle.started.Owned {
		return cause
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), privateWorkloadEnvironmentTimeoutV1)
	defer cancel()
	err := lifecycle.operation(cleanupCtx, func(operation *deploy.OperationLock) error {
		record, found, readErr := operation.ReadWorkloadLifecycleV1()
		if readErr != nil || !found {
			record = lifecycle.binding
		}
		stopErr := stopFailedPrivateWorkloadStartV1(cleanupCtx, lifecycle.command, lifecycle.started, operation, record, lifecycle.options, lifecycle.run)
		return errors.Join(readErr, stopErr)
	})
	return errors.Join(cause, err)
}
