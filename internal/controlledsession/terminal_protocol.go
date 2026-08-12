package controlledsession

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const terminalFrameHeaderSizeV1 = 10

var terminalFrameMagicV1 = [4]byte{'R', 'P', 'T', 'M'}

type terminalWireKindV1 byte

const (
	terminalWireInputV1  terminalWireKindV1 = 0x01
	terminalWireResizeV1 terminalWireKindV1 = 0x02
	terminalWireOutputV1 terminalWireKindV1 = 0x81
	terminalWireEndV1    terminalWireKindV1 = 0x82
)

type TerminalEventKindV1 string

const (
	TerminalEventOutputV1 TerminalEventKindV1 = "output"
	TerminalEventEndV1    TerminalEventKindV1 = "terminal-end"
)

type TerminalEventV1 struct {
	Kind   TerminalEventKindV1
	Bytes  []byte
	Status *WorkloadOutputFinalizationStatusV1
}

func WriteTerminalRequestV1(writer io.Writer, request RequestV1) error {
	if err := ValidateRequestV1(request); err != nil {
		return err
	}
	switch request.Kind {
	case RequestInputV1:
		return writeTerminalFrameV1(writer, terminalWireInputV1, request.Bytes)
	case RequestResizeV1:
		payload := make([]byte, 8)
		binary.BigEndian.PutUint32(payload[:4], request.Columns)
		binary.BigEndian.PutUint32(payload[4:], request.Rows)
		return writeTerminalFrameV1(writer, terminalWireResizeV1, payload)
	default:
		return fmt.Errorf("controlled-session terminal protocol does not carry %q requests", request.Kind)
	}
}

func ReadTerminalRequestV1(reader io.Reader) (RequestV1, error) {
	kind, payload, err := readTerminalFrameV1(reader)
	if err != nil {
		return RequestV1{}, err
	}
	switch kind {
	case terminalWireInputV1:
		request := RequestV1{Kind: RequestInputV1, Bytes: payload}
		return request, ValidateRequestV1(request)
	case terminalWireResizeV1:
		if len(payload) != 8 {
			return RequestV1{}, fmt.Errorf("controlled-session terminal resize payload must contain 8 bytes")
		}
		request := RequestV1{Kind: RequestResizeV1, Columns: binary.BigEndian.Uint32(payload[:4]), Rows: binary.BigEndian.Uint32(payload[4:])}
		return request, ValidateRequestV1(request)
	default:
		return RequestV1{}, fmt.Errorf("controlled-session terminal frame kind 0x%02x is not a request", byte(kind))
	}
}

func WriteTerminalOutputV1(writer io.Writer, content []byte) error {
	if content == nil {
		return fmt.Errorf("controlled-session terminal output requires a byte sequence")
	}
	return writeTerminalFrameV1(writer, terminalWireOutputV1, content)
}

func WriteTerminalEndV1(writer io.Writer, status WorkloadOutputFinalizationStatusV1) error {
	if err := validateWorkloadOutputFinalizationStatusV1(status); err != nil {
		return err
	}
	payload, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("encode controlled-session terminal end: %w", err)
	}
	return writeTerminalFrameV1(writer, terminalWireEndV1, payload)
}

func ReadTerminalEventV1(reader io.Reader) (TerminalEventV1, error) {
	kind, payload, err := readTerminalFrameV1(reader)
	if err != nil {
		return TerminalEventV1{}, err
	}
	switch kind {
	case terminalWireOutputV1:
		return TerminalEventV1{Kind: TerminalEventOutputV1, Bytes: payload}, nil
	case terminalWireEndV1:
		var status WorkloadOutputFinalizationStatusV1
		if err := decodeStrictJSONV1("terminal-end event", payload, &status); err != nil {
			return TerminalEventV1{}, err
		}
		if err := validateWorkloadOutputFinalizationStatusV1(status); err != nil {
			return TerminalEventV1{}, err
		}
		return TerminalEventV1{Kind: TerminalEventEndV1, Status: &status}, nil
	default:
		return TerminalEventV1{}, fmt.Errorf("controlled-session terminal frame kind 0x%02x is not an event", byte(kind))
	}
}

func writeTerminalFrameV1(writer io.Writer, kind terminalWireKindV1, payload []byte) error {
	if len(payload) > MaxFramePayloadV1 {
		return fmt.Errorf("controlled-session terminal frame payload exceeds %d bytes", MaxFramePayloadV1)
	}
	header := make([]byte, terminalFrameHeaderSizeV1)
	copy(header[:4], terminalFrameMagicV1[:])
	header[4] = ProtocolVersionV1
	header[5] = byte(kind)
	binary.BigEndian.PutUint32(header[6:], uint32(len(payload)))
	if err := writeAllV1(writer, header); err != nil {
		return fmt.Errorf("write controlled-session terminal frame header: %w", err)
	}
	if err := writeAllV1(writer, payload); err != nil {
		return fmt.Errorf("write controlled-session terminal frame payload: %w", err)
	}
	return nil
}

func readTerminalFrameV1(reader io.Reader) (terminalWireKindV1, []byte, error) {
	header := make([]byte, terminalFrameHeaderSizeV1)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, fmt.Errorf("read controlled-session terminal frame header: %w", err)
	}
	if !bytes.Equal(header[:4], terminalFrameMagicV1[:]) {
		return 0, nil, fmt.Errorf("controlled-session terminal frame magic is invalid")
	}
	if header[4] != ProtocolVersionV1 {
		return 0, nil, fmt.Errorf("controlled-session terminal protocol version %d is unsupported", header[4])
	}
	length := binary.BigEndian.Uint32(header[6:])
	if length > MaxFramePayloadV1 {
		return 0, nil, fmt.Errorf("controlled-session terminal frame payload length %d exceeds %d bytes", length, MaxFramePayloadV1)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, fmt.Errorf("read controlled-session terminal frame payload: %w", err)
	}
	return terminalWireKindV1(header[5]), payload, nil
}
