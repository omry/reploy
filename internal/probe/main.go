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
	return mainWithActions(args, stdin, stdout, stderr, waitForHoldSignal, copyFixedVolumeTree, runFixedTransient)
}

func mainWithHold(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, hold func() error) int {
	return mainWithActions(args, stdin, stdout, stderr, hold, copyFixedVolumeTree, runFixedTransient)
}

func mainWithActions(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	hold func() error,
	copyVolumeTree func() error,
	runTransient func([]string) error,
) int {
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
	if len(args) >= 1 && args[0] == "run-transient" {
		if err := runTransient(args[1:]); err != nil {
			_, _ = fmt.Fprintf(stderr, "reploy-probe: run transient command: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "reploy-probe accepts no arguments for one canonical stdin request, fixed hold mode, fixed copy-volume-tree mode, or fixed run-transient mode")
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
