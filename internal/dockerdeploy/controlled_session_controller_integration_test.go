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
	t.Helper()
	liveRunID := "run-0000000000000001"
	controllerRoot := shortControlledSessionChannelTestDirectoryV1(t)
	workloadRoot := shortControlledSessionChannelTestDirectoryV1(t)
	channel := ControlledSessionChannelPlanV1{
		HostDirectory:      filepath.Join(workloadRoot, privateRuntimeMetadataDirectoryName, "sessions", liveRunID),
		ContainerDirectory: controlledSessionChannelRootV1,
		ContainerSocket:    path.Join(controlledSessionChannelRootV1, controlledSessionChannelSocketNameV1),
	}
	uid := os.Geteuid()
	gid := os.Getegid()
	identity := RuntimeUserPlan{LocalUser: "reploy", UID: uid, GID: gid, DockerUser: fmt.Sprintf("%d:%d", uid, gid)}
	controllerCurrent := CurrentBuild{Generation: deploy.EnvironmentGenerationState{
		Reference: image, BuildLockDigest: canonical.Digest("sha256:" + strings.Repeat("4", 64)),
	}}
	controllerPlan, err := controlledSessionContainerPlanV1(
		ControlledSessionRoleControllerV1,
		liveRunID,
		controllerCurrent,
		DockerExecutionPlan{
			EnvironmentID: "controller", DeploymentDir: controllerRoot, Phase: blueprint.PhaseStaged,
			Image: image, ContainerName: uniqueDockerIntegrationName("reploy-session-controller"),
			Sandbox: newApplicationSandboxPlanV1(identity),
		},
		controllerCommand,
		channel,
		[]string{controllerRoot, workloadRoot},
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
		DockerExecutionPlan{
			EnvironmentID: "workload", DeploymentDir: workloadRoot, Phase: blueprint.PhaseStaged,
			Image: image, ContainerName: uniqueDockerIntegrationName("reploy-session-workload"),
			Sandbox: newApplicationSandboxPlanV1(identity),
		},
		[]string{"/bin/sh"},
		ControlledSessionChannelPlanV1{},
		[]string{controllerRoot, workloadRoot},
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
			EndpointIDs: []string{},
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
	dockerfile := filepath.Join(workspace, "Dockerfile")
	contents := "FROM " + baseTag + "\nCOPY --chmod=0555 session-channel-helper /session-channel-helper\n"
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
