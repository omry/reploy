//go:build linux

package controlledsession

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControllerTerminalListenerV1IsPrivateSingleUseAndCleansUp(t *testing.T) {
	home := shortControllerBrokerTempHomeV1(t)
	listener, err := PrepareControllerTerminalListenerV1(home)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(listener.SocketPath())
	if filepath.Dir(directory) != home || filepath.Base(directory)[:len("reploy-controlled-session-")] != "reploy-controlled-session-" {
		t.Fatalf("terminal directory escaped expected grammar: %q", directory)
	}
	if info, err := os.Stat(directory); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("terminal directory mode = %v, %v; want 0700", info, err)
	}
	if info, err := os.Stat(listener.SocketPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("terminal socket mode = %v, %v; want 0600", info, err)
	}

	accepted := make(chan *ControllerTerminalConnectionV1, 1)
	failures := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		connection, err := listener.Accept(ctx)
		if err != nil {
			failures <- err
			return
		}
		accepted <- connection
	}()
	peer, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: listener.SocketPath(), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	select {
	case connection := <-accepted:
		if connection == nil {
			t.Fatal("accepted nil terminal connection")
		}
	case err := <-failures:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := os.Lstat(listener.SocketPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claimed terminal socket still exists: %v", err)
	}
	if _, err := listener.Accept(ctx); err == nil {
		t.Fatal("terminal listener accepted a second claim")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("terminal directory survived close: %v", err)
	}
}

func TestControllerTerminalListenerV1RejectsSymlinkHome(t *testing.T) {
	root := t.TempDir()
	realHome := filepath.Join(root, "real")
	if err := os.Mkdir(realHome, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realHome, link); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareControllerTerminalListenerV1(link); err == nil {
		t.Fatal("symlink temporary home was accepted")
	}
}

func TestControllerTerminalListenerV1RejectsNonPrivateHome(t *testing.T) {
	home := shortControllerBrokerTempHomeV1(t)
	if err := os.Chmod(home, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareControllerTerminalListenerV1(home); err == nil || !strings.Contains(err.Error(), "expected 0700") {
		t.Fatalf("non-private home error = %v", err)
	}
}

func TestControllerTerminalConnectionV1AppliesBackpressure(t *testing.T) {
	broker, peer := net.Pipe()
	defer peer.Close()
	connection := &ControllerTerminalConnectionV1{connection: broker}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := connection.WriteOutput(ctx, make([]byte, MaxFramePayloadV1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked output error = %v, want deadline exceeded", err)
	}
}
