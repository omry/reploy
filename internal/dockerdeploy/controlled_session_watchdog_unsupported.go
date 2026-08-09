//go:build !linux

package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/omry/reploy/internal/deploy"
)

var controlledSessionWatchdogExecutableV1 = os.Executable

func startControlledSessionWatchdogV1(context.Context, deploy.ControlledSessionCleanupManifest) (controlledSessionWatchdogRuntimeV1, error) {
	return nil, fmt.Errorf("controlled-session watchdog requires Linux")
}

func runControlledSessionWatchdogChildV1(io.Writer) error {
	return fmt.Errorf("controlled-session watchdog requires Linux")
}
