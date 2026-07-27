package probe

import (
	"fmt"
	"path"
	"strconv"
)

const TransientHome = "/mnt/reploy-home"

func runFixedTransient(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("expected UID, GID, and an absolute command")
	}
	uid, err := parseTransientIdentity("UID", args[0])
	if err != nil {
		return err
	}
	gid, err := parseTransientIdentity("GID", args[1])
	if err != nil {
		return err
	}
	command := args[2:]
	if !path.IsAbs(command[0]) {
		return fmt.Errorf("command must be absolute")
	}
	return runTransientProcess(TransientHome, uid, gid, command)
}

func parseTransientIdentity(label string, value string) (int, error) {
	// Reploy ships a 32-bit arm/v7 helper, so the signed-int range is the
	// portable ownership contract across every embedded helper variant.
	parsed, err := strconv.ParseUint(value, 10, 31)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, fmt.Errorf("%s must be a canonical non-negative integer", label)
	}
	return int(parsed), nil
}
