package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

const liveRunRecoveryCleanupTimeoutV1 = 2 * time.Second

type legacyControlledSessionDockerEndpointResolverV1 func(context.Context) (string, error)

func recoverLiveRunQueueV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	notice io.Writer,
	removeContainer commandRunner,
) (deploy.LiveRunRecoveryV1, error) {
	return recoverLiveRunQueueWithinV1(
		ctx, operation, notice, removeContainer, liveRunRecoveryCleanupTimeoutV1,
		func(ctx context.Context) (string, error) {
			return verifiedLocalDockerEndpointV1(ctx, CommandSpec{Name: "docker"}, defaultDockerPreflightTimeout)
		},
	)
}

func recoverLiveRunQueueWithinV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	notice io.Writer,
	removeContainer commandRunner,
	cleanupTimeout time.Duration,
	resolveLegacyDockerEndpoint legacyControlledSessionDockerEndpointResolverV1,
) (deploy.LiveRunRecoveryV1, error) {
	recovery, err := operation.RecoverLiveRunQueueV1()
	if err != nil {
		return deploy.LiveRunRecoveryV1{}, err
	}
	writeLiveRunRecoveryNoticeV1(notice, recovery)
	if removeContainer == nil {
		writeScheduledLiveRunCleanupNoticeV1(notice, recovery)
		return recovery, nil
	}
	queue, _, err := operation.ReadLiveRunQueueV1()
	if err != nil {
		return deploy.LiveRunRecoveryV1{}, err
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	for _, cleanup := range queue.Cleanup {
		if err := cleanupContext.Err(); err != nil {
			if notice != nil {
				fmt.Fprintf(notice, "warning: deferred remaining recovered container cleanup: %v\n", err)
			}
			break
		}
		removeErr := removeContainer(
			TemporaryContainerCleanupCommand(cleanup.Container),
			RunOptions{Context: cleanupContext},
		)
		if removeErr != nil && !isMissingContainerCleanupError(removeErr) {
			if notice != nil {
				fmt.Fprintf(notice,
					"warning: deferred cleanup of recovered %s %q container %q: %v\n",
					cleanup.Kind, cleanup.Name, cleanup.Container, removeErr,
				)
			}
			continue
		}
		removed, err := operation.CompleteLiveRunContainerCleanupV1(cleanup.Container)
		if err != nil {
			return deploy.LiveRunRecoveryV1{}, err
		}
		if !removed {
			return deploy.LiveRunRecoveryV1{}, fmt.Errorf(
				"recovered container cleanup %q disappeared while the operation lock was held",
				cleanup.Container,
			)
		}
	}
	var controlledSessionCleanupErr error
	for _, ownership := range recovery.ControlledSessions {
		if err := cleanupContext.Err(); err != nil {
			controlledSessionCleanupErr = errors.Join(controlledSessionCleanupErr, err)
			if notice != nil {
				fmt.Fprintf(notice, "warning: deferred remaining recovered controlled-session cleanup: %v\n", err)
			}
			break
		}
		if err := cleanupControlledSessionRecoveryV1(
			cleanupContext, operation, ownership, removeContainer, resolveLegacyDockerEndpoint,
		); err != nil {
			controlledSessionCleanupErr = errors.Join(controlledSessionCleanupErr, err)
			if notice != nil {
				fmt.Fprintf(notice,
					"warning: deferred cleanup of recovered controlled session %q: %v\n",
					ownership.LiveRunID, err,
				)
			}
			continue
		}
		removed, err := operation.CompleteControlledSessionV1(ownership.LiveRunID)
		if err != nil {
			return deploy.LiveRunRecoveryV1{}, err
		}
		if !removed {
			return deploy.LiveRunRecoveryV1{}, fmt.Errorf(
				"recovered controlled-session ownership %q disappeared while the operation lock was held",
				ownership.LiveRunID,
			)
		}
	}
	if controlledSessionCleanupErr != nil {
		return recovery, fmt.Errorf("controlled-session recovery remains incomplete: %w", controlledSessionCleanupErr)
	}
	return recovery, nil
}

func cleanupControlledSessionRecoveryV1(
	ctx context.Context,
	operation *deploy.OperationLock,
	ownership deploy.ControlledSessionOwnershipV1,
	run commandRunner,
	resolveLegacyDockerEndpoint legacyControlledSessionDockerEndpointResolverV1,
) error {
	deploymentDir := filepath.Dir(filepath.Dir(operation.Path()))
	ownerDeploymentDir := deploymentDir
	if ownership.WorkloadDeploymentDirectory != "" {
		controllerParticipant := deploymentDir == ownership.ControllerDeploymentDirectory
		workloadParticipant := deploymentDir == ownership.WorkloadDeploymentDirectory
		if !controllerParticipant && !workloadParticipant {
			var err error
			controllerParticipant, err = sameControlledSessionDeploymentV1(deploymentDir, ownership.ControllerDeploymentDirectory)
			if err != nil {
				return fmt.Errorf("verify controlled-session controller participant: %w", err)
			}
			workloadParticipant, err = sameControlledSessionDeploymentV1(deploymentDir, ownership.WorkloadDeploymentDirectory)
			if err != nil {
				return fmt.Errorf("verify controlled-session workload participant: %w", err)
			}
		}
		if controllerParticipant == workloadParticipant {
			return fmt.Errorf("refuse controlled-session recovery because deployment %q is not an exact session participant", deploymentDir)
		}
		ownerDeploymentDir = ownership.WorkloadDeploymentDirectory
	}
	expectedChannel := filepath.Join(ownerDeploymentDir, privateRuntimeMetadataDirectoryName, "sessions", ownership.LiveRunID)
	if ownership.ChannelDirectory != expectedChannel {
		return fmt.Errorf("refuse controlled-session recovery because channel directory %q is outside the exact deployment session path", ownership.ChannelDirectory)
	}
	endpoint := ownership.DockerEndpoint
	if endpoint == "" {
		if resolveLegacyDockerEndpoint == nil {
			return fmt.Errorf("resolve legacy controlled-session Docker endpoint: resolver is unavailable")
		}
		var err error
		endpoint, err = resolveLegacyDockerEndpoint(ctx)
		if err != nil {
			return fmt.Errorf("resolve legacy controlled-session Docker endpoint: %w", err)
		}
	}
	pinnedRun, err := commandRunnerForPinnedDockerEndpointV1(endpoint, run)
	if err != nil {
		return fmt.Errorf("bind recovered controlled-session Docker endpoint: %w", err)
	}
	var cleanupErr error
	for _, container := range []deploy.ControlledSessionContainerOwnershipV1{ownership.Workload, ownership.Controller} {
		cleanupErr = errors.Join(cleanupErr, cleanupControlledSessionRecoveryContainerV1(ctx, ownership.LiveRunID, container, pinnedRun))
	}
	if ownership.NetworkName != "" {
		cleanupErr = errors.Join(cleanupErr, cleanupControlledSessionRecoveryNetworkV1(ctx, ownership, pinnedRun))
	}
	cleanupErr = errors.Join(cleanupErr, removeControlledSessionChannelDirectoryV1(ownership.ChannelDirectory))
	return cleanupErr
}

func sameControlledSessionDeploymentV1(actual string, expected string) (bool, error) {
	actualInfo, err := os.Stat(actual)
	if err != nil {
		return false, fmt.Errorf("inspect recovered deployment %q: %w", actual, err)
	}
	expectedInfo, err := os.Stat(expected)
	if err != nil {
		return false, fmt.Errorf("inspect recorded deployment %q: %w", expected, err)
	}
	return os.SameFile(actualInfo, expectedInfo), nil
}

func cleanupControlledSessionRecoveryNetworkV1(
	ctx context.Context,
	ownership deploy.ControlledSessionOwnershipV1,
	run commandRunner,
) error {
	target := ownership.NetworkID
	if target == "" {
		target = ownership.NetworkName
	}
	inspection, found, err := inspectControlledSessionRecoveryNetworkV1(ctx, target, run)
	if err != nil {
		return fmt.Errorf("inspect recovered controlled-session network %q: %w", target, err)
	}
	if !found {
		return nil
	}
	if ownership.NetworkID != "" && inspection.ID != ownership.NetworkID {
		return fmt.Errorf("refuse to remove recovered controlled-session network %q because Docker returned full ID %q", ownership.NetworkID, inspection.ID)
	}
	if inspection.Name != ownership.NetworkName {
		return fmt.Errorf("refuse to remove recovered controlled-session network %q because network name does not match", inspection.ID)
	}
	expectedLabels := map[string]string{
		"io.reploy.session.live-run": ownership.LiveRunID,
		"io.reploy.session.role":     deploy.ControlledSessionNetworkRoleV1,
	}
	if !reflect.DeepEqual(inspection.Labels, expectedLabels) {
		return fmt.Errorf("refuse to remove recovered controlled-session network %q because ownership labels do not match", inspection.ID)
	}
	if len(inspection.Members) != 0 {
		return fmt.Errorf("refuse to remove recovered controlled-session network %q because it still has members", inspection.ID)
	}
	removeErr := run(CommandSpec{Name: "docker", Args: []string{"network", "rm", inspection.ID}}, RunOptions{Context: ctx})
	if removeErr != nil && !isMissingDockerNetworkResponseV1(removeErr, "") {
		return fmt.Errorf("remove recovered controlled-session network %q: %w", inspection.ID, removeErr)
	}
	_, stillFound, inspectErr := inspectControlledSessionRecoveryNetworkV1(ctx, inspection.ID, run)
	if inspectErr != nil {
		return fmt.Errorf("verify recovered controlled-session network %q removal: %w", inspection.ID, inspectErr)
	}
	if stillFound {
		return fmt.Errorf("recovered controlled-session network %q still exists after removal", inspection.ID)
	}
	return nil
}

func inspectControlledSessionRecoveryNetworkV1(
	ctx context.Context,
	target string,
	run commandRunner,
) (controlledSessionWatchdogNetworkInspectionV1, bool, error) {
	var output bytes.Buffer
	err := run(CommandSpec{Name: "docker", Args: []string{
		"network", "inspect", "--format", "{{json .Id}} {{json .Name}} {{json .Labels}} {{json .Containers}}", target,
	}}, RunOptions{Context: ctx, Stdout: &output, Stderr: &output})
	if err != nil {
		message := strings.TrimSpace(output.String())
		if isMissingDockerNetworkResponseV1(err, message) {
			return controlledSessionWatchdogNetworkInspectionV1{}, false, nil
		}
		if message != "" {
			return controlledSessionWatchdogNetworkInspectionV1{}, false, fmt.Errorf("%w: %s", err, message)
		}
		return controlledSessionWatchdogNetworkInspectionV1{}, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var inspection controlledSessionWatchdogNetworkInspectionV1
	var members map[string]dockerSessionNetworkContainerV1
	if err := decoder.Decode(&inspection.ID); err != nil {
		return controlledSessionWatchdogNetworkInspectionV1{}, false, fmt.Errorf("decode recovered controlled-session network ID: %w", err)
	}
	parsed, err := parseDockerNetworkIDV1(inspection.ID)
	if err != nil {
		return controlledSessionWatchdogNetworkInspectionV1{}, false, fmt.Errorf("decode recovered controlled-session network ID: %w", err)
	}
	if parsed != inspection.ID {
		return controlledSessionWatchdogNetworkInspectionV1{}, false, fmt.Errorf("recovered controlled-session network ID is not canonical")
	}
	if err := decoder.Decode(&inspection.Name); err != nil {
		return controlledSessionWatchdogNetworkInspectionV1{}, false, fmt.Errorf("decode recovered controlled-session network name: %w", err)
	}
	if err := decoder.Decode(&inspection.Labels); err != nil {
		return controlledSessionWatchdogNetworkInspectionV1{}, false, fmt.Errorf("decode recovered controlled-session network labels: %w", err)
	}
	if err := decoder.Decode(&members); err != nil {
		return controlledSessionWatchdogNetworkInspectionV1{}, false, fmt.Errorf("decode recovered controlled-session network members: %w", err)
	}
	inspection.Members = make(map[string]string, len(members))
	for id, member := range members {
		inspection.Members[id] = member.Name
	}
	return inspection, true, nil
}

func cleanupControlledSessionRecoveryContainerV1(
	ctx context.Context,
	liveRunID string,
	container deploy.ControlledSessionContainerOwnershipV1,
	run commandRunner,
) error {
	target := container.ID
	if target == "" {
		target = container.Name
	}
	containerID, labels, found, err := inspectControlledSessionRecoveryContainerV1(ctx, target, run)
	if err != nil {
		return fmt.Errorf("inspect recovered controlled-session %s container %q: %w", container.Role, target, err)
	}
	if !found {
		return nil
	}
	if container.ID != "" && containerID != container.ID {
		return fmt.Errorf("refuse to remove recovered controlled-session %s container %q because Docker returned full ID %q", container.Role, container.ID, containerID)
	}
	expected := map[string]string{
		"io.reploy.session.build":       container.BuildIdentity,
		"io.reploy.session.environment": container.DeploymentID,
		"io.reploy.session.generation":  container.GenerationReference,
		"io.reploy.session.live-run":    liveRunID,
		"io.reploy.session.role":        container.Role,
	}
	for name, value := range expected {
		if labels[name] != value {
			return fmt.Errorf("refuse to remove recovered controlled-session %s container %q because ownership label %q does not match", container.Role, containerID, name)
		}
	}
	removeErr := run(TemporaryContainerCleanupCommand(containerID), RunOptions{Context: ctx})
	if removeErr != nil && !isMissingContainerCleanupError(removeErr) {
		return fmt.Errorf("remove recovered controlled-session %s container %q: %w", container.Role, containerID, removeErr)
	}
	_, _, stillFound, inspectErr := inspectControlledSessionRecoveryContainerV1(ctx, containerID, run)
	if inspectErr != nil {
		return fmt.Errorf("verify recovered controlled-session %s container %q removal: %w", container.Role, containerID, inspectErr)
	}
	if stillFound {
		return fmt.Errorf("recovered controlled-session %s container %q still exists after removal", container.Role, containerID)
	}
	return nil
}

func inspectControlledSessionRecoveryContainerV1(
	ctx context.Context,
	target string,
	run commandRunner,
) (string, map[string]string, bool, error) {
	var output bytes.Buffer
	err := run(CommandSpec{Name: "docker", Args: []string{
		"container", "inspect", "--format", "{{json .Id}} {{json .Config.Labels}}", target,
	}}, RunOptions{Context: ctx, Stdout: &output, Stderr: &output})
	if err != nil {
		message := strings.TrimSpace(output.String())
		if isMissingContainerCleanupError(err) || strings.Contains(strings.ToLower(message), "no such container") {
			return "", nil, false, nil
		}
		if message != "" {
			return "", nil, false, fmt.Errorf("%w: %s", err, message)
		}
		return "", nil, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var containerID string
	var labels map[string]string
	if err := decoder.Decode(&containerID); err != nil {
		return "", nil, false, fmt.Errorf("decode recovered controlled-session container ID: %w", err)
	}
	if parsed, err := parseDockerContainerIDV1(containerID); err != nil || parsed != containerID || containerID != strings.ToLower(containerID) {
		if err == nil {
			err = fmt.Errorf("Docker returned a noncanonical full container ID %q", containerID)
		}
		return "", nil, false, err
	}
	if err := decoder.Decode(&labels); err != nil {
		return "", nil, false, fmt.Errorf("decode recovered controlled-session container labels: %w", err)
	}
	return containerID, labels, true, nil
}

func writeLiveRunRecoveryNoticeV1(output io.Writer, recovery deploy.LiveRunRecoveryV1) {
	if output == nil {
		return
	}
	for _, recovered := range recovery.Removed {
		entry := recovered.Run
		switch recovered.Reason {
		case deploy.LiveRunRecoveryAbandonedOwnerV1:
			fmt.Fprintf(output,
				"warning: skipped abandoned %s %q (%s): its owning Reploy client exited\n",
				admissionRecoveryKindV1(entry), entry.Name, entry.ID,
			)
		case deploy.LiveRunRecoveryPriorSessionV1:
			fmt.Fprintf(output,
				"warning: skipped prior-session %s %q (%s) after a host restart\n",
				admissionRecoveryKindV1(entry), entry.Name, entry.ID,
			)
		case deploy.LiveRunRecoveryLegacyEntryV1:
			fmt.Fprintf(output,
				"warning: skipped legacy %s %q (%s): its owner cannot be verified\n",
				admissionRecoveryKindV1(entry), entry.Name, entry.ID,
			)
		}
	}
}

func writeScheduledLiveRunCleanupNoticeV1(output io.Writer, recovery deploy.LiveRunRecoveryV1) {
	if output == nil {
		return
	}
	for _, recovered := range recovery.Removed {
		if recovered.Run.Container != "" {
			fmt.Fprintf(output, "warning: scheduled cleanup for transient container %q\n", recovered.Run.Container)
		}
	}
	for _, ownership := range recovery.ControlledSessions {
		fmt.Fprintf(output, "warning: scheduled cleanup for controlled session %q\n", ownership.LiveRunID)
	}
}

// WriteLiveRunRecoveryNoticeV1 reports successful automatic recovery without
// exposing the queue's internal control-marker representation.
func WriteLiveRunRecoveryNoticeV1(output io.Writer, recovery deploy.LiveRunRecoveryV1) {
	writeLiveRunRecoveryNoticeV1(output, recovery)
}

func admissionRecoveryKindV1(entry deploy.LiveRunV1) string {
	switch entry.Kind {
	case deploy.LiveRunKindAppV1:
		return "app command"
	case deploy.LiveRunKindShellV1:
		return "shell"
	case deploy.LiveRunKindControlV1:
		return "lifecycle operation"
	default:
		return "queued operation"
	}
}
