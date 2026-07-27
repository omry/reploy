package dockerdeploy

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDecodeRuntimeStatusV1ReportsExactRunningAge(t *testing.T) {
	now := time.Date(2026, 7, 24, 17, 5, 11, 0, time.UTC)
	status, err := decodeRuntimeStatusV1(
		[]byte(`{"Running":true,"Status":"running","StartedAt":"2026-07-24T17:05:06Z"}`),
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "running" || status.Started != "5 seconds ago" {
		t.Fatalf("runtime status = %#v", status)
	}
}

func TestWriteRuntimeStatusV1SortsReachableEndpoints(t *testing.T) {
	var output bytes.Buffer
	err := WriteRuntimeStatusV1(&output, RuntimeStatusV1{Status: "running"}, DockerExecutionPlan{
		Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
			"web":   {Scheme: "http", PublishAddress: "127.0.0.1", PublishedPort: 8080},
			"admin": {Scheme: "https", PublishAddress: "::1", PublishedPort: 8443},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"status: running",
		"endpoint admin: https://[::1]:8443",
		"endpoint web: http://127.0.0.1:8080",
		"",
	}, "\n")
	if output.String() != want {
		t.Fatalf("status output = %q, want %q", output.String(), want)
	}
}

func TestWriteRuntimeStatusV1DoesNotAdvertiseStoppedEndpoints(t *testing.T) {
	var output bytes.Buffer
	err := WriteRuntimeStatusV1(&output, RuntimeStatusV1{Status: "stopped"}, DockerExecutionPlan{
		Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
			"web": {Scheme: "http", PublishAddress: "127.0.0.1", PublishedPort: 8080},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "status: stopped\n" {
		t.Fatalf("stopped status advertised an endpoint: %q", output.String())
	}
}
