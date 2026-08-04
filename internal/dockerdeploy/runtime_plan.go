package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

const runtimeOutputRoot = "/mnt/reploy-output"

const (
	runtimeShellPlanID    = "shell"
	runtimeWorkloadPlanID = "workload"
)

// RuntimePlansV1 derives every container shape that the resolved environment
// may create. Host source paths are deliberately excluded from the canonical
// plans; only their resolved kind and container-side policy participate.
func RuntimePlansV1(document blueprint.Document, dockerPlan DockerExecutionPlan) ([]deploy.RuntimePlanV1, error) {
	if (document.Environment.Workload == nil) != (dockerPlan.Workload == nil) {
		return nil, fmt.Errorf("runtime workload does not match the resolved Docker plan")
	}
	if err := ValidateApplicationSandboxPlanV1(dockerPlan.Sandbox); err != nil {
		return nil, fmt.Errorf("runtime application sandbox: %w", err)
	}
	resolvedNetwork := normalizeRuntimeNetworkV1(document.Environment.Runtime.Network)
	wantNetwork := ApplicationNetworkPolicyV1{
		Public: resolvedNetwork.Public,
		Local:  resolvedNetwork.Local,
	}
	if dockerPlan.Sandbox.Network != wantNetwork {
		return nil, fmt.Errorf("runtime application network policy does not match the resolved blueprint")
	}
	workloadInboundTCP, err := runtimeWorkloadInboundTCPV1(document, dockerPlan)
	if err != nil {
		return nil, err
	}
	baseMounts, err := runtimeMountsV1(dockerPlan)
	if err != nil {
		return nil, err
	}
	withHome := appendRuntimeMountV1(baseMounts, deploy.RuntimeMountV1{
		Destination: temporaryHomeForPlan(dockerPlan), SourceKind: deploy.RuntimeMountSourceGenerated,
	})
	plans := []deploy.RuntimePlanV1{{
		ID: runtimeShellPlanID, InboundTCP: []string{}, Mounts: cloneRuntimeMountsV1(withHome), Executables: []providers.QualifiedOutput{},
	}}

	commandNames, err := runtimeTransientCommandNamesV1(document)
	if err != nil {
		return nil, err
	}
	for _, name := range commandNames {
		command := document.Environment.Commands[name]
		output, err := runtimeCommandOutputV1(document, name)
		if err != nil {
			return nil, err
		}
		executables := []providers.QualifiedOutput{output}
		plans = append(plans, deploy.RuntimePlanV1{
			ID: runtimeCommandPlanID(name, false), InboundTCP: []string{}, Mounts: cloneRuntimeMountsV1(withHome), Executables: executables,
		})
		if command.NativeCommand || command.DeployedCommand {
			plans = append(plans, deploy.RuntimePlanV1{
				ID: runtimeCommandPlanID(name, true), InboundTCP: []string{},
				Mounts: appendRuntimeMountV1(withHome, deploy.RuntimeMountV1{
					Destination: runtimeOutputRoot, SourceKind: deploy.RuntimeMountSourceDirectory,
				}),
				Executables: executables,
			})
		}
	}

	if document.Environment.Workload != nil {
		output, err := runtimeCommandOutputV1(document, document.Environment.Workload.Command)
		if err != nil {
			return nil, fmt.Errorf("runtime workload: %w", err)
		}
		plans = append(plans, deploy.RuntimePlanV1{
			ID: runtimeWorkloadPlanID, InboundTCP: workloadInboundTCP, Mounts: cloneRuntimeMountsV1(withHome),
			Executables: []providers.QualifiedOutput{output},
		})
	}

	sort.Slice(plans, func(left int, right int) bool { return plans[left].ID < plans[right].ID })
	return plans, nil
}

func runtimeWorkloadInboundTCPV1(document blueprint.Document, dockerPlan DockerExecutionPlan) ([]string, error) {
	if document.Environment.Workload == nil {
		return []string{}, nil
	}
	if len(document.Environment.Workload.Endpoints) != len(dockerPlan.Workload.Endpoints) {
		return nil, fmt.Errorf("runtime workload endpoints do not match the resolved Docker plan")
	}
	ports := make([]int, 0, len(document.Environment.Workload.Endpoints))
	for name, endpoint := range document.Environment.Workload.Endpoints {
		planned, found := dockerPlan.Workload.Endpoints[name]
		if !found || planned.ContainerPort != endpoint.Port {
			return nil, fmt.Errorf("runtime workload endpoint %q does not match the resolved Docker plan", name)
		}
		ports = append(ports, endpoint.Port)
	}
	return deploy.CanonicalRuntimeInboundTCPV1(ports), nil
}

func runtimeCommandPlanID(commandName string, output bool) string {
	id := "command/" + commandName
	if output {
		id += "/output"
	}
	return id
}

func runtimeTransientCommandNamesV1(document blueprint.Document) ([]string, error) {
	names := map[string]bool{}
	for name, command := range document.Environment.Commands {
		if command.NativeCommand || command.DeployedCommand {
			names[name] = true
		}
	}
	stepGroups := [][]blueprint.Step{document.Environment.Install.AfterInstall}
	if workload := document.Environment.Workload; workload != nil {
		stepGroups = append(stepGroups,
			workload.Runtime.BeforeStart,
			workload.Runtime.AfterStart,
			workload.Runtime.BeforeStop,
			workload.Runtime.AfterStop,
		)
	}
	for _, steps := range stepGroups {
		for _, step := range steps {
			for _, action := range step.Actions {
				name, _, err := MatchLifecycleCommand(document, action.Environment)
				if err != nil {
					return nil, fmt.Errorf("runtime lifecycle action: %w", err)
				}
				names[name] = true
			}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func runtimeCommandOutputV1(document blueprint.Document, commandName string) (providers.QualifiedOutput, error) {
	command, found := document.Environment.Commands[commandName]
	if !found {
		return providers.QualifiedOutput{}, fmt.Errorf("runtime command %q is not defined", commandName)
	}
	component, executable, found := document.Environment.ResolveExecutableProfile(command.Executable)
	if !found {
		return providers.QualifiedOutput{}, fmt.Errorf("runtime command %q references unknown executable %q", commandName, command.Executable)
	}
	return providers.QualifiedOutput{Component: component, Name: executable.Binary}, nil
}

func runtimeMountsV1(plan DockerExecutionPlan) ([]deploy.RuntimeMountV1, error) {
	mounts := make([]deploy.RuntimeMountV1, 0, len(plan.Mounts))
	for _, mount := range plan.Mounts {
		sourceKind, err := runtimeMountSourceKindV1(mount)
		if err != nil {
			return nil, fmt.Errorf("runtime mount %q: %w", mount.Name, err)
		}
		mounts = append(mounts, deploy.RuntimeMountV1{
			Destination: mount.Target, SourceKind: sourceKind, ReadOnly: mount.ReadOnly,
		})
	}
	sort.Slice(mounts, func(left int, right int) bool { return mounts[left].Destination < mounts[right].Destination })
	return mounts, nil
}

func runtimeMountSourceKindV1(mount MountExecutionPlan) (string, error) {
	switch mount.Mode {
	case blueprint.MountManagedBind:
		if mount.SourceKind != "" && mount.SourceKind != deploy.RuntimeMountSourceDirectory {
			return "", fmt.Errorf("managed bind source kind must be directory")
		}
		return deploy.RuntimeMountSourceDirectory, nil
	case blueprint.MountVolume, blueprint.MountTmpfs:
		if mount.SourceKind != "" && mount.SourceKind != deploy.RuntimeMountSourceGenerated {
			return "", fmt.Errorf("%s source kind must be generated", mount.Mode)
		}
		return deploy.RuntimeMountSourceGenerated, nil
	case blueprint.MountBind:
		switch mount.SourceKind {
		case deploy.RuntimeMountSourceDirectory, deploy.RuntimeMountSourceFile:
			return mount.SourceKind, nil
		default:
			return "", fmt.Errorf("bind source kind must be file or directory")
		}
	default:
		return "", fmt.Errorf("unsupported mount mode %q", mount.Mode)
	}
}

func appendRuntimeMountV1(mounts []deploy.RuntimeMountV1, mount deploy.RuntimeMountV1) []deploy.RuntimeMountV1 {
	result := append(cloneRuntimeMountsV1(mounts), mount)
	sort.Slice(result, func(left int, right int) bool { return result[left].Destination < result[right].Destination })
	return result
}

func cloneRuntimeMountsV1(mounts []deploy.RuntimeMountV1) []deploy.RuntimeMountV1 {
	return append([]deploy.RuntimeMountV1{}, mounts...)
}
