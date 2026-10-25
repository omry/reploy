//go:build !linux

package dockerdeploy

import "testing"

func TestMappedPodmanConnectionRejectsOtherHostsV1(t *testing.T) {
	if connection, err := dialMappedPodmanV1(t.Context(), "unix:///engine.sock"); err == nil {
		connection.Close()
		t.Fatal("mapped Podman connection admitted outside Linux")
	}
}
