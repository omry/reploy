package controlledsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const PrivateChannelSocketNameV1 = "control.sock"

var ErrControllerDisconnectedV1 = errors.New("controlled-session controller disconnected")

type ChannelClaimPhaseV1 string

const (
	ChannelBeforeClaimV1 ChannelClaimPhaseV1 = "before-claim"
	ChannelAfterClaimV1  ChannelClaimPhaseV1 = "after-claim"
)

// PrivateChannelConfigV1 contains the host-owned facts needed to expose one
// lease-private controller channel. HostDirectory must not already exist.
type PrivateChannelConfigV1 struct {
	HostDirectory string
	Opened        OpenedV1
}

// ChannelClaimErrorV1 distinguishes failures before an authorized controller
// owns the channel from failures after ownership became authoritative.
type ChannelClaimErrorV1 struct {
	Phase ChannelClaimPhaseV1
	Err   error
}

func (err *ChannelClaimErrorV1) Error() string {
	return fmt.Sprintf("controlled-session channel failed %s: %v", err.Phase, err.Err)
}

func (err *ChannelClaimErrorV1) Unwrap() error { return err.Err }

func ClaimFailurePhaseV1(err error) (ChannelClaimPhaseV1, bool) {
	var claimError *ChannelClaimErrorV1
	if !errors.As(err, &claimError) {
		return "", false
	}
	return claimError.Phase, true
}

type privateChannelTransportV1 interface {
	Accept(context.Context) (net.Conn, uint32, uint32, error)
	StopAccepting() error
	Close() error
}

// PrivateChannelV1 owns the listener, claimed connection, and lease-private
// directory for one controlled session.
type PrivateChannelV1 struct {
	hostDirectory string
	opened        EventV1
	expectedUID   uint32
	expectedGID   uint32
	transport     privateChannelTransportV1

	mu         sync.Mutex
	claimBegun bool
	connection *ControllerConnectionV1
	closed     bool
	closeOnce  sync.Once
	closeErr   error
}

// ControllerConnectionV1 is the sole claimed controller connection. Reads and
// writes each admit one frame at a time, so no unbounded in-memory queue forms.
type ControllerConnectionV1 struct {
	connection net.Conn
	readMu     sync.Mutex
	writeMu    sync.Mutex
	closeOnce  sync.Once
	closeErr   error
}

func PreparePrivateChannelV1(config PrivateChannelConfigV1) (*PrivateChannelV1, error) {
	if !filepath.IsAbs(config.HostDirectory) || filepath.Clean(config.HostDirectory) != config.HostDirectory {
		return nil, fmt.Errorf("prepare controlled-session channel requires an absolute clean host directory")
	}
	if err := ValidateAuthorizationV1(config.Opened.Authorization); err != nil {
		return nil, fmt.Errorf("prepare controlled-session channel authorization: %w", err)
	}
	openedPayload := config.Opened
	openedPayload.Authorization = cloneAuthorizationV1(config.Opened.Authorization)
	opened := EventV1{Kind: EventOpenedV1, Opened: &openedPayload}
	if err := ValidateEventV1(opened); err != nil {
		return nil, fmt.Errorf("prepare controlled-session channel opened event: %w", err)
	}
	if err := WriteEventV1(io.Discard, opened); err != nil {
		return nil, fmt.Errorf("prepare controlled-session channel opened frame: %w", err)
	}
	controllerIdentity := openedPayload.Authorization.Controller.RuntimeIdentity
	uid, err := strconv.ParseUint(controllerIdentity.UID, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("prepare controlled-session channel controller UID: %w", err)
	}
	gid, err := strconv.ParseUint(controllerIdentity.GID, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("prepare controlled-session channel controller GID: %w", err)
	}
	transport, err := preparePrivateChannelTransportV1(config.HostDirectory, uint32(uid), uint32(gid))
	if err != nil {
		return nil, fmt.Errorf("prepare controlled-session channel transport: %w", err)
	}
	return &PrivateChannelV1{
		hostDirectory: config.HostDirectory,
		opened:        opened,
		expectedUID:   uint32(uid),
		expectedGID:   uint32(gid),
		transport:     transport,
	}, nil
}

func (channel *PrivateChannelV1) SocketPath() string {
	return filepath.Join(channel.hostDirectory, PrivateChannelSocketNameV1)
}

// Claim accepts the only controller connection, verifies its kernel-reported
// identity, removes the listener pathname, and sends opened as the first event.
// A failed claim is terminal; protocol v1 does not reconnect or transfer
// ownership.
func (channel *PrivateChannelV1) Claim(ctx context.Context) (*ControllerConnectionV1, error) {
	if ctx == nil || ctx.Done() == nil {
		return nil, &ChannelClaimErrorV1{Phase: ChannelBeforeClaimV1, Err: fmt.Errorf("cancelable claim context is required")}
	}
	channel.mu.Lock()
	if channel.closed {
		channel.mu.Unlock()
		return nil, &ChannelClaimErrorV1{Phase: ChannelBeforeClaimV1, Err: fmt.Errorf("channel is closed")}
	}
	if channel.claimBegun {
		channel.mu.Unlock()
		return nil, &ChannelClaimErrorV1{Phase: ChannelBeforeClaimV1, Err: fmt.Errorf("channel has already been claimed")}
	}
	channel.claimBegun = true
	channel.mu.Unlock()

	connection, uid, gid, err := channel.transport.Accept(ctx)
	if err != nil {
		return nil, channel.claimFailureV1(ChannelBeforeClaimV1, err, channel.Close())
	}
	if uid != channel.expectedUID || gid != channel.expectedGID {
		cause := fmt.Errorf("controller peer identity is %d:%d, expected %d:%d", uid, gid, channel.expectedUID, channel.expectedGID)
		return nil, channel.claimFailureV1(ChannelBeforeClaimV1, cause, errors.Join(connection.Close(), channel.Close()))
	}
	if err := channel.transport.StopAccepting(); err != nil {
		cause := fmt.Errorf("retire claimed listener: %w", err)
		return nil, channel.claimFailureV1(ChannelBeforeClaimV1, cause, errors.Join(connection.Close(), channel.Close()))
	}

	claimed := &ControllerConnectionV1{connection: connection}
	channel.mu.Lock()
	if channel.closed {
		channel.mu.Unlock()
		return nil, channel.claimFailureV1(
			ChannelAfterClaimV1,
			fmt.Errorf("channel closed while controller claimed it"),
			errors.Join(claimed.Close(), channel.Close()),
		)
	}
	channel.connection = claimed
	channel.mu.Unlock()
	if err := claimed.WriteEvent(ctx, channel.opened); err != nil {
		return nil, channel.claimFailureV1(ChannelAfterClaimV1, fmt.Errorf("send opened event: %w", err), channel.Close())
	}
	return claimed, nil
}

func (channel *PrivateChannelV1) claimFailureV1(phase ChannelClaimPhaseV1, cause error, cleanup error) error {
	if cleanup != nil {
		cause = errors.Join(cause, fmt.Errorf("clean up controlled-session channel: %w", cleanup))
	}
	return &ChannelClaimErrorV1{Phase: phase, Err: cause}
}

func (channel *PrivateChannelV1) Close() error {
	channel.closeOnce.Do(func() {
		channel.mu.Lock()
		channel.closed = true
		connection := channel.connection
		channel.mu.Unlock()

		var connectionErr error
		if connection != nil {
			connectionErr = connection.Close()
		}
		channel.closeErr = errors.Join(connectionErr, channel.transport.Close())
	})
	return channel.closeErr
}

func (connection *ControllerConnectionV1) ReadRequest(ctx context.Context) (RequestV1, error) {
	if ctx == nil || ctx.Done() == nil {
		return RequestV1{}, fmt.Errorf("read controlled-session controller request: cancelable context is required")
	}
	connection.readMu.Lock()
	defer connection.readMu.Unlock()
	var request RequestV1
	err := withConnectionDeadlineV1(ctx, connection.connection.SetReadDeadline, func() error {
		var readErr error
		request, readErr = ReadRequestV1(connection.connection)
		return readErr
	})
	if err != nil {
		if isControllerDisconnectV1(err) {
			return RequestV1{}, fmt.Errorf("%w while reading request: %v", ErrControllerDisconnectedV1, err)
		}
		return RequestV1{}, fmt.Errorf("read controlled-session controller request: %w", err)
	}
	return request, nil
}

func (connection *ControllerConnectionV1) WriteEvent(ctx context.Context, event EventV1) error {
	if ctx == nil || ctx.Done() == nil {
		return fmt.Errorf("write controlled-session event: cancelable context is required")
	}
	if err := ValidateEventV1(event); err != nil {
		return err
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	err := withConnectionDeadlineV1(ctx, connection.connection.SetWriteDeadline, func() error {
		return WriteEventV1(connection.connection, event)
	})
	if err != nil {
		if isControllerDisconnectV1(err) {
			return fmt.Errorf("%w while writing event: %v", ErrControllerDisconnectedV1, err)
		}
		return fmt.Errorf("write controlled-session event: %w", err)
	}
	return nil
}

func (connection *ControllerConnectionV1) Close() error {
	connection.closeOnce.Do(func() {
		connection.closeErr = connection.connection.Close()
	})
	return connection.closeErr
}

func withConnectionDeadlineV1(ctx context.Context, setDeadline func(time.Time) error, operation func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Time{}
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	if err := setDeadline(deadline); err != nil {
		return err
	}
	cancellationFinished := make(chan struct{})
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(cancellationFinished)
	})
	err := operation()
	if !stopCancellation() {
		<-cancellationFinished
	}
	resetErr := setDeadline(time.Time{})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil && !deadline.IsZero() && !time.Now().Before(deadline) {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return context.DeadlineExceeded
		}
	}
	return errors.Join(err, resetErr)
}

func isControllerDisconnectV1(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) || isPlatformControllerDisconnectV1(err)
}
