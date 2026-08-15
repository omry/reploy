package blueprint

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResolvedDocumentV1RoundTripsNumericAndVariableValues(t *testing.T) {
	document := Document{
		Blueprint: Metadata{Schema: 1, Version: "1.0.0"},
		Environment: Environment{
			ID: "demo", Vars: map[string]any{"port": 8080, "enabled": true},
			Workload: &Workload{Command: "serve", Endpoints: map[string]Endpoint{
				"http": {Port: 8080, Readiness: &Readiness{Timeout: 31 * time.Second, Interval: time.Second}},
			}},
		},
	}
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeResolvedDocumentV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Environment.Workload.Endpoints["http"].Port != 8080 || decoded.Environment.Workload.Endpoints["http"].Readiness.Timeout != 31*time.Second {
		t.Fatalf("decoded document = %#v", decoded)
	}
	reencoded, err := EncodeResolvedDocumentV1(decoded)
	if err != nil || reencoded != payload {
		t.Fatalf("reencoded payload differs: %v", err)
	}
	first, err := ResolvedDocumentDigestV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DocumentDigestV1(decoded)
	if err != nil || first != second {
		t.Fatalf("digests = %q, %q, %v", first, second, err)
	}
}

func TestResolvedDocumentV1PreservesSparseCommandMountOverrides(t *testing.T) {
	document := Document{Environment: Environment{
		ID: "demo",
		Commands: map[string]Command{
			"plain": {Executable: "application.server"},
			"mutate": {
				Executable: "application.server",
				Mounts:     map[string]CommandMountOverride{"config": {Writable: true}},
			},
		},
	}}
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	var encoded struct {
		Document struct {
			Environment struct {
				Commands map[string]map[string]json.RawMessage
			}
		}
	}
	if err := json.Unmarshal([]byte(payload), &encoded); err != nil {
		t.Fatal(err)
	}
	if _, found := encoded.Document.Environment.Commands["plain"]["Mounts"]; found {
		t.Fatalf("plain command encoded an empty mount override: %s", payload)
	}
	if _, found := encoded.Document.Environment.Commands["mutate"]["Mounts"]; !found {
		t.Fatalf("mutating command omitted its mount override: %s", payload)
	}
	decoded, err := DecodeResolvedDocumentV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Environment.Commands["mutate"].Mounts["config"].Writable {
		t.Fatalf("decoded commands = %#v", decoded.Environment.Commands)
	}
}

func TestDecodeResolvedDocumentV1RejectsUnknownAndNoncanonicalData(t *testing.T) {
	for _, payload := range []ResolvedDocumentV1{
		`{"schema":"blueprint-resolved-v2","document":{}}`,
		`{"schema":"blueprint-resolved-v1","document":{},"extra":true}`,
		` {"document":{},"schema":"blueprint-resolved-v1"}`,
	} {
		if _, err := DecodeResolvedDocumentV1(payload); err == nil {
			t.Fatalf("payload unexpectedly accepted: %s", payload)
		}
	}
	if _, err := DecodeResolvedDocumentV1(""); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty payload error = %v", err)
	}
}

func TestDecodeResolvedDocumentV1NamesEnvironmentOnInvalidDocument(t *testing.T) {
	payload := ResolvedDocumentV1(
		`{"schema":"blueprint-resolved-v1","document":{"Environment":{"ID":"demo","RunAs":{}}}}`,
	)
	_, err := DecodeResolvedDocumentV1(payload)
	if err == nil || !strings.Contains(err.Error(), `resolved blueprint for environment "demo"`) ||
		!strings.Contains(err.Error(), `unknown field "RunAs"`) {
		t.Fatalf("invalid resolved blueprint error = %v", err)
	}
}

func TestDecodeResolvedDocumentV1NamesEnvironmentOnPostDecodeFailures(t *testing.T) {
	payload, err := EncodeResolvedDocumentV1(Document{Environment: Environment{ID: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		payload ResolvedDocumentV1
	}{
		{name: "trailing JSON", payload: payload + `{}`},
		{name: "schema", payload: ResolvedDocumentV1(strings.Replace(string(payload), ResolvedDocumentSchemaV1, "blueprint-resolved-v2", 1))},
		{name: "canonical wire form", payload: " " + payload},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeResolvedDocumentV1(test.payload)
			if err == nil || !strings.Contains(err.Error(), `decode resolved blueprint for environment "demo"`) {
				t.Fatalf("invalid resolved blueprint error = %v", err)
			}
		})
	}
}
