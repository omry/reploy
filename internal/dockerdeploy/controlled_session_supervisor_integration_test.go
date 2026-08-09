package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
)

func TestControlledSessionSupervisorDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("controlled-session supervisor Docker integration requires a supported Linux host, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	watchdogExecutable := buildControlledSessionWatchdogExecutableV1(t, ctx)
	previousWatchdogExecutable := controlledSessionWatchdogExecutableV1
	controlledSessionWatchdogExecutableV1 = func() (string, error) { return watchdogExecutable, nil }
	t.Cleanup(func() { controlledSessionWatchdogExecutableV1 = previousWatchdogExecutable })
	image := buildControlledSessionControllerIntegrationImageV1(t, ctx)
	plan := controlledSessionControllerIntegrationPlanV1(t, image, []string{"/session-channel-helper", "supervise"})
	operation, err := deploy.AcquireOperationLock(ctx, plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	status, err := operation.AdmitLiveRunV1(deploy.LiveRunV1{
		ID: plan.LiveRunID, Kind: deploy.LiveRunKindShellV1, Name: plan.Workload.DeploymentID,
		GenerationReference: plan.Workload.GenerationReference, Exclusive: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("controlled-session live run status = %q", status)
	}

	result, err := RunControlledSessionV1(ctx, operation, plan, ControlledSessionRunOptionsV1{
		StartupTimeout: 30 * time.Second, TerminationGrace: 5 * time.Second,
		ControllerFinalizationTimeout: 15 * time.Second, ResultAcknowledgementTimeout: 5 * time.Second,
		CleanupTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionResult.Cause != controlledsession.CauseWorkloadExitV1 ||
		result.SessionResult.WorkloadStatus.Code == nil || *result.SessionResult.WorkloadStatus.Code != 42 ||
		result.SessionResult.WorkloadOutputFinalizationStatus.Kind != controlledsession.WorkloadOutputFinalizationDrainedV1 ||
		result.SessionResult.ControllerFinalizationStatus.Kind != controlledsession.ControllerFinalizationCompletedV1 ||
		result.SessionResult.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 ||
		!result.ResultDelivered || !result.ResultAcknowledged {
		t.Fatalf("supervisor result = %#v", result)
	}
	if result.ControllerStatus.Code == nil || *result.ControllerStatus.Code != 0 ||
		result.DeliveryTailCleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
		t.Fatalf("delivery-tail result = %#v", result)
	}
	for _, container := range []string{plan.Controller.Container, plan.Workload.Container} {
		output, inspectErr := exec.CommandContext(ctx, "docker", "container", "inspect", container).CombinedOutput()
		if inspectErr == nil || !strings.Contains(string(output), "No such container") {
			t.Fatalf("controlled-session container %q survived cleanup: %v\n%s", container, inspectErr, output)
		}
	}
	if _, statErr := os.Stat(plan.Channel.HostDirectory); !os.IsNotExist(statErr) {
		t.Fatalf("private channel directory survived cleanup: %v", statErr)
	}
	check, lockErr := deploy.AcquireOperationLock(ctx, plan.Workload.DeploymentDirectory)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	if queue, found, readErr := check.ReadLiveRunQueueV1(); readErr != nil || found {
		t.Fatalf("verified-clean session retained ownership: %#v, found=%t, error=%v", queue, found, readErr)
	}
	if err := check.Unlock(); err != nil {
		t.Fatal(err)
	}
	proveControlledSessionWatchdogParentLossV1(t, ctx, image, plan)
}

func buildControlledSessionWatchdogExecutableV1(t *testing.T, ctx context.Context) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "reploy")
	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", executable, "./cmd/reploy")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Reploy watchdog executable: %v\n%s", err, output)
	}
	return executable
}

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
