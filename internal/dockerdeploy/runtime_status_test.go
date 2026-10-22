package dockerdeploy

import (
	"bytes"
	"github.com/omry/reploy/internal/deploy"
	"strings"
	"testing"
	"time"
)

func TestWorkloadRuntimeStatusV1UsesConfirmedApplicationState(t *testing.T) {
	now := time.Date(2026, 7, 24, 17, 5, 11, 0, time.UTC)
	current := privateWorkloadContainerV1{ID: strings.Repeat("a", 64), Running: true, StartedAt: "2026-07-24T17:05:06Z"}
	expected := deploy.WorkloadLifecycleV1{Endpoint: strings.Repeat("b", 64), Runtime: strings.Repeat("c", 64)}
	record := expected
	record.ContainerID, record.StartedAt, record.State, record.HandoffComplete, record.StartupComplete = current.ID, current.StartedAt, "up", true, true
	status := workloadRuntimeStatusV1(record, true, expected, current, true, now)
	if status.Status != "up" || status.Started != "5 seconds ago" {
		t.Fatal(status)
	}
	record.State = "starting"
	if got := workloadRuntimeStatusV1(record, true, expected, current, true, now); got.Status != "starting" {
		t.Fatal(got)
	}
	record.State = "stopping"
	if got := workloadRuntimeStatusV1(record, true, expected, current, true, now); got.Status != "stopping" {
		t.Fatal(got)
	}
	current.Running = false
	if got := workloadRuntimeStatusV1(record, true, expected, current, true, now); got.Status != "down" {
		t.Fatal(got)
	}
	current.Running = true
	record.State = "up"
	record.StartedAt = "2026-07-24T17:05:01Z"
	if got := workloadRuntimeStatusV1(record, true, expected, current, true, now); got.Status != "starting" {
		t.Fatal("stale process evidence", got)
	}
	if got := workloadRuntimeStatusV1(record, false, expected, current, true, now); got.Status != "starting" {
		t.Fatal("missing startup evidence", got)
	}
}

func TestWorkloadRuntimeStatusV1UsesRecordedStartupModeAcrossPresenceChanges(t *testing.T) {
	now := time.Date(2026, 7, 24, 17, 5, 11, 0, time.UTC)
	current := privateWorkloadContainerV1{ID: strings.Repeat("a", 64), Running: true, StartedAt: "2026-07-24T17:05:06Z"}
	expected := deploy.WorkloadLifecycleV1{Endpoint: strings.Repeat("b", 64), Runtime: strings.Repeat("c", 64)}
	record := expected
	record.ContainerID, record.StartedAt, record.State, record.StartupComplete = current.ID, current.StartedAt, "up", true

	for _, test := range []struct {
		name            string
		transferClaimed bool
		handoffComplete bool
		currentPrivate  bool
		want            string
	}{
		{name: "no-secret process gains private file", currentPrivate: true, want: "up"},
		{name: "private process loses private file", transferClaimed: true, handoffComplete: true, want: "up"},
		{name: "uncertain private transfer", transferClaimed: true, currentPrivate: false, want: "starting"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := record
			candidate.TransferClaimed, candidate.HandoffComplete = test.transferClaimed, test.handoffComplete
			got := workloadRuntimeStatusV1(candidate, true, expected, current, test.currentPrivate, now)
			if got.Status != test.want {
				t.Fatalf("status = %q, want %q (%#v)", got.Status, test.want, candidate)
			}
		})
	}
}

func TestWriteRuntimeStatusV1SortsReachableEndpoints(t *testing.T) {
	var output bytes.Buffer
	err := WriteRuntimeStatusV1(&output, RuntimeStatusV1{Status: "up"}, DockerExecutionPlan{
		Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
			"web":   {Scheme: "http", PublishAddress: "127.0.0.1", PublishedPort: 8080},
			"admin": {Scheme: "https", PublishAddress: "::1", PublishedPort: 8443},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"status: up",
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
	err := WriteRuntimeStatusV1(&output, RuntimeStatusV1{Status: "down"}, DockerExecutionPlan{
		Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
			"web": {Scheme: "http", PublishAddress: "127.0.0.1", PublishedPort: 8080},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "status: down\n" {
		t.Fatalf("stopped status advertised an endpoint: %q", output.String())
	}
}
