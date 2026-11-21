//go:build linux && (amd64 || arm64)

package probe

import (
	"context"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/mappedlaunch"
)

// Test-only entry for the unchanged native transition. Full production startup
// must refuse missing engine filtering and NNP before release; this separate
// entry preserves the native tail's normalization and refusal controls too.
// It adds no command or bypass to the shipped probe.
func TestMappedNativeTransitionHelperV1(t *testing.T) {
	args := flag.Args()
	if len(args) < 6 || !strings.HasPrefix(args[0], "native-") {
		t.Skip("disposable native transition helper only")
	}
	if args[1] != mappedlaunch.Command || args[4] != "sandbox-exec" {
		t.Fatal("invalid native transition fixture invocation")
	}
	binding, err := mappedlaunch.DecodeBinding([]byte(args[2]))
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, args[3])
	if err != nil {
		t.Fatal(err)
	}
	plan, err := parseMappedSandboxExecPlanV1(args[5:])
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMappedStartupV1(plan); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	if err := waitMappedReleaseV1(ctx, os.Stdin, binding); err != nil {
		t.Fatal(err)
	}
	if err := execMappedApplicationV1(plan, os.Environ()); err != nil {
		t.Fatal(err)
	}
}
