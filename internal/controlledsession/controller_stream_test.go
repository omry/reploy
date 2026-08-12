package controlledsession

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerStreamReaderV1AcceptsExactRequests(t *testing.T) {
	input := strings.Join([]string{
		`{"schema":"reploy-controlled-session-client-v1","type":"resize","columns":120,"rows":40}`,
		`{"schema":"reploy-controlled-session-client-v1","type":"terminate"}`,
		`{"schema":"reploy-controlled-session-client-v1","type":"complete"}`,
		`{"schema":"reploy-controlled-session-client-v1","type":"acknowledge-terminated"}`,
	}, "\n") + "\n"
	reader, err := NewControllerStreamReaderV1(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []ControllerStreamRequestV1{
		{Kind: ControllerStreamRequestResizeV1, Columns: 120, Rows: 40},
		{Kind: ControllerStreamRequestTerminateV1},
		{Kind: ControllerStreamRequestCompleteV1},
		{Kind: ControllerStreamRequestAcknowledgeTerminatedV1},
	}
	for index, expected := range want {
		request, err := reader.ReadRequest()
		if err != nil || request != expected {
			t.Fatalf("request %d = %#v, %v; want %#v", index, request, err, expected)
		}
	}
	if _, err := reader.ReadRequest(); err != io.EOF {
		t.Fatalf("terminal read error = %v, want EOF", err)
	}
}

func TestControllerStreamV1PublicGoldenFixtures(t *testing.T) {
	requests, err := os.Open(filepath.Join("..", "..", "testdata", "controlled-session", "client-v1-requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer requests.Close()
	reader, err := NewControllerStreamReaderV1(requests)
	if err != nil {
		t.Fatal(err)
	}
	wantRequests := []ControllerStreamRequestV1{
		{Kind: ControllerStreamRequestResizeV1, Columns: 120, Rows: 40},
		{Kind: ControllerStreamRequestTerminateV1},
		{Kind: ControllerStreamRequestCompleteV1},
		{Kind: ControllerStreamRequestAcknowledgeTerminatedV1},
	}
	for index, want := range wantRequests {
		got, err := reader.ReadRequest()
		if err != nil || got != want {
			t.Fatalf("golden request %d = %#v, %v; want %#v", index, got, err, want)
		}
	}
	if _, err := reader.ReadRequest(); err != io.EOF {
		t.Fatalf("golden request trailer = %v, want EOF", err)
	}

	code := 0
	events := []ControllerStreamEventV1{
		{Kind: ControllerStreamEventBrokerReadyV1, TerminalSocket: "/mnt/reploy-home/reploy-controlled-session-0123456789abcdef0123456789abcdef/terminal.sock"},
		{Kind: ControllerStreamEventOpenedV1, Opened: &ControllerStreamOpenedV1{
			Operations: []OperationV1{OperationInputV1, OperationResizeV1, OperationTerminateV1, OperationCompleteV1},
			Endpoints:  []EndpointV1{{ID: "api", Scheme: "http", Host: WorkloadEndpointHostV1, Port: 8080}},
			Columns:    120, Rows: 40, OutputFinalizationTimeoutMilliseconds: DefaultOutputFinalizationTimeoutMillisecondsV1,
		}},
		{Kind: ControllerStreamEventReadyV1},
		{Kind: ControllerStreamEventWorkloadExitV1, WorkloadExit: &WorkloadExitV1{Status: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code}}},
		{Kind: ControllerStreamEventTerminatingV1, Terminating: &TerminatingV1{Cause: CauseWorkloadExitV1}},
		{Kind: ControllerStreamEventDiagnosticV1, Diagnostic: &DiagnosticV1{Code: "future_diagnostic", Message: "A generic future diagnostic."}},
		{Kind: ControllerStreamEventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: WorkloadOutputFinalizationDrainedV1}},
		{Kind: ControllerStreamEventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: WorkloadOutputFinalizationFailedV1, Reason: "Output drain expired."}},
		{Kind: ControllerStreamEventTerminatedV1, Terminated: &ResultV1{
			Cause: CauseWorkloadExitV1, WorkloadStatus: ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
			WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1},
			RuntimeObservationStatus:         RuntimeObservationStatusV1{Kind: RuntimeObservationMaintainedV1},
			ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
			CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1}, RecoveryAction: RecoveryNoneV1,
		}},
		{Kind: ControllerStreamEventClientErrorV1, ClientError: &DiagnosticV1{Code: "future_client_error", Message: "A generic future client error."}},
	}
	var output bytes.Buffer
	writer, err := NewControllerStreamWriterV1(&output)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if err := writer.WriteEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	wantEvents, err := os.ReadFile(filepath.Join("..", "..", "testdata", "controlled-session", "client-v1-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), wantEvents) {
		t.Fatalf("public event fixture mismatch:\ngot:\n%s\nwant:\n%s", output.Bytes(), wantEvents)
	}
}

func TestControllerStreamV1PublicInvalidRequestFixtureIsRejected(t *testing.T) {
	fixture, err := os.Open(filepath.Join("..", "..", "testdata", "controlled-session", "client-v1-invalid-requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	scanner := bufio.NewScanner(fixture)
	line := 0
	for scanner.Scan() {
		line++
		reader, err := NewControllerStreamReaderV1(strings.NewReader(scanner.Text() + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if request, err := reader.ReadRequest(); err == nil {
			t.Fatalf("invalid public request line %d was accepted as %#v", line, request)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if line == 0 {
		t.Fatal("invalid public request fixture is empty")
	}
}

func TestControllerStreamReaderV1RejectsNonCanonicalMessages(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "empty", message: "\n", want: "must not be empty"},
		{name: "missing newline", message: `{}`, want: "not newline terminated"},
		{name: "leading whitespace", message: ` {"schema":"reploy-controlled-session-client-v1","type":"terminate"}` + "\n", want: "exactly one JSON object"},
		{name: "duplicate", message: `{"schema":"reploy-controlled-session-client-v1","type":"terminate","type":"terminate"}` + "\n", want: "repeats field"},
		{name: "unknown field", message: `{"schema":"reploy-controlled-session-client-v1","type":"terminate","extra":true}` + "\n", want: "unknown field"},
		{name: "unknown schema", message: `{"schema":"other","type":"terminate"}` + "\n", want: "schema must be"},
		{name: "unknown type", message: `{"schema":"reploy-controlled-session-client-v1","type":"input"}` + "\n", want: "unsupported"},
		{name: "bad dimensions", message: `{"schema":"reploy-controlled-session-client-v1","type":"resize","columns":0,"rows":24}` + "\n", want: "between 1 and 65535"},
		{name: "payload on terminate", message: `{"schema":"reploy-controlled-session-client-v1","type":"terminate","columns":80}` + "\n", want: "unknown field"},
		{name: "invalid utf8", message: string([]byte{0xff, '\n'}), want: "valid UTF-8"},
		{name: "too large", message: strings.Repeat("x", MaxControllerStreamLineV1) + "\n", want: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewControllerStreamReaderV1(strings.NewReader(test.message))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.ReadRequest(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestControllerStreamWriterV1UsesExactJSONLines(t *testing.T) {
	var output bytes.Buffer
	writer, err := NewControllerStreamWriterV1(&output)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventReadyV1}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), `{"schema":"reploy-controlled-session-client-v1","type":"ready"}`+"\n"; got != want {
		t.Fatalf("ready line = %q, want %q", got, want)
	}
	output.Reset()
	opened := testOpenedV1()
	if err := writer.WriteEvent(ControllerStreamEventV1{Kind: ControllerStreamEventOpenedV1, Opened: &ControllerStreamOpenedV1{
		Operations: opened.Authorization.Operations, Endpoints: opened.Endpoints, Columns: opened.Columns, Rows: opened.Rows,
		OutputFinalizationTimeoutMilliseconds: opened.OutputFinalizationTimeoutMilliseconds,
	}}); err != nil {
		t.Fatal(err)
	}
	line := output.String()
	for _, fragment := range []string{`"type":"opened"`, `"operations":["complete","input","resize","terminate"]`, `"authorization"`} {
		if fragment == `"authorization"` {
			if strings.Contains(line, fragment) {
				t.Fatalf("opened projection leaked authorization: %s", line)
			}
			continue
		}
		if !strings.Contains(line, fragment) {
			t.Fatalf("opened projection missing %q: %s", fragment, line)
		}
	}
}
