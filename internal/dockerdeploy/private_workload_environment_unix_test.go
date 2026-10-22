//go:build !windows

package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
	"golang.org/x/sys/unix"
)

func TestHF13Stamp01ObservationRejectsFIFOWithoutBlocking(t *testing.T) {
	for _, action := range []string{"status", "ps", "logs"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			environmentPath := filepath.Join(dir, PrivateWorkloadEnvironmentFileName)
			if err := unix.Mkfifo(environmentPath, 0o600); err != nil {
				t.Fatal(err)
			}

			current, _ := runtimeCurrentBuildFixture(t)
			backend := currentRuntimeObservationTestBackend(
				t,
				dir,
				current,
				CurrentRuntimePlanV1{Docker: DockerExecutionPlan{Workload: &WorkloadExecutionPlan{}}},
				&[]string{},
			)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- runCurrentRuntimeObservationV1(ctx, CurrentRuntimeObservationInputV1{
					DeploymentDir: dir,
					Action:        action,
				}, backend)
			}()

			var (
				err      error
				oldBlock bool
			)
			select {
			case err = <-done:
			case <-time.After(500 * time.Millisecond):
				oldBlock = true
				cancel()
				rescue, rescueErr := openFIFORescueWriter(environmentPath)
				if rescueErr != nil {
					t.Fatalf("rescue FIFO writer: %v", rescueErr)
				}
				select {
				case err = <-done:
				case <-time.After(500 * time.Millisecond):
					_ = rescue.Close()
					t.Fatal("rescued FIFO observation did not finish")
				}
				if closeErr := rescue.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}

			if err == nil || !strings.Contains(err.Error(), "must be a real regular file") {
				t.Fatalf("FIFO %s error = %v", action, err)
			}
			lockContext, lockCancel := context.WithTimeout(context.Background(), time.Second)
			lock, lockErr := deploy.AcquireOperationLock(lockContext, dir)
			lockCancel()
			if lockErr != nil {
				t.Fatalf("FIFO %s retained operation lock: %v", action, lockErr)
			}
			if lockErr := lock.Unlock(); lockErr != nil {
				t.Fatal(lockErr)
			}
			if oldBlock {
				t.Fatalf("FIFO %s observation blocked before regular-file rejection", action)
			}
		})
	}
}

func openFIFORescueWriter(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return file, nil
}
