package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeEventEnforcesPublicReaderContract(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		wantErr string
	}{
		{
			name:    "unknown field",
			payload: `{"schema":"reploy-controlled-session-client-v1","type":"ready","future":true}`,
			wantErr: "unknown field",
		},
		{
			name:    "multiple objects",
			payload: `{"schema":"reploy-controlled-session-client-v1","type":"ready"}{}`,
			wantErr: "trailing token",
		},
		{
			name:    "unknown nested field",
			payload: `{"schema":"reploy-controlled-session-client-v1","type":"workload-exit","status":{"kind":"exited","code":0,"future":true}}`,
			wantErr: "unknown field",
		},
		{
			name:    "duplicate field",
			payload: `{"schema":"reploy-controlled-session-client-v1","type":"ready","type":"ready"}`,
			wantErr: "repeats field",
		},
		{
			name:    "unknown event type",
			payload: `{"schema":"reploy-controlled-session-client-v1","type":"future-event"}`,
			wantErr: "unsupported",
		},
		{
			name:    "field from another event type",
			payload: `{"schema":"reploy-controlled-session-client-v1","type":"ready","code":"wrong_shape"}`,
			wantErr: "unknown field",
		},
		{
			name:    "case variant field",
			payload: `{"schema":"reploy-controlled-session-client-v1","Type":"ready"}`,
			wantErr: "lowercase ASCII snake_case",
		},
		{
			name:    "malformed diagnostic code",
			payload: `{"schema":"reploy-controlled-session-client-v1","type":"diagnostic","code":"Not Valid","message":"bad code"}`,
			wantErr: "code \"Not Valid\" is invalid",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeEvent([]byte(test.payload)); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("decodeEvent() error = %v, want %q", err, test.wantErr)
			}
		})
	}

	value, err := decodeEvent([]byte(`{"schema":"reploy-controlled-session-client-v1","type":"client-error","code":"future_code","message":"future diagnostic"}`))
	if err != nil || value.Type != "client-error" || value.Code != "future_code" {
		t.Fatalf("open diagnostic code event = %#v, %v", value, err)
	}
}

func TestDecodeEventAcceptsPublicGoldenShapes(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "testdata", "controlled-session", "client-v1-events.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		if _, err := decodeEvent(scanner.Bytes()); err != nil {
			t.Fatalf("decode public event line %d: %v", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if line == 0 {
		t.Fatal("public event fixture is empty")
	}
}
