package dockerdeploy

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestMappedPodmanConnectionUsesActualUnixPeerV1(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("positive peer fixture requires a non-root caller")
	}
	path := filepath.Join(t.TempDir(), "engine.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection, err := dialMappedPodmanV1(t.Context(), "unix://"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	accepted.Close()
	for _, endpoint := range []string{"tcp://127.0.0.1:12345", "npipe:////./pipe/docker_engine", "unix://" + filepath.Join(t.TempDir(), "missing.sock")} {
		if c, err := dialMappedPodmanV1(t.Context(), endpoint); err == nil {
			c.Close()
			t.Fatalf("accepted %s", endpoint)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if c, err := dialMappedPodmanV1(ctx, "unix://"+path); err == nil {
		c.Close()
		t.Fatal("cancelled dial succeeded")
	}
}
