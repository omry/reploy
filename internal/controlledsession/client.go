package controlledsession

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
)

// SessionClientV1 is the controller-side owner of one claimed private session
// connection. It consumes the mandatory opened event during construction and
// then admits one event read and one request write concurrently.
type SessionClientV1 struct {
	connection net.Conn
	opened     OpenedV1
	readMu     sync.Mutex
	writeMu    sync.Mutex
	stateMu    sync.RWMutex
	ready      bool
	terminated bool
	closeOnce  sync.Once
	closeErr   error
}

func newSessionClientV1(ctx context.Context, connection net.Conn) (*SessionClientV1, error) {
	if ctx == nil || ctx.Done() == nil {
		return nil, fmt.Errorf("open controlled-session client: cancelable context is required")
	}
	if connection == nil {
		return nil, fmt.Errorf("open controlled-session client: connection is required")
	}
	var event EventV1
	err := withConnectionDeadlineV1(ctx, connection.SetReadDeadline, func() error {
		var readErr error
		event, readErr = ReadEventV1(connection)
		return readErr
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read controlled-session opened event: %w", err), connection.Close())
	}
	if event.Kind != EventOpenedV1 || event.Opened == nil {
		return nil, errors.Join(fmt.Errorf("controlled-session first event must be opened"), connection.Close())
	}
	opened := *event.Opened
	opened.Authorization = cloneAuthorizationV1(event.Opened.Authorization)
	opened.Endpoints = append([]EndpointV1(nil), event.Opened.Endpoints...)
	return &SessionClientV1{connection: connection, opened: opened}, nil
}

// Opened returns an independent copy of the immutable session authorization
// and coordinates received during the connection claim.
func (client *SessionClientV1) Opened() OpenedV1 {
	opened := client.opened
	opened.Authorization = cloneAuthorizationV1(client.opened.Authorization)
	opened.Endpoints = append([]EndpointV1(nil), client.opened.Endpoints...)
	return opened
}

// Ready reports whether the host has verified workload startup and activated
// the lifecycle. It becomes true only after ReadEvent returns the one ready
// event and never becomes false.
func (client *SessionClientV1) Ready() bool {
	client.stateMu.RLock()
	defer client.stateMu.RUnlock()
	return client.ready
}

func (client *SessionClientV1) ReadEvent(ctx context.Context) (EventV1, error) {
	if ctx == nil || ctx.Done() == nil {
		return EventV1{}, fmt.Errorf("read controlled-session client event: cancelable context is required")
	}
	client.readMu.Lock()
	defer client.readMu.Unlock()
	var event EventV1
	err := withConnectionDeadlineV1(ctx, client.connection.SetReadDeadline, func() error {
		var readErr error
		event, readErr = ReadEventV1(client.connection)
		return readErr
	})
	if err != nil {
		return EventV1{}, errors.Join(fmt.Errorf("read controlled-session client event: %w", err), client.Close())
	}
	if event.Kind == EventOpenedV1 {
		return EventV1{}, errors.Join(fmt.Errorf("read controlled-session client event: opened may appear only once"), client.Close())
	}
	if event.Kind == EventReadyV1 {
		client.stateMu.Lock()
		if client.ready {
			client.stateMu.Unlock()
			return EventV1{}, errors.Join(fmt.Errorf("read controlled-session client event: ready may appear only once"), client.Close())
		}
		client.ready = true
		client.stateMu.Unlock()
	}
	if event.Kind == EventTerminatedV1 {
		client.stateMu.Lock()
		client.terminated = true
		client.stateMu.Unlock()
	}
	return event, nil
}

func (client *SessionClientV1) WriteRequest(ctx context.Context, request RequestV1) error {
	if ctx == nil || ctx.Done() == nil {
		return fmt.Errorf("write controlled-session client request: cancelable context is required")
	}
	if err := ValidateRequestV1(request); err != nil {
		return err
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	client.stateMu.RLock()
	ready := client.ready
	terminated := client.terminated
	client.stateMu.RUnlock()
	if request.Kind == RequestAcknowledgeTerminatedV1 {
		if !terminated {
			return fmt.Errorf("write controlled-session client request: terminal result has not been received")
		}
	} else if terminated {
		return fmt.Errorf("write controlled-session client request: session is terminated")
	}
	if request.Kind != RequestAcknowledgeTerminatedV1 && !ready {
		return fmt.Errorf("write controlled-session client request: session is not ready")
	}
	err := withConnectionDeadlineV1(ctx, client.connection.SetWriteDeadline, func() error {
		return WriteRequestV1(client.connection, request)
	})
	if err != nil {
		return errors.Join(fmt.Errorf("write controlled-session client request: %w", err), client.Close())
	}
	return nil
}

func (client *SessionClientV1) Close() error {
	client.closeOnce.Do(func() {
		client.closeErr = client.connection.Close()
	})
	return client.closeErr
}
