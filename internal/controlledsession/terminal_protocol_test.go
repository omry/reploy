package controlledsession

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestTerminalProtocolV1RoundTripsRequestsAndEvents(t *testing.T) {
	requests := []RequestV1{
		{Kind: RequestInputV1, Bytes: []byte{0, 3, 255}},
		{Kind: RequestResizeV1, Columns: 132, Rows: 43},
	}
	for _, want := range requests {
		var wire bytes.Buffer
		if err := WriteTerminalRequestV1(&wire, want); err != nil {
			t.Fatal(err)
		}
		got, err := ReadTerminalRequestV1(&wire)
		if err != nil || got.Kind != want.Kind || !bytes.Equal(got.Bytes, want.Bytes) || got.Columns != want.Columns || got.Rows != want.Rows {
			t.Fatalf("terminal request = %#v, %v; want %#v", got, err, want)
		}
	}

	var wire bytes.Buffer
	if err := WriteTerminalOutputV1(&wire, []byte("hello\x00world")); err != nil {
		t.Fatal(err)
	}
	if err := WriteTerminalEndV1(&wire, WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}); err != nil {
		t.Fatal(err)
	}
	output, err := ReadTerminalEventV1(&wire)
	if err != nil || output.Kind != TerminalEventOutputV1 || string(output.Bytes) != "hello\x00world" {
		t.Fatalf("terminal output = %#v, %v", output, err)
	}
	end, err := ReadTerminalEventV1(&wire)
	if err != nil || end.Kind != TerminalEventEndV1 || end.Status == nil || end.Status.Kind != WorkloadOutputFinalizationDrainedV1 {
		t.Fatalf("terminal end = %#v, %v", end, err)
	}
}

func TestTerminalProtocolV1RejectsMalformedOrWrongDirectionFrames(t *testing.T) {
	var wire bytes.Buffer
	header := make([]byte, terminalFrameHeaderSizeV1)
	copy(header, terminalFrameMagicV1[:])
	header[4] = ProtocolVersionV1
	header[5] = byte(terminalWireResizeV1)
	binary.BigEndian.PutUint32(header[6:], 1)
	wire.Write(header)
	wire.WriteByte(0)
	if _, err := ReadTerminalRequestV1(&wire); err == nil || !strings.Contains(err.Error(), "8 bytes") {
		t.Fatalf("malformed resize error = %v", err)
	}
	wire.Reset()
	if err := WriteTerminalOutputV1(&wire, []byte("output")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTerminalRequestV1(&wire); err == nil || !strings.Contains(err.Error(), "not a request") {
		t.Fatalf("wrong-direction error = %v", err)
	}
	if err := WriteTerminalRequestV1(&wire, RequestV1{Kind: RequestTerminateV1}); err == nil || !strings.Contains(err.Error(), "does not carry") {
		t.Fatalf("terminal terminate error = %v", err)
	}
}
