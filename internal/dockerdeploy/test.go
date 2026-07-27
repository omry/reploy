package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

var runTestCommandOutput = commandOutput
var runCurrentRuntimeTest = RunCurrentRuntimeTestV1

type TestOptions struct {
	Dir                    string
	Timeout                time.Duration
	Stdout                 io.Writer
	RestartingDiagnostics  string
	DockerPreflightTimeout time.Duration
}

func TestServer(options TestOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	stateSchema, err := runtimeStateSchema(options.Dir)
	if err != nil {
		return err
	}
	if stateSchema != deploy.StateSchemaV1 {
		return fmt.Errorf("runtime test state schema %q is unsupported; expected %q", stateSchema, deploy.StateSchemaV1)
	}
	runtime, err := CurrentStagedProviderBuildRuntimeV1()
	if err != nil {
		return err
	}
	return runCurrentRuntimeTest(context.Background(), CurrentRuntimeTestInputV1{
		DeploymentDir: options.Dir, Runtime: runtime, Timeout: options.Timeout, Stdout: options.Stdout,
		RestartingDiagnostics: options.RestartingDiagnostics, DockerPreflightTimeout: options.DockerPreflightTimeout,
	})
}

func requireComposeServiceRunning(dir string, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
	states, err := composeServiceStates(dir, dockerPreflightTimeout)
	if err != nil {
		return err
	}
	if len(states) == 0 {
		return fmt.Errorf("service is not started; run reploy up before testing health")
	}
	for _, state := range states {
		if serviceStateName(state) == "running" {
			return nil
		}
	}
	stateList := strings.Join(states, ", ")
	if serviceStatesContain(states, "restarting") {
		if restartingDiagnostics == "" {
			return fmt.Errorf("service is restarting; current state: %s; run reploy logs and reploy app config check", stateList)
		}
		return fmt.Errorf("service is restarting; current state: %s\n%s", stateList, restartingDiagnostics)
	}
	return fmt.Errorf("service is not running; current state: %s", stateList)
}

func serviceStatesContain(states []string, expected string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	for _, state := range states {
		if serviceStateName(state) == expected {
			return true
		}
	}
	return false
}

func serviceStateName(state string) string {
	state = strings.ToLower(strings.TrimSpace(state))
	if before, _, ok := strings.Cut(state, " ("); ok {
		state = strings.TrimSpace(before)
	}
	return state
}

func composeServiceStates(dir string, dockerPreflightTimeout time.Duration) ([]string, error) {
	projectName, err := deploymentComposeProjectName(dir)
	if err != nil {
		return nil, err
	}
	spec := composeCommandWithProject(dir, projectName, "ps", "--all", "--format", "json")
	output, err := runTestCommandOutput(spec, RunOptions{DockerPreflightTimeout: dockerPreflightTimeout})
	if err != nil {
		return nil, commandErrorWithOutput("docker compose ps", output, err)
	}
	return parseComposeServiceStates(output)
}

func parseComposeServiceStates(output []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var rows []composePSRow
	if err := json.Unmarshal(trimmed, &rows); err == nil {
		return composeRowsStates(rows), nil
	}
	var states []string
	for _, line := range bytes.Split(trimmed, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var row composePSRow
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("parse docker compose ps json: %w", err)
		}
		states = append(states, composeRowState(row))
	}
	return states, nil
}

type composePSRow struct {
	State    string `json:"State"`
	ExitCode *int   `json:"ExitCode,omitempty"`
}

func composeRowsStates(rows []composePSRow) []string {
	states := make([]string, 0, len(rows))
	for _, row := range rows {
		states = append(states, composeRowState(row))
	}
	return states
}

func composeRowState(row composePSRow) string {
	state := strings.TrimSpace(row.State)
	if state == "" {
		state = "unknown"
	}
	if row.ExitCode != nil && *row.ExitCode != 0 && serviceStateName(state) != "running" {
		return fmt.Sprintf("%s (exit code %d)", state, *row.ExitCode)
	}
	return state
}

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func commandOutput(spec CommandSpec, options RunOptions) ([]byte, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if spec.Name == "docker" {
		if err := dockerPreflight(ctx, spec, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout)); err != nil {
			return nil, err
		}
	}
	command := exec.CommandContext(ctx, spec.Name, spec.Args...)
	command.Dir = spec.Dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	output := append([]byte{}, stdout.Bytes()...)
	output = append(output, stderr.Bytes()...)
	return output, err
}
