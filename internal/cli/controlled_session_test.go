package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/dockerdeploy"
)

func TestParseControlledSessionRunOptionsMapsExactSelectionsAndDefaults(t *testing.T) {
	options, err := parseControlledSessionRunOptions([]string{
		"--controller-dir", "controller", "--workload-dir=workload",
		"--endpoint", "api", "--endpoint=metrics", "--columns", "120", "--rows=40",
		"--output-file", "capture.cast", "--", "record", "--quiet", "value",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := controlledSessionRunCLIOptions{
		ControllerDir: "controller", WorkloadDir: "workload", EndpointIDs: []string{"api", "metrics"},
		Columns: 120, Rows: 40, OutputFile: "capture.cast",
		StartupTimeout: controlledSessionDefaultStartupTimeout, TerminationGrace: controlledSessionDefaultTerminationGrace,
		ControllerFinalizationTimeout: controlledSessionDefaultControllerFinalizationTimeout,
		ResultAcknowledgementTimeout:  controlledSessionDefaultResultAcknowledgementTimeout,
		CleanupTimeout:                controlledSessionDefaultCleanupTimeout,
		ControllerCommand:             "record", ControllerArguments: []string{"--quiet", "value"},
	}
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("options = %#v, want %#v", options, want)
	}
}

func TestParseControlledSessionRunOptionsAppliesInclusiveTimeoutBounds(t *testing.T) {
	options, err := parseControlledSessionRunOptions([]string{
		"--controller-dir=c", "--workload-dir=w", "--columns=1", "--rows=65535",
		"--startup-timeout=15s", "--termination-grace=100ms",
		"--controller-finalization-timeout=1h", "--result-acknowledgement-timeout=1m",
		"--cleanup-timeout=5m", "--", "controller",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.StartupTimeout != 15*time.Second || options.TerminationGrace != 100*time.Millisecond ||
		options.ControllerFinalizationTimeout != time.Hour || options.ResultAcknowledgementTimeout != time.Minute ||
		options.CleanupTimeout != 5*time.Minute || options.Columns != 1 || options.Rows != 65535 {
		t.Fatalf("bounded options = %#v", options)
	}
}

func TestParseControlledSessionRunOptionsRejectsUsageErrors(t *testing.T) {
	base := []string{"--controller-dir=c", "--workload-dir=w", "--columns=80", "--rows=24", "--", "controller"}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing delimiter", args: base[:4], want: "must follow --"},
		{name: "missing command", args: base[:5], want: "CONTROLLER_COMMAND"},
		{name: "missing controller", args: []string{"--workload-dir=w", "--columns=80", "--rows=24", "--", "controller"}, want: "--controller-dir is required"},
		{name: "missing columns", args: []string{"--controller-dir=c", "--workload-dir=w", "--rows=24", "--", "controller"}, want: "--columns is required"},
		{name: "zero rows", args: []string{"--controller-dir=c", "--workload-dir=w", "--columns=80", "--rows=0", "--", "controller"}, want: "1 through 65535"},
		{name: "large columns", args: []string{"--controller-dir=c", "--workload-dir=w", "--columns=65536", "--rows=24", "--", "controller"}, want: "1 through 65535"},
		{name: "both outputs", args: []string{"--controller-dir=c", "--workload-dir=w", "--columns=80", "--rows=24", "--output-dir=d", "--output-file=f", "--", "controller"}, want: "mutually exclusive"},
		{name: "duplicate", args: []string{"--controller-dir=c", "--controller-dir=d", "--workload-dir=w", "--columns=80", "--rows=24", "--", "controller"}, want: "may only be provided once"},
		{name: "missing value", args: []string{"--controller-dir", "--workload-dir=w", "--columns=80", "--rows=24", "--", "controller"}, want: "requires a value"},
		{name: "low timeout", args: []string{"--controller-dir=c", "--workload-dir=w", "--columns=80", "--rows=24", "--startup-timeout=14s", "--", "controller"}, want: "15s through 5m0s"},
		{name: "unknown", args: []string{"--controller-dir=c", "--workload-dir=w", "--columns=80", "--rows=24", "--mystery=x", "--", "controller"}, want: "unknown option"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseControlledSessionRunOptions(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestControlledSessionRunMapsInputAndEmitsSuccessfulStructuredResult(t *testing.T) {
	codeZero := 0
	wantRuntime := dockerdeploy.StagedProviderBuildRuntimeV1{UID: 1000, GID: 1000}
	wantResult := successfulControlledSessionRunResultV1(&codeZero)
	installControlledSessionCLIFakes(t,
		func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) { return wantRuntime, nil },
		func(ctx context.Context, input dockerdeploy.CurrentControlledSessionRunInputV1) (dockerdeploy.CurrentControlledSessionRunResultV1, error) {
			if ctx == nil || ctx.Done() == nil {
				t.Fatal("controlled-session run did not receive a cancelable host context")
			}
			if input.ControllerDeploymentDir != "controller" || input.WorkloadDeploymentDir != "workload" ||
				input.ControllerCommand != "record" || !reflect.DeepEqual(input.ControllerArguments, []string{"--cast", "session.cast"}) ||
				!reflect.DeepEqual(input.EndpointIDs, []string{"api", "metrics"}) || input.OutputDir != "artifacts" || input.OutputFile != "" ||
				input.InitialColumns != 100 || input.InitialRows != 30 || !reflect.DeepEqual(input.Runtime, wantRuntime) || input.Notice == nil {
				t.Fatalf("controlled-session input = %#v", input)
			}
			wantOptions := dockerdeploy.ControlledSessionRunOptionsV1{
				StartupTimeout: 20 * time.Second, TerminationGrace: time.Second,
				ControllerFinalizationTimeout: 2 * time.Minute, ResultAcknowledgementTimeout: 4 * time.Second,
				CleanupTimeout: 12 * time.Second,
			}
			if !reflect.DeepEqual(input.SupervisorOptions, wantOptions) {
				t.Fatalf("supervisor options = %#v, want %#v", input.SupervisorOptions, wantOptions)
			}
			return wantResult, nil
		},
		controlledSessionHostCancellation{},
	)

	var stdout, stderr bytes.Buffer
	code := Main([]string{
		"controlled-session", "run", "--controller-dir", "controller", "--workload-dir", "workload",
		"--endpoint", "api", "--endpoint", "metrics", "--columns", "100", "--rows", "30", "--output-dir", "artifacts",
		"--startup-timeout", "20s", "--termination-grace", "1s", "--controller-finalization-timeout", "2m",
		"--result-acknowledgement-timeout", "4s", "--cleanup-timeout", "12s", "--", "record", "--cast", "session.cast",
	}, &stdout, &stderr)
	if code != 0 || stderr.String() != "" {
		t.Fatalf("run code=%d stderr=%q", code, stderr.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 || !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("stdout is not exactly one JSON line: %q", stdout.String())
	}
	var got controlledSessionRunResultJSONV1
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Error != nil || got.SessionResult == nil || got.ControllerOutput == nil ||
		got.ControllerOutput.Kind != dockerdeploy.ControlledSessionControllerOutputDirectoryRetainedV1 {
		t.Fatalf("public result = %#v", got)
	}
}

func TestControlledSessionRunEmitsNullableAdmissionFailure(t *testing.T) {
	installControlledSessionCLIFakes(t,
		func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
			return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
		},
		func(context.Context, dockerdeploy.CurrentControlledSessionRunInputV1) (dockerdeploy.CurrentControlledSessionRunResultV1, error) {
			return dockerdeploy.CurrentControlledSessionRunResultV1{}, errors.New("admission rejected")
		},
		controlledSessionHostCancellation{},
	)
	var stdout, stderr bytes.Buffer
	code := Main([]string{"controlled-session", "run", "--controller-dir=c", "--workload-dir=w", "--columns=80", "--rows=24", "--", "controller"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "admission rejected") {
		t.Fatalf("run code=%d stderr=%q", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"session_result", "result_delivered", "result_acknowledged", "controller_status", "controller_output", "delivery_tail_cleanup_status", "delivery_tail_recovery_action"} {
		if value, found := got[field]; !found || value != nil {
			t.Fatalf("%s = %#v, want explicit null in %s", field, value, stdout.String())
		}
	}
	if got["ok"] != false || got["error"] != "admission rejected" {
		t.Fatalf("failure result = %s", stdout.String())
	}
}

func TestControlledSessionRunPreservesSignalExitAfterStructuredCleanupResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	installControlledSessionCLIFakes(t,
		func() (dockerdeploy.StagedProviderBuildRuntimeV1, error) {
			return dockerdeploy.StagedProviderBuildRuntimeV1{}, nil
		},
		func(got context.Context, _ dockerdeploy.CurrentControlledSessionRunInputV1) (dockerdeploy.CurrentControlledSessionRunResultV1, error) {
			if !errors.Is(got.Err(), context.Canceled) {
				t.Fatalf("host context error = %v", got.Err())
			}
			result := successfulControlledSessionRunResultV1(nil)
			result.SessionResult.Cause = controlledsession.CauseHostCancelV1
			return result, context.Canceled
		},
		controlledSessionHostCancellation{Context: ctx, Stop: func() {}, ExitCode: func() int { return 143 }},
	)
	var stdout, stderr bytes.Buffer
	code := Main([]string{"controlled-session", "run", "--controller-dir=c", "--workload-dir=w", "--columns=80", "--rows=24", "--", "controller"}, &stdout, &stderr)
	if code != 143 || !strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("signal run code=%d stderr=%q", code, stderr.String())
	}
	var got controlledSessionRunResultJSONV1
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || got.SessionResult == nil || got.SessionResult.Cause != controlledsession.CauseHostCancelV1 {
		t.Fatalf("signal result = %#v", got)
	}
}

func TestControlledSessionRunUsageErrorLeavesStdoutEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main([]string{"controlled-session", "run", "--controller-dir=c"}, &stdout, &stderr)
	if code != 2 || stdout.String() != "" || !strings.Contains(stderr.String(), "usage error") {
		t.Fatalf("usage result code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestControlledSessionRunSuccessPredicateRejectsIncompleteResults(t *testing.T) {
	codeZero := 0
	base := successfulControlledSessionRunResultV1(&codeZero)
	tests := []struct {
		name   string
		mutate func(*dockerdeploy.CurrentControlledSessionRunResultV1)
	}{
		{name: "nonzero workload", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			code := 3
			result.SessionResult.WorkloadStatus.Code = &code
		}},
		{name: "host cancel", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.SessionResult.Cause = controlledsession.CauseHostCancelV1
		}},
		{name: "output not drained", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.SessionResult.WorkloadOutputFinalizationStatus.Kind = controlledsession.WorkloadOutputFinalizationFailedV1
		}},
		{name: "observation lost", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.SessionResult.RuntimeObservationStatus.Kind = controlledsession.RuntimeObservationLostV1
		}},
		{name: "controller incomplete", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.SessionResult.ControllerFinalizationStatus.Kind = controlledsession.ControllerFinalizationTimeoutV1
		}},
		{name: "cleanup failed", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.SessionResult.CleanupStatus.Kind = controlledsession.CleanupStatusFailedV1
		}},
		{name: "recovery required", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.SessionResult.RecoveryAction = controlledsession.RecoveryRetryCleanupV1
		}},
		{name: "not delivered", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) { result.ResultDelivered = false }},
		{name: "not acknowledged", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) { result.ResultAcknowledged = false }},
		{name: "output failed", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.ControllerOutput.Kind = dockerdeploy.ControlledSessionControllerOutputFailedV1
		}},
		{name: "tail cleanup failed", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.DeliveryTailCleanupStatus.Kind = controlledsession.CleanupStatusFailedV1
		}},
		{name: "tail recovery", mutate: func(result *dockerdeploy.CurrentControlledSessionRunResultV1) {
			result.DeliveryTailRecoveryAction = controlledsession.RecoveryRetryCleanupV1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := base
			test.mutate(&result)
			if controlledSessionRunSucceededV1(result) {
				t.Fatalf("incomplete result was successful: %#v", result)
			}
		})
	}
	base.SessionResult.Cause = controlledsession.CauseControllerTerminateV1
	base.SessionResult.WorkloadStatus = controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusTerminatedV1}
	if !controlledSessionRunSucceededV1(base) {
		t.Fatal("controller-requested clean termination was not successful")
	}
}

func TestControlledSessionRunResultV1PublicGoldenFixtures(t *testing.T) {
	codeZero, codeOne := 0, 1
	success := successfulControlledSessionRunResultV1(&codeZero)
	success.ControllerOutput = dockerdeploy.ControlledSessionControllerOutputStatusV1{Kind: dockerdeploy.ControlledSessionControllerOutputNotRequestedV1}
	directoryRetained := successfulControlledSessionRunResultV1(&codeZero)
	filePublished := successfulControlledSessionRunResultV1(&codeZero)
	filePublished.ControllerOutput = dockerdeploy.ControlledSessionControllerOutputStatusV1{Kind: dockerdeploy.ControlledSessionControllerOutputFilePublishedV1}
	outputFailed := successfulControlledSessionRunResultV1(&codeZero)
	outputFailed.ControllerOutput = dockerdeploy.ControlledSessionControllerOutputStatusV1{
		Kind: dockerdeploy.ControlledSessionControllerOutputFailedV1, Reason: "controller output publication failed",
	}
	incomplete := successfulControlledSessionRunResultV1(&codeZero)
	incomplete.ControllerStatus.Code = &codeOne
	incomplete.SessionResult.ControllerFinalizationStatus = controlledsession.ControllerFinalizationStatusV1{
		Kind: controlledsession.ControllerFinalizationNotCompletedV1, Reason: "Controller exited before completing finalization.",
	}
	incomplete.ResultDelivered = false
	incomplete.ResultAcknowledged = false
	incomplete.ControllerOutput = dockerdeploy.ControlledSessionControllerOutputStatusV1{
		Kind:   dockerdeploy.ControlledSessionControllerOutputFileDiscardedV1,
		Reason: "controller output file was discarded because controller finalization did not complete",
	}
	cases := []controlledSessionRunResultJSONV1{
		projectControlledSessionRunResultV1(dockerdeploy.CurrentControlledSessionRunResultV1{}, errors.New("admission rejected")),
		projectControlledSessionRunResultV1(dockerdeploy.CurrentControlledSessionRunResultV1{
			ControllerOutput: dockerdeploy.ControlledSessionControllerOutputStatusV1{Kind: dockerdeploy.ControlledSessionControllerOutputNotRequestedV1},
		}, errors.New("plan rejected")),
		projectControlledSessionRunResultV1(success, nil),
		projectControlledSessionRunResultV1(directoryRetained, nil),
		projectControlledSessionRunResultV1(filePublished, nil),
		projectControlledSessionRunResultV1(incomplete, nil),
		projectControlledSessionRunResultV1(outputFailed, errors.New("publish controlled-session controller output: disk full")),
	}
	for index, result := range cases {
		if result.SessionResult != nil {
			if err := controlledsession.ValidateResultV1(*result.SessionResult); err != nil {
				t.Fatalf("golden result %d is invalid: %v", index, err)
			}
		}
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, result := range cases {
		if err := encoder.Encode(result); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "controlled-session", "run-results-v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("public host-result fixture mismatch:\ngot:\n%s\nwant:\n%s", output.Bytes(), want)
	}
}

func installControlledSessionCLIFakes(
	t *testing.T,
	runtime func() (dockerdeploy.StagedProviderBuildRuntimeV1, error),
	run func(context.Context, dockerdeploy.CurrentControlledSessionRunInputV1) (dockerdeploy.CurrentControlledSessionRunResultV1, error),
	host controlledSessionHostCancellation,
) {
	t.Helper()
	oldRuntime, oldRun, oldHost := dockerCurrentControlledSessionRuntime, dockerRunCurrentControlledSession, newControlledSessionHostCancellation
	dockerCurrentControlledSessionRuntime, dockerRunCurrentControlledSession = runtime, run
	if host.Context == nil {
		ctx, cancel := context.WithCancel(context.Background())
		host = controlledSessionHostCancellation{Context: ctx, Stop: cancel, ExitCode: func() int { return 0 }}
	}
	newControlledSessionHostCancellation = func() controlledSessionHostCancellation { return host }
	t.Cleanup(func() {
		dockerCurrentControlledSessionRuntime, dockerRunCurrentControlledSession, newControlledSessionHostCancellation = oldRuntime, oldRun, oldHost
	})
}

func successfulControlledSessionRunResultV1(code *int) dockerdeploy.CurrentControlledSessionRunResultV1 {
	if code == nil {
		zero := 0
		code = &zero
	}
	return dockerdeploy.CurrentControlledSessionRunResultV1{
		ControlledSessionRunResultV1: dockerdeploy.ControlledSessionRunResultV1{
			SessionResult: controlledsession.ResultV1{
				Cause:                            controlledsession.CauseWorkloadExitV1,
				WorkloadStatus:                   controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: code},
				WorkloadOutputFinalizationStatus: controlledsession.WorkloadOutputFinalizationStatusV1{Kind: controlledsession.WorkloadOutputFinalizationDrainedV1},
				RuntimeObservationStatus:         controlledsession.RuntimeObservationStatusV1{Kind: controlledsession.RuntimeObservationMaintainedV1},
				ControllerFinalizationStatus:     controlledsession.ControllerFinalizationStatusV1{Kind: controlledsession.ControllerFinalizationCompletedV1},
				CleanupStatus:                    controlledsession.CleanupStatusV1{Kind: controlledsession.CleanupStatusSucceededV1},
				RecoveryAction:                   controlledsession.RecoveryNoneV1,
			},
			ResultDelivered: true, ResultAcknowledged: true,
			ControllerStatus:           controlledsession.ProcessStatusV1{Kind: controlledsession.ProcessStatusExitedV1, Code: code},
			DeliveryTailCleanupStatus:  controlledsession.CleanupStatusV1{Kind: controlledsession.CleanupStatusSucceededV1},
			DeliveryTailRecoveryAction: controlledsession.RecoveryNoneV1,
		},
		ControllerOutput: dockerdeploy.ControlledSessionControllerOutputStatusV1{Kind: dockerdeploy.ControlledSessionControllerOutputDirectoryRetainedV1},
	}
}
