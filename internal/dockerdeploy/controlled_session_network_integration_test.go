package dockerdeploy

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
)

func TestControlledSessionNetworkingDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("controlled-session networking Docker integration requires a supported Linux host, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	watchdogExecutable := buildControlledSessionWatchdogExecutableV1(t, ctx)
	previousWatchdogExecutable := controlledSessionWatchdogExecutableV1
	controlledSessionWatchdogExecutableV1 = func() (string, error) { return watchdogExecutable, nil }
	t.Cleanup(func() { controlledSessionWatchdogExecutableV1 = previousWatchdogExecutable })
	image := buildControlledSessionControllerIntegrationImageV1(t, ctx)
	localPeer, publicPeer := createControlledSessionNetworkIsolationPeersV1(t, ctx, image)
	endpoints := []ControlledSessionEndpointPlanV1{
		{ID: "browser", Scheme: "http", Host: controlledsession.WorkloadEndpointHostV1, Port: "8080"},
		{ID: "socket", Scheme: "ws", Host: controlledsession.WorkloadEndpointHostV1, Port: "8080"},
	}
	t.Run("trusted controller bootstrap", func(t *testing.T) {
		proveControlledSessionNetworkControllerBootstrapV1(t, ctx, image, endpoints)
	})

	t.Run("HTTP WebSocket isolation and coarse reachability", func(t *testing.T) {
		plan := controlledSessionControllerIntegrationPlanWithEndpointsV1(
			t, image, []string{"/session-channel-helper", "network-supervise", localPeer, publicPeer}, endpoints,
		)
		assertControlledSessionPlanHasNoPublishedPortsV1(t, plan)
		result, err := runControlledSessionNetworkIntegrationPlanV1(t, ctx, plan)
		if err != nil {
			controllerCode := -1
			if result.ControllerStatus.Code != nil {
				controllerCode = *result.ControllerStatus.Code
			}
			t.Fatalf("network proof failed: %v\ncontroller status: %s/%d\nresult: %#v", err, result.ControllerStatus.Kind, controllerCode, result)
		}
		if result.SessionResult.Cause != controlledsession.CauseWorkloadExitV1 ||
			result.SessionResult.WorkloadStatus.Code == nil || *result.SessionResult.WorkloadStatus.Code != 42 ||
			result.SessionResult.WorkloadOutputFinalizationStatus.Kind != controlledsession.WorkloadOutputFinalizationDrainedV1 ||
			result.SessionResult.ControllerFinalizationStatus.Kind != controlledsession.ControllerFinalizationCompletedV1 ||
			result.SessionResult.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 ||
			!result.ResultDelivered || !result.ResultAcknowledged {
			t.Fatalf("network supervisor result = %#v", result)
		}
		if result.ControllerStatus.Code == nil || *result.ControllerStatus.Code != 0 ||
			result.DeliveryTailCleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
			t.Fatalf("network delivery-tail result = %#v", result)
		}
		assertControlledSessionNetworkIntegrationCleanupV1(t, ctx, plan)
	})

	t.Run("controller disconnect cleanup", func(t *testing.T) {
		plan := controlledSessionControllerIntegrationPlanWithEndpointsV1(
			t, image, []string{"/session-channel-helper", "disconnect-after-open"}, endpoints,
		)
		result, err := runControlledSessionNetworkIntegrationPlanV1(t, ctx, plan)
		if err == nil {
			t.Fatalf("controller disconnect unexpectedly succeeded: %#v", result)
		}
		if result.SessionResult.ControllerFinalizationStatus.Kind != controlledsession.ControllerFinalizationStartupFailedV1 &&
			result.SessionResult.ControllerFinalizationStatus.Kind != controlledsession.ControllerFinalizationLostV1 ||
			result.SessionResult.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 ||
			result.DeliveryTailCleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
			t.Fatalf("controller-disconnect cleanup result = %#v, error = %v", result, err)
		}
		assertControlledSessionNetworkIntegrationCleanupV1(t, ctx, plan)
	})
}

func proveControlledSessionNetworkControllerBootstrapV1(
	t *testing.T,
	ctx context.Context,
	image string,
	endpoints []ControlledSessionEndpointPlanV1,
) {
	t.Helper()
	plan := controlledSessionControllerIntegrationPlanWithEndpointsV1(
		t, image, []string{"/session-channel-helper", "wait-signal"}, endpoints,
	)
	network, err := PrepareDockerSessionNetworkV1(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := prepareControlledSessionChannelWithNetworkV1(plan, network.Realization())
	if err != nil {
		_ = network.Cleanup(context.Background())
		t.Fatal(err)
	}
	realized := network.Realization()
	controller, err := prepareDockerControllerV1(ctx, plan.Controller, dockerControllerBackendV1{
		bind: bindPinnedDockerCommandRunnerV1, observe: observeDockerContainerExitV1,
		requireReadyChannel: requirePreparedControlledSessionControllerChannelV1,
		peerAddresses:       realized.WorkloadAddresses,
	})
	if err != nil {
		_ = channel.Close()
		_ = network.Cleanup(context.Background())
		t.Fatal(err)
	}
	workload, err := prepareDockerWorkloadPTYV1(ctx, plan.Workload, dockerWorkloadPTYBackendV1{
		bind: bindPinnedDockerCommandRunnerV1, attach: attachDockerContainerPTYV1,
		resize: resizeDockerContainerPTYV1, observe: observeDockerContainerExitV1,
		peerAddresses: realized.ControllerAddresses,
	})
	if err != nil {
		_ = controller.Cleanup(context.Background())
		_ = channel.Close()
		_ = network.Cleanup(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = workload.Close()
		_ = workload.Cleanup(context.Background())
		_ = controller.Cleanup(context.Background())
		_ = channel.Close()
		_ = network.Cleanup(context.Background())
	})
	if err := network.Attach(ctx, controller.ContainerID(), workload.ContainerID()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(ctx); err != nil {
		t.Fatal(err)
	}
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport, err := channel.Claim(claimCtx)
	if err != nil {
		logs := runDockerIntegration(t, ctx, "logs", controller.ContainerID())
		t.Fatalf("trusted controller did not claim the session channel: %v\ncontroller logs:\n%s", err, logs)
	}
	_ = transport.Close()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	_ = controller.RequestGracefulStop(stopCtx)
}

func runControlledSessionNetworkIntegrationPlanV1(
	t *testing.T,
	ctx context.Context,
	plan ControlledSessionExecutionPlanV1,
) (ControlledSessionRunResultV1, error) {
	t.Helper()
	operation, err := deploy.AcquireOperationLock(ctx, plan.Workload.DeploymentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := operation.AcquireLiveRunLeaseV1(plan.LiveRunID)
	if err != nil {
		_ = operation.Unlock()
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Release(); err != nil {
			t.Errorf("release controlled-session network live-run lease: %v", err)
		}
	}()
	status, err := operation.AdmitLiveRunV1(deploy.LiveRunV1{
		ID: plan.LiveRunID, Kind: deploy.LiveRunKindShellV1, Name: plan.Workload.DeploymentID,
		GenerationReference: plan.Workload.GenerationReference, Exclusive: true,
	}, false)
	if err != nil {
		_ = operation.Unlock()
		t.Fatal(err)
	}
	if status != deploy.LiveRunStatusActiveV1 {
		_ = operation.Unlock()
		t.Fatalf("controlled-session network live run status = %q", status)
	}
	return RunControlledSessionV1(ctx, operation, plan, ControlledSessionRunOptionsV1{
		StartupTimeout: 30 * time.Second, TerminationGrace: 5 * time.Second,
		ControllerFinalizationTimeout: 15 * time.Second, ResultAcknowledgementTimeout: 5 * time.Second,
		CleanupTimeout: 15 * time.Second,
	})
}

func createControlledSessionNetworkIsolationPeersV1(t *testing.T, ctx context.Context, image string) (string, string) {
	t.Helper()
	seed := int(time.Now().UnixNano()%180) + 40
	localNetwork := uniqueDockerIntegrationName("reploy-session-network-local")
	publicNetwork := uniqueDockerIntegrationName("reploy-session-network-public")
	localAddress := fmt.Sprintf("172.30.%d.3", seed)
	publicAddress := fmt.Sprintf("11.%d.0.3", seed)
	runDockerIntegration(t, ctx, "network", "create", "--subnet", fmt.Sprintf("172.30.%d.0/24", seed), localNetwork)
	runDockerIntegration(t, ctx, "network", "create", "--subnet", fmt.Sprintf("11.%d.0.0/24", seed), publicNetwork)
	containers := []string{}
	t.Cleanup(func() {
		for _, container := range containers {
			_ = exec.CommandContext(context.Background(), "docker", "container", "rm", "--force", container).Run()
		}
		_ = exec.CommandContext(context.Background(), "docker", "network", "rm", localNetwork, publicNetwork).Run()
	})
	for _, peer := range []struct {
		name    string
		network string
		address string
	}{
		{name: uniqueDockerIntegrationName("reploy-session-network-local-peer"), network: localNetwork, address: localAddress},
		{name: uniqueDockerIntegrationName("reploy-session-network-public-peer"), network: publicNetwork, address: publicAddress},
	} {
		runDockerIntegration(t, ctx,
			"run", "--detach", "--pull", "never", "--name", peer.name,
			"--network", peer.network, "--ip", peer.address,
			"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
			"--entrypoint", "/session-network-helper", image, "listen", ":9090",
		)
		containers = append(containers, peer.name)
		waitControlledSessionNetworkPeerV1(t, ctx, peer.name)
	}
	return net.JoinHostPort(localAddress, "9090"), net.JoinHostPort(publicAddress, "9090")
}

func waitControlledSessionNetworkPeerV1(t *testing.T, ctx context.Context, container string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		logs := runDockerIntegration(t, ctx, "logs", container)
		if strings.Contains(logs, "LISTEN-READY") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("controlled-session isolation peer %q did not become ready", container)
}

func assertControlledSessionPlanHasNoPublishedPortsV1(t *testing.T, plan ControlledSessionExecutionPlanV1) {
	t.Helper()
	for _, container := range []ControlledSessionContainerPlanV1{plan.Controller, plan.Workload} {
		for _, argument := range container.Create.Args {
			if argument == "-p" || argument == "-P" || argument == "--publish" || argument == "--publish-all" || strings.HasPrefix(argument, "--publish=") {
				t.Fatalf("controlled-session %s plan publishes a host port: %#v", container.Role, container.Create.Args)
			}
		}
	}
}

func assertControlledSessionNetworkIntegrationCleanupV1(t *testing.T, ctx context.Context, plan ControlledSessionExecutionPlanV1) {
	t.Helper()
	for _, container := range []string{plan.Controller.Container, plan.Workload.Container} {
		output, inspectErr := exec.CommandContext(ctx, "docker", "container", "inspect", container).CombinedOutput()
		if inspectErr == nil || !strings.Contains(string(output), "No such container") {
			t.Fatalf("controlled-session container %q survived cleanup: %v\n%s", container, inspectErr, output)
		}
	}
	output, inspectErr := exec.CommandContext(ctx, "docker", "network", "inspect", plan.Controller.SessionNetwork.Name).CombinedOutput()
	if inspectErr == nil || !strings.Contains(strings.ToLower(string(output)), "not found") {
		t.Fatalf("controlled-session network %q survived cleanup: %v\n%s", plan.Controller.SessionNetwork.Name, inspectErr, output)
	}
	if _, statErr := os.Stat(plan.Channel.HostDirectory); !os.IsNotExist(statErr) {
		t.Fatalf("private channel directory survived cleanup: %v", statErr)
	}
	check, lockErr := deploy.AcquireOperationLock(ctx, plan.Workload.DeploymentDirectory)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	if queue, found, readErr := check.ReadLiveRunQueueV1(); readErr != nil || found {
		t.Fatalf("verified-clean network session retained ownership: %#v, found=%t, error=%v", queue, found, readErr)
	}
	if err := check.Unlock(); err != nil {
		t.Fatal(err)
	}
}
