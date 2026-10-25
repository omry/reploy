package dockerdeploy

import (
	"context"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func dialMappedPodmanV1(ctx context.Context, endpoint string) (net.Conn, error) {
	connection, err := dialLocalDockerEndpointV1(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	connectionUnix, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("mapped Podman requires a local Unix connection")
	}
	raw, err := connectionUnix.SyscallConn()
	var peer *unix.Ucred
	if err == nil {
		var socketErr error
		err = raw.Control(func(fd uintptr) { peer, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
		if err == nil {
			err = socketErr
		}
	}
	if err != nil || peer == nil || peer.Uid != uint32(os.Geteuid()) || peer.Gid != uint32(os.Getegid()) || os.Geteuid() == 0 {
		_ = connection.Close()
		return nil, fmt.Errorf("mapped Podman connection did not prove the invoking non-root UID/GID: %v", err)
	}
	return connection, nil
}
