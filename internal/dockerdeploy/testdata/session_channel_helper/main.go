package main

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/omry/reploy/internal/controlledsession"
)

func main() {
	waitForSignal := len(os.Args) == 2 && os.Args[1] == "wait-signal"
	supervise := len(os.Args) == 2 && os.Args[1] == "supervise"
	if len(os.Args) > 1 && !waitForSignal && !supervise {
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
	os.Exit(1)
}
