//go:build linux

package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/omry/reploy/internal/deploy"
)

const (
	controlledSessionWatchdogManifestFD = 3
	controlledSessionWatchdogLivenessFD = 4
	controlledSessionWatchdogReadyFD    = 5
	controlledSessionWatchdogReceiptFD  = 6
)

var controlledSessionWatchdogExecutableV1 = os.Executable

type controlledSessionWatchdogProcessV1 struct {
	pid         int
	liveness    *os.File
	exited      chan struct{}
	exitMu      sync.Mutex
	exitErr     error
	closeOnce   sync.Once
	closeErr    error
	disarmOnce  sync.Once
	disarmErr   error
	receipt     *deploy.ControlledSessionIncidentReceiptTargetV1
	receiptOnce sync.Once
	receiptErr  error
}

func (watchdog *controlledSessionWatchdogProcessV1) Done() <-chan struct{} {
	return watchdog.exited
}

func (watchdog *controlledSessionWatchdogProcessV1) ExitError() error {
	select {
	case <-watchdog.exited:
		watchdog.exitMu.Lock()
		defer watchdog.exitMu.Unlock()
		return watchdog.exitErr
	default:
		return nil
	}
}

func (watchdog *controlledSessionWatchdogProcessV1) Close() error {
	watchdog.closeOnce.Do(func() {
		if err := watchdog.liveness.Close(); err != nil {
			watchdog.closeErr = fmt.Errorf("close controlled-session watchdog liveness pipe: %w", err)
		}
	})
	select {
	case <-watchdog.exited:
		watchdog.removeReceiptTarget()
	default:
	}
	return errors.Join(watchdog.closeErr, watchdog.receiptErr)
}

func startControlledSessionWatchdogV1(
	ctx context.Context,
	manifest deploy.ControlledSessionCleanupManifest,
	receipt *deploy.ControlledSessionIncidentReceiptTargetV1,
) (controlledSessionWatchdogRuntimeV1, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if receipt == nil || receipt.File() == nil || receipt.Path() != manifest.IncidentReceipt {
		return nil, fmt.Errorf("launch controlled-session watchdog requires the exact pre-created incident receipt target")
	}
	content, err := deploy.EncodeControlledSessionCleanupManifest(manifest)
	if err != nil {
		return nil, err
	}
	manifestFile, err := os.CreateTemp("", "reploy-controlled-session-watchdog-")
	if err != nil {
		return nil, fmt.Errorf("create private watchdog manifest file: %w", err)
	}
	manifestPath := manifestFile.Name()
	defer func() {
		_ = manifestFile.Close()
		if manifestPath != "" {
			_ = os.Remove(manifestPath)
		}
	}()
	if err := manifestFile.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("restrict private watchdog manifest file: %w", err)
	}
	if _, err := manifestFile.Write(content); err != nil {
		return nil, fmt.Errorf("write private watchdog manifest file: %w", err)
	}
	if _, err := manifestFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind private watchdog manifest file: %w", err)
	}
	if err := os.Remove(manifestPath); err != nil {
		return nil, fmt.Errorf("unlink private watchdog manifest file: %w", err)
	}
	manifestPath = ""

	livenessRead, livenessWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create watchdog parent-liveness pipe: %w", err)
	}
	defer livenessRead.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		_ = livenessWrite.Close()
		return nil, fmt.Errorf("create watchdog readiness pipe: %w", err)
	}
	defer readyRead.Close()
	defer readyWrite.Close()

	executable, err := controlledSessionWatchdogExecutableV1()
	if err != nil {
		_ = livenessWrite.Close()
		return nil, fmt.Errorf("resolve Reploy executable for controlled-session watchdog: %w", err)
	}
	command := exec.Command(executable, controlledSessionWatchdogChildArgument)
	command.ExtraFiles = []*os.File{manifestFile, livenessRead, readyWrite, receipt.File()}
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = livenessWrite.Close()
		return nil, fmt.Errorf("launch controlled-session watchdog child: %w", err)
	}
	_ = readyWrite.Close()
	watchdog := &controlledSessionWatchdogProcessV1{
		pid: command.Process.Pid, liveness: livenessWrite, exited: make(chan struct{}), receipt: receipt,
	}
	go func() {
		err := command.Wait()
		watchdog.exitMu.Lock()
		watchdog.exitErr = err
		watchdog.exitMu.Unlock()
		close(watchdog.exited)
	}()

	readyResult := make(chan error, 1)
	go func() {
		var ready [1]byte
		count, readErr := io.ReadFull(readyRead, ready[:])
		if readErr != nil {
			readyResult <- readErr
			return
		}
		if count != 1 || ready[0] != controlledSessionWatchdogReadyByte {
			readyResult <- fmt.Errorf("watchdog returned an invalid readiness acknowledgement")
			return
		}
		readyResult <- nil
	}()
	select {
	case err := <-readyResult:
		if err != nil {
			_ = livenessWrite.Close()
			_ = command.Process.Kill()
			<-watchdog.exited
			return nil, fmt.Errorf("wait for controlled-session watchdog readiness: %w", err)
		}
	case <-watchdog.exited:
		_ = livenessWrite.Close()
		err := watchdog.ExitError()
		if err == nil {
			return nil, fmt.Errorf("controlled-session watchdog exited before readiness")
		}
		return nil, fmt.Errorf("controlled-session watchdog exited before readiness: %w", err)
	case <-ctx.Done():
		_ = livenessWrite.Close()
		_ = command.Process.Kill()
		<-watchdog.exited
		return nil, fmt.Errorf("wait for controlled-session watchdog readiness: %w", ctx.Err())
	}
	return watchdog, nil
}

func (watchdog *controlledSessionWatchdogProcessV1) Disarm(ctx context.Context) error {
	watchdog.disarmOnce.Do(func() {
		if count, err := watchdog.liveness.Write([]byte{controlledSessionWatchdogDisarmByte}); err != nil || count != 1 {
			if err == nil {
				err = io.ErrShortWrite
			}
			watchdog.disarmErr = fmt.Errorf("send controlled-session watchdog disarm signal: %w", err)
		}
		watchdog.disarmErr = errors.Join(watchdog.disarmErr, watchdog.Close())
		select {
		case <-watchdog.exited:
			err := watchdog.ExitError()
			if err != nil {
				watchdog.disarmErr = errors.Join(watchdog.disarmErr, fmt.Errorf("wait for controlled-session watchdog child: %w", err))
			}
			watchdog.removeReceiptTarget()
		case <-ctx.Done():
			watchdog.disarmErr = errors.Join(watchdog.disarmErr, fmt.Errorf("wait for controlled-session watchdog child: %w", ctx.Err()))
		}
	})
	return errors.Join(watchdog.disarmErr, watchdog.receiptErr)
}

func (watchdog *controlledSessionWatchdogProcessV1) removeReceiptTarget() {
	watchdog.receiptOnce.Do(func() {
		if watchdog.receipt != nil {
			watchdog.receiptErr = watchdog.receipt.Remove()
		}
	})
}

func runControlledSessionWatchdogChildV1(stderr io.Writer) error {
	manifestFile := os.NewFile(controlledSessionWatchdogManifestFD, "controlled-session-watchdog-manifest")
	liveness := os.NewFile(controlledSessionWatchdogLivenessFD, "controlled-session-watchdog-parent")
	ready := os.NewFile(controlledSessionWatchdogReadyFD, "controlled-session-watchdog-ready")
	receipt := os.NewFile(controlledSessionWatchdogReceiptFD, "controlled-session-watchdog-incident-receipt")
	if manifestFile == nil || liveness == nil || ready == nil || receipt == nil {
		return fmt.Errorf("required inherited watchdog descriptors are unavailable")
	}
	defer manifestFile.Close()
	defer liveness.Close()
	defer ready.Close()
	defer receipt.Close()
	return runControlledSessionWatchdogV1(manifestFile, liveness, ready, productionControlledSessionWatchdogCleanupBackendV1(receipt))
}
