package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunCurrentRuntimeTestV1KeepsLockThroughReadiness(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
		"z": {Scheme: "http", ProbeHost: "127.0.0.1", PublishedPort: 8080, Readiness: &blueprint.Readiness{Path: "/z"}},
		"a": {Scheme: "http", ProbeHost: "127.0.0.1", PublishedPort: 8080, Readiness: &blueprint.Readiness{Path: "/a"}},
	}}}}
	order := []string{}
	backend := currentRuntimeTestBackend(t, dir, current, planned, &order)
	var stdout bytes.Buffer
	err := runCurrentRuntimeTestV1(t.Context(), CurrentRuntimeTestInputV1{
		DeploymentDir: dir, Timeout: 17 * time.Second, Stdout: &stdout,
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acquire", "store", "state", "current", "plan", "match", "inputs", "service", "wait /a", "wait /z"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if stdout.String() != "[STAGING : demo] ok: http://127.0.0.1:8080/a\n[STAGING : demo] ok: http://127.0.0.1:8080/z\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunCurrentRuntimeTestV1StopsBeforeServiceCheckForStaleInputs(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{Endpoints: map[string]EndpointExecutionPlan{
		"http": {Readiness: &blueprint.Readiness{Path: "/ready"}},
	}}}}
	order := []string{}
	backend := currentRuntimeTestBackend(t, dir, current, planned, &order)
	want := errors.New("runtime inputs are stale; run `reploy up`")
	backend.requireInputs = func(*deploy.OperationLock, string, CurrentRuntimePlanV1) error {
		order = append(order, "inputs")
		return want
	}
	err := runCurrentRuntimeTestV1(t.Context(), CurrentRuntimeTestInputV1{DeploymentDir: dir}, backend)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "reploy up") {
		t.Fatalf("stale inputs error = %v", err)
	}
	if containsRuntimeObservationStep(order, "service", "wait /ready") {
		t.Fatalf("stale runtime test reached service checks: %v", order)
	}
}

func currentRuntimeTestBackend(t *testing.T, dir string, current CurrentBuild, planned CurrentRuntimePlanV1, order *[]string) currentRuntimeTestBackendV1 {
	t.Helper()
	var operation *deploy.OperationLock
	return currentRuntimeTestBackendV1{
		acquire: func(ctx context.Context, got string) (*deploy.OperationLock, error) {
			*order = append(*order, "acquire")
			if got != filepath.Clean(dir) {
				t.Fatalf("deployment dir = %q", got)
			}
			var err error
			operation, err = deploy.AcquireOperationLock(ctx, got)
			return operation, err
		},
		newStore: func(string) (providerstore.Store, error) {
			*order = append(*order, "store")
			return providerstore.Store{}, nil
		},
		readState: func(*deploy.OperationLock) (deploy.StateV1, bool, error) {
			*order = append(*order, "state")
			return current.State, true, nil
		},
		loadCurrent: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
			*order = append(*order, "current")
			return current, true, nil
		},
		plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			*order = append(*order, "plan")
			return planned, nil
		},
		matches: func(CurrentBuild, DockerExecutionPlan) (bool, error) {
			*order = append(*order, "match")
			return true, nil
		},
		requireInputs: func(held *deploy.OperationLock, _ string, _ CurrentRuntimePlanV1) error {
			*order = append(*order, "inputs")
			if held != operation || held.RequireHeld() != nil {
				t.Fatal("runtime input check did not hold the operation lock")
			}
			return nil
		},
		serviceCheck: func(string, string, time.Duration) error {
			*order = append(*order, "service")
			if operation.RequireHeld() != nil {
				t.Fatal("operation lock was released during service check")
			}
			return nil
		},
		wait: func(ctx context.Context, endpoint EndpointExecutionPlan, check func(context.Context) error) error {
			*order = append(*order, "wait "+endpoint.Readiness.Path)
			if operation.RequireHeld() != nil {
				t.Fatal("operation lock was released during readiness check")
			}
			if endpoint.Readiness.Timeout != 17*time.Second {
				t.Fatalf("readiness timeout = %v", endpoint.Readiness.Timeout)
			}
			return nil
		},
	}
}
