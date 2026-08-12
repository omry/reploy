package controlledsession

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
)

const (
	ControllerTemporaryHomeV1      = "/mnt/reploy-home"
	ControllerTerminalSocketNameV1 = "terminal.sock"
)

type controllerTerminalTransportV1 interface {
	Accept(context.Context) (net.Conn, error)
	Close() error
}

type ControllerTerminalListenerV1 struct {
	directory string
	socket    string
	transport controllerTerminalTransportV1

	mu         sync.Mutex
	acceptDone bool
	connection *ControllerTerminalConnectionV1
	closeOnce  sync.Once
	closeErr   error
}

type ControllerTerminalConnectionV1 struct {
	connection net.Conn
	readMu     sync.Mutex
	writeMu    sync.Mutex
	closeOnce  sync.Once
	closeErr   error
}

func PrepareControllerTerminalListenerV1(temporaryHome string) (*ControllerTerminalListenerV1, error) {
	if !filepath.IsAbs(temporaryHome) || filepath.Clean(temporaryHome) != temporaryHome {
		return nil, fmt.Errorf("prepare controlled-session terminal listener requires an absolute clean temporary home")
	}
	var random [16]byte
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return nil, fmt.Errorf("prepare controlled-session terminal listener randomness: %w", err)
	}
	directory := filepath.Join(temporaryHome, fmt.Sprintf("reploy-controlled-session-%x", random))
	socket := filepath.Join(directory, ControllerTerminalSocketNameV1)
	transport, err := prepareControllerTerminalTransportV1(temporaryHome, directory, socket)
	if err != nil {
		return nil, err
	}
	return &ControllerTerminalListenerV1{directory: directory, socket: socket, transport: transport}, nil
}

func (listener *ControllerTerminalListenerV1) SocketPath() string { return listener.socket }

func (listener *ControllerTerminalListenerV1) Accept(ctx context.Context) (*ControllerTerminalConnectionV1, error) {
	if ctx == nil || ctx.Done() == nil {
		return nil, fmt.Errorf("accept controlled-session terminal attachment: cancelable context is required")
	}
	listener.mu.Lock()
	if listener.acceptDone {
		listener.mu.Unlock()
		return nil, fmt.Errorf("accept controlled-session terminal attachment: listener has already accepted or failed")
	}
	listener.acceptDone = true
	listener.mu.Unlock()
	connection, err := listener.transport.Accept(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("accept controlled-session terminal attachment: %w", err), listener.Close())
	}
	terminal := &ControllerTerminalConnectionV1{connection: connection}
	listener.mu.Lock()
	listener.connection = terminal
	listener.mu.Unlock()
	return terminal, nil
}

func (listener *ControllerTerminalListenerV1) Close() error {
	listener.closeOnce.Do(func() {
		listener.mu.Lock()
		connection := listener.connection
		listener.mu.Unlock()
		var connectionErr error
		if connection != nil {
			connectionErr = connection.Close()
		}
		listener.closeErr = errors.Join(connectionErr, listener.transport.Close())
	})
	return listener.closeErr
}

func (connection *ControllerTerminalConnectionV1) ReadRequest(ctx context.Context) (RequestV1, error) {
	if ctx == nil || ctx.Done() == nil {
		return RequestV1{}, fmt.Errorf("read controlled-session terminal request: cancelable context is required")
	}
	connection.readMu.Lock()
	defer connection.readMu.Unlock()
	var request RequestV1
	err := withConnectionDeadlineV1(ctx, connection.connection.SetReadDeadline, func() error {
		var readErr error
		request, readErr = ReadTerminalRequestV1(connection.connection)
		return readErr
	})
	if err != nil {
		return RequestV1{}, fmt.Errorf("read controlled-session terminal request: %w", err)
	}
	return request, nil
}

func (connection *ControllerTerminalConnectionV1) WriteOutput(ctx context.Context, content []byte) error {
	if content == nil {
		return fmt.Errorf("write controlled-session terminal output: byte sequence is required")
	}
	return connection.write(ctx, func() error { return WriteTerminalOutputV1(connection.connection, content) })
}

func (connection *ControllerTerminalConnectionV1) WriteEnd(ctx context.Context, status WorkloadOutputFinalizationStatusV1) error {
	return connection.write(ctx, func() error { return WriteTerminalEndV1(connection.connection, status) })
}

func (connection *ControllerTerminalConnectionV1) write(ctx context.Context, write func() error) error {
	if ctx == nil || ctx.Done() == nil {
		return fmt.Errorf("write controlled-session terminal event: cancelable context is required")
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if err := withConnectionDeadlineV1(ctx, connection.connection.SetWriteDeadline, write); err != nil {
		return errors.Join(fmt.Errorf("write controlled-session terminal event: %w", err), connection.Close())
	}
	return nil
}

func (connection *ControllerTerminalConnectionV1) Close() error {
	connection.closeOnce.Do(func() { connection.closeErr = connection.connection.Close() })
	return connection.closeErr
}
