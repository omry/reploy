package main

import (
	"fmt"
	"net"
	"testing"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestRequireReadyPreservesPreReadyOutputV1(t *testing.T) {
	host, controller := net.Pipe()
	t.Cleanup(func() {
		_ = host.Close()
		_ = controller.Close()
	})
	written := make(chan error, 1)
	go func() {
		for _, event := range []controlledsession.EventV1{
			{Kind: controlledsession.EventOutputV1, Bytes: []byte("$ ")},
			{Kind: controlledsession.EventOutputV1, Bytes: []byte("banner\n")},
			{Kind: controlledsession.EventReadyV1},
		} {
			if err := controlledsession.WriteEventV1(host, event); err != nil {
				written <- fmt.Errorf("write %s event: %w", event.Kind, err)
				return
			}
		}
		written <- nil
	}()

	if got, want := string(requireReady(controller)), "$ banner\n"; got != want {
		t.Fatalf("pre-ready output = %q, want %q", got, want)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}
