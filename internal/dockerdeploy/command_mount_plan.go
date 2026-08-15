package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
)

// effectiveCommandDockerPlanV1 derives the mount permissions for one command
// without mutating the environment plan used by the workload or shell.
func effectiveCommandDockerPlanV1(
	document blueprint.Document,
	plan DockerExecutionPlan,
	commandName string,
) (DockerExecutionPlan, error) {
	command, found := document.Environment.Commands[commandName]
	if !found {
		return DockerExecutionPlan{}, fmt.Errorf("unknown environment command %q", commandName)
	}
	if len(command.Mounts) == 0 {
		return plan, nil
	}

	byName := make(map[string]int, len(plan.Mounts))
	for index, mount := range plan.Mounts {
		if _, duplicate := byName[mount.Name]; duplicate {
			return DockerExecutionPlan{}, fmt.Errorf("Docker plan repeats mount %q", mount.Name)
		}
		byName[mount.Name] = index
	}
	names := make([]string, 0, len(command.Mounts))
	for name := range command.Mounts {
		names = append(names, name)
	}
	sort.Strings(names)

	effective := plan
	effective.Mounts = append([]MountExecutionPlan(nil), plan.Mounts...)
	for _, name := range names {
		index, found := byName[name]
		if !found {
			return DockerExecutionPlan{}, fmt.Errorf("command %q overrides unknown Docker mount %q", commandName, name)
		}
		effective.Mounts[index].ReadOnly = !command.Mounts[name].Writable
	}
	return effective, nil
}
