package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunCurrentWorkloadLifecycleV1GatesEveryCreatedContainer(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	command := ResolvedEnvironmentCommand{Name: "check", Argv: []string{"/opt/check"}}
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleCommand, Event: "before_start", Command: &command},
		{Kind: LifecycleStart, Event: "start"},
		{Kind: LifecycleReadiness, Event: "after_start", Endpoint: &EndpointExecutionPlan{}},
		{Kind: LifecycleCommand, Event: "after_start", Command: &command},
	}}
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "up",
		Plan: CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"plan start",
		"gate command/check", "prepare probe", "transient check", "cleanup", "temporary", "run transient", "cleanup probe",
		"gate workload", "command up", "run compose-up", "service check",
		"readiness", "service check",
		"gate command/check", "prepare probe", "transient check", "cleanup", "temporary", "run transient", "cleanup probe",
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("lifecycle order = %v, want %v", order, want)
	}
}

func TestRunCurrentWorkloadLifecycleV1DoesNotGateStopButGatesRestartStart(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleStop, Event: "stop"},
		{Kind: LifecycleStart, Event: "start"},
	}}
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "restart",
		Plan: CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"plan restart", "command down", "run compose-down", "gate workload", "command up", "run compose-up", "service check"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("restart order = %v, want %v", order, want)
	}
}

func TestRunCurrentWorkloadLifecycleV1LocksEachDockerMutationWhenCallerReleasedBroadLock(t *testing.T) {
	dir := t.TempDir()
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleStop, Event: "stop"},
		{Kind: LifecycleStart, Event: "start"},
	}}
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
	backend.acquire = func(ctx context.Context, got string) (*deploy.OperationLock, error) {
		if got != dir {
			t.Fatalf("lock directory = %q", got)
		}
		order = append(order, "acquire")
		return deploy.AcquireOperationLock(ctx, got)
	}
	err := runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Environment: "demo", DeploymentDir: dir, Action: "restart",
		Plan: CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"plan restart", "command down", "acquire", "run compose-down",
		"acquire", "gate workload", "command up", "run compose-up", "service check",
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("per-mutation lock order = %v, want %v", order, want)
	}
}

func TestRunCurrentWorkloadLifecycleV1UsesOwnedSystemCommands(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleStop, Event: "stop"},
		{Kind: LifecycleStart, Event: "start"},
	}}
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
	start := CommandSpec{Name: "/usr/bin/systemctl", Args: []string{"start", "demo.service"}}
	stop := CommandSpec{Name: "/usr/bin/systemctl", Args: []string{"stop", "demo.service"}}
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "restart",
		StartCommand: &start, StopCommand: &stop,
		Plan: CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"plan restart", "run /usr/bin/systemctl", "gate workload", "run /usr/bin/systemctl", "service check"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("system lifecycle order = %v, want %v", order, want)
	}
}

func TestRunCurrentWorkloadLifecycleV1RecoversStaleNetworkOnceForUp(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleStart, Event: "start"}}}
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
	var progress bytes.Buffer
	attempts := 0
	backend.runCommand = func(spec CommandSpec, _ RunOptions) error {
		order = append(order, "run "+spec.Name)
		if spec.Name == "compose-up" {
			attempts++
			if attempts == 1 {
				return errors.New("network demo not found")
			}
		}
		return nil
	}
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "up",
		Progress: &progress,
		Plan:     CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"plan start",
		"gate workload", "command up", "run compose-up",
		"command down", "run compose-down",
		"gate workload", "run compose-up", "service check",
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("stale-network recovery = %v, want %v", order, want)
	}
	if progress.String() != "detected stale Docker network state; running down --remove-orphans and retrying up\n" {
		t.Fatalf("stale-network progress = %q", progress.String())
	}
}

func TestRunCurrentWorkloadLifecycleV1StopsWhenStaleNetworkCleanupFails(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleStart, Event: "start"}}}
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
	want := errors.New("cleanup failed")
	backend.runCommand = func(spec CommandSpec, _ RunOptions) error {
		order = append(order, "run "+spec.Name)
		if spec.Name == "compose-up" {
			return errors.New("network demo not found")
		}
		return want
	}
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "up",
		Plan: CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "recover stale Docker network state") {
		t.Fatalf("cleanup failure = %v", err)
	}
	if got := strings.Join(order, ","); strings.Count(got, "run compose-up") != 1 || strings.Count(got, "run compose-down") != 1 {
		t.Fatalf("cleanup failure order = %v", order)
	}
}

func TestRunCurrentWorkloadLifecycleV1RetriesStaleNetworkOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleStart, Event: "start"}}}
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
	attempts := 0
	want := errors.New("network retry not found")
	backend.runCommand = func(spec CommandSpec, _ RunOptions) error {
		order = append(order, "run "+spec.Name)
		if spec.Name == "compose-up" {
			attempts++
			if attempts == 1 {
				return errors.New("network demo not found")
			}
			return want
		}
		return nil
	}
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "up",
		Plan: CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if !errors.Is(err, want) {
		t.Fatalf("second start failure = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("start attempts = %d, want 2", attempts)
	}
}

func TestRunCurrentWorkloadLifecycleV1PlansStandaloneDown(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	lifecycle := LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleStop, Event: "stop"}}}
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, lifecycle, &order)
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "down",
		Plan: CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"plan stop", "command down", "run compose-down"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("down order = %v, want %v", order, want)
	}
}

func TestRunCurrentWorkloadLifecycleV1UsesOwnedStopCommand(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	order := []string{}
	backend := currentWorkloadLifecycleTestBackend(t, LifecyclePlan{Operations: []LifecycleOperation{{Kind: LifecycleStop, Event: "stop"}}}, &order)
	stop := CommandSpec{Name: "/usr/bin/systemctl", Args: []string{"stop", "demo.service"}}
	err = runCurrentWorkloadLifecycleV1(t.Context(), CurrentWorkloadLifecycleInputV1{
		Operation: operation, Environment: "demo", DeploymentDir: dir, Action: "down", StopCommand: &stop,
		Plan: CurrentRuntimePlanV1{Docker: DockerExecutionPlan{ContainerName: "demo"}},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"plan stop", "run /usr/bin/systemctl"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("owned stop order = %v, want %v", order, want)
	}
}

func currentWorkloadLifecycleTestBackend(t *testing.T, lifecycle LifecyclePlan, order *[]string) currentWorkloadLifecycleBackendV1 {
	t.Helper()
	return currentWorkloadLifecycleBackendV1{
		acquire: func(context.Context, string) (*deploy.OperationLock, error) {
			t.Fatal("unexpected operation-lock acquisition")
			return nil, nil
		},
		planStart: func(CurrentRuntimePlanV1, CurrentBuild) (LifecyclePlan, error) {
			*order = append(*order, "plan start")
			return lifecycle, nil
		},
		planStop: func(CurrentRuntimePlanV1, CurrentBuild) (LifecyclePlan, error) {
			*order = append(*order, "plan stop")
			return lifecycle, nil
		},
		planRestart: func(CurrentRuntimePlanV1, CurrentBuild) (LifecyclePlan, error) {
			*order = append(*order, "plan restart")
			return lifecycle, nil
		},
		execute: ExecuteLifecycle,
		runPublished: func(ctx context.Context, input PublishedRuntimeContainerInput, run PublishedRuntimeContainerRunnerV1) error {
			*order = append(*order, "gate "+input.Invocation.PlanID)
			return run(ctx, CurrentBuild{})
		},
		command: func(_ string, action string) (CommandSpec, error) {
			*order = append(*order, "command "+action)
			return CommandSpec{Name: "compose-" + action}, nil
		},
		prepareProbe: func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
			*order = append(*order, "prepare probe")
			return PreparedProbeWorkspace{}, func() error {
				*order = append(*order, "cleanup probe")
				return nil
			}, nil
		},
		transient: func(_ DockerExecutionPlan, command ResolvedEnvironmentCommand, _ PreparedProbeWorkspace, _ *transientOutputMount, _ bool, _ bool) (CommandSpec, error) {
			*order = append(*order, "transient "+command.Name)
			return CommandSpec{Name: "transient"}, nil
		},
		cleanup: func(name string) CommandSpec {
			if !strings.HasPrefix(name, "demo-command-") {
				t.Fatalf("cleanup name = %q", name)
			}
			*order = append(*order, "cleanup")
			return CommandSpec{Name: "cleanup"}
		},
		runTemporary: func(run temporaryCommandRunner, spec CommandSpec, _ CommandSpec, options RunOptions) error {
			*order = append(*order, "temporary")
			return run(spec, options)
		},
		runCommand: func(spec CommandSpec, _ RunOptions) error {
			*order = append(*order, "run "+spec.Name)
			return nil
		},
		readiness: func(ctx context.Context, _ EndpointExecutionPlan, service func(context.Context) error) error {
			*order = append(*order, "readiness")
			return service(ctx)
		},
		serviceCheck: func(string, string, time.Duration) error {
			*order = append(*order, "service check")
			return nil
		},
	}
}
