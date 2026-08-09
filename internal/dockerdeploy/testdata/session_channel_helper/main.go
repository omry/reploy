package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/omry/reploy/internal/controlledsession"
)

func main() {
	waitForSignal := len(os.Args) == 2 && os.Args[1] == "wait-signal"
	if len(os.Args) > 1 && !waitForSignal {
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
	event, err := controlledsession.ReadEventV1(connection)
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
	if err := controlledsession.WriteRequestV1(connection, controlledsession.RequestV1{Kind: controlledsession.RequestCompleteV1}); err != nil {
		fail("write complete request: %v", err)
	}
	fmt.Println("PASS")
	if waitForSignal {
		<-signals
		os.Exit(23)
	}
}

func fail(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
