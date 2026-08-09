//go:build linux

package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func proveControlledSessionWatchdogParentLossV1(
	t *testing.T,
	ctx context.Context,
	image string,
	plan ControlledSessionExecutionPlanV1,
) {
	t.Helper()
	create := func(containerPlan ControlledSessionContainerPlanV1) string {
		args := []string{"container", "create", "--name", containerPlan.Container}
		for _, label := range containerPlan.Labels {
			args = append(args, "--label", label.Name+"="+label.Value)
		}
		args = append(args, image, "/bin/sh")
		containerID := strings.TrimSpace(runDockerIntegration(t, ctx, args...))
		t.Cleanup(func() {
			_ = exec.Command("docker", "container", "rm", "--force", containerID).Run()
		})
		return containerID
	}
	controllerID := create(plan.Controller)
	workloadID := create(plan.Workload)
	if err := os.MkdirAll(plan.Channel.HostDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFromPlanV1(plan, controllerID, workloadID)
	bootSession, err := deploy.CurrentBootSessionIDV1()
	if err != nil {
		t.Fatal(err)
	}
	ownership.BootSession = bootSession
	manifest, err := deploy.ControlledSessionCleanupManifestFromOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := startControlledSessionWatchdogV1(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	watchdog, ok := runtime.(*controlledSessionWatchdogProcessV1)
	if !ok {
		t.Fatalf("watchdog runtime = %T", runtime)
	}
	if err := watchdog.liveness.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-watchdog.done:
		if err != nil {
			t.Fatalf("watchdog parent-loss cleanup: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("wait for watchdog parent-loss cleanup: %v", ctx.Err())
	}
	for _, containerID := range []string{controllerID, workloadID} {
		output, inspectErr := exec.CommandContext(ctx, "docker", "container", "inspect", containerID).CombinedOutput()
		if inspectErr == nil || !strings.Contains(string(output), "No such container") {
			t.Fatalf("watchdog left controlled-session container %q: %v\n%s", containerID, inspectErr, output)
		}
	}
	if _, statErr := os.Stat(plan.Channel.HostDirectory); !os.IsNotExist(statErr) {
		t.Fatalf("watchdog left private channel directory: %v", statErr)
	}
}
