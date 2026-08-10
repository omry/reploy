package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestControlledSessionPrivateChannelDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("private channel Docker integration requires a supported Linux host, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	image := buildSessionChannelHelperImageV1(t, ctx)

	identity := controlledsession.RuntimeIdentityV1{
		Username: "reploy", UID: strconv.Itoa(os.Geteuid()), GID: strconv.Itoa(os.Getegid()), SupplementaryGIDs: []string{},
	}
	if os.Geteuid() == 0 {
		identity.Username = "root"
	}
	authorization := testControlledSessionChannelAuthorizationV1(t, identity)
	channel, err := controlledsession.PreparePrivateChannelV1(controlledsession.PrivateChannelConfigV1{
		HostDirectory: filepath.Join(shortControlledSessionChannelTestDirectoryV1(t), "session"),
		Opened: controlledsession.OpenedV2{
			Authorization: authorization, Endpoints: []controlledsession.EndpointV2{}, Columns: 80, Rows: 24,
			OutputFinalizationTimeoutMilliseconds: controlledsession.DefaultOutputFinalizationTimeoutMillisecondsV1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = channel.Close() })

	containerOutput := make(chan struct {
		output string
		err    error
	}, 1)
	container := uniqueDockerIntegrationName("reploy-session-channel")
	go func() {
		channelDirectory := filepath.Dir(channel.SocketPath())
		command := exec.CommandContext(ctx, "docker",
			"run", "--rm", "--name", container, "--network", "none",
			"--user", identity.UID+":"+identity.GID,
			"--mount", "type=bind,src="+channelDirectory+",dst=/run/reploy/session,readonly",
			"--env", "REPLOY_SESSION_SOCKET=/run/reploy/session/"+controlledsession.PrivateChannelSocketNameV1,
			image,
		)
		output, err := command.CombinedOutput()
		containerOutput <- struct {
			output string
			err    error
		}{output: string(output), err: err}
	}()
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "container", "rm", "--force", container).Run()
	})

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
	result := <-containerOutput
	if result.err != nil || strings.TrimSpace(result.output) != "PASS" {
		t.Fatalf("session channel helper: %v\n%s", result.err, result.output)
	}
}

func shortControlledSessionChannelTestDirectoryV1(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "reploy-cs-it-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove short controlled-session channel test directory: %v", err)
		}
	})
	return directory
}

func buildSessionChannelHelperImageV1(t *testing.T, ctx context.Context) string {
	t.Helper()
	if output, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("Docker is unavailable: %v\n%s", err, output)
	}
	workspace := t.TempDir()
	helper := filepath.Join(workspace, "session-channel-helper")
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate session channel integration source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", helper, "./internal/dockerdeploy/testdata/session_channel_helper")
	build.Dir = repositoryRoot
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOCACHE="+filepath.Join(workspace, "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build session channel helper: %v\n%s", err, output)
	}
	dockerfile := filepath.Join(workspace, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\nCOPY session-channel-helper /session-channel-helper\nENTRYPOINT [\"/session-channel-helper\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	image := uniqueDockerIntegrationName("reploy-session-channel-helper")
	command := exec.CommandContext(ctx, "docker", "build", "--pull=false", "--tag", image, workspace)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build session channel helper image: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.CommandContext(context.Background(), "docker", "image", "rm", image).CombinedOutput(); err != nil {
			t.Errorf("remove session channel helper image: %v\n%s", err, output)
		}
	})
	return image
}

func testControlledSessionChannelAuthorizationV1(t *testing.T, identity controlledsession.RuntimeIdentityV1) controlledsession.AuthorizationV1 {
	t.Helper()
	input, backend := controlledSessionPlanFixtureV1(t)
	input.ControllerRuntime.Docker.Sandbox.RuntimeUser.LocalUser = identity.Username
	uidValue, err := strconv.ParseUint(identity.UID, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	gidValue, err := strconv.ParseUint(identity.GID, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	uid, gid := uint32(uidValue), uint32(gidValue)
	input.ControllerRuntime.Docker.Sandbox.RuntimeUser.UID = uid
	input.ControllerRuntime.Docker.Sandbox.RuntimeUser.GID = gid
	input.ControllerRuntime.Docker.Sandbox.RuntimeUser.DockerUser = fmt.Sprintf("%d:%d", uid, gid)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Authorization
}
