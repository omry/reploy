//go:build linux

package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
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
	dockerEndpoint, err := verifiedLocalDockerEndpointV1(ctx, controlledSessionCommandSpecV1(plan.Controller.Create), defaultDockerPreflightTimeout)
	if err != nil {
		t.Fatal(err)
	}
	ownership := controlledSessionOwnershipFromPlanV1(plan, dockerEndpoint, controllerID, workloadID)
	bootSession, err := deploy.CurrentBootSessionIDV1()
	if err != nil {
		t.Fatal(err)
	}
	ownership.BootSession = bootSession
	manifest, err := deploy.ControlledSessionCleanupManifestFromOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := deploy.AcquireOperationLock(ctx, plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	receiptTarget, err := operation.PrepareControlledSessionIncidentReceiptV1(plan.Channel.HostDirectory, plan.LiveRunID)
	unlockErr := operation.Unlock()
	if err != nil || unlockErr != nil {
		t.Fatalf("prepare incident receipt: %v; unlock: %v", err, unlockErr)
	}
	runtime, err := startControlledSessionWatchdogV1(ctx, manifest, receiptTarget)
	if err != nil {
		t.Fatal(err)
	}
	watchdog, ok := runtime.(*controlledSessionWatchdogProcessV1)
	if !ok {
		t.Fatalf("watchdog runtime = %T", runtime)
	}
	watchdogGroup, err := syscall.Getpgid(watchdog.pid)
	if err != nil {
		t.Fatal(err)
	}
	if watchdogGroup == syscall.Getpgrp() {
		t.Fatalf("watchdog inherited parent process group %d", watchdogGroup)
	}
	if err := watchdog.liveness.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watchdog.exited:
		err := watchdog.ExitError()
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
	operation, err = deploy.AcquireExistingOperationLock(ctx, plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	receipts, readErr := operation.ReadControlledSessionIncidentReceiptsV1()
	unlockErr = operation.Unlock()
	if readErr != nil || unlockErr != nil || len(receipts) != 1 || receipts[0].LiveRunID != plan.LiveRunID ||
		receipts[0].CleanupStatus != deploy.ControlledSessionIncidentCleanupSucceededV1 {
		t.Fatalf("incident receipts = %#v, read=%v, unlock=%v", receipts, readErr, unlockErr)
	}
}
