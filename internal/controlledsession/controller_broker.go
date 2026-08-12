package controlledsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const DefaultControllerAttachTimeoutV1 = 10 * time.Second

type ControllerBrokerOptionsV1 struct {
	SessionSocket string
	TemporaryHome string
	Input         io.Reader
	Output        io.Writer
	AttachTimeout time.Duration
}

type controllerBrokerRequestSourceV1 string

const (
	controllerBrokerRequestPublicV1   controllerBrokerRequestSourceV1 = "public"
	controllerBrokerRequestTerminalV1 controllerBrokerRequestSourceV1 = "terminal"
)

type controllerBrokerRequestV1 struct {
	source  controllerBrokerRequestSourceV1
	request RequestV1
}

type controllerBrokerInputErrorV1 struct {
	source controllerBrokerRequestSourceV1
	err    error
}

type controllerBrokerTerminalAcceptResultV1 struct {
	connection *ControllerTerminalConnectionV1
	err        error
}

type controllerBrokerSessionResultV1 struct {
	session *SessionClientV1
	err     error
}

type controllerBrokerTerminalOutputResultV1 struct {
	err error
}

type controllerBrokerStateV1 struct {
	operations            map[OperationV1]bool
	terminalWriteTimeout  time.Duration
	ready                 bool
	workloadExited        bool
	terminating           bool
	outputsFinalized      bool
	terminalEnded         bool
	terminated            bool
	completeSent          bool
	acknowledged          bool
	pendingTerminalInput  *RequestV1
	pendingTerminalResize *RequestV1
}

type controllerBrokerSessionV1 interface {
	Opened() OpenedV1
	ReadEvent(context.Context) (EventV1, error)
	WriteRequest(context.Context, RequestV1) error
	Close() error
}

func RunControllerBrokerV1(ctx context.Context, options ControllerBrokerOptionsV1) (resultErr error) {
	if ctx == nil || ctx.Done() == nil {
		return fmt.Errorf("run controlled-session controller broker: cancelable context is required")
	}
	if !filepath.IsAbs(options.SessionSocket) || filepath.Clean(options.SessionSocket) != options.SessionSocket {
		return fmt.Errorf("run controlled-session controller broker: REPLOY_SESSION_SOCKET must contain an absolute clean path")
	}
	if options.TemporaryHome == "" {
		options.TemporaryHome = ControllerTemporaryHomeV1
	}
	if options.AttachTimeout == 0 {
		options.AttachTimeout = DefaultControllerAttachTimeoutV1
	}
	if options.AttachTimeout < 0 {
		return fmt.Errorf("run controlled-session controller broker: attach timeout must be positive")
	}
	streamReader, err := NewControllerStreamReaderV1(options.Input)
	if err != nil {
		return err
	}
	streamWriter, err := NewControllerStreamWriterV1(options.Output)
	if err != nil {
		return err
	}
	terminalListener, err := PrepareControllerTerminalListenerV1(options.TemporaryHome)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, terminalListener.Close()) }()
	if err := streamWriter.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventBrokerReadyV1, TerminalSocket: terminalListener.SocketPath()}); err != nil {
		return err
	}
	attachCtx, cancelAttach := context.WithTimeout(ctx, options.AttachTimeout)
	defer cancelAttach()
	setupCtx, cancelSetup := context.WithCancel(ctx)
	defer cancelSetup()
	terminalResult := make(chan controllerBrokerTerminalAcceptResultV1)
	go func() {
		terminal, acceptErr := terminalListener.Accept(attachCtx)
		select {
		case terminalResult <- controllerBrokerTerminalAcceptResultV1{connection: terminal, err: acceptErr}:
		case <-setupCtx.Done():
			if terminal != nil {
				_ = terminal.Close()
			}
		}
	}()
	sessionResult := make(chan controllerBrokerSessionResultV1)
	go func() {
		session, dialErr := DialSessionClientV1(setupCtx, options.SessionSocket)
		select {
		case sessionResult <- controllerBrokerSessionResultV1{session: session, err: dialErr}:
		case <-setupCtx.Done():
			if session != nil {
				_ = session.Close()
			}
		}
	}()
	var session *SessionClientV1
	var terminal *ControllerTerminalConnectionV1
	for session == nil || terminal == nil {
		select {
		case <-ctx.Done():
			return failControllerBrokerV1(streamWriter, "broker_canceled", ctx.Err())
		case terminalAccepted := <-terminalResult:
			if terminalAccepted.err != nil {
				code := "terminal_attachment_error"
				if errors.Is(terminalAccepted.err, context.DeadlineExceeded) {
					code = "attach_timeout"
				}
				return failControllerBrokerV1(streamWriter, code, terminalAccepted.err)
			}
			terminal = terminalAccepted.connection
			cancelAttach()
		case sessionOpened := <-sessionResult:
			if sessionOpened.err != nil {
				return failControllerBrokerV1(streamWriter, "host_transport_error", sessionOpened.err)
			}
			session = sessionOpened.session
			defer func() { resultErr = errors.Join(resultErr, session.Close()) }()
			opened := session.Opened()
			publicOpened := &ControllerStreamOpenedV1{
				Operations:                            append([]OperationV1{}, opened.Authorization.Operations...),
				Endpoints:                             append([]EndpointV1{}, opened.Endpoints...),
				Columns:                               opened.Columns,
				Rows:                                  opened.Rows,
				OutputFinalizationTimeoutMilliseconds: opened.OutputFinalizationTimeoutMilliseconds,
			}
			if err := streamWriter.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventOpenedV1, Opened: publicOpened}); err != nil {
				return err
			}
		}
	}
	cancelSetup()
	opened := session.Opened()
	terminalWriteTimeout := time.Duration(opened.OutputFinalizationTimeoutMilliseconds) * time.Millisecond
	return runClaimedControllerBrokerV1(ctx, session, terminal, streamReader, streamWriter, opened.Authorization.Operations, terminalWriteTimeout)
}

func runClaimedControllerBrokerV1(
	ctx context.Context,
	session controllerBrokerSessionV1,
	terminal *ControllerTerminalConnectionV1,
	streamReader *ControllerStreamReaderV1,
	streamWriter *ControllerStreamWriterV1,
	operations []OperationV1,
	terminalWriteTimeout time.Duration,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	requests := make(chan controllerBrokerRequestV1)
	inputErrors := make(chan controllerBrokerInputErrorV1, 2)
	hostEvents := make(chan EventV1)
	hostErrors := make(chan error, 1)
	terminalOutputs := make(chan []byte)
	terminalOutputResults := make(chan controllerBrokerTerminalOutputResultV1)
	go readControllerBrokerPublicRequestsV1(runCtx, streamReader, requests, inputErrors)
	go readControllerBrokerTerminalRequestsV1(runCtx, terminal, requests, inputErrors)
	go readControllerBrokerHostEventsV1(runCtx, session, hostEvents, hostErrors)
	go writeControllerBrokerTerminalOutputV1(runCtx, terminalWriteTimeout, terminal, terminalOutputs, terminalOutputResults)
	state := controllerBrokerStateV1{
		operations:           make(map[OperationV1]bool, len(operations)),
		terminalWriteTimeout: terminalWriteTimeout,
	}
	for _, operation := range operations {
		state.operations[operation] = true
	}
	var pendingOutput []byte
	var outputPending bool
	var outputWrite chan<- []byte
	var outputResult <-chan controllerBrokerTerminalOutputResultV1
	for {
		readHostEvents := hostEvents
		if outputPending {
			readHostEvents = nil
			if outputResult == nil {
				outputWrite = terminalOutputs
			}
		}
		select {
		case <-ctx.Done():
			return failControllerBrokerV1(streamWriter, "broker_canceled", ctx.Err())
		case inputErr := <-inputErrors:
			if inputErr.source == controllerBrokerRequestTerminalV1 && state.terminalEnded && errors.Is(inputErr.err, io.EOF) {
				continue
			}
			if inputErr.source == controllerBrokerRequestPublicV1 && state.acknowledged && errors.Is(inputErr.err, io.EOF) {
				continue
			}
			code := "public_stream_error"
			if inputErr.source == controllerBrokerRequestTerminalV1 {
				code = "terminal_attachment_error"
			}
			return failControllerBrokerV1(streamWriter, code, inputErr.err)
		case request := <-requests:
			if err := state.applyRequest(ctx, session, request); err != nil {
				return failControllerBrokerV1(streamWriter, "request_rejected", err)
			}
		case event := <-readHostEvents:
			if event.Kind == EventOutputV1 {
				if err := state.validateHostOutputEvent(event); err != nil {
					return failControllerBrokerV1(streamWriter, "host_event_error", err)
				}
				pendingOutput = make([]byte, len(event.Bytes))
				copy(pendingOutput, event.Bytes)
				outputPending = true
				continue
			}
			if err := state.applyHostEvent(ctx, session, terminal, streamWriter, event); err != nil {
				return failControllerBrokerV1(streamWriter, "host_event_error", err)
			}
		case hostErr := <-hostErrors:
			if state.acknowledged && errors.Is(hostErr, io.EOF) {
				return nil
			}
			return failControllerBrokerV1(streamWriter, "host_transport_error", hostErr)
		case outputWrite <- pendingOutput:
			outputWrite = nil
			outputResult = terminalOutputResults
		case result := <-outputResult:
			if result.err != nil {
				return failControllerBrokerV1(streamWriter, "terminal_attachment_error", result.err)
			}
			pendingOutput = nil
			outputPending = false
			outputResult = nil
		}
	}
}

func readControllerBrokerPublicRequestsV1(ctx context.Context, reader *ControllerStreamReaderV1, requests chan<- controllerBrokerRequestV1, failures chan<- controllerBrokerInputErrorV1) {
	for {
		message, err := reader.ReadRequest()
		if err != nil {
			select {
			case failures <- controllerBrokerInputErrorV1{source: controllerBrokerRequestPublicV1, err: err}:
			case <-ctx.Done():
			}
			return
		}
		request := RequestV1{Kind: RequestKindV1(message.Kind), Columns: message.Columns, Rows: message.Rows}
		select {
		case requests <- controllerBrokerRequestV1{source: controllerBrokerRequestPublicV1, request: request}:
		case <-ctx.Done():
			return
		}
	}
}

func readControllerBrokerTerminalRequestsV1(ctx context.Context, terminal *ControllerTerminalConnectionV1, requests chan<- controllerBrokerRequestV1, failures chan<- controllerBrokerInputErrorV1) {
	for {
		request, err := terminal.ReadRequest(ctx)
		if err != nil {
			select {
			case failures <- controllerBrokerInputErrorV1{source: controllerBrokerRequestTerminalV1, err: err}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case requests <- controllerBrokerRequestV1{source: controllerBrokerRequestTerminalV1, request: request}:
		case <-ctx.Done():
			return
		}
	}
}

func readControllerBrokerHostEventsV1(ctx context.Context, session controllerBrokerSessionV1, events chan<- EventV1, failures chan<- error) {
	for {
		event, err := session.ReadEvent(ctx)
		if err != nil {
			select {
			case failures <- err:
			case <-ctx.Done():
			}
			return
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return
		}
	}
}

func writeControllerBrokerTerminalOutputV1(ctx context.Context, timeout time.Duration, terminal *ControllerTerminalConnectionV1, outputs <-chan []byte, results chan<- controllerBrokerTerminalOutputResultV1) {
	for {
		select {
		case output := <-outputs:
			writeCtx, cancelWrite := context.WithTimeout(ctx, timeout)
			err := terminal.WriteOutput(writeCtx, output)
			cancelWrite()
			select {
			case results <- controllerBrokerTerminalOutputResultV1{err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (state *controllerBrokerStateV1) applyRequest(ctx context.Context, session controllerBrokerSessionV1, input controllerBrokerRequestV1) error {
	request := input.request
	if input.source == controllerBrokerRequestPublicV1 && request.Kind == RequestInputV1 {
		return fmt.Errorf("public controller stream cannot carry terminal input")
	}
	if input.source == controllerBrokerRequestTerminalV1 && request.Kind != RequestInputV1 && request.Kind != RequestResizeV1 {
		return fmt.Errorf("terminal attachment cannot carry %q", request.Kind)
	}
	if input.source == controllerBrokerRequestTerminalV1 && (state.terminating || state.terminated) {
		return nil
	}
	if request.Kind == RequestAcknowledgeTerminatedV1 {
		if !state.terminated || state.acknowledged {
			return fmt.Errorf("acknowledge-terminated is valid exactly once after terminated")
		}
		if err := session.WriteRequest(ctx, request); err != nil {
			return err
		}
		state.acknowledged = true
		return nil
	}
	if !state.ready {
		if input.source == controllerBrokerRequestTerminalV1 {
			operation := operationForRequestV1(request.Kind)
			if operation == "" || !state.operations[operation] {
				return fmt.Errorf("operation %q was not granted", operation)
			}
			switch request.Kind {
			case RequestInputV1:
				pendingLength := len(request.Bytes)
				if state.pendingTerminalInput != nil {
					pendingLength += len(state.pendingTerminalInput.Bytes)
				}
				if pendingLength > MaxFramePayloadV1 {
					return fmt.Errorf("terminal input buffered before ready exceeds %d bytes", MaxFramePayloadV1)
				}
				if state.pendingTerminalInput == nil {
					state.pendingTerminalInput = &RequestV1{Kind: RequestInputV1, Bytes: make([]byte, 0, pendingLength)}
				}
				state.pendingTerminalInput.Bytes = append(state.pendingTerminalInput.Bytes, request.Bytes...)
				return nil
			case RequestResizeV1:
				pending := request
				state.pendingTerminalResize = &pending
				return nil
			}
		}
		return fmt.Errorf("%s is premature before ready", request.Kind)
	}
	if state.terminated {
		return fmt.Errorf("%s is invalid after terminated", request.Kind)
	}
	operation := operationForRequestV1(request.Kind)
	if operation == "" || !state.operations[operation] {
		return fmt.Errorf("operation %q was not granted", operation)
	}
	switch request.Kind {
	case RequestInputV1, RequestResizeV1:
		if state.terminating {
			return fmt.Errorf("%s is invalid while terminating", request.Kind)
		}
	case RequestTerminateV1:
		// Repeated terminate requests remain idempotent at the host lifecycle.
	case RequestCompleteV1:
		if !state.terminating || !state.outputsFinalized || state.completeSent {
			return fmt.Errorf("complete requires finalized workload output and may be sent once")
		}
	default:
		return fmt.Errorf("request %q is unsupported", request.Kind)
	}
	if err := session.WriteRequest(ctx, request); err != nil {
		return err
	}
	if request.Kind == RequestCompleteV1 {
		state.completeSent = true
	}
	return nil
}

func (state *controllerBrokerStateV1) applyHostEvent(ctx context.Context, session controllerBrokerSessionV1, terminal *ControllerTerminalConnectionV1, writer *ControllerStreamWriterV1, event EventV1) error {
	switch event.Kind {
	case EventReadyV1:
		if state.ready || state.terminating || state.terminated {
			return fmt.Errorf("ready event is out of order")
		}
		if state.pendingTerminalResize != nil {
			if err := session.WriteRequest(ctx, *state.pendingTerminalResize); err != nil {
				return err
			}
			state.pendingTerminalResize = nil
		}
		state.ready = true
		if err := writer.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventReadyV1}); err != nil {
			return err
		}
		if state.pendingTerminalInput != nil {
			if err := session.WriteRequest(ctx, *state.pendingTerminalInput); err != nil {
				return err
			}
			state.pendingTerminalInput = nil
		}
		return nil
	case EventOutputV1:
		if err := state.validateHostOutputEvent(event); err != nil {
			return err
		}
		return terminal.WriteOutput(ctx, event.Bytes)
	case EventWorkloadExitV1:
		if state.workloadExited || state.outputsFinalized || state.terminated || event.WorkloadExit == nil {
			return fmt.Errorf("workload-exit event is out of order")
		}
		state.workloadExited = true
		return writer.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventWorkloadExitV1, WorkloadExit: event.WorkloadExit})
	case EventTerminatingV1:
		if state.terminating || state.terminated || event.Terminating == nil {
			return fmt.Errorf("terminating event is out of order")
		}
		state.terminating = true
		return writer.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventTerminatingV1, Terminating: event.Terminating})
	case EventDiagnosticV1:
		if state.terminated || event.Diagnostic == nil {
			return fmt.Errorf("diagnostic event is out of order")
		}
		return writer.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventDiagnosticV1, Diagnostic: event.Diagnostic})
	case EventWorkloadOutputsFinalizedV1:
		if !state.terminating || state.outputsFinalized || state.terminated || event.WorkloadOutputsFinalized == nil {
			return fmt.Errorf("workload-outputs-finalized event is out of order")
		}
		if state.ready && !state.workloadExited && event.WorkloadOutputsFinalized.Status == WorkloadOutputFinalizationDrainedV1 {
			return fmt.Errorf("drained workload output requires a prior workload-exit event")
		}
		if err := writer.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: event.WorkloadOutputsFinalized}); err != nil {
			return err
		}
		status := WorkloadOutputFinalizationStatusV1{Kind: event.WorkloadOutputsFinalized.Status, Reason: event.WorkloadOutputsFinalized.Reason}
		if err := state.writeTerminalEnd(ctx, terminal, status); err != nil {
			return err
		}
		state.outputsFinalized = true
		state.terminalEnded = true
		return nil
	case EventTerminatedV1:
		if state.terminated || event.Terminated == nil || state.ready && !state.terminating {
			return fmt.Errorf("terminated event is out of order")
		}
		if !state.terminalEnded {
			if state.ready {
				return fmt.Errorf("terminated event arrived before terminal output ended")
			}
			if err := state.writeTerminalEnd(ctx, terminal, event.Terminated.WorkloadOutputFinalizationStatus); err != nil {
				return err
			}
			state.terminalEnded = true
			state.outputsFinalized = true
		}
		if err := writer.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventTerminatedV1, Terminated: event.Terminated}); err != nil {
			return err
		}
		state.terminated = true
		return nil
	case EventOpenedV1:
		return fmt.Errorf("opened event may appear only once")
	default:
		return fmt.Errorf("host event kind %q is unsupported", event.Kind)
	}
}

func (state *controllerBrokerStateV1) writeTerminalEnd(ctx context.Context, terminal *ControllerTerminalConnectionV1, status WorkloadOutputFinalizationStatusV1) error {
	writeCtx, cancelWrite := context.WithTimeout(ctx, state.terminalWriteTimeout)
	defer cancelWrite()
	return terminal.WriteEnd(writeCtx, status)
}

func (state *controllerBrokerStateV1) validateHostOutputEvent(event EventV1) error {
	if state.outputsFinalized || state.terminated || event.Bytes == nil {
		return fmt.Errorf("output event is out of order")
	}
	return nil
}

func failControllerBrokerV1(writer *ControllerStreamWriterV1, code string, cause error) error {
	message := controllerBrokerPublicErrorMessageV1(cause)
	_ = writer.WriteEvent(ControllerStreamEventV1{
		Kind:        ControllerStreamEventClientErrorV1,
		ClientError: &DiagnosticV1{Code: code, Message: message},
	})
	return cause
}

func controllerBrokerPublicErrorMessageV1(err error) string {
	message := strings.TrimSpace(err.Error())
	message = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return ' '
		}
		return character
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		message = "controlled-session client failed"
	}
	const limit = 512
	if len(message) > limit {
		for len(message) > limit {
			_, size := utf8.DecodeLastRuneInString(message)
			message = message[:len(message)-size]
		}
	}
	return message
}
