//go:build windows

package deploy

import "os"

// The controlled-session watchdog is Linux-only. Windows receipt targets can
// therefore never remain live in an inherited watchdog process.
func lockControlledSessionIncidentTargetV1(*os.File) error { return nil }

func controlledSessionIncidentTargetInUseV1(string) (bool, error) { return false, nil }
