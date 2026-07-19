package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

type LifecycleOperationKind string

const (
	LifecycleMaterialize LifecycleOperationKind = "materialize"
	LifecycleCommand     LifecycleOperationKind = "command"
	LifecycleReadiness   LifecycleOperationKind = "readiness"
	LifecycleStart       LifecycleOperationKind = "start"
	LifecycleStop        LifecycleOperationKind = "stop"
	LifecycleSuccess     LifecycleOperationKind = "success"
)

type LifecycleOperation struct {
	Kind     LifecycleOperationKind
	Event    string
	Command  *ResolvedEnvironmentCommand
	Endpoint *EndpointExecutionPlan
	Lines    []string
}

type LifecyclePlan struct {
	Operations []LifecycleOperation
}

func PlanInstallLifecycle(document blueprint.Document, dockerPlan DockerExecutionPlan, outputs map[string]providers.ExecutableOutput, start bool) (LifecyclePlan, error) {
	plan := LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleMaterialize, Event: "install"}}}
	if err := appendLifecycleSteps(&plan, "after_install", document.Environment.Install.AfterInstall, document, dockerPlan, outputs); err != nil {
		return LifecyclePlan{}, err
	}
	if start {
		if err := appendStartLifecycle(&plan, document, dockerPlan, outputs); err != nil {
			return LifecyclePlan{}, err
		}
	}
	if len(document.Environment.Install.Success.Lines) > 0 {
		lines, err := resolveInstallSuccessLines(document, dockerPlan)
		if err != nil {
			return LifecyclePlan{}, fmt.Errorf("resolve install success lines: %w", err)
		}
		plan.Operations = append(plan.Operations, LifecycleOperation{Kind: LifecycleSuccess, Event: "success", Lines: lines})
	}
	return plan, nil
}

func resolveInstallSuccessLines(document blueprint.Document, plan DockerExecutionPlan) ([]string, error) {
	return resolveEnvironmentOperationStrings(document, plan, document.Environment.Install.Success.Lines)
}

func resolveEnvironmentOperationStrings(document blueprint.Document, plan DockerExecutionPlan, values []string) ([]string, error) {
	paths := map[string]any{}
	for name, item := range document.Environment.Paths {
		paths[name] = map[string]any{
			"container": item.Container,
			"writable":  item.Writable,
			"update":    string(item.Update),
		}
	}
	environmentEndpoints := map[string]any{}
	reployEndpoints := map[string]any{}
	if plan.Workload != nil && document.Environment.Workload != nil {
		for name, endpoint := range plan.Workload.Endpoints {
			environmentEndpoint := document.Environment.Workload.Endpoints[name]
			environmentEndpoints[name] = map[string]any{"scheme": environmentEndpoint.Scheme, "port": environmentEndpoint.Port}
			reployEndpoints[name] = map[string]any{
				"bind":    map[string]any{"address": endpoint.BindAddress, "port": endpoint.ContainerPort},
				"publish": map[string]any{"address": endpoint.PublishAddress, "port": endpoint.PublishedPort},
			}
		}
	}
	roots := map[string]any{
		"blueprint": map[string]any{"schema": document.Blueprint.Schema, "version": document.Blueprint.Version},
		"environment": map[string]any{
			"id":       document.Environment.ID,
			"paths":    paths,
			"workload": map[string]any{"endpoints": environmentEndpoints},
		},
		"docker":          map[string]any{"image": document.Docker.Image},
		"reploy.workload": map[string]any{"endpoints": reployEndpoints},
	}
	return blueprint.ResolveOperationStrings(values, document.Environment.Vars, plan.Phase, plan.Scope, roots)
}

func PlanStartLifecycle(document blueprint.Document, dockerPlan DockerExecutionPlan, outputs map[string]providers.ExecutableOutput) (LifecyclePlan, error) {
	plan := LifecyclePlan{}
	if err := appendStartLifecycle(&plan, document, dockerPlan, outputs); err != nil {
		return LifecyclePlan{}, err
	}
	return plan, nil
}

func appendStartLifecycle(plan *LifecyclePlan, document blueprint.Document, dockerPlan DockerExecutionPlan, outputs map[string]providers.ExecutableOutput) error {
	if document.Environment.Workload == nil || dockerPlan.Workload == nil {
		return fmt.Errorf("environment has no workload to start")
	}
	if err := appendLifecycleSteps(plan, "before_start", document.Environment.Workload.Runtime.BeforeStart, document, dockerPlan, outputs); err != nil {
		return err
	}
	plan.Operations = append(plan.Operations, LifecycleOperation{Kind: LifecycleStart, Event: "start"})
	return appendLifecycleSteps(plan, "after_start", document.Environment.Workload.Runtime.AfterStart, document, dockerPlan, outputs)
}

func PlanStopLifecycle(document blueprint.Document, dockerPlan DockerExecutionPlan, outputs map[string]providers.ExecutableOutput) (LifecyclePlan, error) {
	if document.Environment.Workload == nil || dockerPlan.Workload == nil {
		return LifecyclePlan{}, fmt.Errorf("environment has no workload to stop")
	}
	plan := LifecyclePlan{}
	if err := appendLifecycleSteps(&plan, "before_stop", document.Environment.Workload.Runtime.BeforeStop, document, dockerPlan, outputs); err != nil {
		return LifecyclePlan{}, err
	}
	plan.Operations = append(plan.Operations, LifecycleOperation{Kind: LifecycleStop, Event: "stop"})
	if err := appendLifecycleSteps(&plan, "after_stop", document.Environment.Workload.Runtime.AfterStop, document, dockerPlan, outputs); err != nil {
		return LifecyclePlan{}, err
	}
	return plan, nil
}

func PlanRestartLifecycle(document blueprint.Document, dockerPlan DockerExecutionPlan, outputs map[string]providers.ExecutableOutput) (LifecyclePlan, error) {
	stop, err := PlanStopLifecycle(document, dockerPlan, outputs)
	if err != nil {
		return LifecyclePlan{}, err
	}
	start, err := PlanStartLifecycle(document, dockerPlan, outputs)
	if err != nil {
		return LifecyclePlan{}, err
	}
	return LifecyclePlan{Operations: append(stop.Operations, start.Operations...)}, nil
}

func appendLifecycleSteps(plan *LifecyclePlan, event string, steps []blueprint.Step, document blueprint.Document, dockerPlan DockerExecutionPlan, outputs map[string]providers.ExecutableOutput) error {
	for stepIndex, step := range steps {
		for _, endpointName := range step.Requires.Endpoints {
			if dockerPlan.Workload == nil {
				return fmt.Errorf("%s step %d requires endpoint %q without a workload", event, stepIndex, endpointName)
			}
			endpoint, exists := dockerPlan.Workload.Endpoints[endpointName]
			if !exists {
				return fmt.Errorf("%s step %d requires unknown endpoint %q", event, stepIndex, endpointName)
			}
			if endpoint.Readiness == nil {
				return fmt.Errorf("%s step %d requires endpoint %q without readiness", event, stepIndex, endpointName)
			}
			copyEndpoint := endpoint
			plan.Operations = append(plan.Operations, LifecycleOperation{Kind: LifecycleReadiness, Event: event, Endpoint: &copyEndpoint})
		}
		for actionIndex, action := range step.Actions {
			name, forwarded, err := MatchLifecycleCommand(document, action.Environment)
			if err != nil {
				return fmt.Errorf("%s step %d action %d: %w", event, stepIndex, actionIndex, err)
			}
			command, err := ResolveEnvironmentCommandForPlan(document, outputs, dockerPlan, name, forwarded)
			if err != nil {
				return fmt.Errorf("%s step %d action %d: %w", event, stepIndex, actionIndex, err)
			}
			plan.Operations = append(plan.Operations, LifecycleOperation{Kind: LifecycleCommand, Event: event, Command: &command})
		}
	}
	return nil
}

type LifecycleExecutor struct {
	Materialize func(context.Context) error
	RunCommand  func(context.Context, ResolvedEnvironmentCommand) error
	Readiness   func(context.Context, EndpointExecutionPlan) error
	Start       func(context.Context) error
	Stop        func(context.Context) error
	Success     func(context.Context, []string) error
}

func ExecuteLifecycle(ctx context.Context, plan LifecyclePlan, executor LifecycleExecutor) error {
	for index, operation := range plan.Operations {
		var err error
		switch operation.Kind {
		case LifecycleMaterialize:
			err = requireLifecycleCallback("materialize", executor.Materialize, ctx)
		case LifecycleCommand:
			if executor.RunCommand == nil {
				err = fmt.Errorf("command executor is unavailable")
			} else {
				err = executor.RunCommand(ctx, *operation.Command)
			}
		case LifecycleReadiness:
			if executor.Readiness == nil {
				err = fmt.Errorf("readiness executor is unavailable")
			} else {
				err = executor.Readiness(ctx, *operation.Endpoint)
			}
		case LifecycleStart:
			err = requireLifecycleCallback("start", executor.Start, ctx)
		case LifecycleStop:
			err = requireLifecycleCallback("stop", executor.Stop, ctx)
		case LifecycleSuccess:
			if executor.Success == nil {
				err = fmt.Errorf("success output executor is unavailable")
			} else {
				err = executor.Success(ctx, operation.Lines)
			}
		default:
			err = fmt.Errorf("unknown lifecycle operation %q", operation.Kind)
		}
		if err != nil {
			return fmt.Errorf("lifecycle %s operation %d (%s): %w", operation.Event, index, operation.Kind, err)
		}
	}
	return nil
}

func requireLifecycleCallback(name string, callback func(context.Context) error, ctx context.Context) error {
	if callback == nil {
		return fmt.Errorf("%s executor is unavailable", name)
	}
	return callback(ctx)
}

func splitLifecyclePlan(plan LifecyclePlan, pivot LifecycleOperationKind) (LifecyclePlan, LifecyclePlan, error) {
	for index, operation := range plan.Operations {
		if operation.Kind == pivot {
			return LifecyclePlan{Operations: append([]LifecycleOperation(nil), plan.Operations[:index]...)}, LifecyclePlan{Operations: append([]LifecycleOperation(nil), plan.Operations[index+1:]...)}, nil
		}
	}
	return LifecyclePlan{}, LifecyclePlan{}, fmt.Errorf("lifecycle plan has no %s operation", pivot)
}
