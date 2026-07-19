package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func lifecycleTestInputs() (blueprint.Document, DockerExecutionPlan, map[string]providers.ExecutableOutput) {
	document := commandTestDocument()
	document.Environment.Workload = &blueprint.Workload{
		Command: "serve",
		Runtime: blueprint.RuntimeEvents{
			BeforeStart: []blueprint.Step{{Actions: []blueprint.Action{{Environment: []string{"config", "show", "--", "offline"}}}}},
			AfterStart:  []blueprint.Step{{Requires: blueprint.Requirements{Endpoints: []string{"http"}}, Actions: []blueprint.Action{{Environment: []string{"serve", "--verbose"}}}}},
			BeforeStop:  []blueprint.Step{{Actions: []blueprint.Action{{Environment: []string{"config", "show"}}}}},
		},
	}
	document.Environment.Install.AfterInstall = []blueprint.Step{{Actions: []blueprint.Action{{Environment: []string{"config", "show", "--", "installed"}}}}}
	document.Environment.Install.Success.Lines = []string{"installed {{ environment.id }} at {{ reploy.workload.endpoints.http.publish.address }}:{{ reploy.workload.endpoints.http.publish.port }}"}
	document.Environment.ID = "demo"
	readiness := &blueprint.Readiness{Path: "/ready", Timeout: time.Second, Interval: time.Millisecond}
	plan := DockerExecutionPlan{Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{"http": {Scheme: "http", ProbeHost: "127.0.0.1", PublishAddress: "127.0.0.1", PublishedPort: 8080, Readiness: readiness}}}}
	outputs := map[string]providers.ExecutableOutput{"server": {Component: "application", Binary: "demo", ImagePath: "/opt/demo"}}
	return document, plan, outputs
}

func TestPlanInstallLifecycleOrdersEvents(t *testing.T) {
	document, dockerPlan, outputs := lifecycleTestInputs()
	plan, err := PlanInstallLifecycle(document, dockerPlan, outputs, true)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]LifecycleOperationKind, len(plan.Operations))
	for index, operation := range plan.Operations {
		kinds[index] = operation.Kind
	}
	want := []LifecycleOperationKind{LifecycleMaterialize, LifecycleCommand, LifecycleCommand, LifecycleStart, LifecycleReadiness, LifecycleCommand, LifecycleSuccess}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %#v", kinds)
	}
	if got := plan.Operations[len(plan.Operations)-1].Lines[0]; got != "installed demo at 127.0.0.1:8080" {
		t.Fatalf("success line = %q", got)
	}
}

func TestPlanRestartLifecycleStopsBeforeStarting(t *testing.T) {
	document, dockerPlan, outputs := lifecycleTestInputs()
	plan, err := PlanRestartLifecycle(document, dockerPlan, outputs)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]LifecycleOperationKind, len(plan.Operations))
	for index, operation := range plan.Operations {
		kinds[index] = operation.Kind
	}
	want := []LifecycleOperationKind{
		LifecycleCommand,
		LifecycleStop,
		LifecycleCommand,
		LifecycleStart,
		LifecycleReadiness,
		LifecycleCommand,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %#v, want %#v", kinds, want)
	}
}

func TestExecuteLifecycleStopsAtFirstFailure(t *testing.T) {
	called := []string{}
	err := ExecuteLifecycle(context.Background(), LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleStart, Event: "start"}, {Kind: LifecycleStop, Event: "stop"},
	}}, LifecycleExecutor{
		Start: func(context.Context) error { called = append(called, "start"); return fmt.Errorf("boom") },
		Stop:  func(context.Context) error { called = append(called, "stop"); return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "boom") || !reflect.DeepEqual(called, []string{"start"}) {
		t.Fatalf("error/called = %v / %#v", err, called)
	}
}

func TestWaitForHTTPReadinessStatusAndRedirectRules(t *testing.T) {
	transport := &sequenceReadinessTransport{statuses: []int{http.StatusServiceUnavailable, http.StatusOK}}
	endpoint := EndpointExecutionPlan{Readiness: &blueprint.Readiness{Timeout: time.Second, Interval: time.Millisecond}}
	if err := waitForHTTPReadiness(context.Background(), endpoint, "http://127.0.0.1:8080/ready", &http.Client{Transport: transport}); err != nil {
		t.Fatal(err)
	}
	if transport.attempts != 2 {
		t.Fatalf("attempts = %d", transport.attempts)
	}
	endpoint.Readiness.Timeout = 15 * time.Millisecond
	redirect := &sequenceReadinessTransport{statuses: []int{http.StatusFound}}
	if err := waitForHTTPReadiness(context.Background(), endpoint, "http://127.0.0.1:8080/ready", &http.Client{Transport: redirect}); err == nil || !strings.Contains(err.Error(), "HTTP status 302") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestWaitForHTTPReadinessFailsWhenWorkloadExits(t *testing.T) {
	transport := &sequenceReadinessTransport{statuses: []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable}}
	endpoint := EndpointExecutionPlan{Readiness: &blueprint.Readiness{Timeout: time.Second, Interval: time.Millisecond}}
	checks := 0
	err := waitForHTTPReadinessWithServiceCheck(
		context.Background(), endpoint, "http://127.0.0.1:8080/ready", &http.Client{Transport: transport},
		func(context.Context) error {
			checks++
			if checks > 1 {
				return fmt.Errorf("service is exited (2)")
			}
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "left running state") || !strings.Contains(err.Error(), "exited (2)") {
		t.Fatalf("error = %v", err)
	}
	if transport.attempts != 1 {
		t.Fatalf("HTTP attempts = %d, want readiness to stop before the second request", transport.attempts)
	}
}

func TestWaitForHTTPSReadinessDefaultsToUnverifiedLocalTLS(t *testing.T) {
	unverified := readinessHTTPClient(&blueprint.Readiness{TLSVerify: false})
	if !unverified.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("local TLS readiness should default to verification disabled")
	}
	verified := readinessHTTPClient(&blueprint.Readiness{TLSVerify: true})
	if verified.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("explicit TLS verification was ignored")
	}
	request := &http.Request{}
	if err := unverified.CheckRedirect(request, []*http.Request{{}}); err != http.ErrUseLastResponse {
		t.Fatalf("redirect policy = %v", err)
	}
}

type sequenceReadinessTransport struct {
	statuses []int
	attempts int
}

func (transport *sequenceReadinessTransport) RoundTrip(*http.Request) (*http.Response, error) {
	index := transport.attempts
	transport.attempts++
	if index >= len(transport.statuses) {
		index = len(transport.statuses) - 1
	}
	if index < 0 {
		return nil, fmt.Errorf("no response configured")
	}
	return &http.Response{StatusCode: transport.statuses[index], Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}
