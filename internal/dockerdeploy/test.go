package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

var runTestCommandOutput = commandOutput

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
	state, err := loadState(options.Dir)
	if err != nil {
		return err
	}
	stdout, _ := deploymentOutputWritersForDeployment(options.Dir, state, options.Stdout, nil)
	options.Stdout = stdout
	serverURL, err := ServerURL(options.Dir)
	if err != nil {
		return err
	}
	healthURL := serverURL.String()
	health, err := healthConfig(options.Dir)
	if err != nil {
		return err
	}
	if err := ensureRuntimeCompose(options.Dir); err != nil {
		return fmt.Errorf("ensure runtime compose: %w", err)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !healthTLSVerify(health)},
		},
	}
	if err := requireComposeServiceRunning(options.Dir, options.RestartingDiagnostics, options.DockerPreflightTimeout); err != nil {
		return err
	}
	check := func() error {
		response, err := client.Get(healthURL)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				if options.Stdout != nil {
					fmt.Fprintf(options.Stdout, "ok: %s\n", healthURL)
				}
				return nil
			}
			return fmt.Errorf("health check returned HTTP %d", response.StatusCode)
		}
		return err
	}
	lastErr := check()
	if lastErr == nil {
		return nil
	}
	deadline := time.Now().Add(options.Timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("server health check failed: %w", lastErr)
		}
		time.Sleep(1 * time.Second)
		if err := requireComposeServiceRunning(options.Dir, options.RestartingDiagnostics, options.DockerPreflightTimeout); err != nil {
			return err
		}
		lastErr = check()
		if lastErr == nil {
			return nil
		}
	}
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

func ServerURL(dir string) (*url.URL, error) {
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	values, err := readDockerEnv(absoluteDir)
	if err != nil {
		return nil, err
	}
	health, err := healthConfig(absoluteDir)
	if err != nil {
		return nil, err
	}
	scheme := envValue(values, health.SchemeEnv, defaultString(health.DefaultScheme, "https"))
	host := envValue(values, health.HostEnv, defaultString(health.DefaultHost, "127.0.0.1"))
	port := envValue(values, health.PortEnv, health.DefaultPort)
	if port == "" {
		return nil, fmt.Errorf("blueprint health probe is missing docker.health.default_port")
	}
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), Path: health.Path}, nil
}

func healthConfig(dir string) (deploy.DockerHealthConfig, error) {
	state, err := loadState(dir)
	if err != nil {
		return deploy.DockerHealthConfig{}, err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return deploy.DockerHealthConfig{}, err
	}
	health := pack.Docker.Health
	if health.Path == "" {
		return deploy.DockerHealthConfig{}, fmt.Errorf("blueprint does not declare docker.health.path")
	}
	if health.SchemeEnv == "" {
		return deploy.DockerHealthConfig{}, fmt.Errorf("blueprint health probe is missing docker.health.scheme_env")
	}
	if health.HostEnv == "" {
		return deploy.DockerHealthConfig{}, fmt.Errorf("blueprint health probe is missing docker.health.host_env")
	}
	if health.PortEnv == "" {
		return deploy.DockerHealthConfig{}, fmt.Errorf("blueprint health probe is missing docker.health.port_env")
	}
	return health, nil
}

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func healthTLSVerify(health deploy.DockerHealthConfig) bool {
	if health.TLSVerify == nil {
		return true
	}
	return *health.TLSVerify
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
