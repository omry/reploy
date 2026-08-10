package controlledsession

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestProtocolV2RequestsRoundTripBinarySafeFrames(t *testing.T) {
	requests := []RequestV1{
		{Kind: RequestInputV1, Bytes: []byte{0, 3, '\n', 0xff}},
		{Kind: RequestResizeV1, Columns: 120, Rows: 40},
		{Kind: RequestTerminateV1},
		{Kind: RequestCompleteV1},
		{Kind: RequestAcknowledgeTerminatedV1},
	}
	var stream bytes.Buffer
	for _, request := range requests {
		if err := WriteRequestV2(&stream, request); err != nil {
			t.Fatalf("WriteRequestV2(%s) error = %v", request.Kind, err)
		}
	}
	for _, want := range requests {
		got, err := ReadRequestV2(&stream)
		if err != nil {
			t.Fatalf("ReadRequestV2(%s) error = %v", want.Kind, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadRequestV2() = %#v, want %#v", got, want)
		}
	}
}

func TestProtocolV2EventsRoundTripStrictTypedFrames(t *testing.T) {
	code := 0
	authorization := testAuthorizationV1()
	events := []EventV1{
		{Kind: EventOpenedV1, Opened: &OpenedV2{
			Authorization: authorization, Endpoints: testEndpointsV1(), Columns: 80, Rows: 24,
			OutputFinalizationTimeoutMilliseconds: DefaultOutputFinalizationTimeoutMillisecondsV1,
		}},
		{Kind: EventOutputV1, Bytes: []byte{0, '\n', 0xff}},
		{Kind: EventWorkloadExitV1, WorkloadExit: &WorkloadExitV1{Status: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code}}},
		{Kind: EventTerminatingV1, Terminating: &TerminatingV1{Cause: CauseWorkloadExitV1}},
		{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "cleanup_failed", Message: "container removal failed"}},
		{Kind: EventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: WorkloadOutputFinalizationDrainedV1}},
		{Kind: EventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: WorkloadOutputFinalizationFailedV1, Reason: "output deadline expired"}},
		{Kind: EventTerminatedV1, Terminated: &ResultV1{
			Cause: CauseWorkloadExitV1, WorkloadStatus: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
			WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1},
			RuntimeObservationStatus:         RuntimeObservationStatusV1{Kind: RuntimeObservationMaintainedV1},
			ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
			CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
		}},
		{Kind: EventTerminatedV1, Terminated: &ResultV1{
			Cause: CauseWorkloadExitV1, WorkloadStatus: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
			WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1},
			RuntimeObservationStatus:         RuntimeObservationStatusV1{Kind: RuntimeObservationLostV1, Reason: "docker unavailable"},
			ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
			CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
		}},
	}
	var stream bytes.Buffer
	for _, event := range events {
		if err := WriteEventV2(&stream, event); err != nil {
			t.Fatalf("WriteEventV2(%s) error = %v", event.Kind, err)
		}
	}
	for _, want := range events {
		got, err := ReadEventV2(&stream)
		if err != nil {
			t.Fatalf("ReadEventV2(%s) error = %v", want.Kind, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadEventV2() = %#v, want %#v", got, want)
		}
	}
}

func TestProtocolV2RejectsWrongDirectionAndUnboundedInput(t *testing.T) {
	var event bytes.Buffer
	if err := WriteEventV2(&event, EventV1{Kind: EventOutputV1, Bytes: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRequestV2(&event); err == nil || !strings.Contains(err.Error(), "not a controller request") {
		t.Fatalf("ReadRequestV2(event) error = %v", err)
	}

	header := make([]byte, frameHeaderSizeV1)
	copy(header, frameMagicV1[:])
	header[4] = ProtocolVersionV2
	header[5] = byte(wireRequestInputV1)
	binary.BigEndian.PutUint32(header[6:], MaxFramePayloadV1+1)
	if _, err := ReadRequestV2(bytes.NewReader(header)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadRequestV2(oversized) error = %v", err)
	}
}

func TestProtocolV2RejectsBadMagicVersionTruncationAndUnknownJSON(t *testing.T) {
	validHeader := func() []byte {
		header := make([]byte, frameHeaderSizeV1)
		copy(header, frameMagicV1[:])
		header[4] = ProtocolVersionV2
		header[5] = byte(wireRequestCompleteV1)
		return header
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "bad magic", data: append([]byte("NOPE"), validHeader()[4:]...), want: "magic"},
		{name: "old version", data: func() []byte { value := validHeader(); value[4] = ProtocolVersionV1; return value }(), want: "version 1"},
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
			if _, err := ReadRequestV2(bytes.NewReader(test.data)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadRequestV2() error = %v, want containing %q", err, test.want)
			}
		})
	}

	payload := []byte(`{"code":"bad","message":"failure","extra":true}`)
	var framed bytes.Buffer
	if err := writeFrameV2(&framed, wireEventDiagnosticV1, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV2(&framed); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ReadEventV2(unknown JSON) error = %v", err)
	}

	duplicate := []byte(`{"code":"first","code":"second","message":"failure"}`)
	framed.Reset()
	if err := writeFrameV2(&framed, wireEventDiagnosticV1, duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV2(&framed); err == nil || !strings.Contains(err.Error(), "repeats field") {
		t.Fatalf("ReadEventV2(duplicate JSON) error = %v", err)
	}

	caseVariant := []byte(`{"Code":"bad","message":"failure"}`)
	framed.Reset()
	if err := writeFrameV2(&framed, wireEventDiagnosticV1, caseVariant); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV2(&framed); err == nil || !strings.Contains(err.Error(), "lowercase ASCII snake_case") {
		t.Fatalf("ReadEventV2(case-variant JSON) error = %v", err)
	}

	caseVariantDuplicate := []byte(`{"code":"first","Code":"second","message":"failure"}`)
	framed.Reset()
	if err := writeFrameV2(&framed, wireEventDiagnosticV1, caseVariantDuplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV2(&framed); err == nil || !strings.Contains(err.Error(), "lowercase ASCII snake_case") {
		t.Fatalf("ReadEventV2(case-variant duplicate JSON) error = %v", err)
	}

	nestedCaseVariant := []byte(`{"cause":"workload-exit","workload_status":{"Kind":"exited","code":0},"workload_output_finalization_status":{"kind":"drained"},"runtime_observation_status":{"kind":"maintained"},"controller_finalization_status":{"kind":"completed"},"cleanup_status":{"kind":"succeeded"},"recovery_action":"none"}`)
	framed.Reset()
	if err := writeFrameV2(&framed, wireEventTerminatedV1, nestedCaseVariant); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV2(&framed); err == nil || !strings.Contains(err.Error(), "lowercase ASCII snake_case") {
		t.Fatalf("ReadEventV2(nested case-variant JSON) error = %v", err)
	}
}

func TestWireKindAssignmentsV1AreStable(t *testing.T) {
	tests := []struct {
		name string
		got  wireKindV1
		want byte
	}{
		{name: "request input", got: wireRequestInputV1, want: 0x01},
		{name: "request resize", got: wireRequestResizeV1, want: 0x02},
		{name: "request terminate", got: wireRequestTerminateV1, want: 0x03},
		{name: "request complete", got: wireRequestCompleteV1, want: 0x04},
		{name: "request acknowledge terminated", got: wireRequestAcknowledgeTerminatedV1, want: 0x05},
		{name: "event opened", got: wireEventOpenedV1, want: 0x81},
		{name: "event output", got: wireEventOutputV1, want: 0x82},
		{name: "event workload exit", got: wireEventWorkloadExitV1, want: 0x83},
		{name: "event terminating", got: wireEventTerminatingV1, want: 0x84},
		{name: "event diagnostic", got: wireEventDiagnosticV1, want: 0x85},
		{name: "event terminated", got: wireEventTerminatedV1, want: 0x86},
		{name: "event workload outputs finalized", got: wireEventWorkloadOutputsFinalizedV1, want: 0x87},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if byte(test.got) != test.want {
				t.Fatalf("wire kind = 0x%02x, want 0x%02x", byte(test.got), test.want)
			}
		})
	}

	var framed bytes.Buffer
	if err := WriteRequestV2(&framed, RequestV1{Kind: RequestCompleteV1}); err != nil {
		t.Fatal(err)
	}
	want := []byte{'R', 'P', 'S', 'N', ProtocolVersionV2, 0x04, 0, 0, 0, 0}
	if !bytes.Equal(framed.Bytes(), want) {
		t.Fatalf("complete frame = %x, want %x", framed.Bytes(), want)
	}
}

func TestProtocolV2ExpandedOpenedDoesNotReuseV1WireVersion(t *testing.T) {
	authorization := testAuthorizationV1()
	var framed bytes.Buffer
	if err := WriteEventV2(&framed, EventV1{Kind: EventOpenedV1, Opened: &OpenedV2{
		Authorization: authorization, Endpoints: testEndpointsV1(), Columns: 80, Rows: 24,
		OutputFinalizationTimeoutMilliseconds: DefaultOutputFinalizationTimeoutMillisecondsV1,
	}}); err != nil {
		t.Fatal(err)
	}
	if got := framed.Bytes()[4]; got != ProtocolVersionV2 {
		t.Fatalf("expanded opened protocol version = %d, want %d", got, ProtocolVersionV2)
	}

	oldVersion := append([]byte(nil), framed.Bytes()...)
	oldVersion[4] = ProtocolVersionV1
	if _, err := ReadEventV2(bytes.NewReader(oldVersion)); err == nil || !strings.Contains(err.Error(), "version 1") {
		t.Fatalf("ReadEventV2(v1 expanded opened) error = %v", err)
	}
}

func TestValidateEndpointV2PreservesValidDeclaredURISchemes(t *testing.T) {
	for _, scheme := range []string{"HTTP", "grpc+unix", "A" + strings.Repeat("b", 64)} {
		endpoint := EndpointV2{ID: "browser", Scheme: scheme, Host: WorkloadEndpointHostV2, Port: 8080}
		if err := ValidateEndpointV2(endpoint); err != nil {
			t.Fatalf("ValidateEndpointV2(%q) error = %v", scheme, err)
		}
	}
	for _, scheme := range []string{"", "1http", "http scheme"} {
		endpoint := EndpointV2{ID: "browser", Scheme: scheme, Host: WorkloadEndpointHostV2, Port: 8080}
		if err := ValidateEndpointV2(endpoint); err == nil || !strings.Contains(err.Error(), "URI-scheme") {
			t.Fatalf("ValidateEndpointV2(%q) error = %v", scheme, err)
		}
	}
}

func TestValidateEventV1RejectsInvalidOutputFinalizationOutcomes(t *testing.T) {
	code := 0
	tests := []EventV1{
		{Kind: EventOpenedV1, Opened: &OpenedV2{Authorization: testAuthorizationV1(), Endpoints: testEndpointsV1(), Columns: 80, Rows: 24}},
		{Kind: EventWorkloadOutputsFinalizedV1},
		{Kind: EventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: WorkloadOutputFinalizationDrainedV1, Reason: "unexpected"}},
		{Kind: EventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: WorkloadOutputFinalizationFailedV1}},
		{Kind: EventTerminatedV1, Terminated: &ResultV1{
			Cause: CauseWorkloadExitV1, WorkloadStatus: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
			ControllerFinalizationStatus: ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
			CleanupStatus:                CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
		}},
	}
	for _, event := range tests {
		if err := ValidateEventV1(event); err == nil {
			t.Fatalf("ValidateEventV1(%#v) unexpectedly succeeded", event)
		}
	}
}

func TestReadEventV2RejectsWorkloadExitWithoutStatus(t *testing.T) {
	result := ResultV1{
		Cause:                            CauseWorkloadExitV1,
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusUnknownV1},
		WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1},
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var framed bytes.Buffer
	if err := writeFrameV2(&framed, wireEventTerminatedV1, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEventV2(&framed); err == nil || !strings.Contains(err.Error(), "workload-exit termination requires a known workload status") {
		t.Fatalf("ReadEventV2(workload exit without status) error = %v", err)
	}
}

func TestReadEventV2RejectsContradictoryRuntimeObservationLossResults(t *testing.T) {
	code := 0
	valid := ResultV1{
		Cause:                            CauseRuntimeObservationLostV1,
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationFailedV1, Reason: "runtime observation lost"},
		RuntimeObservationStatus:         RuntimeObservationStatusV1{Kind: RuntimeObservationLostV1, Reason: "docker unavailable"},
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationLostV1, Reason: "docker unavailable"},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	}

	tests := []struct {
		name      string
		wantError string
		mutate    func(*ResultV1)
	}{
		{
			name:      "runtime observation maintained",
			wantError: "runtime-observation-loss termination requires lost runtime observation status",
			mutate: func(result *ResultV1) {
				result.RuntimeObservationStatus = RuntimeObservationStatusV1{Kind: RuntimeObservationMaintainedV1}
			},
		},
		{
			name:      "runtime loss with outputs drained",
			wantError: "runtime-observation-loss termination requires failed workload output finalization",
			mutate: func(result *ResultV1) {
				result.WorkloadOutputFinalizationStatus = WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}
			},
		},
		{
			name:      "controller loss with completed finalization",
			wantError: "controller-loss termination requires lost controller finalization status",
			mutate: func(result *ResultV1) {
				result.Cause = CauseControllerLostV1
				result.ControllerFinalizationStatus = ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1}
			},
		},
		{
			name:      "startup failure without startup-failed finalization",
			wantError: "startup-failure termination requires startup-failed controller finalization status",
			mutate: func(result *ResultV1) {
				result.Cause = CauseStartupFailureV1
				result.ControllerFinalizationStatus = ControllerFinalizationStatusV1{Kind: ControllerFinalizationNotCompletedV1}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid
			test.mutate(&result)
			payload, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var framed bytes.Buffer
			if err := writeFrameV2(&framed, wireEventTerminatedV1, payload); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadEventV2(&framed); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ReadEventV2(contradictory terminated result) error = %v", err)
			}
		})
	}
}

func TestValidateRequestV1RejectsAmbiguousUnionPayloads(t *testing.T) {
	tests := []RequestV1{
		{Kind: RequestInputV1},
		{Kind: RequestResizeV1, Columns: 80},
		{Kind: RequestCompleteV1, Bytes: []byte{}},
		{Kind: RequestAcknowledgeTerminatedV1, Columns: 1},
	}
	for _, request := range tests {
		if err := ValidateRequestV1(request); err == nil {
			t.Fatalf("ValidateRequestV1(%#v) unexpectedly succeeded", request)
		}
	}
}

func TestValidateEventV1RejectsInvalidProtocolCodes(t *testing.T) {
	tests := []string{
		"",
		"resource-exhausted",
		"Resource_exhausted",
		"resource__exhausted",
		"resource_exhausted_",
		strings.Repeat("a", maxProtocolCodeLengthV1+1),
	}
	for _, code := range tests {
		event := EventV1{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: code, Message: "failure"}}
		if err := ValidateEventV1(event); err == nil {
			t.Fatalf("ValidateEventV1(diagnostic code %q) unexpectedly succeeded", code)
		}
	}
}

func TestReadRequestV2RejectsAcknowledgeTerminatedPayload(t *testing.T) {
	var framed bytes.Buffer
	if err := writeFrameV2(&framed, wireRequestAcknowledgeTerminatedV1, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRequestV2(&framed); err == nil || !strings.Contains(err.Error(), "must not contain a payload") {
		t.Fatalf("ReadRequestV2(acknowledge payload) error = %v", err)
	}
}

func FuzzReadRequestV2(f *testing.F) {
	f.Add([]byte{})
	var valid bytes.Buffer
	if err := WriteRequestV2(&valid, RequestV1{Kind: RequestInputV1, Bytes: []byte{0, 3, 0xff}}); err != nil {
		f.Fatal(err)
	}
	f.Add(valid.Bytes())
	f.Fuzz(func(t *testing.T, content []byte) {
		_, _ = ReadRequestV2(bytes.NewReader(content))
	})
}

func FuzzReadEventV2(f *testing.F) {
	f.Add([]byte{})
	var valid bytes.Buffer
	if err := WriteEventV2(&valid, EventV1{Kind: EventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "test", Message: "seed"}}); err != nil {
		f.Fatal(err)
	}
	f.Add(valid.Bytes())
	f.Fuzz(func(t *testing.T, content []byte) {
		_, _ = ReadEventV2(bytes.NewReader(content))
	})
}
