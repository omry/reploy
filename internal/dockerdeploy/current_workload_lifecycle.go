package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func isStaleDockerNetworkError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "network ") && strings.Contains(err.Error(), " not found")
}

type CurrentWorkloadLifecycleInputV1 struct {
	Operation           *deploy.OperationLock
	Store               providerstore.Store
	Current             CurrentBuild
	Plan                CurrentRuntimePlanV1
	Environment         string
	DeploymentDir       string
	Action              string
	RunOptions          RunOptions
	Progress            io.Writer
	StartCommand        *CommandSpec
	StopCommand         *CommandSpec
	PrivateEnvironment  privateWorkloadEnvironmentV1
	PrivateRuntimeMasks []privateRuntimeMaskV1
}

type currentWorkloadLifecycleBackendV1 struct {
	acquire      func(context.Context, string) (*deploy.OperationLock, error)
	planStart    func(CurrentRuntimePlanV1, CurrentBuild) (LifecyclePlan, error)
	planStop     func(CurrentRuntimePlanV1, CurrentBuild) (LifecyclePlan, error)
	planRestart  func(CurrentRuntimePlanV1, CurrentBuild) (LifecyclePlan, error)
	execute      func(context.Context, LifecyclePlan, LifecycleExecutor) error
	runPublished func(context.Context, PublishedRuntimeContainerInput, PublishedRuntimeContainerRunnerV1) error
	command      func(string, string) (CommandSpec, error)
	transient    func(DockerExecutionPlan, ResolvedEnvironmentCommand, *transientOutputMount, bool, bool) (CommandSpec, error)
	cleanup      func(string) CommandSpec
	runTemporary func(temporaryCommandRunner, CommandSpec, CommandSpec, RunOptions) error
	runCommand   func(CommandSpec, RunOptions) error
	inject       func(context.Context, string, string, ApplicationSandboxPlanV1, privateWorkloadEnvironmentV1, RunOptions, commandRunner) error
	readiness    func(context.Context, EndpointExecutionPlan, func(context.Context) error) error
	serviceCheck func(string, string, time.Duration) error
}

func RunCurrentWorkloadLifecycleV1(ctx context.Context, input CurrentWorkloadLifecycleInputV1) error {
	return runCurrentWorkloadLifecycleV1(ctx, input, currentWorkloadLifecycleBackendV1{
		acquire: deploy.AcquireOperationLock,
		planStart: func(plan CurrentRuntimePlanV1, current CurrentBuild) (LifecyclePlan, error) {
			return planLockedStartLifecycleV1(plan.Document, plan.Docker, current.Lock.Catalog)
		},
		planStop: func(plan CurrentRuntimePlanV1, current CurrentBuild) (LifecyclePlan, error) {
			return planLockedStopLifecycleV1(plan.Document, plan.Docker, current.Lock.Catalog)
		},
		planRestart: func(plan CurrentRuntimePlanV1, current CurrentBuild) (LifecyclePlan, error) {
			return planLockedRestartLifecycleV1(plan.Document, plan.Docker, current.Lock.Catalog)
		},
		execute:      ExecuteLifecycle,
		runPublished: RunPublishedRuntimeContainerV1,
		command:      RuntimeCommand,
		transient:    TransientCommandSpec,
		cleanup:      TemporaryContainerCleanupCommand,
		runTemporary: runTemporaryContainerCommand,
		runCommand:   runRuntimeCommand,
		inject:       injectPrivateWorkloadEnvironmentV1,
		readiness:    WaitForHTTPReadinessWithServiceCheck,
		serviceCheck: requireComposeServiceRunning,
	})
}

func runCurrentWorkloadLifecycleV1(ctx context.Context, input CurrentWorkloadLifecycleInputV1, backend currentWorkloadLifecycleBackendV1) error {
	if ctx == nil {
		return fmt.Errorf("run current workload lifecycle requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.Operation != nil {
		if err := input.Operation.RequireHeld(); err != nil {
			return err
		}
	} else if input.DeploymentDir == "" {
		return fmt.Errorf("run current workload lifecycle requires a deployment directory")
	}
	if input.Action != "up" && input.Action != "down" && input.Action != "restart" {
		return fmt.Errorf("current workload lifecycle action must be up, down, or restart")
	}
	if backend.acquire == nil || backend.planStart == nil || backend.planStop == nil || backend.planRestart == nil || backend.execute == nil || backend.runPublished == nil || backend.command == nil || backend.transient == nil || backend.cleanup == nil || backend.runTemporary == nil || backend.runCommand == nil || backend.inject == nil || backend.readiness == nil || backend.serviceCheck == nil {
		return fmt.Errorf("run current workload lifecycle requires a complete backend")
	}
	var lifecycle LifecyclePlan
	var err error
	if input.Action == "restart" {
		lifecycle, err = backend.planRestart(input.Plan, input.Current)
	} else if input.Action == "down" {
		lifecycle, err = backend.planStop(input.Plan, input.Current)
	} else {
		lifecycle, err = backend.planStart(input.Plan, input.Current)
	}
	if err != nil {
		return err
	}
	withOperation := func(runCtx context.Context, run func(*deploy.OperationLock) error) (err error) {
		operation := input.Operation
		owned := false
		if operation == nil {
			operation, err = backend.acquire(runCtx, input.DeploymentDir)
			if err != nil {
				return err
			}
			owned = true
		}
		if owned {
			defer func() {
				if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
					err = unlockErr
				}
			}()
		}
		return run(operation)
	}
	runPublished := func(runCtx context.Context, invocation RuntimeInvocationV1, run PublishedRuntimeContainerRunnerV1) error {
		return withOperation(runCtx, func(operation *deploy.OperationLock) error {
			return backend.runPublished(runCtx, PublishedRuntimeContainerInput{
				Operation: operation, Store: input.Store, Environment: input.Environment,
				DeploymentDir: input.DeploymentDir, DockerPlan: input.Plan.Docker, Invocation: invocation,
			}, run)
		})
	}
	runOptions := input.RunOptions
	runOptions.Context = ctx
	executor := LifecycleExecutor{
		RunCommand: func(commandCtx context.Context, command ResolvedEnvironmentCommand) error {
			commandPlan, err := effectiveCommandDockerPlanV1(input.Plan.Document, input.Plan.Docker, command.Name)
			if err != nil {
				return err
			}
			invocation, err := CommandRuntimeInvocationV1(commandPlan, command.Name, nil)
			if err != nil {
				return err
			}
			return runPublished(commandCtx, invocation, func(runCtx context.Context, gated CurrentBuild) error {
				spec, err := backend.transient(commandPlan, command, nil, false, false)
				if err != nil {
					return err
				}
				options := runOptions
				options.Context = runCtx
				return backend.runTemporary(backend.runCommand, spec, backend.cleanup(transientCommandContainerName(input.Plan.Docker)), options)
			})
		},
		Readiness: func(readinessCtx context.Context, endpoint EndpointExecutionPlan) error {
			return backend.readiness(readinessCtx, endpoint, func(context.Context) error {
				return backend.serviceCheck(input.DeploymentDir, "", input.RunOptions.DockerPreflightTimeout)
			})
		},
		Start: func(startCtx context.Context) error {
			if input.StartCommand == nil && input.PrivateRuntimeMasks != nil {
				if err := validatePrivateRuntimeMaskSnapshotV1(input.Plan.Docker, input.PrivateRuntimeMasks); err != nil {
					return fmt.Errorf("validate private runtime isolation before workload creation: %w", err)
				}
			}
			invocation, err := WorkloadRuntimeInvocationV1(input.Plan.Docker)
			if err != nil {
				return err
			}
			var spec CommandSpec
			planned := false
			launch := func(ctx context.Context) error {
				if input.StartCommand != nil {
					if !planned {
						spec = *input.StartCommand
						planned = true
					}
					options := runOptions
					options.Context = ctx
					return backend.runCommand(spec, options)
				}
				return runPublished(ctx, invocation, func(runCtx context.Context, _ CurrentBuild) error {
					if !planned {
						var err error
						spec, err = backend.command(input.DeploymentDir, "up")
						if err != nil {
							return err
						}
						planned = true
					}
					options := runOptions
					options.Context = runCtx
					return backend.runCommand(spec, options)
				})
			}
			err = launch(startCtx)
			if err != nil && input.Action == "up" && input.StartCommand == nil && isStaleDockerNetworkError(err) {
				if input.RunOptions.Stderr != nil {
					fmt.Fprintf(input.RunOptions.Stderr, "%v\n", err)
				}
				if input.Progress != nil {
					fmt.Fprintln(input.Progress, "detected stale Docker network state; running down --remove-orphans and retrying up")
				}
				down, downErr := backend.command(input.DeploymentDir, "down")
				if downErr != nil {
					return downErr
				}
				options := runOptions
				options.Context = startCtx
				downErr = withOperation(startCtx, func(*deploy.OperationLock) error {
					return backend.runCommand(down, options)
				})
				if downErr != nil {
					return fmt.Errorf("recover stale Docker network state: %w", downErr)
				}
				err = launch(startCtx)
			}
			if err != nil {
				return err
			}
			if input.PrivateEnvironment.Present && input.StartCommand == nil {
				if err := backend.inject(
					startCtx,
					spec.Name,
					input.Plan.Docker.ContainerName,
					input.Plan.Docker.Sandbox,
					input.PrivateEnvironment,
					runOptions,
					backend.runCommand,
				); err != nil {
					down, downErr := backend.command(input.DeploymentDir, "down")
					if downErr != nil {
						return errors.Join(err, downErr)
					}
					cleanupOptions := runOptions
					cleanupOptions.Context = context.WithoutCancel(startCtx)
					cleanupOptions.Stdin = nil
					cleanupOptions.Stdout = nil
					cleanupOptions.Stderr = nil
					return errors.Join(err, cleanupPrivateWorkloadContainerV1(down, cleanupOptions, backend.runCommand))
				}
			}
			return backend.serviceCheck(input.DeploymentDir, "", input.RunOptions.DockerPreflightTimeout)
		},
		Stop: func(stopCtx context.Context) error {
			var spec CommandSpec
			if input.StopCommand != nil {
				spec = *input.StopCommand
			} else {
				var err error
				spec, err = backend.command(input.DeploymentDir, "down")
				if err != nil {
					return err
				}
			}
			options := runOptions
			options.Context = stopCtx
			return withOperation(stopCtx, func(*deploy.OperationLock) error {
				return backend.runCommand(spec, options)
			})
		},
	}
	return backend.execute(ctx, lifecycle, executor)
}
