package blueprint

import (
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
