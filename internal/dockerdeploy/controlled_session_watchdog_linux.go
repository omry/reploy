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
)

var controlledSessionWatchdogExecutableV1 = os.Executable

type controlledSessionWatchdogProcessV1 struct {
	pid       int
	liveness  *os.File
	done      chan error
	once      sync.Once
	disarmErr error
}

func startControlledSessionWatchdogV1(ctx context.Context, manifest deploy.ControlledSessionCleanupManifest) (controlledSessionWatchdogRuntimeV1, error) {
	if ctx == nil {
		ctx = context.Background()
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
	command.ExtraFiles = []*os.File{manifestFile, livenessRead, readyWrite}
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = livenessWrite.Close()
		return nil, fmt.Errorf("launch controlled-session watchdog child: %w", err)
	}
	_ = readyWrite.Close()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()

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
			<-done
			return nil, fmt.Errorf("wait for controlled-session watchdog readiness: %w", err)
		}
	case err := <-done:
		_ = livenessWrite.Close()
		if err == nil {
			return nil, fmt.Errorf("controlled-session watchdog exited before readiness")
		}
		return nil, fmt.Errorf("controlled-session watchdog exited before readiness: %w", err)
	case <-ctx.Done():
		_ = livenessWrite.Close()
		_ = command.Process.Kill()
		<-done
		return nil, fmt.Errorf("wait for controlled-session watchdog readiness: %w", ctx.Err())
	}
	return &controlledSessionWatchdogProcessV1{pid: command.Process.Pid, liveness: livenessWrite, done: done}, nil
}

func (watchdog *controlledSessionWatchdogProcessV1) Disarm(ctx context.Context) error {
	watchdog.once.Do(func() {
		if count, err := watchdog.liveness.Write([]byte{controlledSessionWatchdogDisarmByte}); err != nil || count != 1 {
			if err == nil {
				err = io.ErrShortWrite
			}
			watchdog.disarmErr = fmt.Errorf("send controlled-session watchdog disarm signal: %w", err)
		}
		if err := watchdog.liveness.Close(); err != nil {
			watchdog.disarmErr = errors.Join(watchdog.disarmErr, fmt.Errorf("close controlled-session watchdog liveness pipe: %w", err))
		}
		select {
		case err := <-watchdog.done:
			if err != nil {
				watchdog.disarmErr = errors.Join(watchdog.disarmErr, fmt.Errorf("wait for controlled-session watchdog child: %w", err))
			}
		case <-ctx.Done():
			watchdog.disarmErr = errors.Join(watchdog.disarmErr, fmt.Errorf("wait for controlled-session watchdog child: %w", ctx.Err()))
		}
	})
	return watchdog.disarmErr
}

func runControlledSessionWatchdogChildV1(stderr io.Writer) error {
	manifestFile := os.NewFile(controlledSessionWatchdogManifestFD, "controlled-session-watchdog-manifest")
	liveness := os.NewFile(controlledSessionWatchdogLivenessFD, "controlled-session-watchdog-parent")
	ready := os.NewFile(controlledSessionWatchdogReadyFD, "controlled-session-watchdog-ready")
	if manifestFile == nil || liveness == nil || ready == nil {
		return fmt.Errorf("required inherited watchdog descriptors are unavailable")
	}
	defer manifestFile.Close()
	defer liveness.Close()
	defer ready.Close()
	return runControlledSessionWatchdogV1(manifestFile, liveness, ready, productionControlledSessionWatchdogCleanupBackendV1())
}
