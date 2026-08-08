package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/deploy"
)

func TestDockerWorkloadPTYIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	image, _ := buildApplicationStartupVerifierIntegrationImage(t, ctx)

	t.Run("PTY input output resize Ctrl-C and exact exit", func(t *testing.T) {
		plan := controlledSessionWorkloadIntegrationPlanV1(t, image, 80, 24)
		workload, capture := prepareControlledSessionWorkloadIntegrationV1(t, ctx, plan)
		if err := workload.Start(ctx); err != nil {
			t.Fatal(err)
		}

		writeWorkloadPTYIntegrationV1(t, ctx, workload, "stty size; printf 'SIZE-1-DONE\\n'\n")
		capture.waitFor(t, []byte("24 80"), 10*time.Second)
		capture.waitFor(t, []byte("SIZE-1-DONE"), 10*time.Second)
		if err := workload.Resize(ctx, 132, 43); err != nil {
			t.Fatal(err)
		}
		writeWorkloadPTYIntegrationV1(t, ctx, workload, "stty size; printf 'SIZE-2-DONE\\n'\n")
		capture.waitFor(t, []byte("43 132"), 10*time.Second)
		capture.waitFor(t, []byte("SIZE-2-DONE"), 10*time.Second)

		writeWorkloadPTYIntegrationV1(t, ctx, workload, "printf '\\001\\002\\177\\377'; printf 'BINARY-DONE\\n'\n")
		capture.waitFor(t, []byte{0x01, 0x02, 0x7f, 0xff}, 10*time.Second)
		capture.waitFor(t, []byte("BINARY-DONE"), 10*time.Second)

		writeWorkloadPTYIntegrationV1(t, ctx, workload, "stty raw -echo; printf '\\036RAW-ACTIVE\\n'; dd bs=1 count=4 2>/dev/null | od -An -t x1; stty sane; printf 'RAW-DONE\\n'\n")
		capture.waitFor(t, append([]byte{0x1e}, []byte("RAW-ACTIVE")...), 10*time.Second)
		if err := workload.WriteInput(ctx, []byte{0x00, 0x01, 0x7f, 0xff}); err != nil {
			t.Fatal(err)
		}
		capture.waitFor(t, []byte("00 01 7f ff"), 10*time.Second)
		capture.waitFor(t, []byte("RAW-DONE"), 10*time.Second)

		writeWorkloadPTYIntegrationV1(t, ctx, workload, "printf '\\036SLEEP-ACTIVE\\n'; sleep 30\n")
		capture.waitFor(t, append([]byte{0x1e}, []byte("SLEEP-ACTIVE")...), 10*time.Second)
		if err := workload.WriteInput(ctx, []byte{0x03}); err != nil {
			t.Fatal(err)
		}
		writeWorkloadPTYIntegrationV1(t, ctx, workload, "printf 'INTERRUPT-DONE\\n'; exit 42\n")
		capture.waitFor(t, []byte("INTERRUPT-DONE"), 10*time.Second)
		status, err := workload.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if status.Kind != controlledsession.ProcessStatusExitedV1 || status.Code == nil || *status.Code != 42 {
			t.Fatalf("workload exit status = %#v", status)
		}
		capture.waitDone(t, 10*time.Second)
	})

	t.Run("graceful and forced stop", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			stop     func(context.Context, *DockerWorkloadPTYV1) error
			wantCode int
		}{
			{name: "graceful", stop: func(ctx context.Context, workload *DockerWorkloadPTYV1) error {
				return workload.RequestGracefulStop(ctx)
			}, wantCode: 23},
			{name: "forced", stop: func(ctx context.Context, workload *DockerWorkloadPTYV1) error {
				return workload.ForceStop(ctx)
			}, wantCode: 137},
		} {
			t.Run(test.name, func(t *testing.T) {
				plan := controlledSessionWorkloadIntegrationPlanV1(t, image, 80, 24)
				workload, capture := prepareControlledSessionWorkloadIntegrationV1(t, ctx, plan)
				if err := workload.Start(ctx); err != nil {
					t.Fatal(err)
				}
				command := "printf '\\036STOP-READY\\n'; while :; do sleep 1; done\n"
				if test.name == "graceful" {
					command = "trap 'exit 23' TERM; printf '\\036STOP-READY\\n'; while :; do sleep 1; done\n"
				}
				writeWorkloadPTYIntegrationV1(t, ctx, workload, command)
				capture.waitFor(t, append([]byte{0x1e}, []byte("STOP-READY")...), 10*time.Second)
				if err := test.stop(ctx, workload); err != nil {
					t.Fatal(err)
				}
				status, err := workload.Wait(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if status.Code == nil || *status.Code != test.wantCode {
					t.Fatalf("stop exit status = %#v, want code %d", status, test.wantCode)
				}
				capture.waitDone(t, 10*time.Second)
			})
		}
	})
}

func controlledSessionWorkloadIntegrationPlanV1(
	t *testing.T,
	image string,
	columns uint32,
	rows uint32,
) ControlledSessionContainerPlanV1 {
	t.Helper()
	root := t.TempDir()
	buildIdentity := canonical.Digest("sha256:" + strings.Repeat("3", 64))
	current := CurrentBuild{Generation: deploy.EnvironmentGenerationState{
		Reference: image, BuildLockDigest: buildIdentity,
	}}
	dockerPlan := DockerExecutionPlan{
		EnvironmentID: "workload", DeploymentDir: root, Phase: blueprint.PhaseStaged,
		Image: image, ContainerName: uniqueDockerIntegrationName("reploy-session-pty"),
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{
			LocalUser: "reploy", UID: 12345, GID: 23456, DockerUser: "12345:23456",
		}),
	}
	plan, err := controlledSessionContainerPlanV1(
		ControlledSessionRoleWorkloadV1,
		"run-0000000000000001",
		current,
		dockerPlan,
		[]string{"/bin/sh"},
		ControlledSessionChannelPlanV1{},
		[]string{root},
		columns,
		rows,
	)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func prepareControlledSessionWorkloadIntegrationV1(
	t *testing.T,
	ctx context.Context,
	plan ControlledSessionContainerPlanV1,
) (*DockerWorkloadPTYV1, *dockerPTYIntegrationCaptureV1) {
	t.Helper()
	workload, err := PrepareDockerWorkloadPTYV1(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = workload.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(cleanupCtx, "docker", plan.Cleanup.Args...).CombinedOutput(); err != nil {
			t.Errorf("cleanup Docker workload PTY integration container %q: %v\n%s", plan.Container, err, output)
		}
	})
	output, err := workload.Output()
	if err != nil {
		t.Fatal(err)
	}
	return workload, startDockerPTYIntegrationCaptureV1(output)
}

func writeWorkloadPTYIntegrationV1(t *testing.T, ctx context.Context, workload *DockerWorkloadPTYV1, value string) {
	t.Helper()
	if err := workload.WriteInput(ctx, []byte(value)); err != nil {
		t.Fatal(err)
	}
}

type dockerPTYIntegrationCaptureV1 struct {
	mu     sync.Mutex
	bytes  []byte
	notify chan struct{}
	done   chan struct{}
	err    error
}

func startDockerPTYIntegrationCaptureV1(source io.ReadCloser) *dockerPTYIntegrationCaptureV1 {
	capture := &dockerPTYIntegrationCaptureV1{notify: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		defer close(capture.done)
		buffer := make([]byte, 4096)
		for {
			count, err := source.Read(buffer)
			if count > 0 {
				capture.mu.Lock()
				capture.bytes = append(capture.bytes, buffer[:count]...)
				capture.mu.Unlock()
				select {
				case capture.notify <- struct{}{}:
				default:
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					capture.mu.Lock()
					capture.err = err
					capture.mu.Unlock()
				}
				return
			}
		}
	}()
	return capture
}

func (capture *dockerPTYIntegrationCaptureV1) waitFor(t *testing.T, want []byte, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		capture.mu.Lock()
		found := bytes.Contains(capture.bytes, want)
		got := append([]byte(nil), capture.bytes...)
		err := capture.err
		capture.mu.Unlock()
		if found {
			return
		}
		select {
		case <-capture.notify:
		case <-capture.done:
			t.Fatalf("PTY output closed before %q; output = %q, error = %v", want, got, err)
		case <-timer.C:
			t.Fatalf("timed out waiting for PTY output %q; output = %q", want, got)
		}
	}
}

func (capture *dockerPTYIntegrationCaptureV1) waitDone(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-capture.done:
		capture.mu.Lock()
		err := capture.err
		capture.mu.Unlock()
		if err != nil {
			t.Fatalf("PTY capture error = %v", err)
		}
	case <-time.After(timeout):
		t.Fatal("timed out waiting for PTY output closure")
	}
}
