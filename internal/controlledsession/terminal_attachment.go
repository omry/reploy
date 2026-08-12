package controlledsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

const terminalAttachmentInputBufferSizeV1 = 32 * 1024

type TerminalAttachmentOptionsV1 struct {
	SocketPath string
	Input      io.Reader
	Output     io.Writer
}

type terminalAttachmentConnectionV1 struct {
	connection net.Conn
	writeMu    sync.Mutex
	closeOnce  sync.Once
	closeErr   error
}

type terminalAttachmentResizeV1 struct {
	request RequestV1
	err     error
}

type terminalAttachmentTTYV1 struct {
	initialResize *RequestV1
	resizes       <-chan terminalAttachmentResizeV1
	restore       func() error
}

type terminalAttachmentEventResultV1 struct {
	event TerminalEventV1
	err   error
}

func runTerminalAttachmentV1(
	ctx context.Context,
	connection *terminalAttachmentConnectionV1,
	input io.Reader,
	output io.Writer,
	tty terminalAttachmentTTYV1,
) (resultErr error) {
	if ctx == nil || ctx.Done() == nil {
		return fmt.Errorf("run controlled-session terminal attachment: cancelable context is required")
	}
	if connection == nil || connection.connection == nil {
		return fmt.Errorf("run controlled-session terminal attachment: connection is required")
	}
	if input == nil || output == nil {
		return fmt.Errorf("run controlled-session terminal attachment: input and output are required")
	}
	if tty.restore == nil {
		tty.restore = func() error { return nil }
	}
	defer func() { resultErr = errors.Join(resultErr, tty.restore(), connection.Close()) }()

	if tty.initialResize != nil {
		if err := connection.WriteRequest(ctx, *tty.initialResize); err != nil {
			return fmt.Errorf("send controlled-session initial terminal dimensions: %w", err)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan terminalAttachmentEventResultV1, 1)
	go readTerminalAttachmentEventsV1(runCtx, connection, events)
	inputResult := make(chan error, 1)
	go func() { inputResult <- forwardTerminalAttachmentInputV1(runCtx, connection, input) }()

	resizes := tty.resizes
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-inputResult:
			inputResult = nil
			if err != nil {
				return err
			}
		case resized, ok := <-resizes:
			if !ok {
				resizes = nil
				continue
			}
			if resized.err != nil {
				return fmt.Errorf("read controlled-session terminal dimensions: %w", resized.err)
			}
			if err := connection.WriteRequest(ctx, resized.request); err != nil {
				return fmt.Errorf("send controlled-session terminal resize: %w", err)
			}
		case result := <-events:
			if result.err != nil {
				return result.err
			}
			switch result.event.Kind {
			case TerminalEventOutputV1:
				if err := writeAllV1(output, result.event.Bytes); err != nil {
					return fmt.Errorf("write controlled-session terminal output: %w", err)
				}
			case TerminalEventEndV1:
				if result.event.Status == nil {
					return fmt.Errorf("read controlled-session terminal end: status is missing")
				}
				if result.event.Status.Kind == WorkloadOutputFinalizationFailedV1 {
					return fmt.Errorf("controlled-session terminal output finalization failed: %s", result.event.Status.Reason)
				}
				return nil
			default:
				return fmt.Errorf("read controlled-session terminal event: unsupported kind %q", result.event.Kind)
			}
		}
	}
}

func readTerminalAttachmentEventsV1(ctx context.Context, connection *terminalAttachmentConnectionV1, results chan<- terminalAttachmentEventResultV1) {
	for {
		event, err := connection.ReadEvent(ctx)
		select {
		case results <- terminalAttachmentEventResultV1{event: event, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil || event.Kind == TerminalEventEndV1 {
			return
		}
	}
}

func forwardTerminalAttachmentInputV1(ctx context.Context, connection *terminalAttachmentConnectionV1, input io.Reader) error {
	buffer := make([]byte, terminalAttachmentInputBufferSizeV1)
	for {
		read, err := input.Read(buffer)
		if read > 0 {
			if writeErr := connection.WriteRequest(ctx, RequestV1{Kind: RequestInputV1, Bytes: buffer[:read]}); writeErr != nil {
				return fmt.Errorf("send controlled-session terminal input: %w", writeErr)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read controlled-session terminal input: %w", err)
		}
		if read == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return fmt.Errorf("read controlled-session terminal input: %w", io.ErrNoProgress)
			}
		}
	}
}

func (connection *terminalAttachmentConnectionV1) ReadEvent(ctx context.Context) (TerminalEventV1, error) {
	var event TerminalEventV1
	err := withConnectionDeadlineV1(ctx, connection.connection.SetReadDeadline, func() error {
		var readErr error
		event, readErr = ReadTerminalEventV1(connection.connection)
		return readErr
	})
	if err != nil {
		return TerminalEventV1{}, fmt.Errorf("read controlled-session terminal event: %w", err)
	}
	return event, nil
}

func (connection *terminalAttachmentConnectionV1) WriteRequest(ctx context.Context, request RequestV1) error {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if err := withConnectionDeadlineV1(ctx, connection.connection.SetWriteDeadline, func() error {
		return WriteTerminalRequestV1(connection.connection, request)
	}); err != nil {
		return fmt.Errorf("write controlled-session terminal request: %w", err)
	}
	return nil
}

func (connection *terminalAttachmentConnectionV1) Close() error {
	connection.closeOnce.Do(func() { connection.closeErr = connection.connection.Close() })
	return connection.closeErr
}
