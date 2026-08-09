package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

const (
	controlledSessionWatchdogChildArgument  = "__controlled-session-watchdog"
	controlledSessionWatchdogDisarmByte     = byte(1)
	controlledSessionWatchdogReadyByte      = byte(1)
	controlledSessionWatchdogManifestLimit  = 64 * 1024
	controlledSessionWatchdogCleanupTimeout = 30 * time.Second
)

type controlledSessionWatchdogRuntimeV1 interface {
	Disarm(context.Context) error
}

type controlledSessionWatchdogCleanupBackendV1 struct {
	currentBootSession func() (string, error)
	inspectContainer   func(context.Context, string) (map[string]string, bool, error)
	removeContainer    func(context.Context, string) error
	removeChannel      func(string) error
}

// RunControlledSessionWatchdogChild handles the private same-executable child
// mode. The child accepts no resource selection in argv; it reads one frozen
// manifest from its inherited descriptor and then watches the parent pipe.
func RunControlledSessionWatchdogChild(args []string, stderr io.Writer) (int, bool) {
	if len(args) != 1 || args[0] != controlledSessionWatchdogChildArgument {
		return 0, false
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if err := runControlledSessionWatchdogChildV1(stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "controlled-session watchdog: %v\n", err)
		return 1, true
	}
	return 0, true
}

func runControlledSessionWatchdogV1(
	manifestReader io.Reader,
	liveness io.Reader,
	ready io.Writer,
	backend controlledSessionWatchdogCleanupBackendV1,
) error {
	if manifestReader == nil || liveness == nil || ready == nil ||
		backend.currentBootSession == nil || backend.inspectContainer == nil ||
		backend.removeContainer == nil || backend.removeChannel == nil {
		return fmt.Errorf("watchdog backend is incomplete")
	}
	content, err := io.ReadAll(io.LimitReader(manifestReader, controlledSessionWatchdogManifestLimit+1))
	if err != nil {
		return fmt.Errorf("read cleanup manifest: %w", err)
	}
	if len(content) > controlledSessionWatchdogManifestLimit {
		return fmt.Errorf("cleanup manifest exceeds %d bytes", controlledSessionWatchdogManifestLimit)
	}
	manifest, err := deploy.DecodeControlledSessionCleanupManifest(content)
	if err != nil {
		return err
	}
	if len(manifest.Networks) != 0 || len(manifest.Volumes) != 0 {
		return fmt.Errorf("cleanup manifest names resources unsupported by this watchdog")
	}
	if err := requireControlledSessionWatchdogBootV1(manifest, backend); err != nil {
		return err
	}
	if count, err := ready.Write([]byte{controlledSessionWatchdogReadyByte}); err != nil || count != 1 {
		if err == nil {
			err = io.ErrShortWrite
		}
		return fmt.Errorf("acknowledge watchdog readiness: %w", err)
	}

	var signal [1]byte
	// The exact byte requests disarm; EOF or any other result means parent
	// loss. Both paths converge on exact cleanup verification before exit.
	_, _ = io.ReadFull(liveness, signal[:])
	if err := requireControlledSessionWatchdogBootV1(manifest, backend); err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), controlledSessionWatchdogCleanupTimeout)
	defer cancel()
	return cleanupControlledSessionFromWatchdogV1(cleanupCtx, manifest, backend)
}

func requireControlledSessionWatchdogBootV1(
	manifest deploy.ControlledSessionCleanupManifest,
	backend controlledSessionWatchdogCleanupBackendV1,
) error {
	bootSession, err := backend.currentBootSession()
	if err != nil {
		return fmt.Errorf("read current boot identity: %w", err)
	}
	if bootSession != manifest.BootSession {
		return fmt.Errorf("cleanup manifest belongs to a different host boot")
	}
	return nil
}

func cleanupControlledSessionFromWatchdogV1(
	ctx context.Context,
	manifest deploy.ControlledSessionCleanupManifest,
	backend controlledSessionWatchdogCleanupBackendV1,
) error {
	if backend.inspectContainer == nil || backend.removeContainer == nil || backend.removeChannel == nil {
		return fmt.Errorf("watchdog cleanup backend is incomplete")
	}
	if err := deploy.ValidateControlledSessionCleanupManifest(manifest); err != nil {
		return err
	}
	var cleanupErr error
	for _, container := range []deploy.ControlledSessionContainerOwnershipV1{manifest.Workload, manifest.Controller} {
		cleanupErr = errors.Join(cleanupErr, cleanupControlledSessionWatchdogContainerV1(ctx, manifest.LiveRunID, container, backend))
	}
	cleanupErr = errors.Join(cleanupErr, backend.removeChannel(manifest.ChannelDirectory))
	return cleanupErr
}

func cleanupControlledSessionWatchdogContainerV1(
	ctx context.Context,
	liveRunID string,
	container deploy.ControlledSessionContainerOwnershipV1,
	backend controlledSessionWatchdogCleanupBackendV1,
) error {
	labels, found, err := backend.inspectContainer(ctx, container.ID)
	if err != nil {
		return fmt.Errorf("inspect controlled-session %s container %q: %w", container.Role, container.ID, err)
	}
	if !found {
		return nil
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
			return fmt.Errorf("refuse to remove controlled-session %s container %q because ownership label %q does not match", container.Role, container.ID, name)
		}
	}
	removeErr := backend.removeContainer(ctx, container.ID)
	_, stillFound, inspectErr := backend.inspectContainer(ctx, container.ID)
	if inspectErr != nil {
		return errors.Join(removeErr, fmt.Errorf("verify controlled-session %s container %q removal: %w", container.Role, container.ID, inspectErr))
	}
	if stillFound {
		return errors.Join(removeErr, fmt.Errorf("controlled-session %s container %q still exists after removal", container.Role, container.ID))
	}
	return nil
}

func productionControlledSessionWatchdogCleanupBackendV1() controlledSessionWatchdogCleanupBackendV1 {
	return controlledSessionWatchdogCleanupBackendV1{
		currentBootSession: deploy.CurrentBootSessionIDV1,
		inspectContainer:   inspectControlledSessionWatchdogContainerV1,
		removeContainer: func(ctx context.Context, containerID string) error {
			return runDockerCommand(CommandSpec{Name: "docker", Args: []string{"container", "rm", "--force", containerID}}, RunOptions{Context: ctx})
		},
		removeChannel: removeControlledSessionChannelDirectoryV1,
	}
}

func removeControlledSessionChannelDirectoryV1(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove controlled-session channel directory %q: %w", path, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("controlled-session channel directory %q still exists after removal", path)
		}
		return fmt.Errorf("verify controlled-session channel directory %q removal: %w", path, err)
	}
	return nil
}

func inspectControlledSessionWatchdogContainerV1(ctx context.Context, containerID string) (map[string]string, bool, error) {
	var output bytes.Buffer
	err := runDockerCommand(CommandSpec{Name: "docker", Args: []string{
		"container", "inspect", "--format", "{{json .Id}} {{json .Config.Labels}}", containerID,
	}}, RunOptions{Context: ctx, Stdout: &output, Stderr: &output})
	if err != nil {
		message := strings.TrimSpace(output.String())
		if strings.Contains(message, "No such object") || strings.Contains(message, "No such container") {
			return nil, false, nil
		}
		if message != "" {
			return nil, false, fmt.Errorf("%w: %s", err, message)
		}
		return nil, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var inspectedID string
	var labels map[string]string
	if err := decoder.Decode(&inspectedID); err != nil {
		return nil, false, fmt.Errorf("decode inspected container ID: %w", err)
	}
	if err := decoder.Decode(&labels); err != nil {
		return nil, false, fmt.Errorf("decode inspected container labels: %w", err)
	}
	if inspectedID != containerID {
		return nil, false, fmt.Errorf("Docker inspected container %q as unexpected full ID %q", containerID, inspectedID)
	}
	return labels, true, nil
}
