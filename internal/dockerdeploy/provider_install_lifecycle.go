package dockerdeploy

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func planLockedAfterInstallLifecycleV1(
	document blueprint.Document,
	dockerPlan DockerExecutionPlan,
	catalog []providers.RealizedOutput,
) (LifecyclePlan, error) {
	plan := LifecyclePlan{}
	if err := appendLockedLifecycleStepsV1(&plan, "after_install", document.Environment.Install.AfterInstall, document, dockerPlan, catalog); err != nil {
		return LifecyclePlan{}, err
	}
	return plan, nil
}

func planLockedStartLifecycleV1(
	document blueprint.Document,
	dockerPlan DockerExecutionPlan,
	catalog []providers.RealizedOutput,
) (LifecyclePlan, error) {
	if document.Environment.Workload == nil || dockerPlan.Workload == nil {
		return LifecyclePlan{}, fmt.Errorf("environment has no workload to start")
	}
	plan := LifecyclePlan{}
	if err := appendLockedLifecycleStepsV1(&plan, "before_start", document.Environment.Workload.Runtime.BeforeStart, document, dockerPlan, catalog); err != nil {
		return LifecyclePlan{}, err
	}
	plan.Operations = append(plan.Operations, LifecycleOperation{Kind: LifecycleStart, Event: "start"})
	if err := appendLockedLifecycleStepsV1(&plan, "after_start", document.Environment.Workload.Runtime.AfterStart, document, dockerPlan, catalog); err != nil {
		return LifecyclePlan{}, err
	}
	return plan, nil
}

func planLockedStopLifecycleV1(
	document blueprint.Document,
	dockerPlan DockerExecutionPlan,
	catalog []providers.RealizedOutput,
) (LifecyclePlan, error) {
	if document.Environment.Workload == nil || dockerPlan.Workload == nil {
		return LifecyclePlan{}, fmt.Errorf("environment has no workload to stop")
	}
	plan := LifecyclePlan{}
	if err := appendLockedLifecycleStepsV1(&plan, "before_stop", document.Environment.Workload.Runtime.BeforeStop, document, dockerPlan, catalog); err != nil {
		return LifecyclePlan{}, err
	}
	plan.Operations = append(plan.Operations, LifecycleOperation{Kind: LifecycleStop, Event: "stop"})
	if err := appendLockedLifecycleStepsV1(&plan, "after_stop", document.Environment.Workload.Runtime.AfterStop, document, dockerPlan, catalog); err != nil {
		return LifecyclePlan{}, err
	}
	return plan, nil
}

func planLockedRestartLifecycleV1(
	document blueprint.Document,
	dockerPlan DockerExecutionPlan,
	catalog []providers.RealizedOutput,
) (LifecyclePlan, error) {
	stop, err := planLockedStopLifecycleV1(document, dockerPlan, catalog)
	if err != nil {
		return LifecyclePlan{}, err
	}
	start, err := planLockedStartLifecycleV1(document, dockerPlan, catalog)
	if err != nil {
		return LifecyclePlan{}, err
	}
	return LifecyclePlan{Operations: append(stop.Operations, start.Operations...)}, nil
}

func appendLockedLifecycleStepsV1(
	plan *LifecyclePlan,
	event string,
	steps []blueprint.Step,
	document blueprint.Document,
	dockerPlan DockerExecutionPlan,
	catalog []providers.RealizedOutput,
) error {
	return appendLifecycleStepsWithResolver(plan, event, steps, document, dockerPlan, func(name string, forwarded []string) (ResolvedEnvironmentCommand, error) {
		return resolveLockedEnvironmentCommandForPlanV1(document, catalog, dockerPlan, name, forwarded)
	})
}
