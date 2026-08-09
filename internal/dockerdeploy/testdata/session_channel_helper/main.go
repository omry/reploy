package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/controlledsession"
)

func main() {
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
}

func fail(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
