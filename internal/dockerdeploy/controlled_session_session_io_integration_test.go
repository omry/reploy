package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestControlledSessionIOBridgeDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("controlled-session I/O bridge integration requires a supported Linux host, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	image, _ := buildApplicationStartupVerifierIntegrationImage(t, ctx)

	t.Run("typed requests and ordered PTY output", func(t *testing.T) {
		workload, bridge, client, requests := prepareControlledSessionIOBridgeIntegrationV1(t, ctx, image)
		capture := startSessionIOEventCaptureV1(client)
		if err := workload.Start(ctx); err != nil {
			t.Fatal(err)
		}

		writeSessionIORequestIntegrationV1(t, client, controlledsession.RequestV1{
			Kind:  controlledsession.RequestInputV1,
			Bytes: []byte("stty size; printf 'SIZE-1-DONE\\n'\n"),
		})
		waitSessionIORequestIntegrationV1(t, requests, controlledsession.RequestInputV1)
		capture.waitForOutput(t, []byte("24 80"), 10*time.Second)
		capture.waitForOutput(t, []byte("SIZE-1-DONE"), 10*time.Second)

		writeSessionIORequestIntegrationV1(t, client, controlledsession.RequestV1{
			Kind: controlledsession.RequestResizeV1, Columns: 132, Rows: 43,
		})
		waitSessionIORequestIntegrationV1(t, requests, controlledsession.RequestResizeV1)
		writeSessionIORequestIntegrationV1(t, client, controlledsession.RequestV1{
			Kind:  controlledsession.RequestInputV1,
			Bytes: []byte("stty size; printf '\\001\\002\\177\\377'; printf 'SIZE-2-DONE\\n'\n"),
		})
		waitSessionIORequestIntegrationV1(t, requests, controlledsession.RequestInputV1)
		capture.waitForOutput(t, []byte("43 132"), 10*time.Second)
		capture.waitForOutput(t, []byte{0x01, 0x02, 0x7f, 0xff}, 10*time.Second)
		capture.waitForOutput(t, []byte("SIZE-2-DONE"), 10*time.Second)

		writeSessionIORequestIntegrationV1(t, client, controlledsession.RequestV1{
			Kind:  controlledsession.RequestInputV1,
			Bytes: []byte("printf '\\036SLEEP-ACTIVE\\n'; sleep 30\n"),
		})
		waitSessionIORequestIntegrationV1(t, requests, controlledsession.RequestInputV1)
		capture.waitForOutput(t, append([]byte{0x1e}, []byte("SLEEP-ACTIVE")...), 10*time.Second)
		writeSessionIORequestIntegrationV1(t, client, controlledsession.RequestV1{
			Kind: controlledsession.RequestInputV1, Bytes: []byte{0x03},
		})
		waitSessionIORequestIntegrationV1(t, requests, controlledsession.RequestInputV1)
		writeSessionIORequestIntegrationV1(t, client, controlledsession.RequestV1{
			Kind:  controlledsession.RequestInputV1,
			Bytes: []byte("printf 'INTERRUPT-DONE\\n'; exit 42\n"),
		})
		waitSessionIORequestIntegrationV1(t, requests, controlledsession.RequestInputV1)
		capture.waitForOutput(t, []byte("INTERRUPT-DONE"), 10*time.Second)

		status, err := workload.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if status.Kind != controlledsession.ProcessStatusExitedV1 || status.Code == nil || *status.Code != 42 {
			t.Fatalf("workload status = %#v", status)
		}
		result, err := bridge.FinalizeOutput(time.Now().Add(10 * time.Second))
		if err != nil || result.Status.Kind != controlledsession.WorkloadOutputFinalizationDrainedV1 || result.Err != nil {
			t.Fatalf("FinalizeOutput() = %#v, %v", result, err)
		}
		bridge.StopRequests()
		if err := bridge.WaitRequests(ctx); err != nil {
			t.Fatalf("WaitRequests() = %v", err)
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		capture.waitDone(t, 10*time.Second)
	})

	t.Run("slow output preserves request flow and disconnect fails delivery", func(t *testing.T) {
		workload, bridge, client, requests := prepareControlledSessionIOBridgeIntegrationV1(t, ctx, image)
		if err := workload.Start(ctx); err != nil {
			t.Fatal(err)
		}
		writeSessionIORequestIntegrationV1(t, client, controlledsession.RequestV1{
			Kind:  controlledsession.RequestInputV1,
			Bytes: []byte("head -c 16777216 /dev/zero; printf 'SLOW-DONE\\n'\n"),
		})
		waitSessionIORequestIntegrationV1(t, requests, controlledsession.RequestInputV1)
		time.Sleep(200 * time.Millisecond)
		writeSessionIORequestIntegrationV1(t, client, controlledsession.RequestV1{
			Kind: controlledsession.RequestResizeV1, Columns: 101, Rows: 37,
		})
		waitSessionIORequestIntegrationV1(t, requests, controlledsession.RequestResizeV1)
		select {
		case <-bridge.OutputDone():
			t.Fatal("output bridge stopped instead of applying backpressure")
		default:
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		if err := bridge.WaitRequests(ctx); !errors.Is(err, controlledsession.ErrControllerDisconnectedV1) {
			t.Fatalf("WaitRequests() = %v", err)
		}
		result, err := bridge.FinalizeOutput(time.Now().Add(10 * time.Second))
		if err != nil || result.Status.Kind != controlledsession.WorkloadOutputFinalizationFailedV1 ||
			!errors.Is(result.Err, controlledsession.ErrControllerDisconnectedV1) {
			t.Fatalf("FinalizeOutput() = %#v, %v", result, err)
		}
	})
}

func prepareControlledSessionIOBridgeIntegrationV1(
	t *testing.T,
	ctx context.Context,
	image string,
) (*DockerWorkloadPTYV1, *controlledsession.SessionIOBridgeV1, *net.UnixConn, <-chan controlledsession.RequestV1) {
	t.Helper()
	identity := controlledsession.RuntimeIdentityV1{
		Username: "reploy", UID: strconv.Itoa(os.Geteuid()), GID: strconv.Itoa(os.Getegid()), SupplementaryGIDs: []string{},
	}
	if os.Geteuid() == 0 {
		identity.Username = "root"
	}
	authorization := testControlledSessionChannelAuthorizationV1(t, identity)
	channel, err := controlledsession.PreparePrivateChannelV1(controlledsession.PrivateChannelConfigV1{
		HostDirectory: filepath.Join(shortControlledSessionChannelTestDirectoryV1(t), "bridge"),
		Opened: controlledsession.OpenedV1{
			Authorization: authorization, Columns: 80, Rows: 24,
			OutputFinalizationTimeoutMilliseconds: controlledsession.DefaultOutputFinalizationTimeoutMillisecondsV1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = channel.Close() })
	claim := make(chan struct {
		connection *controlledsession.ControllerConnectionV1
		err        error
	}, 1)
	go func() {
		connection, err := channel.Claim(ctx)
		claim <- struct {
			connection *controlledsession.ControllerConnectionV1
			err        error
		}{connection: connection, err: err}
	}()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: channel.SocketPath(), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if opened, err := controlledsession.ReadEventV1(client); err != nil || opened.Kind != controlledsession.EventOpenedV1 {
		t.Fatalf("opened event = %#v, %v", opened, err)
	}
	claimed := <-claim
	if claimed.err != nil {
		t.Fatal(claimed.err)
	}

	plan := controlledSessionWorkloadIntegrationPlanV1(t, image, 80, 24)
	workload, err := PrepareDockerWorkloadPTYV1(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = workload.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, err := exec.CommandContext(cleanupCtx, plan.Cleanup.Name, plan.Cleanup.Args...).CombinedOutput()
		if err != nil && !strings.Contains(string(output), "No such container") {
			t.Errorf("cleanup controlled-session I/O bridge workload %q: %v\n%s", plan.Container, err, output)
		}
	})
	output, err := workload.Output()
	if err != nil {
		t.Fatal(err)
	}
	machine, err := controlledsession.NewMachineV1(authorization)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Observe(controlledsession.ObservationV1{Kind: controlledsession.ObservationActivatedV1}); err != nil {
		t.Fatal(err)
	}
	requests := make(chan controlledsession.RequestV1, 16)
	bridge, err := controlledsession.StartSessionIOBridgeV1(claimed.connection, output, func(requestCtx context.Context, request controlledsession.RequestV1) error {
		if _, err := machine.ApplyRequest(request); err != nil {
			return err
		}
		handled, err := controlledsession.ApplyAcceptedWorkloadPTYRequestV1(requestCtx, workload, request)
		if err != nil {
			return err
		}
		if !handled {
			return fmt.Errorf("integration request %q unexpectedly has no PTY effect", request.Kind)
		}
		requests <- request
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.StopRequests() })
	return workload, bridge, client, requests
}

func writeSessionIORequestIntegrationV1(t *testing.T, client *net.UnixConn, request controlledsession.RequestV1) {
	t.Helper()
	if err := controlledsession.WriteRequestV1(client, request); err != nil {
		t.Fatal(err)
	}
}

func waitSessionIORequestIntegrationV1(t *testing.T, requests <-chan controlledsession.RequestV1, want controlledsession.RequestKindV1) {
	t.Helper()
	select {
	case request := <-requests:
		if request.Kind != want {
			t.Fatalf("handled request = %#v, want kind %q", request, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for handled %q request", want)
	}
}

type sessionIOEventCaptureV1 struct {
	mu     sync.Mutex
	output []byte
	done   chan struct{}
	notify chan struct{}
	err    error
}

func startSessionIOEventCaptureV1(client *net.UnixConn) *sessionIOEventCaptureV1 {
	capture := &sessionIOEventCaptureV1{done: make(chan struct{}), notify: make(chan struct{}, 1)}
	go func() {
		defer close(capture.done)
		for {
			event, err := controlledsession.ReadEventV1(client)
			capture.mu.Lock()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					capture.err = err
				}
				capture.mu.Unlock()
				return
			}
			if event.Kind == controlledsession.EventOutputV1 {
				capture.output = append(capture.output, event.Bytes...)
			}
			capture.mu.Unlock()
			select {
			case capture.notify <- struct{}{}:
			default:
			}
		}
	}()
	return capture
}

func (capture *sessionIOEventCaptureV1) waitForOutput(t *testing.T, want []byte, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		capture.mu.Lock()
		found := bytes.Contains(capture.output, want)
		output := append([]byte(nil), capture.output...)
		err := capture.err
		capture.mu.Unlock()
		if found {
			return
		}
		select {
		case <-capture.notify:
		case <-capture.done:
			t.Fatalf("controller event stream closed before %q; output = %q, error = %v", want, output, err)
		case <-timer.C:
			t.Fatalf("timed out waiting for controller output %q; output = %q", want, output)
		}
	}
}

func (capture *sessionIOEventCaptureV1) waitDone(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-capture.done:
		capture.mu.Lock()
		defer capture.mu.Unlock()
		if capture.err != nil {
			t.Fatalf("controller event capture failed: %v", capture.err)
		}
	case <-time.After(timeout):
		t.Fatal("timed out waiting for controller event capture to stop")
	}
}
