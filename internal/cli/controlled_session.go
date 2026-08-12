package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/omry/reploy/internal/controlledsession"
	"github.com/omry/reploy/internal/dockerdeploy"
)

const controlledSessionRunResultSchemaV1 = "reploy-controlled-session-run-result-v1"

const (
	controlledSessionDefaultStartupTimeout                = 30 * time.Second
	controlledSessionDefaultTerminationGrace              = 5 * time.Second
	controlledSessionDefaultControllerFinalizationTimeout = 5 * time.Minute
	controlledSessionDefaultResultAcknowledgementTimeout  = 10 * time.Second
	controlledSessionDefaultCleanupTimeout                = 30 * time.Second
)

var dockerRunCurrentControlledSession = dockerdeploy.RunCurrentControlledSessionV1
var dockerCurrentControlledSessionRuntime = dockerdeploy.CurrentStagedProviderBuildRuntimeV1
var newControlledSessionHostCancellation = makeControlledSessionHostCancellation

type controlledSessionRunCLIOptions struct {
	ControllerDir                 string
	WorkloadDir                   string
	EndpointIDs                   []string
	Columns                       uint32
	Rows                          uint32
	OutputFile                    string
	OutputDir                     string
	StartupTimeout                time.Duration
	TerminationGrace              time.Duration
	ControllerFinalizationTimeout time.Duration
	ResultAcknowledgementTimeout  time.Duration
	CleanupTimeout                time.Duration
	ControllerCommand             string
	ControllerArguments           []string
}

type controlledSessionHostCancellation struct {
	Context  context.Context
	Stop     func()
	ExitCode func() int
}

type controlledSessionRunResultJSONV1 struct {
	Schema                     string                                                  `json:"schema"`
	OK                         bool                                                    `json:"ok"`
	Error                      *string                                                 `json:"error"`
	SessionResult              *controlledsession.ResultV1                             `json:"session_result"`
	ResultDelivered            *bool                                                   `json:"result_delivered"`
	ResultAcknowledged         *bool                                                   `json:"result_acknowledged"`
	ControllerStatus           *controlledsession.ProcessStatusV1                      `json:"controller_status"`
	ControllerOutput           *dockerdeploy.ControlledSessionControllerOutputStatusV1 `json:"controller_output"`
	DeliveryTailCleanupStatus  *controlledsession.CleanupStatusV1                      `json:"delivery_tail_cleanup_status"`
	DeliveryTailRecoveryAction *controlledsession.RecoveryActionV1                     `json:"delivery_tail_recovery_action"`
}

func runControlledSession(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "reploy controlled-session usage error: expected command")
		printControlledSessionShortUsage(stderr)
		return 2
	}
	if isHelpArg(args[0]) {
		printControlledSessionHelp(stdout)
		return 0
	}
	if args[0] != "run" {
		fmt.Fprintf(stderr, "reploy controlled-session usage error: unknown command: %s\n", args[0])
		printControlledSessionShortUsage(stderr)
		return 2
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		printControlledSessionRunHelp(stdout)
		return 0
	}
	if globalOptions.DockerTimeoutSet {
		fmt.Fprintln(stderr, "reploy controlled-session run usage error: --docker-timeout is not supported")
		printControlledSessionRunShortUsage(stderr)
		return 2
	}
	options, err := parseControlledSessionRunOptions(args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "reploy controlled-session run usage error: %v\n", err)
		printControlledSessionRunShortUsage(stderr)
		return 2
	}

	host := newControlledSessionHostCancellation()
	defer host.Stop()
	runtime, runtimeErr := dockerCurrentControlledSessionRuntime()
	var result dockerdeploy.CurrentControlledSessionRunResultV1
	if runtimeErr == nil {
		result, err = dockerRunCurrentControlledSession(host.Context, dockerdeploy.CurrentControlledSessionRunInputV1{
			ControllerDeploymentDir: options.ControllerDir,
			WorkloadDeploymentDir:   options.WorkloadDir,
			ControllerCommand:       options.ControllerCommand,
			ControllerArguments:     append([]string(nil), options.ControllerArguments...),
			EndpointIDs:             append([]string(nil), options.EndpointIDs...),
			OutputDir:               options.OutputDir,
			OutputFile:              options.OutputFile,
			InitialColumns:          options.Columns,
			InitialRows:             options.Rows,
			Runtime:                 runtime,
			SupervisorOptions: dockerdeploy.ControlledSessionRunOptionsV1{
				StartupTimeout: options.StartupTimeout, TerminationGrace: options.TerminationGrace,
				ControllerFinalizationTimeout: options.ControllerFinalizationTimeout,
				ResultAcknowledgementTimeout:  options.ResultAcknowledgementTimeout,
				CleanupTimeout:                options.CleanupTimeout,
			},
			Notice: stderr,
		})
	} else {
		err = runtimeErr
	}
	if err != nil {
		fmt.Fprintf(stderr, "reploy controlled-session run error: %v\n", err)
	}
	publicResult := projectControlledSessionRunResultV1(result, err)
	if encodeErr := json.NewEncoder(stdout).Encode(publicResult); encodeErr != nil {
		fmt.Fprintf(stderr, "reploy controlled-session run error: encode structured result: %v\n", encodeErr)
		if code := host.ExitCode(); code != 0 {
			return code
		}
		return 1
	}
	if code := host.ExitCode(); code != 0 {
		return code
	}
	if publicResult.OK {
		return 0
	}
	return 1
}

func parseControlledSessionRunOptions(args []string) (controlledSessionRunCLIOptions, error) {
	options := controlledSessionRunCLIOptions{
		StartupTimeout:                controlledSessionDefaultStartupTimeout,
		TerminationGrace:              controlledSessionDefaultTerminationGrace,
		ControllerFinalizationTimeout: controlledSessionDefaultControllerFinalizationTimeout,
		ResultAcknowledgementTimeout:  controlledSessionDefaultResultAcknowledgementTimeout,
		CleanupTimeout:                controlledSessionDefaultCleanupTimeout,
	}
	columnsSet, rowsSet := false, false
	seen := map[string]bool{}
	delimiter := -1
	for index, arg := range args {
		if arg == "--" {
			delimiter = index
			break
		}
	}
	if delimiter < 0 {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("controller command must follow --")
	}
	if delimiter+1 >= len(args) || strings.TrimSpace(args[delimiter+1]) == "" {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("CONTROLLER_COMMAND is required after --")
	}
	for index := 0; index < delimiter; index++ {
		arg := args[index]
		name, inlineValue, hasInlineValue := strings.Cut(arg, "=")
		valueFor := func() (string, error) {
			if hasInlineValue {
				if inlineValue == "" {
					return "", fmt.Errorf("%s requires a value", name)
				}
				return inlineValue, nil
			}
			if index+1 >= delimiter || strings.HasPrefix(args[index+1], "--") {
				return "", fmt.Errorf("%s requires a value", name)
			}
			index++
			return args[index], nil
		}
		value := ""
		var err error
		switch name {
		case "--controller-dir", "--workload-dir", "--endpoint", "--columns", "--rows", "--output-file", "--output-dir",
			"--startup-timeout", "--termination-grace", "--controller-finalization-timeout", "--result-acknowledgement-timeout", "--cleanup-timeout":
			value, err = valueFor()
			if err != nil {
				return controlledSessionRunCLIOptions{}, err
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return controlledSessionRunCLIOptions{}, fmt.Errorf("unknown option: %s", name)
			}
			return controlledSessionRunCLIOptions{}, fmt.Errorf("unexpected argument before --: %s", arg)
		}
		if name != "--endpoint" {
			if seen[name] {
				return controlledSessionRunCLIOptions{}, fmt.Errorf("%s may only be provided once", name)
			}
			seen[name] = true
		}
		switch name {
		case "--controller-dir":
			options.ControllerDir = value
		case "--workload-dir":
			options.WorkloadDir = value
		case "--endpoint":
			if strings.TrimSpace(value) == "" {
				return controlledSessionRunCLIOptions{}, fmt.Errorf("--endpoint must not be empty")
			}
			options.EndpointIDs = append(options.EndpointIDs, value)
		case "--columns":
			options.Columns, err = parseControlledSessionDimension("--columns", value)
			columnsSet = err == nil
		case "--rows":
			options.Rows, err = parseControlledSessionDimension("--rows", value)
			rowsSet = err == nil
		case "--output-file":
			options.OutputFile = value
		case "--output-dir":
			options.OutputDir = value
		case "--startup-timeout":
			options.StartupTimeout, err = parseControlledSessionTimeout(name, value, 15*time.Second, 5*time.Minute)
		case "--termination-grace":
			options.TerminationGrace, err = parseControlledSessionTimeout(name, value, 100*time.Millisecond, time.Minute)
		case "--controller-finalization-timeout":
			options.ControllerFinalizationTimeout, err = parseControlledSessionTimeout(name, value, time.Second, time.Hour)
		case "--result-acknowledgement-timeout":
			options.ResultAcknowledgementTimeout, err = parseControlledSessionTimeout(name, value, time.Second, time.Minute)
		case "--cleanup-timeout":
			options.CleanupTimeout, err = parseControlledSessionTimeout(name, value, time.Second, 5*time.Minute)
		}
		if err != nil {
			return controlledSessionRunCLIOptions{}, err
		}
	}
	if strings.TrimSpace(options.ControllerDir) == "" {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("--controller-dir is required")
	}
	if strings.TrimSpace(options.WorkloadDir) == "" {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("--workload-dir is required")
	}
	if !columnsSet {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("--columns is required")
	}
	if !rowsSet {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("--rows is required")
	}
	if options.OutputFile != "" && options.OutputDir != "" {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("--output-file and --output-dir are mutually exclusive")
	}
	if options.OutputFile != "" && strings.TrimSpace(options.OutputFile) == "" {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("--output-file must not be empty")
	}
	if options.OutputDir != "" && strings.TrimSpace(options.OutputDir) == "" {
		return controlledSessionRunCLIOptions{}, fmt.Errorf("--output-dir must not be empty")
	}
	options.ControllerCommand = args[delimiter+1]
	options.ControllerArguments = append([]string(nil), args[delimiter+2:]...)
	return options, nil
}

func parseControlledSessionDimension(name string, value string) (uint32, error) {
	dimension, err := strconv.ParseUint(value, 10, 16)
	if err != nil || dimension == 0 {
		return 0, fmt.Errorf("%s must be an integer from 1 through 65535", name)
	}
	return uint32(dimension), nil
}

func parseControlledSessionTimeout(name string, value string, minimum time.Duration, maximum time.Duration) (time.Duration, error) {
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s duration: %s", name, value)
	}
	if timeout < minimum || timeout > maximum {
		return 0, fmt.Errorf("%s must be from %s through %s", name, minimum, maximum)
	}
	return timeout, nil
}

func projectControlledSessionRunResultV1(result dockerdeploy.CurrentControlledSessionRunResultV1, runErr error) controlledSessionRunResultJSONV1 {
	public := controlledSessionRunResultJSONV1{Schema: controlledSessionRunResultSchemaV1}
	if runErr != nil {
		message := runErr.Error()
		public.Error = &message
	}
	if result.SessionResult.Cause != "" {
		sessionResult := result.SessionResult
		public.SessionResult = &sessionResult
		delivered, acknowledged := result.ResultDelivered, result.ResultAcknowledged
		public.ResultDelivered, public.ResultAcknowledged = &delivered, &acknowledged
	}
	if result.ControllerStatus.Kind != "" {
		status := result.ControllerStatus
		public.ControllerStatus = &status
	}
	if result.ControllerOutput.Kind != "" {
		status := result.ControllerOutput
		public.ControllerOutput = &status
	}
	if result.DeliveryTailCleanupStatus.Kind != "" {
		status := result.DeliveryTailCleanupStatus
		public.DeliveryTailCleanupStatus = &status
	}
	if result.DeliveryTailRecoveryAction != "" {
		action := result.DeliveryTailRecoveryAction
		public.DeliveryTailRecoveryAction = &action
	}
	public.OK = runErr == nil && controlledSessionRunSucceededV1(result)
	return public
}

func controlledSessionRunSucceededV1(result dockerdeploy.CurrentControlledSessionRunResultV1) bool {
	session := result.SessionResult
	causeSucceeded := session.Cause == controlledsession.CauseControllerTerminateV1
	if session.Cause == controlledsession.CauseWorkloadExitV1 && session.WorkloadStatus.Kind == controlledsession.ProcessStatusExitedV1 && session.WorkloadStatus.Code != nil && *session.WorkloadStatus.Code == 0 {
		causeSucceeded = true
	}
	outputSucceeded := result.ControllerOutput.Kind == dockerdeploy.ControlledSessionControllerOutputNotRequestedV1 ||
		result.ControllerOutput.Kind == dockerdeploy.ControlledSessionControllerOutputDirectoryRetainedV1 ||
		result.ControllerOutput.Kind == dockerdeploy.ControlledSessionControllerOutputFilePublishedV1
	return causeSucceeded &&
		session.WorkloadOutputFinalizationStatus.Kind == controlledsession.WorkloadOutputFinalizationDrainedV1 &&
		session.RuntimeObservationStatus.Kind == controlledsession.RuntimeObservationMaintainedV1 &&
		session.ControllerFinalizationStatus.Kind == controlledsession.ControllerFinalizationCompletedV1 &&
		session.CleanupStatus.Kind == controlledsession.CleanupStatusSucceededV1 &&
		session.RecoveryAction == controlledsession.RecoveryNoneV1 &&
		result.ResultDelivered && result.ResultAcknowledged &&
		result.DeliveryTailCleanupStatus.Kind == controlledsession.CleanupStatusSucceededV1 &&
		result.DeliveryTailRecoveryAction == controlledsession.RecoveryNoneV1 && outputSucceeded
}

func makeControlledSessionHostCancellation() controlledSessionHostCancellation {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, controlledSessionTerminationSignals()...)
	var exitCode atomic.Int32
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			signal.Stop(signals)
			cancel()
		})
	}
	go func() {
		select {
		case received := <-signals:
			exitCode.Store(int32(controlledSessionSignalExitCode(received)))
			stop()
		case <-ctx.Done():
		}
	}()
	return controlledSessionHostCancellation{
		Context: ctx, Stop: stop, ExitCode: func() int { return int(exitCode.Load()) },
	}
}

func printControlledSessionShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy controlled-session run [OPTIONS] -- CONTROLLER_COMMAND [ARG ...]")
	fmt.Fprintln(output, "Run 'reploy controlled-session --help' for controlled-session help.")
}

func printControlledSessionRunShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy controlled-session run --controller-dir DIR --workload-dir DIR [--endpoint ID ...] --columns N --rows N [--output-file FILE | --output-dir DIR] [TIMEOUT OPTIONS] -- CONTROLLER_COMMAND [ARG ...]")
}

func printControlledSessionHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy controlled-session COMMAND

Run a controller-managed session against one exact current workload.

Commands:
  run          Run a controlled session

Run 'reploy controlled-session run --help' for invocation options.
`, "\n"))
}

func printControlledSessionRunHelp(output io.Writer) {
	printControlledSessionRunShortUsage(output)
	fmt.Fprint(output, strings.TrimLeft(`

The workload runs its declared persistent shell. CONTROLLER_COMMAND and all
arguments after -- run only in the selected controller deployment.

Required options:
  --controller-dir DIR                 Controller deployment directory
  --workload-dir DIR                   Workload deployment directory
  --columns N                          Initial terminal columns, 1 through 65535
  --rows N                             Initial terminal rows, 1 through 65535

Optional selections:
  --endpoint ID                        Grant a declared workload endpoint; repeatable
  --output-file FILE                   Publish one controller-created file
  --output-dir DIR                     Retain a controller output directory

Timeouts:
  --startup-timeout DURATION           Default 30s; 15s through 5m
  --termination-grace DURATION         Default 5s; 100ms through 1m
  --controller-finalization-timeout DURATION
                                        Default 5m; 1s through 1h
  --result-acknowledgement-timeout DURATION
                                        Default 10s; 1s through 1m
  --cleanup-timeout DURATION           Default 30s; 1s through 5m
`, "\n"))
}
