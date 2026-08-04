package controlledsession

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

func TestRequestV1RoundTripsBinarySafeFrames(t *testing.T) {
	requests := []RequestV1{
		{Kind: RequestInputV1, Bytes: []byte{0, 3, '\n', 0xff}},
		{Kind: RequestResizeV1, Columns: 120, Rows: 40},
		{Kind: RequestTerminateV1},
		{Kind: RequestCompleteV1},
		{Kind: RequestOpenEndpointV1, EndpointID: "browser"},
		{Kind: RequestAcknowledgeTerminatedV1},
	}
	var stream bytes.Buffer
	for _, request := range requests {
		if err := WriteRequestV1(&stream, request); err != nil {
			t.Fatalf("WriteRequestV1(%s) error = %v", request.Kind, err)
		}
	}
	for _, want := range requests {
		got, err := ReadRequestV1(&stream)
		if err != nil {
			t.Fatalf("ReadRequestV1(%s) error = %v", want.Kind, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadRequestV1() = %#v, want %#v", got, want)
		}
	}
}

func TestEventV1RoundTripsStrictTypedFrames(t *testing.T) {
	code := 0
	authorization := testAuthorizationV1()
	events := []EventV1{
		{Kind: EventOpenedV1, Opened: &OpenedV1{Authorization: authorization, Columns: 80, Rows: 24}},
		{Kind: EventOutputV1, Bytes: []byte{0, '\n', 0xff}},
		{Kind: EventWorkloadExitV1, WorkloadExit: &WorkloadExitV1{Status: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code}}},
		{Kind: EventTerminatingV1, Terminating: &TerminatingV1{Cause: CauseWorkloadExitV1}},
		{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "cleanup-failed", Message: "container removal failed"}},
		{Kind: EventEndpointClosedV1, EndpointClosed: &EndpointClosedV1{EndpointID: "browser", Reason: "session terminating"}},
		{Kind: EventTerminatedV1, Terminated: &ResultV1{
			Cause: CauseWorkloadExitV1, WorkloadStatus: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
			ControllerStatus: ControllerStatusV1{Kind: ControllerStatusCompletedV1},
			CleanupStatus:    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
		}},
	}
	var stream bytes.Buffer
	for _, event := range events {
		if err := WriteEventV1(&stream, event); err != nil {
			t.Fatalf("WriteEventV1(%s) error = %v", event.Kind, err)
		}
	}
	for _, want := range events {
		got, err := ReadEventV1(&stream)
		if err != nil {
			t.Fatalf("ReadEventV1(%s) error = %v", want.Kind, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadEventV1() = %#v, want %#v", got, want)
		}
	}
}

func TestFrameV1RejectsWrongProtocolDirectionAndUnboundedInput(t *testing.T) {
	var event bytes.Buffer
	if err := WriteEventV1(&event, EventV1{Kind: EventOutputV1, Bytes: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRequestV1(&event); err == nil || !strings.Contains(err.Error(), "not a controller request") {
		t.Fatalf("ReadRequestV1(event) error = %v", err)
	}

	header := make([]byte, frameHeaderSizeV1)
	copy(header, frameMagicV1[:])
	header[4] = ProtocolVersionV1
	header[5] = byte(wireRequestInputV1)
	binary.BigEndian.PutUint32(header[6:], MaxFramePayloadV1+1)
	if _, err := ReadRequestV1(bytes.NewReader(header)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadRequestV1(oversized) error = %v", err)
	}
}

func TestFrameV1RejectsBadMagicVersionTruncationAndUnknownJSON(t *testing.T) {
	validHeader := func() []byte {
		header := make([]byte, frameHeaderSizeV1)
		copy(header, frameMagicV1[:])
		header[4] = ProtocolVersionV1
		header[5] = byte(wireRequestCompleteV1)
		return header
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "bad magic", data: append([]byte("NOPE"), validHeader()[4:]...), want: "magic"},
		{name: "bad version", data: func() []byte { value := validHeader(); value[4] = 2; return value }(), want: "version 2"},
		{name: "short header", data: validHeader()[:5], want: "frame header"},
		{name: "short payload", data: func() []byte {
			value := validHeader()
			value[5] = byte(wireRequestInputV1)
			binary.BigEndian.PutUint32(value[6:], 2)
			return append(value, 1)
		}(), want: "frame payload"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ReadRequestV1(bytes.NewReader(test.data)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadRequestV1() error = %v, want containing %q", err, test.want)
			}
		})
	}

	payload := []byte(`{"code":"bad","message":"failure","extra":true}`)
	var framed bytes.Buffer
	if err := writeFrameV1(&framed, wireEventDiagnosticV1, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV1(&framed); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ReadEventV1(unknown JSON) error = %v", err)
	}

	duplicate := []byte(`{"code":"first","code":"second","message":"failure"}`)
	framed.Reset()
	if err := writeFrameV1(&framed, wireEventDiagnosticV1, duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV1(&framed); err == nil || !strings.Contains(err.Error(), "repeats field") {
		t.Fatalf("ReadEventV1(duplicate JSON) error = %v", err)
	}

	caseVariant := []byte(`{"Code":"bad","message":"failure"}`)
	framed.Reset()
	if err := writeFrameV1(&framed, wireEventDiagnosticV1, caseVariant); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV1(&framed); err == nil || !strings.Contains(err.Error(), "lowercase ASCII snake_case") {
		t.Fatalf("ReadEventV1(case-variant JSON) error = %v", err)
	}

	caseVariantDuplicate := []byte(`{"code":"first","Code":"second","message":"failure"}`)
	framed.Reset()
	if err := writeFrameV1(&framed, wireEventDiagnosticV1, caseVariantDuplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV1(&framed); err == nil || !strings.Contains(err.Error(), "lowercase ASCII snake_case") {
		t.Fatalf("ReadEventV1(case-variant duplicate JSON) error = %v", err)
	}

	nestedCaseVariant := []byte(`{"cause":"workload-exit","workload_status":{"Kind":"exited","code":0},"controller_status":{"kind":"completed"},"cleanup_status":{"kind":"succeeded"},"recovery_action":"none"}`)
	framed.Reset()
	if err := writeFrameV1(&framed, wireEventTerminatedV1, nestedCaseVariant); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV1(&framed); err == nil || !strings.Contains(err.Error(), "lowercase ASCII snake_case") {
		t.Fatalf("ReadEventV1(nested case-variant JSON) error = %v", err)
	}
}

func TestValidateRequestV1RejectsAmbiguousUnionPayloads(t *testing.T) {
	tests := []RequestV1{
		{Kind: RequestInputV1},
		{Kind: RequestResizeV1, Columns: 80},
		{Kind: RequestCompleteV1, Bytes: []byte{}},
		{Kind: RequestOpenEndpointV1, EndpointID: "../other"},
		{Kind: RequestAcknowledgeTerminatedV1, EndpointID: "browser"},
	}
	for _, request := range tests {
		if err := ValidateRequestV1(request); err == nil {
			t.Fatalf("ValidateRequestV1(%#v) unexpectedly succeeded", request)
		}
	}
}

func TestReadRequestV1RejectsAcknowledgeTerminatedPayload(t *testing.T) {
	var framed bytes.Buffer
	if err := writeFrameV1(&framed, wireRequestAcknowledgeTerminatedV1, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRequestV1(&framed); err == nil || !strings.Contains(err.Error(), "must not contain a payload") {
		t.Fatalf("ReadRequestV1(acknowledge payload) error = %v", err)
	}
}

func FuzzReadRequestV1(f *testing.F) {
	f.Add([]byte{})
	var valid bytes.Buffer
	if err := WriteRequestV1(&valid, RequestV1{Kind: RequestInputV1, Bytes: []byte{0, 3, 0xff}}); err != nil {
		f.Fatal(err)
	}
	f.Add(valid.Bytes())
	f.Fuzz(func(t *testing.T, content []byte) {
		_, _ = ReadRequestV1(bytes.NewReader(content))
	})
}

func FuzzReadEventV1(f *testing.F) {
	f.Add([]byte{})
	var valid bytes.Buffer
	if err := WriteEventV1(&valid, EventV1{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "test", Message: "seed"}}); err != nil {
		f.Fatal(err)
	}
	f.Add(valid.Bytes())
	f.Fuzz(func(t *testing.T, content []byte) {
		_, _ = ReadEventV1(bytes.NewReader(content))
	})
}
