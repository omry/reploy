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

type privateUnixChannelTransportV1 struct {
	directory string
	socket    string
	listener  *net.UnixListener
	stopOnce  sync.Once
	stopErr   error
	closeOnce sync.Once
	closeErr  error
}

func preparePrivateChannelTransportV1(directory string, uid uint32, gid uint32) (result privateChannelTransportV1, resultErr error) {
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		return nil, fmt.Errorf("create channel parent directory: %w", err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create fresh channel directory: %w", err)
	}
	socket := filepath.Join(directory, PrivateChannelSocketNameV1)
	var listener *net.UnixListener
	cleanupRequired := true
	defer func() {
		if cleanupRequired {
			if cleanupErr := cleanupPrivateChannelPreparationV1(listener, socket, directory); cleanupErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("clean up failed channel preparation: %w", cleanupErr))
			}
		}
	}()
	if err := os.Chown(directory, int(uid), int(gid)); err != nil {
		return nil, fmt.Errorf("set channel directory ownership to %d:%d: %w", uid, gid, err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("set channel directory mode: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", socket, err)
	}
	listener.SetUnlinkOnClose(true)
	transport := &privateUnixChannelTransportV1{directory: directory, socket: socket, listener: listener}
	if err := os.Chown(socket, int(uid), int(gid)); err != nil {
		return nil, fmt.Errorf("set channel socket ownership to %d:%d: %w", uid, gid, err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		return nil, fmt.Errorf("set channel socket mode: %w", err)
	}
	cleanupRequired = false
	return transport, nil
}

func cleanupPrivateChannelPreparationV1(listener *net.UnixListener, socket string, directory string) error {
	var listenerErr error
	if listener != nil {
		listenerErr = listener.Close()
	}
	return errors.Join(listenerErr, removeIfExistsV1(socket), os.Remove(directory))
}

func (transport *privateUnixChannelTransportV1) Accept(ctx context.Context) (net.Conn, uint32, uint32, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, 0, err
	}
	deadline := time.Time{}
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	if err := transport.listener.SetDeadline(deadline); err != nil {
		return nil, 0, 0, err
	}
	cancellationFinished := make(chan struct{})
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = transport.listener.SetDeadline(time.Now())
		close(cancellationFinished)
	})
	connection, err := transport.listener.AcceptUnix()
	if !stopCancellation() {
		<-cancellationFinished
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if connection != nil {
			if closeErr := connection.Close(); closeErr != nil {
				ctxErr = errors.Join(ctxErr, fmt.Errorf("close controller connection canceled before claim: %w", closeErr))
			}
		}
		return nil, 0, 0, ctxErr
	}
	if err != nil {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				return nil, 0, 0, context.DeadlineExceeded
			}
		}
		return nil, 0, 0, err
	}
	uid, gid, err := unixPeerIdentityV1(connection)
	if err != nil {
		if closeErr := connection.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close unverified controller connection: %w", closeErr))
		}
		return nil, 0, 0, err
	}
	return connection, uid, gid, nil
}

func (transport *privateUnixChannelTransportV1) StopAccepting() error {
	transport.stopOnce.Do(func() {
		transport.stopErr = errors.Join(
			transport.listener.Close(),
			removeIfExistsV1(transport.socket),
		)
	})
	return transport.stopErr
}

func (transport *privateUnixChannelTransportV1) Close() error {
	transport.closeOnce.Do(func() {
		transport.closeErr = errors.Join(
			transport.StopAccepting(),
			removeIfExistsV1(transport.socket),
			os.Remove(transport.directory),
		)
	})
	return transport.closeErr
}

func unixPeerIdentityV1(connection *net.UnixConn) (uint32, uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, 0, fmt.Errorf("inspect controller peer connection: %w", err)
	}
	var credentials *unix.Ucred
	var socketErr error
	if err := raw.Control(func(descriptor uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(descriptor), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, 0, fmt.Errorf("inspect controller peer descriptor: %w", err)
	}
	if socketErr != nil {
		return 0, 0, fmt.Errorf("inspect controller peer credentials: %w", socketErr)
	}
	return credentials.Uid, credentials.Gid, nil
}

func removeIfExistsV1(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func isPlatformControllerDisconnectV1(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED)
}
