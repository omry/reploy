//go:build linux

package dockerdeploy

import (
	"fmt"
	"net"
	"os"
	"strings"
)

func notifyInstalledServiceReadyV1() error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + strings.TrimPrefix(socket, "@")
	}
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("connect to systemd readiness socket: %w", err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("READY=1\nSTATUS=Monitoring Reploy workload container\n")); err != nil {
		return fmt.Errorf("notify systemd that the workload is ready: %w", err)
	}
	return nil
}
