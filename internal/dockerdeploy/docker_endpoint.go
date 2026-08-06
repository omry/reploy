package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const dockerContextHostFormatV1 = "{{.Endpoints.docker.Host}}"

func requireDefaultLocalDockerEndpointV1(ctx context.Context) error {
	return requireLocalDockerEndpointV1(ctx, CommandSpec{Name: "docker"}, defaultDockerPreflightTimeout)
}

func requireLocalDockerEndpointV1(ctx context.Context, spec CommandSpec, timeout time.Duration) error {
	endpoint, source, err := effectiveDockerEndpointV1(ctx, spec, timeout)
	if err != nil {
		return err
	}
	if localDockerEndpointV1(endpoint) {
		return nil
	}
	return fmt.Errorf(
		"remote Docker endpoint %q selected by %s is not supported; switch to a local Docker Engine or Docker Desktop context",
		endpoint,
		source,
	)
}

func effectiveDockerEndpointV1(ctx context.Context, spec CommandSpec, timeout time.Duration) (string, string, error) {
	if spec.Name == "" {
		spec.Name = "docker"
	}
	if contextName := commandEnvironmentValueV1(spec, "DOCKER_CONTEXT"); contextName != "" {
		endpoint, err := inspectDockerContextEndpointV1(ctx, spec, timeout, contextName)
		return endpoint, fmt.Sprintf("Docker context %q", contextName), err
	}
	if endpoint := commandEnvironmentValueV1(spec, "DOCKER_HOST"); endpoint != "" {
		return endpoint, "DOCKER_HOST", nil
	}
	endpoint, err := inspectDockerContextEndpointV1(ctx, spec, timeout, "")
	return endpoint, "the active Docker context", err
}

func inspectDockerContextEndpointV1(ctx context.Context, spec CommandSpec, timeout time.Duration, contextName string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(timeout))
	defer cancel()
	args := []string{"context", "inspect", "--format", dockerContextHostFormatV1}
	if contextName != "" {
		args = append(args, contextName)
	}
	command := exec.CommandContext(probeCtx, spec.Name, args...)
	command.Dir = spec.Dir
	if len(spec.Env) > 0 {
		command.Env = append(os.Environ(), spec.Env...)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if probeCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("Docker context inspection did not respond within %s", effectiveDockerPreflightTimeout(timeout))
		}
		if text := trimmedCommandOutput(strings.Join([]string{stdout.String(), stderr.String()}, "\n")); text != "" {
			return "", fmt.Errorf("inspect Docker endpoint: %w\ncommand output:\n%s", err, text)
		}
		return "", fmt.Errorf("inspect Docker endpoint: %w", err)
	}
	endpoint := strings.TrimSpace(stdout.String())
	if endpoint == "" {
		return "", fmt.Errorf("Docker context did not report an endpoint")
	}
	return endpoint, nil
}

func commandEnvironmentValueV1(spec CommandSpec, name string) string {
	value := os.Getenv(name)
	prefix := name + "="
	for _, assignment := range spec.Env {
		if strings.HasPrefix(assignment, prefix) {
			value = strings.TrimPrefix(assignment, prefix)
		}
	}
	return strings.TrimSpace(value)
}

func localDockerEndpointV1(endpoint string) bool {
	scheme, _, found := strings.Cut(strings.TrimSpace(endpoint), ":")
	if !found {
		return false
	}
	switch strings.ToLower(scheme) {
	case "unix", "npipe":
		return true
	default:
		return false
	}
}
