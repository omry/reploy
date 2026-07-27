package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
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

func resolveInstallSuccessLines(document blueprint.Document, plan DockerExecutionPlan) ([]string, error) {
	return resolveEnvironmentOperationStrings(document, plan, document.Environment.Install.Success.Lines)
}

func resolveEnvironmentOperationStrings(document blueprint.Document, plan DockerExecutionPlan, values []string) ([]string, error) {
	mounts := map[string]any{}
	for name, item := range document.Environment.Mounts {
		mounts[name] = map[string]any{
			"target":        item.Target,
			"writable":      item.Writable,
			"update_policy": string(item.UpdatePolicy),
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
			"mounts":   mounts,
			"workload": map[string]any{"endpoints": environmentEndpoints},
		},
		"docker":          map[string]any{"image": document.Docker.Image},
		"reploy.workload": map[string]any{"endpoints": reployEndpoints},
	}
	return blueprint.ResolveOperationStrings(values, document.Environment.Vars, plan.Phase, plan.Scope, roots)
}

func appendLifecycleStepsWithResolver(
	plan *LifecyclePlan,
	event string,
	steps []blueprint.Step,
	document blueprint.Document,
	dockerPlan DockerExecutionPlan,
	resolve func(string, []string) (ResolvedEnvironmentCommand, error),
) error {
	if resolve == nil {
		return fmt.Errorf("%s lifecycle command resolver is unavailable", event)
	}
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
			command, err := resolve(name, forwarded)
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
