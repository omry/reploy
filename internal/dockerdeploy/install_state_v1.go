package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/deploy"
)

func installationStateForPlanV1(plan installPlan) (deploy.InstallationStateV1, error) {
	ports := make([]deploy.InstallationPortBindingV1, len(plan.Ports))
	for index, port := range plan.Ports {
		ports[index] = deploy.InstallationPortBindingV1{
			Name: port.Name, HostBind: port.HostBind, HostPort: port.HostPort, ContainerPort: port.ContainerPort,
		}
	}
	sort.Slice(ports, func(left int, right int) bool { return ports[left].Name < ports[right].Name })
	state := deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
		TargetDir: plan.TargetDir, Scope: string(plan.Scope),
		Service: plan.Service, UnitPath: plan.UnitPath, InstanceID: plan.InstanceID,
		ComposeProject: plan.ComposeProject, ContainerName: plan.ContainerName, NetworkName: plan.NetworkName,
		Ports: ports,
	}
	if err := deploy.ValidateInstallationStateV1(state); err != nil {
		return deploy.InstallationStateV1{}, fmt.Errorf("install plan state: %w", err)
	}
	return state, nil
}
