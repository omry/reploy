package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
)

func TestDockerControllerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("controller Docker integration requires a supported Linux host, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	image := buildControlledSessionControllerIntegrationImageV1(t, ctx)

	t.Run("preserves an owned inert create collision", func(t *testing.T) {
		plan := controlledSessionControllerIntegrationPlanV1(t, image, []string{"/session-channel-helper"})
		channel, err := PrepareControlledSessionChannelV1(plan)
		if err != nil {
			t.Fatal(err)
		}
		defer channel.Close()
		runDockerIntegration(t, ctx, plan.Controller.Create.Args...)
		t.Cleanup(func() {
			_ = exec.CommandContext(context.Background(), "docker", plan.Controller.Cleanup.Args...).Run()
		})
		if _, err := PrepareDockerControllerV1(ctx, plan.Controller); err == nil || !strings.Contains(err.Error(), "already in use") {
			t.Fatalf("owned create collision error = %v", err)
		}
		if err := exec.CommandContext(ctx, "docker", "container", "inspect", plan.Controller.Container).Run(); err != nil {
			t.Fatalf("owned inert controller was removed without an exact create result: %v", err)
		}
	})

	t.Run("preserves a foreign create collision", func(t *testing.T) {
		plan := controlledSessionControllerIntegrationPlanV1(t, image, []string{"/session-channel-helper"})
		channel, err := PrepareControlledSessionChannelV1(plan)
		if err != nil {
			t.Fatal(err)
		}
		defer channel.Close()
		runDockerIntegration(t, ctx, "create", "--name", plan.Controller.Container, image, "/bin/true")
		t.Cleanup(func() {
			_ = exec.CommandContext(context.Background(), "docker", plan.Controller.Cleanup.Args...).Run()
		})
		if _, err := PrepareDockerControllerV1(ctx, plan.Controller); err == nil || !strings.Contains(err.Error(), "already in use") {
			t.Fatalf("foreign create collision error = %v", err)
		}
		if err := exec.CommandContext(ctx, "docker", "container", "inspect", plan.Controller.Container).Run(); err != nil {
			t.Fatalf("foreign container was removed: %v", err)
		}
	})

	t.Run("preserves a running owned create collision", func(t *testing.T) {
		plan := controlledSessionControllerIntegrationPlanV1(t, image, []string{"/session-channel-helper", "wait-signal"})
		channel, err := PrepareControlledSessionChannelV1(plan)
		if err != nil {
			t.Fatal(err)
		}
		defer channel.Close()
		runDockerIntegration(t, ctx, plan.Controller.Create.Args...)
		runDockerIntegration(t, ctx, plan.Controller.Start.Args...)
		t.Cleanup(func() {
			_ = exec.CommandContext(context.Background(), "docker", plan.Controller.Cleanup.Args...).Run()
		})
		if _, err := PrepareDockerControllerV1(ctx, plan.Controller); err == nil || !strings.Contains(err.Error(), "already in use") {
			t.Fatalf("running owned collision error = %v", err)
		}
		state := strings.TrimSpace(runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.State.Status}}", plan.Controller.Container))
		if state != "running" {
			t.Fatalf("running owned controller state = %q", state)
		}
	})

	t.Run("pins lifecycle to the created container ID", func(t *testing.T) {
		plan := controlledSessionControllerIntegrationPlanV1(t, image, []string{"/session-channel-helper", "wait-signal"})
		channel, controller := prepareControlledSessionControllerIntegrationV1(t, ctx, plan)
		originalID := strings.TrimSpace(runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.Id}}", plan.Controller.Container))
		displacedName := uniqueDockerIntegrationName("reploy-session-displaced-controller")
		runDockerIntegration(t, ctx, "container", "rename", originalID, displacedName)
		runDockerIntegration(t, ctx, plan.Controller.Create.Args...)
		t.Cleanup(func() {
			_ = exec.CommandContext(context.Background(), "docker", plan.Controller.Cleanup.Args...).Run()
		})
		if err := controller.Start(ctx); err != nil {
			t.Fatal(err)
		}
		connection, err := channel.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		request, err := connection.ReadRequest(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if request.Kind != controlledsession.RequestCompleteV1 {
			t.Fatalf("controller request = %#v", request)
		}
		if state := strings.TrimSpace(runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.State.Status}}", originalID)); state != "running" {
			t.Fatalf("original controller state = %q", state)
		}
		if state := strings.TrimSpace(runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.State.Status}}", plan.Controller.Container)); state != "created" {
			t.Fatalf("replacement controller state = %q", state)
		}
		if err := controller.Cleanup(ctx); err != nil {
			t.Fatal(err)
		}
		if err := exec.CommandContext(ctx, "docker", "container", "inspect", originalID).Run(); err == nil {
			t.Fatal("original controller survived ID-pinned cleanup")
		}
		if err := exec.CommandContext(ctx, "docker", "container", "inspect", plan.Controller.Container).Run(); err != nil {
			t.Fatalf("replacement controller was removed: %v", err)
		}
	})

	t.Run("launches through the prepared private channel", func(t *testing.T) {
		plan := controlledSessionControllerIntegrationPlanV1(t, image, []string{"/session-channel-helper"})
		channel, controller := prepareControlledSessionControllerIntegrationV1(t, ctx, plan)
		if err := controller.Start(ctx); err != nil {
			t.Fatal(err)
		}
		connection, err := channel.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		request, err := connection.ReadRequest(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if request.Kind != controlledsession.RequestCompleteV1 {
			t.Fatalf("controller request = %#v", request)
		}
		status, err := controller.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertControlledSessionControllerExitCodeV1(t, status, 0)
		if err := controller.Cleanup(ctx); err != nil {
			t.Fatal(err)
		}
	})

	for _, test := range []struct {
		name     string
		stop     func(context.Context, *DockerControllerV1) error
		wantCode int
	}{
		{name: "graceful stop", stop: func(ctx context.Context, controller *DockerControllerV1) error {
			return controller.RequestGracefulStop(ctx)
		}, wantCode: 23},
		{name: "forced stop", stop: func(ctx context.Context, controller *DockerControllerV1) error {
			return controller.ForceStop(ctx)
		}, wantCode: 137},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := controlledSessionControllerIntegrationPlanV1(t, image, []string{"/session-channel-helper", "wait-signal"})
			channel, controller := prepareControlledSessionControllerIntegrationV1(t, ctx, plan)
			if err := controller.Start(ctx); err != nil {
				t.Fatal(err)
			}
			connection, err := channel.Claim(ctx)
			if err != nil {
				t.Fatal(err)
			}
			request, err := connection.ReadRequest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if request.Kind != controlledsession.RequestCompleteV1 {
				t.Fatalf("controller request = %#v", request)
			}
			if err := test.stop(ctx, controller); err != nil {
				t.Fatal(err)
			}
			status, err := controller.Wait(ctx)
			if err != nil {
				t.Fatal(err)
			}
			assertControlledSessionControllerExitCodeV1(t, status, test.wantCode)
			if err := controller.Cleanup(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func prepareControlledSessionControllerIntegrationV1(
	t *testing.T,
	ctx context.Context,
	plan ControlledSessionExecutionPlanV1,
) (*controlledsession.PrivateChannelV1, *DockerControllerV1) {
	t.Helper()
	channel, err := PrepareControlledSessionChannelV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := PrepareDockerControllerV1(ctx, plan.Controller)
	if err != nil {
		_ = channel.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := controller.Cleanup(cleanupCtx); err != nil {
			t.Errorf("clean controller integration container: %v", err)
		}
		if err := channel.Close(); err != nil {
			t.Errorf("close controller integration channel: %v", err)
		}
	})
	return channel, controller
}

func controlledSessionControllerIntegrationPlanV1(
	t *testing.T,
	image string,
	controllerCommand []string,
) ControlledSessionExecutionPlanV1 {
	return controlledSessionControllerIntegrationPlanWithEndpointsV1(t, image, controllerCommand, nil)
}

func controlledSessionControllerIntegrationPlanWithEndpointsV1(
	t *testing.T,
	image string,
	controllerCommand []string,
	endpoints []ControlledSessionEndpointPlanV1,
) ControlledSessionExecutionPlanV1 {
	t.Helper()
	liveRunID := "run-0000000000000001"
	controllerRoot := shortControlledSessionChannelTestDirectoryV1(t)
	workloadRoot := shortControlledSessionChannelTestDirectoryV1(t)
	channel := ControlledSessionChannelPlanV1{
		HostDirectory:      filepath.Join(workloadRoot, privateRuntimeMetadataDirectoryName, "sessions", liveRunID),
		ContainerDirectory: controlledSessionChannelRootV1,
		ContainerSocket:    path.Join(controlledSessionChannelRootV1, controlledSessionChannelSocketNameV1),
	}
	_, uid, gid, _, err := currentHostRuntimeIdentityV1()
	if err != nil {
		t.Fatal(err)
	}
	runtimeUID, err := runtimeIDFromNativeIntV1(uid)
	if err != nil {
		t.Fatal(err)
	}
	runtimeGID, err := runtimeIDFromNativeIntV1(gid)
	if err != nil {
		t.Fatal(err)
	}
	identity := RuntimeUserPlan{LocalUser: "reploy", UID: runtimeUID, GID: runtimeGID, DockerUser: fmt.Sprintf("%d:%d", runtimeUID, runtimeGID)}
	controllerCurrent := CurrentBuild{Generation: deploy.EnvironmentGenerationState{
		Reference: image, BuildLockDigest: canonical.Digest("sha256:" + strings.Repeat("4", 64)),
	}}
	controllerResourceName := uniqueDockerIntegrationName("reploy-session-controller")
	controllerDockerPlan := DockerExecutionPlan{
		EnvironmentID: "controller", DeploymentDir: controllerRoot, Phase: blueprint.PhaseStaged,
		Image: image, ContainerName: controllerResourceName,
		NetworkName: controllerResourceName,
		Sandbox:     newApplicationSandboxPlanV1(identity),
	}
	workloadResourceName := uniqueDockerIntegrationName("reploy-session-workload")
	workloadDockerPlan := DockerExecutionPlan{
		EnvironmentID: "workload", DeploymentDir: workloadRoot, Phase: blueprint.PhaseStaged,
		Image: image, ContainerName: workloadResourceName,
		NetworkName: workloadResourceName,
		Sandbox:     newApplicationSandboxPlanV1(identity),
	}
	controllerNetwork, workloadNetwork := disabledControlledSessionNetworkPlanV1(), disabledControlledSessionNetworkPlanV1()
	if len(endpoints) != 0 {
		controllerNetwork, workloadNetwork = controlledSessionNetworkPlansV1(workloadDockerPlan.NetworkName, liveRunID, endpoints)
	}
	controllerPlan, err := controlledSessionContainerPlanV1(
		ControlledSessionRoleControllerV1,
		liveRunID,
		controllerCurrent,
		controllerDockerPlan,
		controllerCommand,
		channel,
		[]string{controllerRoot, workloadRoot},
		controllerNetwork,
		nil,
		0,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	workloadCurrent := CurrentBuild{Generation: deploy.EnvironmentGenerationState{
		Reference: image, BuildLockDigest: canonical.Digest("sha256:" + strings.Repeat("5", 64)),
	}}
	workloadPlan, err := controlledSessionContainerPlanV1(
		ControlledSessionRoleWorkloadV1,
		liveRunID,
		workloadCurrent,
		workloadDockerPlan,
		[]string{"/bin/sh"},
		channel,
		[]string{controllerRoot, workloadRoot},
		workloadNetwork,
		nil,
		80,
		24,
	)
	if err != nil {
		t.Fatal(err)
	}
	controllerDigest, err := ControlledSessionContainerPlanDigestV1(controllerPlan)
	if err != nil {
		t.Fatal(err)
	}
	workloadDigest, err := ControlledSessionContainerPlanDigestV1(workloadPlan)
	if err != nil {
		t.Fatal(err)
	}
	plan := ControlledSessionExecutionPlanV1{
		Schema:     ControlledSessionExecutionPlanSchemaV1,
		LiveRunID:  liveRunID,
		Channel:    channel,
		Controller: controllerPlan,
		Workload:   workloadPlan,
		Authorization: controlledsession.AuthorizationV1{
			Schema:     controlledsession.AuthorizationSchemaV1,
			Handle:     "session-" + strings.Repeat("a", 64),
			LiveRunID:  liveRunID,
			Controller: environmentAuthorizationForContainerV1(controllerPlan, controllerDigest),
			Workload:   environmentAuthorizationForContainerV1(workloadPlan, workloadDigest),
			Operations: []controlledsession.OperationV1{
				controlledsession.OperationCompleteV1,
				controlledsession.OperationInputV1,
				controlledsession.OperationResizeV1,
				controlledsession.OperationTerminateV1,
			},
			EndpointIDs: controlledSessionEndpointIDsV1(endpoints),
		},
	}
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertControlledSessionControllerExitCodeV1(
	t *testing.T,
	status controlledsession.ProcessStatusV1,
	want int,
) {
	t.Helper()
	if status.Kind != controlledsession.ProcessStatusExitedV1 || status.Code == nil || *status.Code != want {
		t.Fatalf("controller exit status = %#v, want %d", status, want)
	}
}

func buildControlledSessionControllerIntegrationImageV1(t *testing.T, ctx context.Context) string {
	t.Helper()
	base, _ := buildApplicationStartupVerifierIntegrationImage(t, ctx)
	baseTag := uniqueDockerIntegrationName("reploy-session-controller-base")
	if output, err := exec.CommandContext(ctx, "docker", "image", "tag", base, baseTag).CombinedOutput(); err != nil {
		t.Fatalf("tag controlled-session controller base image: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.CommandContext(context.Background(), "docker", "image", "rm", baseTag).CombinedOutput(); err != nil {
			t.Errorf("remove controlled-session controller base tag: %v\n%s", err, output)
		}
	})
	workspace := t.TempDir()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate controller integration source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	helper := filepath.Join(workspace, "session-channel-helper")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", helper, "./internal/dockerdeploy/testdata/session_channel_helper")
	build.Dir = repositoryRoot
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOCACHE="+filepath.Join(workspace, "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build controlled-session controller helper: %v\n%s", err, output)
	}
	networkHelper := filepath.Join(workspace, "session-network-helper")
	build = exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", networkHelper, "./internal/dockerdeploy/testdata/session_network_helper")
	build.Dir = repositoryRoot
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOCACHE="+filepath.Join(workspace, "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build controlled-session network helper: %v\n%s", err, output)
	}
	dockerfile := filepath.Join(workspace, "Dockerfile")
	contents := "FROM " + baseTag + "\n" +
		"COPY --chmod=0555 session-channel-helper /session-channel-helper\n" +
		"COPY --chmod=0555 session-network-helper /session-network-helper\n"
	if err := os.WriteFile(dockerfile, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	image := uniqueDockerIntegrationName("reploy-session-controller-helper")
	command := exec.CommandContext(ctx, "docker", "build", "--pull=false", "--tag", image, workspace)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build controlled-session controller image: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.CommandContext(context.Background(), "docker", "image", "rm", image).CombinedOutput(); err != nil {
			t.Errorf("remove controlled-session controller image: %v\n%s", err, output)
		}
	})
	return image
}
