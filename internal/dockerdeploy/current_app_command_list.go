package dockerdeploy

import (
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

func currentAppCommandListV1(document blueprint.Document, deployedOnly bool) AppCommandListResult {
	commands := make([]AppCommandListEntry, 0, len(document.Environment.Commands))
	for name, command := range document.Environment.Commands {
		if !command.NativeCommand || deployedOnly && !command.DeployedCommand || len(command.Trigger) == 0 {
			continue
		}
		forwardArgs := false
		for _, segment := range command.Order {
			if segment == blueprint.ArgumentForwarded {
				forwardArgs = true
				break
			}
		}
		commands = append(commands, AppCommandListEntry{
			Trigger: append([]string(nil), command.Trigger...), Name: name, ForwardArgs: forwardArgs,
			ForwardFlags: append([]string(nil), command.ForwardFlags...),
		})
	}
	sort.Slice(commands, func(left int, right int) bool {
		leftTrigger := strings.Join(commands[left].Trigger, " ")
		rightTrigger := strings.Join(commands[right].Trigger, " ")
		if leftTrigger != rightTrigger {
			return leftTrigger < rightTrigger
		}
		return commands[left].Name < commands[right].Name
	})
	return AppCommandListResult{AppID: document.Environment.ID, Commands: commands}
}
