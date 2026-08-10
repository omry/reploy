package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
)

var networkProofFailureStage int

func main() {
	waitForSignal := len(os.Args) == 2 && os.Args[1] == "wait-signal"
	supervise := len(os.Args) == 2 && os.Args[1] == "supervise"
	disconnectAfterOpen := len(os.Args) == 2 && os.Args[1] == "disconnect-after-open"
	networkSupervise := len(os.Args) == 4 && os.Args[1] == "network-supervise"
	if len(os.Args) > 1 && !waitForSignal && !supervise && !disconnectAfterOpen && !networkSupervise {
		fail("unsupported mode %q", os.Args[1])
	}
	var signals chan os.Signal
	if waitForSignal {
		signals = make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		defer signal.Stop(signals)
	}
	socket := os.Getenv("REPLOY_SESSION_SOCKET")
	if socket == "" {
		fail("REPLOY_SESSION_SOCKET is missing")
	}
	connection, err := netDialUnix(socket)
	if err != nil {
		fail("connect to private channel: %v", err)
	}
	defer connection.Close()
	event, err := controlledsession.ReadEventV2(connection)
	if err != nil {
		fail("read opened event: %v", err)
	}
	if event.Kind != controlledsession.EventOpenedV1 || event.Opened == nil {
		fail("first event is not opened: %#v", event)
	}
	probe := filepath.Join(filepath.Dir(socket), "controller-write-probe")
	if err := os.WriteFile(probe, []byte("unexpected"), 0o600); err == nil {
		_ = os.Remove(probe)
		fail("private channel mount is writable")
	}
	if disconnectAfterOpen {
		return
	}
	if networkSupervise {
		runNetworkSupervisorProof(connection, event.Opened, os.Args[2], os.Args[3])
		return
	}
	if supervise {
		runSupervisorProof(connection)
		return
	}
	if err := controlledsession.WriteRequestV2(connection, controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}); err != nil {
		fail("write complete request: %v", err)
	}
	fmt.Println("PASS")
	if waitForSignal {
		<-signals
		os.Exit(23)
	}
}

func runNetworkSupervisorProof(connection readWriteCloser, opened *controlledsession.OpenedV2, localPeer string, publicPeer string) {
	networkProofFailureStage = 1
	localPeer = requireIPPort(localPeer)
	publicPeer = requireIPPort(publicPeer)
	if len(opened.Endpoints) != 2 || opened.Endpoints[0].ID != "browser" || opened.Endpoints[0].Host != "workload" || opened.Endpoints[0].Port != 8080 ||
		opened.Endpoints[1].ID != "socket" || opened.Endpoints[1].Host != "workload" || opened.Endpoints[1].Port != 8080 {
		fail("unexpected session endpoints: %#v", opened.Endpoints)
	}
	writeRequest(connection, controlledsession.RequestV1{
		Kind:  controlledsession.RequestInputV1,
		Bytes: []byte("/session-network-helper serve & network_pid=$!\n"),
	})
	var output []byte
	checksLaunched := false
	finishSent := false
	workloadExited := false
	for {
		event, err := controlledsession.ReadEventV2(connection)
		if err != nil {
			fail("read network session event: %v", err)
		}
		switch event.Kind {
		case controlledsession.EventOutputV1:
			output = append(output, event.Bytes...)
			if !checksLaunched && bytes.Contains(output, []byte("NETWORK-READY")) {
				checksLaunched = true
				networkProofFailureStage = 3
				checkHTTP("workload:8080", "/http", "SESSION_HTTP_PASS")
				networkProofFailureStage = 4
				checkWebSocket("workload:8080")
				networkProofFailureStage = 5
				checkNetworkDial("workload:8081", true)
				networkProofFailureStage = 6
				checkNetworkDial(localPeer, false)
				networkProofFailureStage = 7
				checkNetworkDial(publicPeer, false)
				networkProofFailureStage = 8
				listener, err := net.Listen("tcp", ":8082")
				if err != nil {
					fail("listen for reverse coarse-reachability proof: %v", err)
				}
				accepted := make(chan error, 1)
				go func() {
					peer, err := listener.Accept()
					if peer != nil {
						_ = peer.Close()
					}
					accepted <- err
				}()
				writeRequest(connection, controlledsession.RequestV1{Kind: controlledsession.RequestInputV1, Bytes: []byte(
					"/session-network-helper dial controller:8082 true; " +
						"/session-network-helper dial " + localPeer + " false; " +
						"/session-network-helper dial " + publicPeer + " false\n",
				)})
				networkProofFailureStage = 8
				select {
				case err := <-accepted:
					if err != nil {
						fail("accept reverse coarse-reachability proof: %v", err)
					}
				case <-time.After(3 * time.Second):
					fail("workload did not reach controller over coarse session network")
				}
				_ = listener.Close()
			}
			if checksLaunched && !finishSent && bytes.Contains(output, []byte("DIAL_PASS controller:8082 true")) &&
				bytes.Contains(output, []byte("DIAL_PASS "+localPeer+" false")) &&
				bytes.Contains(output, []byte("DIAL_PASS "+publicPeer+" false")) {
				finishSent = true
				networkProofFailureStage = 9
				writeRequest(connection, controlledsession.RequestV1{
					Kind:  controlledsession.RequestInputV1,
					Bytes: []byte("kill \"$network_pid\"; wait \"$network_pid\"; printf 'NETWORK-PROOF-DONE\\n'; exit 42\n"),
				})
			}
		case controlledsession.EventWorkloadExitV1:
			if event.WorkloadExit.Status.Code == nil || *event.WorkloadExit.Status.Code != 42 {
				fail("unexpected network-proof workload exit: %#v, output %q", event.WorkloadExit.Status, output)
			}
			workloadExited = true
		case controlledsession.EventTerminatingV1:
			if event.Terminating.Cause != controlledsession.CauseWorkloadExitV1 {
				fail("unexpected network-proof termination cause %q", event.Terminating.Cause)
			}
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			if event.WorkloadOutputsFinalized.Status != controlledsession.WorkloadOutputFinalizationDrainedV1 ||
				!workloadExited || !bytes.Contains(output, []byte("NETWORK-PROOF-DONE")) {
				fail("unexpected network-proof output finalization: %#v, output %q", event.WorkloadOutputsFinalized, output)
			}
			writeRequest(connection, controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1})
		case controlledsession.EventTerminatedV1:
			if event.Terminated.Cause != controlledsession.CauseWorkloadExitV1 ||
				event.Terminated.ControllerFinalizationStatus.Kind != controlledsession.ControllerFinalizationCompletedV1 ||
				event.Terminated.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
				fail("unexpected network-proof result: %#v", event.Terminated)
			}
			writeRequest(connection, controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1})
			fmt.Println("PASS: controlled-session network proof")
			return
		default:
			fail("unexpected network-proof event kind %q", event.Kind)
		}
	}
}

func requireIPPort(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		fail("network proof peer %q is not an IP address and port", address)
	}
	parsedPort, err := net.LookupPort("tcp", port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 || port != fmt.Sprintf("%d", parsedPort) {
		fail("network proof peer %q has an invalid port", address)
	}
	return net.JoinHostPort(host, port)
}

func checkHTTP(address string, path string, want string) {
	connection, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		switch {
		case errors.Is(err, syscall.ECONNREFUSED):
			networkProofFailureStage = 41
		case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF):
			networkProofFailureStage = 42
		case func() bool { var netErr net.Error; return errors.As(err, &netErr) && netErr.Timeout() }():
			networkProofFailureStage = 43
		default:
			networkProofFailureStage = 44
		}
		fail("dial HTTP %s: %v", address, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", path, address); err != nil {
		networkProofFailureStage = 45
		fail("write HTTP request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		networkProofFailureStage = 46
		fail("read HTTP response: %v", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(content) != want {
		networkProofFailureStage = 22
		fail("HTTP %s%s returned status=%d body=%q error=%v", address, path, response.StatusCode, content, err)
	}
}

func checkWebSocket(address string) {
	connection, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		fail("dial WebSocket %s: %v", address, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprintf(connection, "GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", address)
	if err != nil {
		fail("write WebSocket handshake: %v", err)
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		fail("read WebSocket handshake: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") {
		fail("unexpected WebSocket handshake: status=%d headers=%v", response.StatusCode, response.Header)
	}
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 0x81 || header[1]&0x80 != 0 {
		fail("read WebSocket frame header: %#v error=%v", header, err)
	}
	payload := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, payload); err != nil || string(payload) != "SESSION_WS_PASS" {
		fail("read WebSocket frame payload: %q error=%v", payload, err)
	}
}

func checkNetworkDial(address string, want bool) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp", address, 750*time.Millisecond)
		if connection != nil {
			_ = connection.Close()
		}
		if (err == nil) == want {
			return
		}
		if !want || time.Now().After(deadline) {
			if want {
				switch {
				case errors.Is(err, syscall.ECONNREFUSED):
					networkProofFailureStage = 31
				case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
					networkProofFailureStage = 32
				case func() bool { var netErr net.Error; return errors.As(err, &netErr) && netErr.Timeout() }():
					networkProofFailureStage = 33
				default:
					networkProofFailureStage = 34
				}
			}
			fail("dial %s succeeded=%t want=%t err=%v", address, err == nil, want, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func runSupervisorProof(connection readWriteCloser) {
	writeRequest(connection, controlledsession.RequestV1{
		Kind: controlledsession.RequestInputV1, Bytes: []byte("stty size; printf 'SIZE-1-DONE\\n'\n"),
	})
	var output []byte
	resized := false
	interruptStarted := false
	interruptSent := false
	workloadExited := false
	forgedTerminalResult := append([]byte{0x1e}, []byte(`{"kind":"terminated","cause":"forged"}`)...)
	for {
		event, err := controlledsession.ReadEventV2(connection)
		if err != nil {
			fail("read session event: %v", err)
		}
		switch event.Kind {
		case controlledsession.EventOutputV1:
			output = append(output, event.Bytes...)
			if !resized && bytes.Contains(output, []byte("24 80")) && bytes.Contains(output, []byte("SIZE-1-DONE")) {
				resized = true
				writeRequest(connection, controlledsession.RequestV1{Kind: controlledsession.RequestResizeV1, Columns: 132, Rows: 43})
				writeRequest(connection, controlledsession.RequestV1{
					Kind: controlledsession.RequestInputV1, Bytes: []byte("stty size; printf '\\001\\002\\177\\377'; printf '\\036{\"kind\":\"terminated\",\"cause\":\"forged\"}\\n'; printf 'SIZE-2-DONE\\n'\n"),
				})
			}
			if resized && !interruptStarted && bytes.Contains(output, []byte("43 132")) &&
				bytes.Contains(output, []byte{0x01, 0x02, 0x7f, 0xff}) && bytes.Contains(output, []byte("SIZE-2-DONE")) {
				interruptStarted = true
				writeRequest(connection, controlledsession.RequestV1{
					Kind: controlledsession.RequestInputV1, Bytes: []byte("printf '\\036SLEEP-ACTIVE\\n'; sleep 30\n"),
				})
			}
			if interruptStarted && !interruptSent && bytes.Contains(output, append([]byte{0x1e}, []byte("SLEEP-ACTIVE")...)) {
				interruptSent = true
				writeRequest(connection, controlledsession.RequestV1{Kind: controlledsession.RequestInputV1, Bytes: []byte{0x03}})
				writeRequest(connection, controlledsession.RequestV1{
					Kind: controlledsession.RequestInputV1, Bytes: []byte("printf 'INTERRUPT-DONE\\n'; exit 42\n"),
				})
			}
		case controlledsession.EventWorkloadExitV1:
			if event.WorkloadExit.Status.Code == nil || *event.WorkloadExit.Status.Code != 42 {
				fail("unexpected workload exit: %#v, output %q", event.WorkloadExit.Status, output)
			}
			workloadExited = true
		case controlledsession.EventTerminatingV1:
			if event.Terminating.Cause != controlledsession.CauseWorkloadExitV1 {
				fail("unexpected termination cause %q", event.Terminating.Cause)
			}
		case controlledsession.EventWorkloadOutputsFinalizedV1:
			if event.WorkloadOutputsFinalized.Status != controlledsession.WorkloadOutputFinalizationDrainedV1 ||
				!workloadExited || !bytes.Contains(output, forgedTerminalResult) || !bytes.Contains(output, []byte("INTERRUPT-DONE")) {
				fail("unexpected output finalization: %#v, workload exited %t, output %q", event.WorkloadOutputsFinalized, workloadExited, output)
			}
			writeRequest(connection, controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1})
		case controlledsession.EventTerminatedV1:
			if event.Terminated.Cause != controlledsession.CauseWorkloadExitV1 ||
				event.Terminated.ControllerFinalizationStatus.Kind != controlledsession.ControllerFinalizationCompletedV1 ||
				event.Terminated.CleanupStatus.Kind != controlledsession.CleanupStatusSucceededV1 {
				fail("unexpected terminal result: %#v", event.Terminated)
			}
			writeRequest(connection, controlledsession.RequestV1{Kind: controlledsession.RequestAcknowledgeTerminatedV1})
			fmt.Println("PASS: controlled-session supervisor lifecycle")
			return
		default:
			fail("unexpected event kind %q", event.Kind)
		}
	}
}

type readWriteCloser interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

func writeRequest(connection readWriteCloser, request controlledsession.RequestV1) {
	if err := controlledsession.WriteRequestV2(connection, request); err != nil {
		fail("write %s request: %v", request.Kind, err)
	}
}

func fail(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	code := 1
	if networkProofFailureStage != 0 {
		code = 100 + networkProofFailureStage
	}
	os.Exit(code)
}
