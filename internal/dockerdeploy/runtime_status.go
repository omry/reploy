package dockerdeploy

import (
	"context"
	"fmt"
	"github.com/omry/reploy/internal/deploy"
	"io"
	"net"
	"sort"
	"strings"
	"time"
)

type RuntimeStatusV1 struct {
	Status  string
	Started string
	Detail  string
}

func ObserveRuntimeStatusV1(ctx context.Context, operation *deploy.OperationLock, plan DockerExecutionPlan, timeout time.Duration) (RuntimeStatusV1, error) {
	if ctx == nil || operation == nil || strings.TrimSpace(plan.ContainerName) == "" {
		return RuntimeStatusV1{}, fmt.Errorf("runtime status requires a context, operation lock, and container name")
	}
	command, run, err := bindPinnedDockerCommandRunnerV1(ctx, CommandSpec{Name: "docker"}, timeout)
	if err != nil {
		return RuntimeStatusV1{}, err
	}
	binding, err := workloadRuntimeBindingV1(plan)
	if err != nil {
		return RuntimeStatusV1{}, err
	}
	expected, err := privateWorkloadRecordV1(command, binding)
	if err != nil {
		return RuntimeStatusV1{}, err
	}
	record, found, err := operation.ReadWorkloadLifecycleV1()
	if err != nil {
		return RuntimeStatusV1{}, err
	}
	current, err := inspectPrivateWorkloadContainerV1(ctx, command, plan.ContainerName, RunOptions{DockerPreflightTimeout: timeout}, run)
	if err != nil {
		return RuntimeStatusV1{}, err
	}
	return workloadRuntimeStatusV1(record, found, expected, current, plan.PrivateEnvironment, time.Now()), nil
}

func workloadRuntimeStatusV1(record deploy.WorkloadLifecycleV1, found bool, expected deploy.WorkloadLifecycleV1, current privateWorkloadContainerV1, private bool, now time.Time) RuntimeStatusV1 {
	if !current.Running {
		return RuntimeStatusV1{Status: "down"}
	}
	result := RuntimeStatusV1{Status: "starting", Detail: "Startup completion is unconfirmed; use `reploy restart` if startup was interrupted."}
	started, err := time.Parse(time.RFC3339Nano, current.StartedAt)
	if err == nil {
		result.Started = runtimeElapsedDescription(now.Sub(started))
	}
	same := found && record.Endpoint == expected.Endpoint && record.ContainerID == current.ID && record.StartedAt == current.StartedAt
	if same && record.State == "stopping" {
		result.Status, result.Detail = "stopping", ""
	}
	// A confirmed record describes the active process and its original
	// startup mode. Current .env presence applies only to a fresh process;
	// claimed private transfers still require completed handoff evidence.
	if same && record.Runtime == expected.Runtime && record.State == "up" && record.StartupComplete &&
		(!record.TransferClaimed || record.HandoffComplete) {
		result.Status, result.Detail = "up", ""
	}
	return result
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
	if status.Detail != "" {
		if _, err := fmt.Fprintln(output, status.Detail); err != nil {
			return err
		}
	}
	if status.Started != "" {
		if _, err := fmt.Fprintf(output, "started: %s\n", status.Started); err != nil {
			return err
		}
	}
	if value != "up" || plan.Workload == nil {
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
