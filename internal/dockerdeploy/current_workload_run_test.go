package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRunCurrentWorkloadV1OrdersPrecheckPublicationFinalGateAndLaunch(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Workload = &blueprint.Workload{Command: "serve"}
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	planned := CurrentRuntimePlanV1{
		Document: document,
		Docker: DockerExecutionPlan{
			EnvironmentID: "demo", Phase: blueprint.PhaseStaged, Image: current.Generation.Reference,
			ContainerName: "demo", NetworkName: "demo", Workload: &WorkloadExecutionPlan{Command: "serve"},
		},
	}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	err = runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{
		DeploymentDir: dir, Action: "up",
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1000, GID: 1000},
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acquire", "store", "state", "admit up", "current", "plan", "invocation", "precheck", "publish", "lifecycle up", "acquire", "complete"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestRunCurrentWorkloadV1KeepsPrivateEnvironmentOutOfPublishedStateAndPassesItOnlyToLaunch(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Workload = &blueprint.Workload{Command: "serve"}
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	planned := CurrentRuntimePlanV1{
		Document: document,
		Docker: DockerExecutionPlan{
			EnvironmentID: "demo", Phase: blueprint.PhaseStaged, Image: current.Generation.Reference,
			ContainerName: "demo", NetworkName: "demo", Workload: &WorkloadExecutionPlan{Command: "serve"},
		},
	}
	private := privateWorkloadEnvironmentV1{
		Present: true,
		Payload: []byte("TOKEN=private-value\n\n"),
		Raw:     []byte("TOKEN=private-value\n"),
	}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	backend.privateEnvironment = func(gotDir string) (privateWorkloadEnvironmentV1, error) {
		if gotDir != dir {
			t.Fatalf("private environment dir = %q", gotDir)
		}
		return private, nil
	}
	backend.publishInputs = func(_ *deploy.OperationLock, _ string, plan CurrentRuntimePlanV1) (bool, error) {
		order = append(order, "publish")
		if !plan.Docker.PrivateEnvironment {
			t.Fatal("published Compose plan did not enable the private launcher")
		}
		return true, nil
	}
	backend.runLifecycle = func(_ context.Context, input CurrentWorkloadLifecycleInputV1) error {
		order = append(order, "lifecycle "+input.Action)
		if !reflect.DeepEqual(input.PrivateEnvironment, private) {
			t.Fatalf("lifecycle private environment = %#v", input.PrivateEnvironment)
		}
		return nil
	}
	if err := runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{
		DeploymentDir: dir, Action: "up",
		Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1000, GID: 1000},
	}, backend); err != nil {
		t.Fatal(err)
	}
}

func TestRunCurrentWorkloadV1KeepsQueueResponsiveDuringLifecycle(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Workload = &blueprint.Workload{Command: "serve"}
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	planned := CurrentRuntimePlanV1{
		Document: document,
		Docker: DockerExecutionPlan{
			EnvironmentID: "demo", Phase: blueprint.PhaseStaged, Image: current.Generation.Reference,
			ContainerName: "demo", NetworkName: "demo", Workload: &WorkloadExecutionPlan{Command: "serve"},
		},
	}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	backend.admit = AdmitControlOperationV1
	backend.complete = CompleteControlAdmissionV1
	lifecycleStarted := make(chan struct{})
	finishLifecycle := make(chan struct{})
	backend.runLifecycle = func(context.Context, CurrentWorkloadLifecycleInputV1) error {
		close(lifecycleStarted)
		<-finishLifecycle
		return nil
	}
	result := make(chan error, 1)
	go func() {
		result <- runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{
			DeploymentDir: dir, Action: "up",
		}, backend)
	}()
	<-lifecycleStarted

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	operation, err := deploy.AcquireOperationLock(ctx, dir)
	if err != nil {
		t.Fatalf("operation lock remained held during lifecycle execution: %v", err)
	}
	if _, found, err := operation.RecoverAbandonedControlMarkerV1(); err != nil || found {
		t.Fatalf("live lifecycle operation recovered: found=%t error=%v", found, err)
	}
	if _, err := operation.AdmitLiveRunV1(liveRunAdmissionFixtureV1("run-0000000000000002", false), false); !errors.Is(err, deploy.ErrLiveRunConflict) {
		t.Fatalf("immediate run during lifecycle error = %v", err)
	}
	waiter := liveRunAdmissionFixtureV1("run-0000000000000001", false)
	if status, err := operation.AdmitLiveRunV1(waiter, true); err != nil || status != deploy.LiveRunStatusWaitingV1 {
		t.Fatalf("run queued during lifecycle = %q, %v", status, err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	close(finishLifecycle)
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	inspection, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Unlock()
	queue, found, err := inspection.ReadLiveRunQueueV1()
	if err != nil || !found || len(queue.Runs) != 1 || queue.Runs[0].ID != waiter.ID || queue.Runs[0].Status != deploy.LiveRunStatusActiveV1 {
		t.Fatalf("queue after lifecycle = %#v, found=%t error=%v", queue, found, err)
	}
}

func TestRunCurrentWorkloadV1DoesNotPublishOrLaunchAfterStalePrecheck(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Workload = &blueprint.Workload{Command: "serve"}
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	planned := CurrentRuntimePlanV1{Document: document, Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{Command: "serve"}}}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	want := errors.New("stale build")
	backend.precheck = func(RuntimeReadinessInput) error {
		order = append(order, "precheck")
		return want
	}
	err = runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "restart"}, backend)
	if !errors.Is(err, want) {
		t.Fatalf("stale error = %v", err)
	}
	for _, forbidden := range []string{"publish", "lifecycle restart"} {
		for _, step := range order {
			if step == forbidden {
				t.Fatalf("stale launch reached %q: %v", forbidden, order)
			}
		}
	}
	if order[len(order)-1] != "complete" {
		t.Fatalf("stale launch did not complete admission: %v", order)
	}
}

func TestRunCurrentWorkloadV1RejectsGenerationChangedWhileWaiting(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Workload = &blueprint.Workload{Command: "serve"}
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}}}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	backend.admit = func(ctx context.Context, gotDir string, operation *deploy.OperationLock, input ControlAdmissionInputV1) (AdmittedControlV1, error) {
		order = append(order, "admit restart")
		if err := operation.Unlock(); err != nil {
			return AdmittedControlV1{}, err
		}
		reacquired, err := deploy.AcquireOperationLock(ctx, gotDir)
		if err != nil {
			return AdmittedControlV1{}, err
		}
		return AdmittedControlV1{Operation: reacquired, Marker: deploy.ControlMarkerV1{ID: "control-0000000000000001", Operation: input.Operation}}, nil
	}
	backend.loadCurrent = func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
		order = append(order, "current")
		changed := current
		changed.Generation.Reference = "reploy/env/demo:g-reinstalled"
		return changed, true, nil
	}
	err = runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{
		DeploymentDir: dir, Action: "restart", ControlMode: ControlAdmissionWaitV1,
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "generation changed while restart was waiting") {
		t.Fatalf("generation change error = %v", err)
	}
	for _, forbidden := range []string{"plan", "lifecycle restart"} {
		for _, step := range order {
			if step == forbidden {
				t.Fatalf("generation change reached %q: %v", forbidden, order)
			}
		}
	}
	if order[len(order)-1] != "complete" {
		t.Fatalf("generation change did not complete admission: %v", order)
	}
}

func TestRunCurrentWorkloadV1FallsBackToOwnedStopForStaleBuild(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Workload = &blueprint.Workload{Command: "serve"}
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	planned := CurrentRuntimePlanV1{Document: document, Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{Command: "serve"}}}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	backend.precheck = func(RuntimeReadinessInput) error {
		order = append(order, "precheck")
		return errors.New("host mount disappeared")
	}
	backend.stopOwned = func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
		order = append(order, "owned stop")
		return nil
	}
	err = runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "down"}, backend)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acquire", "store", "state", "admit stop", "current", "plan", "invocation", "precheck", "owned stop", "complete"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("stale stop order = %v, want %v", order, want)
	}
}

func TestRunCurrentWorkloadV1MissingCurrentStillAdmitsBeforeRecoveryStop(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	current.State.Current = nil
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{Command: "serve"}}}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	backend.admit = func(_ context.Context, _ string, operation *deploy.OperationLock, input ControlAdmissionInputV1) (AdmittedControlV1, error) {
		order = append(order, "admit "+string(input.Operation))
		if input.GenerationReference != unavailableRuntimeGenerationV1 || input.Mode != ControlAdmissionForceV1 {
			t.Fatalf("recovery stop admission = %#v", input)
		}
		return AdmittedControlV1{
			Operation: operation,
			Marker: deploy.ControlMarkerV1{
				ID: "control-0000000000000001", Operation: input.Operation,
			},
		}, nil
	}
	backend.stopOwned = func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
		order = append(order, "owned stop")
		return nil
	}
	if err := runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "down"}, backend); err != nil {
		t.Fatal(err)
	}
	want := []string{"acquire", "store", "state", "admit stop", "owned stop", "complete"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("missing-current stop order = %v, want %v", order, want)
	}
}

func TestRunCurrentWorkloadV1RejectsMissingBuildAndUnsupportedAction(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}}}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	backend.loadCurrent = func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
		return CurrentBuild{}, false, nil
	}
	if err := runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "up"}, backend); err == nil || !strings.Contains(err.Error(), "reploy build") {
		t.Fatalf("missing build error = %v", err)
	}
	if err := runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "status"}, backend); err == nil || !strings.Contains(err.Error(), "up, down, or restart") {
		t.Fatalf("unsupported action error = %v", err)
	}
}

func TestRunCurrentWorkloadV1NamesTheUnsupportedWorkloadAction(t *testing.T) {
	for _, test := range []struct {
		action string
		verb   string
	}{
		{action: "up", verb: "start"},
		{action: "down", verb: "stop"},
		{action: "restart", verb: "restart"},
	} {
		t.Run(test.action, func(t *testing.T) {
			dir := t.TempDir()
			current, _ := runtimeCurrentBuildFixture(t)
			planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{}}
			order := []string{}
			backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
			var stderr bytes.Buffer
			err := runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{
				DeploymentDir: dir, Action: test.action, RunOptions: RunOptions{Stderr: &stderr},
			}, backend)
			message := stderr.String()
			if err != nil {
				message = err.Error()
			}
			if !strings.Contains(message, "no workload to "+test.verb) {
				t.Fatalf("%s result = %v, stderr = %q", test.action, err, stderr.String())
			}
		})
	}
}

func TestRunCurrentWorkloadV1AddsStartupDiagnosticsToLifecycleFailure(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.Workload = &blueprint.Workload{Command: "serve"}
	planned := CurrentRuntimePlanV1{
		Document: document,
		Docker:   DockerExecutionPlan{Workload: &WorkloadExecutionPlan{Command: "serve"}},
	}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	backend.runLifecycle = func(context.Context, CurrentWorkloadLifecycleInputV1) error {
		return errors.New("lifecycle start operation 0 (start): service is not running")
	}
	want := errors.New("diagnosed startup")
	backend.startupFailure = func(message string, err error, options RuntimeOptions, _ time.Time) error {
		if message != "service failed after start" || !strings.Contains(err.Error(), "service is not running") {
			t.Fatalf("startup failure = %q, %v", message, err)
		}
		if options.Dir != dir || options.Action != "up" {
			t.Fatalf("startup options = %#v", options)
		}
		return want
	}
	err = runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "up"}, backend)
	if !errors.Is(err, want) {
		t.Fatalf("startup diagnostic error = %v", err)
	}
}

func TestCurrentWorkloadCommandsV1UsesRecordedSystemService(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd command discovery requires a POSIX host")
	}
	binDir := t.TempDir()
	systemctl := filepath.Join(binDir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	state := deploy.StateV1{Deployment: &deploy.DeploymentStateV1{Installation: deploy.InstallationStateV1{
		Scope: "system", UnitPath: "/etc/systemd/system/demo.service", Service: "demo",
	}}}
	start, stop, err := currentWorkloadCommandsV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if start == nil || stop == nil || start.Name != systemctl || stop.Name != systemctl {
		t.Fatalf("system commands = %#v / %#v", start, stop)
	}
	if !reflect.DeepEqual(start.Args, []string{"start", "demo.service"}) || !reflect.DeepEqual(stop.Args, []string{"stop", "demo.service"}) {
		t.Fatalf("system command args = %#v / %#v", start.Args, stop.Args)
	}
}

func TestStopOwnedCurrentWorkloadV1UsesRecordedComposeProject(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Unlock() })
	state := deploy.StateV1{Deployment: &deploy.DeploymentStateV1{Installation: deploy.InstallationStateV1{
		Scope: "user", TargetDir: dir, ComposeProject: "demo-owned",
	}}}
	original := runRuntimeCommand
	t.Cleanup(func() { runRuntimeCommand = original })
	var got CommandSpec
	runRuntimeCommand = func(spec CommandSpec, _ RunOptions) error {
		got = spec
		return nil
	}
	if err := stopOwnedCurrentWorkloadV1(t.Context(), operation, state, dir, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if got.Dir != dir || !containsAdjacent(got.Args, "--project-name", "demo-owned") || !containsAdjacent(got.Args, "down", "--remove-orphans") {
		t.Fatalf("owned stop command = %#v", got)
	}
}

func TestCurrentBuildRecoveryMessageV1DistinguishesStagedAndInstalled(t *testing.T) {
	if got := currentBuildRecoveryMessageV1(deploy.StateV1{}, "stale"); got != "stale; run `reploy build`" {
		t.Fatalf("staged recovery = %q", got)
	}
	installed := deploy.StateV1{Deployment: &deploy.DeploymentStateV1{}}
	if got := currentBuildRecoveryMessageV1(installed, "stale"); !strings.Contains(got, "original `reploy install` command") || strings.Contains(got, "reploy build") {
		t.Fatalf("installed recovery = %q", got)
	}
}

func TestRunCurrentWorkloadV1ConflictMentionsWait(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	planned := CurrentRuntimePlanV1{Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}}}
	order := []string{}
	backend := currentWorkloadRunTestBackend(t, dir, current, planned, &order)
	var notice bytes.Buffer
	backend.admit = func(_ context.Context, _ string, operation *deploy.OperationLock, input ControlAdmissionInputV1) (AdmittedControlV1, error) {
		if input.Notice != &notice {
			t.Fatalf("control notice writer = %#v", input.Notice)
		}
		if err := operation.Unlock(); err != nil {
			return AdmittedControlV1{}, err
		}
		return AdmittedControlV1{}, deploy.ErrLiveRunConflict
	}
	err := runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "up", Notice: &notice}, backend)
	if !errors.Is(err, deploy.ErrLiveRunConflict) || !strings.Contains(err.Error(), "rerun with --wait") {
		t.Fatalf("control conflict error = %v", err)
	}
}

func TestCurrentWorkloadControlModeV1MakesStopAndRestartDisruptiveByDefault(t *testing.T) {
	for _, action := range []string{"down", "restart"} {
		if got := currentWorkloadControlModeV1(action, ""); got != ControlAdmissionForceV1 {
			t.Fatalf("%s default mode = %q", action, got)
		}
		if got := currentWorkloadControlModeV1(action, ControlAdmissionWaitV1); got != ControlAdmissionDrainV1 {
			t.Fatalf("%s --wait mode = %q", action, got)
		}
	}
	if got := currentWorkloadControlModeV1("up", ""); got != ControlAdmissionImmediateV1 {
		t.Fatalf("up default mode = %q", got)
	}
	if got := currentWorkloadControlModeV1("up", ControlAdmissionWaitV1); got != ControlAdmissionWaitV1 {
		t.Fatalf("up --wait mode = %q", got)
	}
}

func currentWorkloadRunTestBackend(
	t *testing.T,
	dir string,
	current CurrentBuild,
	planned CurrentRuntimePlanV1,
	order *[]string,
) currentWorkloadRunBackendV1 {
	t.Helper()
	return currentWorkloadRunBackendV1{
		acquire: func(ctx context.Context, got string) (*deploy.OperationLock, error) {
			*order = append(*order, "acquire")
			if got != filepath.Clean(dir) {
				t.Fatalf("deployment dir = %q", got)
			}
			return deploy.AcquireOperationLock(ctx, got)
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
		admit: func(_ context.Context, _ string, operation *deploy.OperationLock, input ControlAdmissionInputV1) (AdmittedControlV1, error) {
			*order = append(*order, "admit "+string(input.Operation))
			wantMode := ControlAdmissionImmediateV1
			if input.Operation == deploy.ControlOperationStopV1 || input.Operation == deploy.ControlOperationRestartV1 {
				wantMode = ControlAdmissionForceV1
			}
			if input.GenerationReference != current.Generation.Reference || input.Mode != wantMode {
				t.Fatalf("control admission input = %+v", input)
			}
			return AdmittedControlV1{
				Operation: operation,
				Marker:    deploy.ControlMarkerV1{ID: "control-0000000000000001", Operation: input.Operation},
			}, nil
		},
		complete: func(operation *deploy.OperationLock, _ string, lease *deploy.ControlLeaseV1) error {
			*order = append(*order, "complete")
			return errors.Join(lease.Release(), operation.Unlock())
		},
		plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
			*order = append(*order, "plan")
			return planned, nil
		},
		invocation: func(DockerExecutionPlan) (RuntimeInvocationV1, error) {
			*order = append(*order, "invocation")
			return RuntimeInvocationV1{PlanID: runtimeWorkloadPlanID, Sources: []RuntimeHostSourceV1{}}, nil
		},
		precheck: func(RuntimeReadinessInput) error {
			*order = append(*order, "precheck")
			return nil
		},
		workloadCommands: func(deploy.StateV1) (*CommandSpec, *CommandSpec, error) {
			return nil, nil, nil
		},
		stopOwned: func(context.Context, *deploy.OperationLock, deploy.StateV1, string, RunOptions) error {
			*order = append(*order, "owned stop")
			return nil
		},
		publishInputs: func(*deploy.OperationLock, string, CurrentRuntimePlanV1) (bool, error) {
			*order = append(*order, "publish")
			return true, nil
		},
		runLifecycle: func(_ context.Context, input CurrentWorkloadLifecycleInputV1) error {
			*order = append(*order, "lifecycle "+input.Action)
			return nil
		},
		startupFailure: runtimePostStartError,
		privateEnvironment: func(string) (privateWorkloadEnvironmentV1, error) {
			return privateWorkloadEnvironmentV1{}, nil
		},
	}
}
