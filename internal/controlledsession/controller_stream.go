package controlledsession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"
)

const (
	ControllerStreamSchemaV1  = "reploy-controlled-session-client-v1"
	MaxControllerStreamLineV1 = 1 << 20
)

type ControllerStreamRequestKindV1 string

const (
	ControllerStreamRequestResizeV1                ControllerStreamRequestKindV1 = "resize"
	ControllerStreamRequestTerminateV1             ControllerStreamRequestKindV1 = "terminate"
	ControllerStreamRequestCompleteV1              ControllerStreamRequestKindV1 = "complete"
	ControllerStreamRequestAcknowledgeTerminatedV1 ControllerStreamRequestKindV1 = "acknowledge-terminated"
)

type ControllerStreamRequestV1 struct {
	Kind    ControllerStreamRequestKindV1
	Columns uint32
	Rows    uint32
}

type ControllerStreamEventKindV1 string

const (
	ControllerStreamEventBrokerReadyV1              ControllerStreamEventKindV1 = "broker-ready"
	ControllerStreamEventOpenedV1                   ControllerStreamEventKindV1 = "opened"
	ControllerStreamEventReadyV1                    ControllerStreamEventKindV1 = "ready"
	ControllerStreamEventWorkloadExitV1             ControllerStreamEventKindV1 = "workload-exit"
	ControllerStreamEventTerminatingV1              ControllerStreamEventKindV1 = "terminating"
	ControllerStreamEventDiagnosticV1               ControllerStreamEventKindV1 = "diagnostic"
	ControllerStreamEventWorkloadOutputsFinalizedV1 ControllerStreamEventKindV1 = "workload-outputs-finalized"
	ControllerStreamEventTerminatedV1               ControllerStreamEventKindV1 = "terminated"
	ControllerStreamEventClientErrorV1              ControllerStreamEventKindV1 = "client-error"
)

type ControllerStreamOpenedV1 struct {
	Operations                            []OperationV1 `json:"operations"`
	Endpoints                             []EndpointV1  `json:"endpoints"`
	Columns                               uint32        `json:"columns"`
	Rows                                  uint32        `json:"rows"`
	OutputFinalizationTimeoutMilliseconds uint32        `json:"output_finalization_timeout_milliseconds"`
}

type ControllerStreamEventV1 struct {
	Kind                     ControllerStreamEventKindV1
	TerminalSocket           string
	Opened                   *ControllerStreamOpenedV1
	WorkloadExit             *WorkloadExitV1
	Terminating              *TerminatingV1
	Diagnostic               *DiagnosticV1
	WorkloadOutputsFinalized *WorkloadOutputsFinalizedV1
	Terminated               *ResultV1
	ClientError              *DiagnosticV1
}

type ControllerStreamReaderV1 struct {
	reader *bufio.Reader
}

type ControllerStreamWriterV1 struct {
	writer io.Writer
	mu     sync.Mutex
}

func NewControllerStreamReaderV1(reader io.Reader) (*ControllerStreamReaderV1, error) {
	if reader == nil {
		return nil, fmt.Errorf("create controlled-session controller stream reader: input is required")
	}
	return &ControllerStreamReaderV1{reader: bufio.NewReaderSize(reader, MaxControllerStreamLineV1+1)}, nil
}

func NewControllerStreamWriterV1(writer io.Writer) (*ControllerStreamWriterV1, error) {
	if writer == nil {
		return nil, fmt.Errorf("create controlled-session controller stream writer: output is required")
	}
	return &ControllerStreamWriterV1{writer: writer}, nil
}

func (reader *ControllerStreamReaderV1) ReadRequest() (ControllerStreamRequestV1, error) {
	line, err := reader.reader.ReadSlice('\n')
	if err != nil {
		if err == bufio.ErrBufferFull || len(line) > MaxControllerStreamLineV1 {
			return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller message exceeds %d bytes", MaxControllerStreamLineV1)
		}
		if err == io.EOF && len(line) != 0 {
			return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller message is not newline terminated")
		}
		return ControllerStreamRequestV1{}, err
	}
	if len(line) > MaxControllerStreamLineV1 {
		return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller message exceeds %d bytes", MaxControllerStreamLineV1)
	}
	payload := line[:len(line)-1]
	if len(payload) == 0 {
		return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller message must not be empty")
	}
	if !utf8.Valid(payload) {
		return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller message is not valid UTF-8 JSON")
	}
	if !bytes.Equal(bytes.TrimSpace(payload), payload) {
		return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller message must contain exactly one JSON object")
	}
	var envelope struct {
		Schema string                        `json:"schema"`
		Type   ControllerStreamRequestKindV1 `json:"type"`
	}
	if err := rejectDuplicateJSONFieldsV1(payload); err != nil {
		return ControllerStreamRequestV1{}, fmt.Errorf("decode controlled-session controller message envelope: %w", err)
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ControllerStreamRequestV1{}, fmt.Errorf("decode controlled-session controller message envelope: %w", err)
	}
	if envelope.Schema != ControllerStreamSchemaV1 {
		return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller message schema must be %q", ControllerStreamSchemaV1)
	}
	switch envelope.Type {
	case ControllerStreamRequestResizeV1:
		var message struct {
			Schema  string                        `json:"schema"`
			Type    ControllerStreamRequestKindV1 `json:"type"`
			Columns uint32                        `json:"columns"`
			Rows    uint32                        `json:"rows"`
		}
		if err := decodeStrictJSONV1("controller resize message", payload, &message); err != nil {
			return ControllerStreamRequestV1{}, err
		}
		request := ControllerStreamRequestV1{Kind: message.Type, Columns: message.Columns, Rows: message.Rows}
		if !validDimensionsV1(request.Columns, request.Rows) {
			return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller resize requires dimensions between 1 and 65535")
		}
		return request, nil
	case ControllerStreamRequestTerminateV1, ControllerStreamRequestCompleteV1, ControllerStreamRequestAcknowledgeTerminatedV1:
		var message struct {
			Schema string                        `json:"schema"`
			Type   ControllerStreamRequestKindV1 `json:"type"`
		}
		if err := decodeStrictJSONV1("controller request message", payload, &message); err != nil {
			return ControllerStreamRequestV1{}, err
		}
		return ControllerStreamRequestV1{Kind: message.Type}, nil
	default:
		return ControllerStreamRequestV1{}, fmt.Errorf("controlled-session controller message type %q is unsupported", envelope.Type)
	}
}

func (writer *ControllerStreamWriterV1) WriteEvent(event ControllerStreamEventV1) error {
	value, err := controllerStreamEventWireV1(event)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode controlled-session controller event: %w", err)
	}
	payload = append(payload, '\n')
	if len(payload) > MaxControllerStreamLineV1 {
		return fmt.Errorf("controlled-session controller event exceeds %d bytes", MaxControllerStreamLineV1)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writeAllV1(writer.writer, payload); err != nil {
		return fmt.Errorf("write controlled-session controller event: %w", err)
	}
	return nil
}

func controllerStreamEventWireV1(event ControllerStreamEventV1) (any, error) {
	base := struct {
		Schema string                      `json:"schema"`
		Type   ControllerStreamEventKindV1 `json:"type"`
	}{Schema: ControllerStreamSchemaV1, Type: event.Kind}
	switch event.Kind {
	case ControllerStreamEventBrokerReadyV1:
		if event.TerminalSocket == "" || controllerStreamEventPayloadCountV1(event) != 1 {
			return nil, fmt.Errorf("controlled-session broker-ready event requires exactly one terminal socket")
		}
		return struct {
			Schema         string                      `json:"schema"`
			Type           ControllerStreamEventKindV1 `json:"type"`
			TerminalSocket string                      `json:"terminal_socket"`
		}{base.Schema, base.Type, event.TerminalSocket}, nil
	case ControllerStreamEventOpenedV1:
		if event.Opened == nil || controllerStreamEventPayloadCountV1(event) != 1 || event.Opened.Operations == nil || event.Opened.Endpoints == nil || !validDimensionsV1(event.Opened.Columns, event.Opened.Rows) || event.Opened.OutputFinalizationTimeoutMilliseconds == 0 {
			return nil, fmt.Errorf("controlled-session opened controller event is invalid")
		}
		return struct {
			Schema                                string                      `json:"schema"`
			Type                                  ControllerStreamEventKindV1 `json:"type"`
			Operations                            []OperationV1               `json:"operations"`
			Endpoints                             []EndpointV1                `json:"endpoints"`
			Columns                               uint32                      `json:"columns"`
			Rows                                  uint32                      `json:"rows"`
			OutputFinalizationTimeoutMilliseconds uint32                      `json:"output_finalization_timeout_milliseconds"`
		}{base.Schema, base.Type, event.Opened.Operations, event.Opened.Endpoints, event.Opened.Columns, event.Opened.Rows, event.Opened.OutputFinalizationTimeoutMilliseconds}, nil
	case ControllerStreamEventReadyV1:
		if controllerStreamEventPayloadCountV1(event) != 0 {
			return nil, fmt.Errorf("controlled-session ready controller event must not contain a payload")
		}
		return base, nil
	case ControllerStreamEventWorkloadExitV1:
		if event.WorkloadExit == nil || controllerStreamEventPayloadCountV1(event) != 1 {
			return nil, fmt.Errorf("controlled-session workload-exit controller event is invalid")
		}
		return struct {
			Schema string                      `json:"schema"`
			Type   ControllerStreamEventKindV1 `json:"type"`
			Status ProcessStatusV1             `json:"status"`
		}{base.Schema, base.Type, event.WorkloadExit.Status}, nil
	case ControllerStreamEventTerminatingV1:
		if event.Terminating == nil || controllerStreamEventPayloadCountV1(event) != 1 {
			return nil, fmt.Errorf("controlled-session terminating controller event is invalid")
		}
		return struct {
			Schema string                      `json:"schema"`
			Type   ControllerStreamEventKindV1 `json:"type"`
			Cause  TerminationCauseV1          `json:"cause"`
		}{base.Schema, base.Type, event.Terminating.Cause}, nil
	case ControllerStreamEventDiagnosticV1, ControllerStreamEventClientErrorV1:
		diagnostic := event.Diagnostic
		if event.Kind == ControllerStreamEventClientErrorV1 {
			diagnostic = event.ClientError
		}
		if diagnostic == nil || controllerStreamEventPayloadCountV1(event) != 1 || validateProtocolCodeV1("controller event code", diagnostic.Code) != nil || validateRequiredSafeTextV1("controller event message", diagnostic.Message) != nil {
			return nil, fmt.Errorf("controlled-session %s controller event is invalid", event.Kind)
		}
		return struct {
			Schema  string                      `json:"schema"`
			Type    ControllerStreamEventKindV1 `json:"type"`
			Code    string                      `json:"code"`
			Message string                      `json:"message"`
		}{base.Schema, base.Type, diagnostic.Code, diagnostic.Message}, nil
	case ControllerStreamEventWorkloadOutputsFinalizedV1:
		if event.WorkloadOutputsFinalized == nil || controllerStreamEventPayloadCountV1(event) != 1 {
			return nil, fmt.Errorf("controlled-session workload-outputs-finalized controller event is invalid")
		}
		if err := validateWorkloadOutputFinalizationStatusV1(WorkloadOutputFinalizationStatusV1{Kind: event.WorkloadOutputsFinalized.Status, Reason: event.WorkloadOutputsFinalized.Reason}); err != nil {
			return nil, err
		}
		return struct {
			Schema string                                 `json:"schema"`
			Type   ControllerStreamEventKindV1            `json:"type"`
			Status WorkloadOutputFinalizationStatusKindV1 `json:"status"`
			Reason string                                 `json:"reason,omitempty"`
		}{base.Schema, base.Type, event.WorkloadOutputsFinalized.Status, event.WorkloadOutputsFinalized.Reason}, nil
	case ControllerStreamEventTerminatedV1:
		if event.Terminated == nil || controllerStreamEventPayloadCountV1(event) != 1 {
			return nil, fmt.Errorf("controlled-session terminated controller event is invalid")
		}
		if err := ValidateResultV1(*event.Terminated); err != nil {
			return nil, err
		}
		return struct {
			Schema string                      `json:"schema"`
			Type   ControllerStreamEventKindV1 `json:"type"`
			Result ResultV1                    `json:"result"`
		}{base.Schema, base.Type, *event.Terminated}, nil
	default:
		return nil, fmt.Errorf("controlled-session controller event kind %q is unsupported", event.Kind)
	}
}

func controllerStreamEventPayloadCountV1(event ControllerStreamEventV1) int {
	count := 0
	for _, present := range []bool{event.TerminalSocket != "", event.Opened != nil, event.WorkloadExit != nil, event.Terminating != nil, event.Diagnostic != nil, event.WorkloadOutputsFinalized != nil, event.Terminated != nil, event.ClientError != nil} {
		if present {
			count++
		}
	}
	return count
}
