package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"
)

type RuntimeStatusV1 struct {
	Status  string
	Started string
}

func ObserveRuntimeStatusV1(ctx context.Context, plan DockerExecutionPlan, dockerPreflightTimeout time.Duration) (RuntimeStatusV1, error) {
	if ctx == nil {
		return RuntimeStatusV1{}, fmt.Errorf("observe runtime status requires a context")
	}
	if strings.TrimSpace(plan.ContainerName) == "" {
		return RuntimeStatusV1{}, fmt.Errorf("observe runtime status requires a container name")
	}
	output, err := commandOutput(
		CommandSpec{
			Name: "docker",
			Args: []string{"inspect", "--format", "{{json .State}}", plan.ContainerName},
		},
		RunOptions{Context: ctx, DockerPreflightTimeout: dockerPreflightTimeout},
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such object") {
			return RuntimeStatusV1{Status: "stopped"}, nil
		}
		return RuntimeStatusV1{}, fmt.Errorf("inspect runtime status: %w", err)
	}
	result, err := decodeRuntimeStatusV1(output, time.Now())
	if err != nil {
		return RuntimeStatusV1{}, err
	}
	return result, nil
}

func decodeRuntimeStatusV1(output []byte, now time.Time) (RuntimeStatusV1, error) {
	var state struct {
		Running   bool   `json:"Running"`
		Status    string `json:"Status"`
		StartedAt string `json:"StartedAt"`
	}
	if err := json.Unmarshal(output, &state); err != nil {
		return RuntimeStatusV1{}, fmt.Errorf("decode runtime container status: %w", err)
	}
	result := RuntimeStatusV1{Status: "stopped"}
	if state.Running {
		result.Status = "running"
		startedAt, err := time.Parse(time.RFC3339Nano, state.StartedAt)
		if err != nil {
			return RuntimeStatusV1{}, fmt.Errorf("decode runtime container start time: %w", err)
		}
		result.Started = runtimeElapsedDescription(now.Sub(startedAt))
	}
	return result, nil
}

func WriteRuntimeStatusV1(output io.Writer, status RuntimeStatusV1, plan DockerExecutionPlan) error {
	if output == nil {
		return nil
	}
	value := strings.TrimSpace(status.Status)
	if value == "" {
		value = "unknown"
	}
	if _, err := fmt.Fprintf(output, "status: %s\n", value); err != nil {
		return err
	}
	if status.Started != "" {
		if _, err := fmt.Fprintf(output, "started: %s\n", status.Started); err != nil {
			return err
		}
	}
	if value != "running" || plan.Workload == nil {
		return nil
	}
	names := make([]string, 0, len(plan.Workload.Endpoints))
	for name := range plan.Workload.Endpoints {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		endpoint := plan.Workload.Endpoints[name]
		scheme := strings.TrimSpace(endpoint.Scheme)
		if scheme == "" {
			scheme = "tcp"
		}
		address := net.JoinHostPort(endpoint.PublishAddress, fmt.Sprintf("%d", endpoint.PublishedPort))
		if _, err := fmt.Fprintf(output, "endpoint %s: %s://%s\n", name, scheme, address); err != nil {
			return err
		}
	}
	return nil
}

func runtimeElapsedDescription(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Minute:
		seconds := int(elapsed / time.Second)
		return fmt.Sprintf("%d %s ago", seconds, pluralRuntimeUnit(seconds, "second"))
	case elapsed < time.Hour:
		minutes := int(elapsed / time.Minute)
		return fmt.Sprintf("%d %s ago", minutes, pluralRuntimeUnit(minutes, "minute"))
	case elapsed < 24*time.Hour:
		hours := int(elapsed / time.Hour)
		return fmt.Sprintf("%d %s ago", hours, pluralRuntimeUnit(hours, "hour"))
	default:
		days := int(elapsed / (24 * time.Hour))
		return fmt.Sprintf("%d %s ago", days, pluralRuntimeUnit(days, "day"))
	}
}

func pluralRuntimeUnit(value int, unit string) string {
	if value == 1 {
		return unit
	}
	return unit + "s"
}
