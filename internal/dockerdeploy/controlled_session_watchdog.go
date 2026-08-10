package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

const (
	controlledSessionWatchdogChildArgument  = "__controlled-session-watchdog"
	controlledSessionWatchdogDisarmByte     = byte(1)
	controlledSessionWatchdogReadyByte      = byte(1)
	controlledSessionWatchdogManifestLimit  = 64 * 1024
	controlledSessionWatchdogCleanupTimeout = 30 * time.Second
	controlledSessionWatchdogRetryInitial   = time.Second
	controlledSessionWatchdogRetryMaximum   = 30 * time.Second
)

type controlledSessionWatchdogRuntimeV1 interface {
	Done() <-chan struct{}
	ExitError() error
	Close() error
	Disarm(context.Context) error
}

type controlledSessionWatchdogCleanupBackendV1 struct {
	currentBootSession func() (string, error)
	bindDockerEndpoint func(string) error
	inspectContainer   func(context.Context, string) (map[string]string, bool, error)
	removeContainer    func(context.Context, string) error
	removeChannel      func(string) error
	dockerUnavailable  func(context.Context) (bool, error)
	waitRetry          func(time.Duration)
	now                func() time.Time
	writeIncident      func(deploy.ControlledSessionIncidentReceiptV1) error
}

type controlledSessionWatchdogCleanupReportV1 struct {
	controller deploy.ControlledSessionIncidentResourceStatusV1
	workload   deploy.ControlledSessionIncidentResourceStatusV1
	channel    deploy.ControlledSessionIncidentResourceStatusV1
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
		backend.currentBootSession == nil || backend.bindDockerEndpoint == nil || backend.inspectContainer == nil ||
		backend.removeContainer == nil || backend.removeChannel == nil ||
		backend.now == nil || backend.writeIncident == nil {
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
	if err := backend.bindDockerEndpoint(manifest.DockerEndpoint); err != nil {
		return fmt.Errorf("bind watchdog Docker endpoint: %w", err)
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
	count, signalErr := io.ReadFull(liveness, signal[:])
	parentLost := signalErr != nil || count != 1 || signal[0] != controlledSessionWatchdogDisarmByte
	if !parentLost {
		if err := requireControlledSessionWatchdogBootV1(manifest, backend); err != nil {
			return err
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), controlledSessionWatchdogCleanupTimeout)
		defer cancel()
		_, cleanupErr := cleanupControlledSessionFromWatchdogWithReportV1(cleanupCtx, manifest, backend)
		return cleanupErr
	}
	return cleanupControlledSessionAfterParentLossV1(manifest, backend)
}

func cleanupControlledSessionAfterParentLossV1(
	manifest deploy.ControlledSessionCleanupManifest,
	backend controlledSessionWatchdogCleanupBackendV1,
) error {
	for attempt := 0; ; attempt++ {
		if err := requireControlledSessionWatchdogBootV1(manifest, backend); err != nil {
			return err
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), controlledSessionWatchdogCleanupTimeout)
		report, cleanupErr := cleanupControlledSessionFromWatchdogWithReportV1(cleanupCtx, manifest, backend)
		cancel()
		if cleanupErr == nil {
			receipt := controlledSessionWatchdogIncidentReceiptV1(manifest, report, backend.now())
			return backend.writeIncident(receipt)
		}
		if backend.dockerUnavailable == nil || backend.waitRetry == nil {
			receipt := controlledSessionWatchdogIncidentReceiptV1(manifest, report, backend.now())
			return errors.Join(cleanupErr, backend.writeIncident(receipt), fmt.Errorf("watchdog Docker retry backend is incomplete"))
		}
		probeCtx, probeCancel := context.WithTimeout(context.Background(), defaultDockerPreflightTimeout)
		unavailable, probeErr := backend.dockerUnavailable(probeCtx)
		probeCancel()
		if probeErr != nil || !unavailable {
			receipt := controlledSessionWatchdogIncidentReceiptV1(manifest, report, backend.now())
			return errors.Join(cleanupErr, probeErr, backend.writeIncident(receipt))
		}
		backend.waitRetry(controlledSessionWatchdogRetryDelayV1(attempt))
	}
}

func controlledSessionWatchdogRetryDelayV1(attempt int) time.Duration {
	delay := controlledSessionWatchdogRetryInitial
	for step := 0; step < attempt && delay < controlledSessionWatchdogRetryMaximum; step++ {
		delay *= 2
		if delay > controlledSessionWatchdogRetryMaximum {
			delay = controlledSessionWatchdogRetryMaximum
		}
	}
	return delay
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
	_, err := cleanupControlledSessionFromWatchdogWithReportV1(ctx, manifest, backend)
	return err
}

func cleanupControlledSessionFromWatchdogWithReportV1(
	ctx context.Context,
	manifest deploy.ControlledSessionCleanupManifest,
	backend controlledSessionWatchdogCleanupBackendV1,
) (controlledSessionWatchdogCleanupReportV1, error) {
	report := controlledSessionWatchdogCleanupReportV1{
		controller: deploy.ControlledSessionIncidentResourceCleanupFailedV1,
		workload:   deploy.ControlledSessionIncidentResourceCleanupFailedV1,
		channel:    deploy.ControlledSessionIncidentResourceCleanupFailedV1,
	}
	if backend.inspectContainer == nil || backend.removeContainer == nil || backend.removeChannel == nil {
		return report, fmt.Errorf("watchdog cleanup backend is incomplete")
	}
	if err := deploy.ValidateControlledSessionCleanupManifest(manifest); err != nil {
		return report, err
	}
	var cleanupErr error
	if err := cleanupControlledSessionWatchdogContainerV1(ctx, manifest.LiveRunID, manifest.Workload, backend); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	} else {
		report.workload = deploy.ControlledSessionIncidentResourceVerifiedAbsentV1
	}
	if err := cleanupControlledSessionWatchdogContainerV1(ctx, manifest.LiveRunID, manifest.Controller, backend); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	} else {
		report.controller = deploy.ControlledSessionIncidentResourceVerifiedAbsentV1
	}
	if err := backend.removeChannel(manifest.ChannelDirectory); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	} else {
		report.channel = deploy.ControlledSessionIncidentResourceVerifiedAbsentV1
	}
	return report, cleanupErr
}

func controlledSessionWatchdogIncidentReceiptV1(
	manifest deploy.ControlledSessionCleanupManifest,
	report controlledSessionWatchdogCleanupReportV1,
	recordedAt time.Time,
) deploy.ControlledSessionIncidentReceiptV1 {
	container := func(
		ownership deploy.ControlledSessionContainerOwnershipV1,
		status deploy.ControlledSessionIncidentResourceStatusV1,
	) deploy.ControlledSessionIncidentContainerV1 {
		return deploy.ControlledSessionIncidentContainerV1{
			Role: ownership.Role, ID: ownership.ID, DeploymentID: ownership.DeploymentID,
			GenerationReference: ownership.GenerationReference, BuildIdentity: ownership.BuildIdentity,
			CleanupStatus: status,
		}
	}
	cleanupStatus := deploy.ControlledSessionIncidentCleanupSucceededV1
	recoveryAction := deploy.ControlledSessionIncidentRecoveryNoneV1
	if report.controller != deploy.ControlledSessionIncidentResourceVerifiedAbsentV1 ||
		report.workload != deploy.ControlledSessionIncidentResourceVerifiedAbsentV1 ||
		report.channel != deploy.ControlledSessionIncidentResourceVerifiedAbsentV1 {
		cleanupStatus = deploy.ControlledSessionIncidentCleanupFailedV1
		recoveryAction = deploy.ControlledSessionIncidentRecoveryNextOperationV1
	}
	return deploy.ControlledSessionIncidentReceiptV1{
		Schema: deploy.ControlledSessionIncidentReceiptSchemaV1, LiveRunID: manifest.LiveRunID,
		BootSession: manifest.BootSession, RecordedAt: recordedAt.UTC().Format(time.RFC3339Nano),
		Trigger:              deploy.ControlledSessionIncidentParentLostV1,
		Controller:           container(manifest.Controller, report.controller),
		Workload:             container(manifest.Workload, report.workload),
		ChannelCleanupStatus: report.channel, CleanupStatus: cleanupStatus, RecoveryAction: recoveryAction,
	}
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

func productionControlledSessionWatchdogCleanupBackendV1(receipt *os.File) controlledSessionWatchdogCleanupBackendV1 {
	var dockerRun commandRunner
	var dockerEndpoint string
	return controlledSessionWatchdogCleanupBackendV1{
		currentBootSession: deploy.CurrentBootSessionIDV1,
		bindDockerEndpoint: func(endpoint string) error {
			var err error
			dockerRun, err = commandRunnerForPinnedDockerEndpointV1(endpoint, runCommandWithoutDockerPreflight)
			if err == nil {
				dockerEndpoint = endpoint
			}
			return err
		},
		inspectContainer: func(ctx context.Context, containerID string) (map[string]string, bool, error) {
			return inspectControlledSessionWatchdogContainerV1(ctx, containerID, dockerRun)
		},
		removeContainer: func(ctx context.Context, containerID string) error {
			return dockerRun(CommandSpec{Name: "docker", Args: []string{"container", "rm", "--force", containerID}}, RunOptions{Context: ctx})
		},
		removeChannel: removeControlledSessionChannelDirectoryV1,
		dockerUnavailable: func(ctx context.Context) (bool, error) {
			return controlledSessionWatchdogDockerUnavailableV1(ctx, dockerEndpoint, probeControlledSessionWatchdogDockerV1)
		},
		waitRetry: time.Sleep,
		now:       time.Now,
		writeIncident: func(incident deploy.ControlledSessionIncidentReceiptV1) error {
			return deploy.WriteControlledSessionIncidentReceiptV1(receipt, incident)
		},
	}
}

type controlledSessionWatchdogDockerProbeV1 func(context.Context, string) error

func controlledSessionWatchdogDockerUnavailableV1(
	ctx context.Context,
	endpoint string,
	probe controlledSessionWatchdogDockerProbeV1,
) (bool, error) {
	if probe == nil {
		return false, fmt.Errorf("probe watchdog Docker daemon requires a probe")
	}
	err := probe(ctx, endpoint)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true, nil
	}
	return false, fmt.Errorf("probe watchdog Docker daemon: %w", err)
}

func probeControlledSessionWatchdogDockerV1(ctx context.Context, endpoint string) error {
	const prefix = "unix://"
	if !strings.HasPrefix(strings.ToLower(endpoint), prefix) {
		return fmt.Errorf("watchdog Docker endpoint %q is not a Unix socket", endpoint)
	}
	socketPath := endpoint[len(prefix):]
	if socketPath == "" {
		return fmt.Errorf("watchdog Docker endpoint has an empty Unix socket path")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/_ping", nil)
	if err != nil {
		return fmt.Errorf("prepare watchdog Docker ping: %w", err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("watchdog Docker ping returned HTTP status %s", response.Status)
	}
	return nil
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

func inspectControlledSessionWatchdogContainerV1(ctx context.Context, containerID string, run commandRunner) (map[string]string, bool, error) {
	var output bytes.Buffer
	err := run(CommandSpec{Name: "docker", Args: []string{
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
