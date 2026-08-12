package controlledsession

import (
	"bytes"
	"io"
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
