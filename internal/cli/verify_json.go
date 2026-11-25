package cli

import (
	"fmt"
	"strings"
)

// Keep --format local to verify; all existing directory parsing remains shared.
func parseDockerVerifyOptions(args []string) (dockerCommandOptions, bool, error) {
	var rest []string
	formatSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--dir" {
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, false, fmt.Errorf("--dir requires a value")
			}
			rest = append(rest, arg, value)
			continue
		}
		if arg != "--format" && !strings.HasPrefix(arg, "--format=") {
			rest = append(rest, arg)
			continue
		}
		if formatSet {
			return dockerCommandOptions{}, false, fmt.Errorf("--format may only be provided once")
		}
		formatSet = true
		value := strings.TrimPrefix(arg, "--format=")
		if arg == "--format" {
			var ok bool
			value, ok = optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, false, fmt.Errorf("--format requires a value")
			}
		}
		if value != "json" {
			return dockerCommandOptions{}, false, fmt.Errorf("unsupported --format: %s", value)
		}
	}
	options, err := parseDockerCommandOptions(rest, false)
	return options, formatSet, err
}
