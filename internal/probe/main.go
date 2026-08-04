package probe

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/omry/reploy/internal/canonical"
)

func Main(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) >= 1 && (args[0] == "sandbox-exec" || args[0] == "restricted-exec") {
		installRules := args[0] == "sandbox-exec"
		plan, err := parseSandboxExecPlanV1(args[1:], installRules)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "reploy-probe: %s: %v\n", args[0], err)
			return 2
		}
		if err := sandboxAndExecApplicationV1(plan); err != nil {
			_, _ = fmt.Fprintf(stderr, "reploy-probe: %s: %v\n", args[0], err)
			return 1
		}
		return 0
	}
	return mainWithActions(
		args, stdin, stdout, stderr,
		waitForHoldSignal, copyFixedVolumeTree,
		readApplicationKernelStatus, execApplication,
	)
}

func mainWithHold(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, hold func() error) int {
	return mainWithActions(
		args, stdin, stdout, stderr,
		hold, copyFixedVolumeTree,
		readApplicationKernelStatus, execApplication,
	)
}

func mainWithActions(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	hold func() error,
	copyVolumeTree func() error,
	readKernelStatus func() ([]byte, error),
	execApplication func([]string) error,
) int {
	if len(args) == 5 && args[0] == "install-local-account" {
		if err := installApplicationLocalAccount(args[1], args[2], args[3], args[4]); err != nil {
			_, _ = fmt.Fprintf(stderr, "reploy-probe: install local account: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) >= 1 && args[0] == "verify-exec" {
		if len(args) < 3 || args[1] != "--" {
			_, _ = fmt.Fprintln(stderr, "reploy-probe: verify-exec requires -- followed by an absolute application command")
			return 2
		}
		if err := verifyAndExecApplication(args[2:], readKernelStatus, execApplication); err != nil {
			_, _ = fmt.Fprintf(stderr, "reploy-probe: application startup verification failed: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "hold" {
		if err := hold(); err != nil {
			_, _ = fmt.Fprintf(stderr, "reploy-probe: hold validation container: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "copy-volume-tree" {
		if err := copyVolumeTree(); err != nil {
			_, _ = fmt.Fprintf(stderr, "reploy-probe: copy volume tree: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "reploy-probe accepts no arguments for one canonical stdin request, fixed hold mode, fixed copy-volume-tree mode, fixed install-local-account mode, fixed verify-exec mode, or a sandbox-exec/restricted-exec contract")
		return 2
	}
	content, err := io.ReadAll(stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reploy-probe: read request: %v\n", err)
		return 1
	}
	request, err := DecodeRequestV1(content)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reploy-probe: %v\n", err)
		return 1
	}
	response, err := Inspect(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reploy-probe: %v\n", err)
		return 1
	}
	encoded, err := canonical.Marshal(response)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reploy-probe: encode response: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(encoded); err != nil {
		_, _ = fmt.Fprintf(stderr, "reploy-probe: write response: %v\n", err)
		return 1
	}
	return 0
}

func waitForHoldSignal() error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	return nil
}
