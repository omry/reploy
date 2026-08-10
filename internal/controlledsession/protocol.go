package controlledsession

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"

	"github.com/omry/reploy/internal/endpointname"
)

const (
	// ProtocolVersionV1 is the original wire version whose opened event did
	// not contain endpoint coordinates. It remains reserved so the expanded
	// opened event cannot be mistaken for the old strict JSON schema.
	ProtocolVersionV1                                     = 1
	ProtocolVersionV2                                     = 2
	MaxFramePayloadV1                                     = 1 << 20
	DefaultOutputFinalizationTimeoutMillisecondsV1 uint32 = 30_000
	frameHeaderSizeV1                                     = 10
	maxProtocolCodeLengthV1                               = 63
)

var frameMagicV1 = [4]byte{'R', 'P', 'S', 'N'}

var protocolCodePatternV1 = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)
var endpointSchemePatternV2 = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)

const WorkloadEndpointHostV2 = "workload"

type RequestKindV1 string

const (
	RequestInputV1                 RequestKindV1 = "input"
	RequestResizeV1                RequestKindV1 = "resize"
	RequestTerminateV1             RequestKindV1 = "terminate"
	RequestCompleteV1              RequestKindV1 = "complete"
	RequestAcknowledgeTerminatedV1 RequestKindV1 = "acknowledge-terminated"
)

type RequestV1 struct {
	Kind    RequestKindV1
	Bytes   []byte
	Columns uint32
	Rows    uint32
}

type EventKindV1 string

const (
	EventOpenedV1                   EventKindV1 = "opened"
	EventOutputV1                   EventKindV1 = "output"
	EventWorkloadExitV1             EventKindV1 = "workload-exit"
	EventTerminatingV1              EventKindV1 = "terminating"
	EventDiagnosticV1               EventKindV1 = "diagnostic"
	EventWorkloadOutputsFinalizedV1 EventKindV1 = "workload-outputs-finalized"
	EventTerminatedV1               EventKindV1 = "terminated"
)

// OpenedV2 expands the original opened payload with immutable session-local
// endpoint coordinates and therefore requires protocol wire version 2.
type OpenedV2 struct {
	Authorization                         AuthorizationV1 `json:"authorization"`
	Endpoints                             []EndpointV2    `json:"endpoints"`
	Columns                               uint32          `json:"columns"`
	Rows                                  uint32          `json:"rows"`
	OutputFinalizationTimeoutMilliseconds uint32          `json:"output_finalization_timeout_milliseconds"`
}

// EndpointV2 is one immutable session-local coordinate granted to the
// controller. Traffic uses native TCP; only these coordinates cross the
// private session channel.
type EndpointV2 struct {
	ID     string `json:"id"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   uint32 `json:"port"`
}

type WorkloadExitV1 struct {
	Status ProcessStatusV1 `json:"status"`
}

type TerminatingV1 struct {
	Cause TerminationCauseV1 `json:"cause"`
}

type DiagnosticV1 struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type WorkloadOutputsFinalizedV1 struct {
	Status WorkloadOutputFinalizationStatusKindV1 `json:"status"`
	Reason string                                 `json:"reason,omitempty"`
}

type EventV1 struct {
	Kind                     EventKindV1
	Bytes                    []byte
	Opened                   *OpenedV2
	WorkloadExit             *WorkloadExitV1
	Terminating              *TerminatingV1
	Diagnostic               *DiagnosticV1
	WorkloadOutputsFinalized *WorkloadOutputsFinalizedV1
	Terminated               *ResultV1
}

type wireKindV1 byte

const (
	wireRequestInputV1                 wireKindV1 = 0x01
	wireRequestResizeV1                wireKindV1 = 0x02
	wireRequestTerminateV1             wireKindV1 = 0x03
	wireRequestCompleteV1              wireKindV1 = 0x04
	wireRequestAcknowledgeTerminatedV1 wireKindV1 = 0x05
)

const (
	wireEventOpenedV1                   wireKindV1 = 0x81
	wireEventOutputV1                   wireKindV1 = 0x82
	wireEventWorkloadExitV1             wireKindV1 = 0x83
	wireEventTerminatingV1              wireKindV1 = 0x84
	wireEventDiagnosticV1               wireKindV1 = 0x85
	wireEventTerminatedV1               wireKindV1 = 0x86
	wireEventWorkloadOutputsFinalizedV1 wireKindV1 = 0x87
)

func ValidateRequestV1(request RequestV1) error {
	switch request.Kind {
	case RequestInputV1:
		if request.Bytes == nil || request.Columns != 0 || request.Rows != 0 {
			return fmt.Errorf("controlled-session input request must contain only a byte sequence")
		}
	case RequestResizeV1:
		if request.Bytes != nil || !validDimensionsV1(request.Columns, request.Rows) {
			return fmt.Errorf("controlled-session resize request requires dimensions between 1 and 65535")
		}
	case RequestTerminateV1, RequestCompleteV1, RequestAcknowledgeTerminatedV1:
		if request.Bytes != nil || request.Columns != 0 || request.Rows != 0 {
			return fmt.Errorf("controlled-session %s request must not contain a payload", request.Kind)
		}
	default:
		return fmt.Errorf("controlled-session request kind %q is unsupported", request.Kind)
	}
	return nil
}

func ValidateEventV1(event EventV1) error {
	switch event.Kind {
	case EventOpenedV1:
		if event.Opened == nil || eventPayloadCountV1(event) != 1 {
			return fmt.Errorf("controlled-session opened event requires exactly one opened payload")
		}
		if err := ValidateAuthorizationV1(event.Opened.Authorization); err != nil {
			return fmt.Errorf("controlled-session opened event: %w", err)
		}
		if err := validateOpenedEndpointsV2(event.Opened.Endpoints, event.Opened.Authorization.EndpointIDs); err != nil {
			return err
		}
		if !validDimensionsV1(event.Opened.Columns, event.Opened.Rows) {
			return fmt.Errorf("controlled-session opened dimensions must be between 1 and 65535")
		}
		if event.Opened.OutputFinalizationTimeoutMilliseconds == 0 {
			return fmt.Errorf("controlled-session opened event requires a finite output-finalization timeout")
		}
	case EventOutputV1:
		if event.Bytes == nil || eventPayloadCountV1(event) != 1 {
			return fmt.Errorf("controlled-session output event must contain only a byte sequence")
		}
	case EventWorkloadExitV1:
		if event.WorkloadExit == nil || eventPayloadCountV1(event) != 1 {
			return fmt.Errorf("controlled-session workload-exit event requires exactly one status payload")
		}
		if err := validateProcessStatusV1(event.WorkloadExit.Status, false); err != nil {
			return fmt.Errorf("controlled-session workload-exit event: %w", err)
		}
	case EventTerminatingV1:
		if event.Terminating == nil || eventPayloadCountV1(event) != 1 {
			return fmt.Errorf("controlled-session terminating event requires exactly one cause payload")
		}
		if !validTerminationCauseV1(event.Terminating.Cause) {
			return fmt.Errorf("controlled-session terminating cause %q is invalid", event.Terminating.Cause)
		}
	case EventDiagnosticV1:
		if event.Diagnostic == nil || eventPayloadCountV1(event) != 1 {
			return fmt.Errorf("controlled-session diagnostic event requires exactly one diagnostic payload")
		}
		if err := validateProtocolCodeV1("diagnostic code", event.Diagnostic.Code); err != nil {
			return err
		}
		if err := validateRequiredSafeTextV1("diagnostic message", event.Diagnostic.Message); err != nil {
			return err
		}
	case EventWorkloadOutputsFinalizedV1:
		if event.WorkloadOutputsFinalized == nil || eventPayloadCountV1(event) != 1 {
			return fmt.Errorf("controlled-session workload-outputs-finalized event requires exactly one finalization payload")
		}
		if err := validateWorkloadOutputFinalizationStatusV1(WorkloadOutputFinalizationStatusV1{
			Kind:   event.WorkloadOutputsFinalized.Status,
			Reason: event.WorkloadOutputsFinalized.Reason,
		}); err != nil {
			return fmt.Errorf("controlled-session workload-outputs-finalized event: %w", err)
		}
	case EventTerminatedV1:
		if event.Terminated == nil || eventPayloadCountV1(event) != 1 {
			return fmt.Errorf("controlled-session terminated event requires exactly one result payload")
		}
		if err := ValidateResultV1(*event.Terminated); err != nil {
			return fmt.Errorf("controlled-session terminated event: %w", err)
		}
	default:
		return fmt.Errorf("controlled-session event kind %q is unsupported", event.Kind)
	}
	return nil
}

func validateOpenedEndpointsV2(endpoints []EndpointV2, authorizedIDs []string) error {
	if endpoints == nil {
		return fmt.Errorf("controlled-session opened endpoints must use an array")
	}
	if len(endpoints) != len(authorizedIDs) {
		return fmt.Errorf("controlled-session opened endpoints must match the authorized endpoint IDs")
	}
	for index, endpoint := range endpoints {
		if endpoint.ID != authorizedIDs[index] {
			return fmt.Errorf("controlled-session opened endpoint %d must match authorized endpoint ID %q", index, authorizedIDs[index])
		}
		if err := ValidateEndpointV2(endpoint); err != nil {
			return err
		}
	}
	return nil
}

func ValidateEndpointV2(endpoint EndpointV2) error {
	if err := endpointname.Validate(endpoint.ID); err != nil {
		return fmt.Errorf("controlled-session endpoint ID %q: %w", endpoint.ID, err)
	}
	if !endpointSchemePatternV2.MatchString(endpoint.Scheme) {
		return fmt.Errorf("controlled-session endpoint %q scheme must use URI-scheme syntax", endpoint.ID)
	}
	if endpoint.Host != WorkloadEndpointHostV2 {
		return fmt.Errorf("controlled-session endpoint %q host must be %q", endpoint.ID, WorkloadEndpointHostV2)
	}
	if endpoint.Port == 0 || endpoint.Port > 65535 {
		return fmt.Errorf("controlled-session endpoint %q port must be between 1 and 65535", endpoint.ID)
	}
	return nil
}

func WriteRequestV2(writer io.Writer, request RequestV1) error {
	if err := ValidateRequestV1(request); err != nil {
		return err
	}
	kind, payload := encodeRequestPayloadV1(request)
	return writeFrameV2(writer, kind, payload)
}

func ReadRequestV2(reader io.Reader) (RequestV1, error) {
	kind, payload, err := readFrameV2(reader)
	if err != nil {
		return RequestV1{}, err
	}
	request, err := decodeRequestPayloadV1(kind, payload)
	if err != nil {
		return RequestV1{}, err
	}
	if err := ValidateRequestV1(request); err != nil {
		return RequestV1{}, err
	}
	return request, nil
}

func WriteEventV2(writer io.Writer, event EventV1) error {
	if err := ValidateEventV1(event); err != nil {
		return err
	}
	kind, payload, err := encodeEventPayloadV1(event)
	if err != nil {
		return err
	}
	return writeFrameV2(writer, kind, payload)
}

func ReadEventV2(reader io.Reader) (EventV1, error) {
	kind, payload, err := readFrameV2(reader)
	if err != nil {
		return EventV1{}, err
	}
	event, err := decodeEventPayloadV1(kind, payload)
	if err != nil {
		return EventV1{}, err
	}
	if err := ValidateEventV1(event); err != nil {
		return EventV1{}, err
	}
	return event, nil
}

func encodeRequestPayloadV1(request RequestV1) (wireKindV1, []byte) {
	switch request.Kind {
	case RequestInputV1:
		return wireRequestInputV1, request.Bytes
	case RequestResizeV1:
		payload := make([]byte, 8)
		binary.BigEndian.PutUint32(payload[0:4], request.Columns)
		binary.BigEndian.PutUint32(payload[4:8], request.Rows)
		return wireRequestResizeV1, payload
	case RequestTerminateV1:
		return wireRequestTerminateV1, nil
	case RequestCompleteV1:
		return wireRequestCompleteV1, nil
	case RequestAcknowledgeTerminatedV1:
		return wireRequestAcknowledgeTerminatedV1, nil
	default:
		panic("validated request has unsupported kind")
	}
}

func decodeRequestPayloadV1(kind wireKindV1, payload []byte) (RequestV1, error) {
	switch kind {
	case wireRequestInputV1:
		return RequestV1{Kind: RequestInputV1, Bytes: payload}, nil
	case wireRequestResizeV1:
		if len(payload) != 8 {
			return RequestV1{}, fmt.Errorf("controlled-session resize frame payload must contain 8 bytes")
		}
		return RequestV1{Kind: RequestResizeV1, Columns: binary.BigEndian.Uint32(payload[:4]), Rows: binary.BigEndian.Uint32(payload[4:])}, nil
	case wireRequestTerminateV1:
		if len(payload) != 0 {
			return RequestV1{}, fmt.Errorf("controlled-session terminate frame must not contain a payload")
		}
		return RequestV1{Kind: RequestTerminateV1}, nil
	case wireRequestCompleteV1:
		if len(payload) != 0 {
			return RequestV1{}, fmt.Errorf("controlled-session complete frame must not contain a payload")
		}
		return RequestV1{Kind: RequestCompleteV1}, nil
	case wireRequestAcknowledgeTerminatedV1:
		if len(payload) != 0 {
			return RequestV1{}, fmt.Errorf("controlled-session acknowledge-terminated frame must not contain a payload")
		}
		return RequestV1{Kind: RequestAcknowledgeTerminatedV1}, nil
	default:
		return RequestV1{}, fmt.Errorf("controlled-session frame kind 0x%02x is not a controller request", byte(kind))
	}
}

func encodeEventPayloadV1(event EventV1) (wireKindV1, []byte, error) {
	switch event.Kind {
	case EventOpenedV1:
		return marshalEventPayloadV1(wireEventOpenedV1, event.Opened)
	case EventOutputV1:
		return wireEventOutputV1, event.Bytes, nil
	case EventWorkloadExitV1:
		return marshalEventPayloadV1(wireEventWorkloadExitV1, event.WorkloadExit)
	case EventTerminatingV1:
		return marshalEventPayloadV1(wireEventTerminatingV1, event.Terminating)
	case EventDiagnosticV1:
		return marshalEventPayloadV1(wireEventDiagnosticV1, event.Diagnostic)
	case EventWorkloadOutputsFinalizedV1:
		return marshalEventPayloadV1(wireEventWorkloadOutputsFinalizedV1, event.WorkloadOutputsFinalized)
	case EventTerminatedV1:
		return marshalEventPayloadV1(wireEventTerminatedV1, event.Terminated)
	default:
		panic("validated event has unsupported kind")
	}
}

func marshalEventPayloadV1(kind wireKindV1, value any) (wireKindV1, []byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return 0, nil, fmt.Errorf("encode controlled-session event: %w", err)
	}
	return kind, payload, nil
}

func decodeEventPayloadV1(kind wireKindV1, payload []byte) (EventV1, error) {
	switch kind {
	case wireEventOpenedV1:
		value := new(OpenedV2)
		return EventV1{Kind: EventOpenedV1, Opened: value}, decodeStrictJSONV1("opened event", payload, value)
	case wireEventOutputV1:
		return EventV1{Kind: EventOutputV1, Bytes: payload}, nil
	case wireEventWorkloadExitV1:
		value := new(WorkloadExitV1)
		return EventV1{Kind: EventWorkloadExitV1, WorkloadExit: value}, decodeStrictJSONV1("workload-exit event", payload, value)
	case wireEventTerminatingV1:
		value := new(TerminatingV1)
		return EventV1{Kind: EventTerminatingV1, Terminating: value}, decodeStrictJSONV1("terminating event", payload, value)
	case wireEventDiagnosticV1:
		value := new(DiagnosticV1)
		return EventV1{Kind: EventDiagnosticV1, Diagnostic: value}, decodeStrictJSONV1("diagnostic event", payload, value)
	case wireEventWorkloadOutputsFinalizedV1:
		value := new(WorkloadOutputsFinalizedV1)
		return EventV1{Kind: EventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: value}, decodeStrictJSONV1("workload-outputs-finalized event", payload, value)
	case wireEventTerminatedV1:
		value := new(ResultV1)
		return EventV1{Kind: EventTerminatedV1, Terminated: value}, decodeStrictJSONV1("terminated event", payload, value)
	default:
		return EventV1{}, fmt.Errorf("controlled-session frame kind 0x%02x is not a session event", byte(kind))
	}
}

func eventPayloadCountV1(event EventV1) int {
	count := 0
	for _, present := range []bool{
		event.Bytes != nil,
		event.Opened != nil,
		event.WorkloadExit != nil,
		event.Terminating != nil,
		event.Diagnostic != nil,
		event.WorkloadOutputsFinalized != nil,
		event.Terminated != nil,
	} {
		if present {
			count++
		}
	}
	return count
}

func writeFrameV2(writer io.Writer, kind wireKindV1, payload []byte) error {
	if len(payload) > MaxFramePayloadV1 {
		return fmt.Errorf("controlled-session frame payload exceeds %d bytes", MaxFramePayloadV1)
	}
	header := make([]byte, frameHeaderSizeV1)
	copy(header[:4], frameMagicV1[:])
	header[4] = ProtocolVersionV2
	header[5] = byte(kind)
	binary.BigEndian.PutUint32(header[6:], uint32(len(payload)))
	if err := writeAllV1(writer, header); err != nil {
		return fmt.Errorf("write controlled-session frame header: %w", err)
	}
	if err := writeAllV1(writer, payload); err != nil {
		return fmt.Errorf("write controlled-session frame payload: %w", err)
	}
	return nil
}

func readFrameV2(reader io.Reader) (wireKindV1, []byte, error) {
	header := make([]byte, frameHeaderSizeV1)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, fmt.Errorf("read controlled-session frame header: %w", err)
	}
	if !bytes.Equal(header[:4], frameMagicV1[:]) {
		return 0, nil, fmt.Errorf("controlled-session frame magic is invalid")
	}
	if header[4] != ProtocolVersionV2 {
		return 0, nil, fmt.Errorf("controlled-session protocol version %d is unsupported", header[4])
	}
	length := binary.BigEndian.Uint32(header[6:])
	if length > MaxFramePayloadV1 {
		return 0, nil, fmt.Errorf("controlled-session frame payload length %d exceeds %d bytes", length, MaxFramePayloadV1)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, fmt.Errorf("read controlled-session frame payload: %w", err)
	}
	return wireKindV1(header[5]), payload, nil
}

func decodeStrictJSONV1(subject string, payload []byte, value any) error {
	if !utf8.Valid(payload) {
		return fmt.Errorf("controlled-session %s is not valid UTF-8 JSON", subject)
	}
	if err := rejectDuplicateJSONFieldsV1(payload); err != nil {
		return fmt.Errorf("decode controlled-session %s: %w", subject, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode controlled-session %s: %w", subject, err)
	}
	var trailer any
	if err := decoder.Decode(&trailer); err != io.EOF {
		if err == nil {
			return fmt.Errorf("controlled-session %s contains trailing JSON", subject)
		}
		return fmt.Errorf("decode controlled-session %s trailer: %w", subject, err)
	}
	return nil
}

func rejectDuplicateJSONFieldsV1(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := scanJSONValueV1(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON contains trailing token %v", token)
		}
		return err
	}
	return nil
}

func scanJSONValueV1(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if !validWireJSONFieldNameV1(key) {
				return fmt.Errorf("JSON object field %q is not lowercase ASCII snake_case", key)
			}
			if seen[key] {
				return fmt.Errorf("JSON object repeats field %q", key)
			}
			seen[key] = true
			if err := scanJSONValueV1(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValueV1(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("JSON array is not closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func validWireJSONFieldNameV1(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' {
			continue
		}
		if index != 0 && ((character >= '0' && character <= '9') || character == '_') {
			continue
		}
		return false
	}
	return true
}

func writeAllV1(writer io.Writer, content []byte) error {
	for len(content) != 0 {
		written, err := writer.Write(content)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(content) {
			return io.ErrShortWrite
		}
		content = content[written:]
	}
	return nil
}

func operationForRequestV1(kind RequestKindV1) OperationV1 {
	switch kind {
	case RequestInputV1:
		return OperationInputV1
	case RequestResizeV1:
		return OperationResizeV1
	case RequestTerminateV1:
		return OperationTerminateV1
	case RequestCompleteV1:
		return OperationCompleteV1
	default:
		return ""
	}
}

func validDimensionsV1(columns uint32, rows uint32) bool {
	return columns >= 1 && columns <= 65535 && rows >= 1 && rows <= 65535
}

func validateProtocolCodeV1(subject string, value string) error {
	if len(value) == 0 || len(value) > maxProtocolCodeLengthV1 || !protocolCodePatternV1.MatchString(value) {
		return fmt.Errorf("controlled-session %s %q must be a lowercase ASCII snake_case identifier no longer than %d bytes", subject, value, maxProtocolCodeLengthV1)
	}
	return nil
}
