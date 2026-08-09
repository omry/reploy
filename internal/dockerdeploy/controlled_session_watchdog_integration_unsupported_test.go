//go:build !linux

package dockerdeploy

import (
	"context"
	"testing"
)

func proveControlledSessionWatchdogParentLossV1(
	t *testing.T,
	_ context.Context,
	_ string,
	_ ControlledSessionExecutionPlanV1,
) {
	t.Helper()
	t.Fatal("controlled-session watchdog parent-loss integration requires Linux")
}
