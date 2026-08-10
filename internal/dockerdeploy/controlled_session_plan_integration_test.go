package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

func TestControlledSessionContainerPlansDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	image, _ := buildApplicationStartupVerifierIntegrationImage(t, ctx)
	controllerRoot := t.TempDir()
	workloadRoot := t.TempDir()
	liveRunID := "run-0000000000000001"
	channel := ControlledSessionChannelPlanV1{
		HostDirectory:      filepath.Join(workloadRoot, privateRuntimeMetadataDirectoryName, "sessions", liveRunID),
		ContainerDirectory: controlledSessionChannelRootV1,
		ContainerSocket:    controlledSessionChannelRootV1 + "/" + controlledSessionChannelSocketNameV1,
	}
	if err := os.MkdirAll(channel.HostDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	buildIdentity := canonical.Digest("sha256:" + strings.Repeat("1", 64))
	current := CurrentBuild{Generation: deploy.EnvironmentGenerationState{
		Reference: image, BuildLockDigest: buildIdentity,
	}}
	basePlan := DockerExecutionPlan{
		EnvironmentID: "controller", DeploymentDir: controllerRoot, Phase: blueprint.PhaseStaged,
		Image: image, ContainerName: uniqueDockerIntegrationName("reploy-session-plan"),
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{
			LocalUser: "reploy", UID: 12345, GID: 23456, DockerUser: "12345:23456",
		}),
	}
	protectedRoots := []string{controllerRoot, workloadRoot}

	controller, err := controlledSessionContainerPlanV1(
		ControlledSessionRoleControllerV1, liveRunID, current, basePlan,
		[]string{"/bin/sh", "-c", "printf 'controlled-session-controller-pass\\n'"},
		channel, protectedRoots, disabledControlledSessionNetworkPlanV1(), 0, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "docker", controller.Cleanup.Args...).Run() })
	runDockerIntegration(t, ctx, controller.Create.Args...)
	output := runDockerIntegration(t, ctx, "start", "--attach", controller.Container)
	if strings.TrimSpace(output) != "controlled-session-controller-pass" {
		t.Fatalf("controller output = %q", output)
	}

	workloadDockerPlan := basePlan
	workloadDockerPlan.EnvironmentID = "workload"
	workloadDockerPlan.DeploymentDir = workloadRoot
	workloadDockerPlan.ContainerName = uniqueDockerIntegrationName("reploy-session-plan")
	workload, err := controlledSessionContainerPlanV1(
		ControlledSessionRoleWorkloadV1, liveRunID, current, workloadDockerPlan,
		[]string{"/bin/sh"}, ControlledSessionChannelPlanV1{}, protectedRoots, disabledControlledSessionNetworkPlanV1(), 80, 24,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "docker", workload.Cleanup.Args...).Run() })
	runDockerIntegration(t, ctx, workload.Create.Args...)
	runDockerIntegration(t, ctx, workload.Start.Args...)
	if state := strings.TrimSpace(runDockerIntegration(t, ctx, "inspect", "--format", "{{.State.Running}}", workload.Container)); state != "true" {
		t.Fatalf("workload running state = %q", state)
	}
}
