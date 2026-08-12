//go:build linux

package controlledsession

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type controllerUnixTerminalTransportV1 struct {
	directory string
	socket    string
	listener  *net.UnixListener
	stopOnce  sync.Once
	stopErr   error
	closeOnce sync.Once
	closeErr  error
}

func prepareControllerTerminalTransportV1(temporaryHome string, directory string, socket string) (controllerTerminalTransportV1, error) {
	info, err := os.Lstat(temporaryHome)
	if err != nil {
		return nil, fmt.Errorf("inspect controlled-session temporary home: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("controlled-session temporary home must be a real directory, not a symlink")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("controlled-session temporary home ownership is unavailable")
	}
	if identity.Uid != uint32(os.Geteuid()) || identity.Gid != uint32(os.Getegid()) {
		return nil, fmt.Errorf("controlled-session temporary home is owned by %d:%d, expected %d:%d", identity.Uid, identity.Gid, os.Geteuid(), os.Getegid())
	}
	if info.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("controlled-session temporary home mode is %04o, expected 0700", info.Mode().Perm())
	}
	if filepath.Dir(directory) != temporaryHome || filepath.Base(socket) != ControllerTerminalSocketNameV1 || filepath.Dir(socket) != directory {
		return nil, fmt.Errorf("controlled-session terminal path escaped the private temporary home")
	}
	if len(socket) > len(unix.RawSockaddrUnix{}.Path)-1 {
		return nil, fmt.Errorf("controlled-session terminal socket path exceeds the Linux AF_UNIX maximum")
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create controlled-session terminal directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("set controlled-session terminal directory mode: %w", err)
	}
	var listener *net.UnixListener
	cleanup := true
	defer func() {
		if cleanup {
			if listener != nil {
				_ = listener.Close()
			}
			_ = os.Remove(socket)
			_ = os.Remove(directory)
		}
	}()
	listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on controlled-session terminal socket: %w", err)
	}
	listener.SetUnlinkOnClose(true)
	if err := os.Chmod(socket, 0o600); err != nil {
		return nil, fmt.Errorf("set controlled-session terminal socket mode: %w", err)
	}
	cleanup = false
	return &controllerUnixTerminalTransportV1{directory: directory, socket: socket, listener: listener}, nil
}

func (transport *controllerUnixTerminalTransportV1) Accept(ctx context.Context) (net.Conn, error) {
	deadline := time.Time{}
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	if err := transport.listener.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = transport.listener.SetDeadline(time.Now()) })
	connection, err := transport.listener.AcceptUnix()
	stop()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if connection != nil {
			_ = connection.Close()
		}
		return nil, ctxErr
	}
	if err != nil {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				return nil, context.DeadlineExceeded
			}
		}
		return nil, err
	}
	uid, gid, err := unixPeerIdentityV1(connection)
	if err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	if uid != uint32(os.Geteuid()) || gid != uint32(os.Getegid()) {
		return nil, errors.Join(fmt.Errorf("terminal attachment identity is %d:%d, expected %d:%d", uid, gid, os.Geteuid(), os.Getegid()), connection.Close())
	}
	if err := transport.stopAccepting(); err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	return connection, nil
}

func (transport *controllerUnixTerminalTransportV1) stopAccepting() error {
	transport.stopOnce.Do(func() {
		transport.stopErr = errors.Join(transport.listener.Close(), removeIfExistsV1(transport.socket))
	})
	return transport.stopErr
}

func (transport *controllerUnixTerminalTransportV1) Close() error {
	transport.closeOnce.Do(func() {
		transport.closeErr = errors.Join(transport.stopAccepting(), removeIfExistsV1(transport.socket), os.Remove(transport.directory))
	})
	return transport.closeErr
}
