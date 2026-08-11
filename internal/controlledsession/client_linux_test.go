//go:build linux

package controlledsession

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDialSessionClientV1ClaimsPrivateChannel(t *testing.T) {
	channel, config := prepareCurrentIdentityChannelV1(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	claimResult := make(chan struct {
		connection *ControllerConnectionV1
		err        error
	}, 1)
	go func() {
		connection, err := channel.Claim(ctx)
		claimResult <- struct {
			connection *ControllerConnectionV1
			err        error
		}{connection: connection, err: err}
	}()

	client, err := DialSessionClientV1(ctx, channel.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if opened := client.Opened(); !reflect.DeepEqual(opened, config.Opened) {
		t.Fatalf("opened = %#v, want %#v", opened, config.Opened)
	}
	claimed := <-claimResult
	if claimed.err != nil {
		t.Fatal(claimed.err)
	}
	defer claimed.connection.Close()
}

func TestDialSessionClientV1RejectsInvalidSocketPaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for _, path := range []string{"", "relative/control.sock", "/tmp/session/../control.sock"} {
		if _, err := DialSessionClientV1(ctx, path); err == nil || !strings.Contains(err.Error(), "absolute clean socket path") {
			t.Fatalf("DialSessionClientV1(%q) error = %v", path, err)
		}
	}
}
