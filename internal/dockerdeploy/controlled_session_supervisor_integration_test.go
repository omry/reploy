package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
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
	image := buildControlledSessionControllerIntegrationImageV1(t, ctx)
	plan := controlledSessionControllerIntegrationPlanV1(t, image, []string{"/session-channel-helper", "supervise"})

	result, err := RunControlledSessionV1(ctx, plan, ControlledSessionRunOptionsV1{
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
}
