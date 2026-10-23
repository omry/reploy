package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func installedServiceManagerFixture(t *testing.T, output, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("systemd command fixture requires a Unix shell")
	}
	dir := t.TempDir()
	outputPath, argsPath := filepath.Join(dir, "output"), filepath.Join(dir, "args")
	if err := os.WriteFile(outputPath, []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$HF17_TEST_MANAGER_ARGS\"\n" + script
	if script == "" {
		content += "while IFS= read -r line; do printf '%s\\n' \"$line\"; done <\"$HF17_TEST_MANAGER_OUTPUT\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HF17_TEST_MANAGER_ARGS", argsPath)
	t.Setenv("HF17_TEST_MANAGER_OUTPUT", outputPath)
	return argsPath
}

func installedServiceExitRunner(t *testing.T, output string, failure error) commandRunner {
	t.Helper()
	return func(command CommandSpec, options RunOptions) error {
		if command.Name != "/selected/docker" || !reflect.DeepEqual(command.Args, []string{"wait", "exact-container-id"}) || options.Stdin != nil || options.Context == nil {
			t.Fatalf("changed exact wait boundary: command=%#v options=%#v", command, options)
		}
		if _, err := io.WriteString(options.Stdout, output); err != nil {
			t.Fatal(err)
		}
		return failure
	}
}

func TestInstalledServiceNonzeroExitRequiresExactManagerStop(t *testing.T) {
	for _, test := range []struct {
		name, state string
		pid         int
		wantSuccess bool
	}{
		{"intentional stop", "deactivating", os.Getpid(), true},
		{"unexpected exit", "active", os.Getpid(), false},
		{"another controller", "deactivating", os.Getpid() + 1, false},
		{"inactive unit", "inactive", 0, false},
		{"failed unit", "failed", os.Getpid(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			argsPath := installedServiceManagerFixture(t, fmt.Sprintf("MainPID=%d\nActiveState=%s\n", test.pid, test.state), "")
			var diagnostic bytes.Buffer
			err := waitInstalledServiceWorkloadV1(t.Context(), "/selected/docker", "exact-container-id", "installed-test", RunOptions{Stderr: &diagnostic}, installedServiceExitRunner(t, "137\n", nil))
			if (err == nil) != test.wantSuccess {
				t.Fatalf("success=%v error=%v diagnostic=%q", test.wantSuccess, err, diagnostic.String())
			}
			if test.wantSuccess {
				if !strings.Contains(diagnostic.String(), "stopped by systemd; container exit status 137") {
					t.Fatalf("lost exit diagnostic: %q", diagnostic.String())
				}
			} else if !strings.Contains(err.Error(), "container exited with status 137") || diagnostic.Len() != 0 {
				t.Fatalf("unexpected failure changed: error=%v diagnostic=%q", err, diagnostic.String())
			}
			args, err := os.ReadFile(argsPath)
			if err != nil || string(args) != "--system\nshow\n--property=ActiveState,MainPID\n--\ninstalled-test.service\n" {
				t.Fatalf("manager query=%q error=%v", args, err)
			}
		})
	}
}

func TestInstalledServiceStopIntentIsIndependentOfContainerExitCode(t *testing.T) {
	for _, code := range []int{1, 137, 143, 255} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			installedServiceManagerFixture(t, fmt.Sprintf("ActiveState=deactivating\nMainPID=%d\n", os.Getpid()), "")
			if err := waitInstalledServiceWorkloadV1(t.Context(), "/selected/docker", "exact-container-id", "installed-test", RunOptions{}, installedServiceExitRunner(t, fmt.Sprintln(code), nil)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstalledServiceWaitDoesNotQueryManagerForSuccessOrInvalidExit(t *testing.T) {
	for _, status := range []string{"0\n", "", "bad", "137\n0", "-1", "256"} {
		t.Run(fmt.Sprintf("status=%q", status), func(t *testing.T) {
			argsPath := installedServiceManagerFixture(t, fmt.Sprintf("ActiveState=deactivating\nMainPID=%d\n", os.Getpid()), "")
			err := waitInstalledServiceWorkloadV1(t.Context(), "/selected/docker", "exact-container-id", "installed-test", RunOptions{}, installedServiceExitRunner(t, status, nil))
			if (err == nil) != (status == "0\n") {
				t.Fatalf("status=%q error=%v", status, err)
			}
			if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
				t.Fatalf("queried manager without a valid nonzero exit: %v", err)
			}
		})
	}
}

func TestInstalledServiceWaitPreservesEngineFailureWithoutManagerQuery(t *testing.T) {
	argsPath := installedServiceManagerFixture(t, "", "")
	cause := errors.New("engine transport failed")
	err := waitInstalledServiceWorkloadV1(t.Context(), "/selected/docker", "exact-container-id", "installed-test", RunOptions{}, installedServiceExitRunner(t, "137\n", cause))
	if !errors.Is(err, cause) {
		t.Fatalf("lost engine failure: %v", err)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatalf("queried manager after transport failure: %v", err)
	}
}

func TestInstalledServiceMissingManagerPreservesContainerFailure(t *testing.T) {
	argsPath := installedServiceManagerFixture(t, "", "")
	if err := os.Remove(filepath.Join(filepath.Dir(argsPath), "systemctl")); err != nil {
		t.Fatal(err)
	}
	err := waitInstalledServiceWorkloadV1(t.Context(), "/selected/docker", "exact-container-id", "installed-test", RunOptions{}, installedServiceExitRunner(t, "137\n", nil))
	if !errors.Is(err, exec.ErrNotFound) || !strings.Contains(err.Error(), "container exited with status 137") {
		t.Fatalf("missing manager hid container failure: %v", err)
	}
}

func TestInstalledServiceManagerEvidenceFailurePreservesContainerFailure(t *testing.T) {
	for _, output := range []string{"", "ActiveState=deactivating\n", "MainPID=bad\nActiveState=deactivating\n", "MainPID=1\nActiveState=deactivating\nMainPID=1\n", "MainPID=1\nActiveState=deactivating\nUnknown=value\n"} {
		t.Run(fmt.Sprintf("output=%q", output), func(t *testing.T) {
			installedServiceManagerFixture(t, output, "")
			err := waitInstalledServiceWorkloadV1(t.Context(), "/selected/docker", "exact-container-id", "installed-test", RunOptions{}, installedServiceExitRunner(t, "137\n", nil))
			if err == nil || !strings.Contains(err.Error(), "container exited with status 137") || !strings.Contains(err.Error(), "systemd returned") {
				t.Fatalf("invalid evidence hid failure: %v", err)
			}
		})
	}
}

func TestInstalledServiceManagerQueryFailureAndDeadlinePreserveFailure(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint(timeout), func(t *testing.T) {
			script := "printf 'manager unavailable\\n' >&2\nexit 7\n"
			if timeout {
				script = "exec /bin/sleep 10\n"
			}
			installedServiceManagerFixture(t, "", script)
			ctx := t.Context()
			if timeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
			}
			var diagnostic bytes.Buffer
			started := time.Now()
			err := waitInstalledServiceWorkloadV1(ctx, "/selected/docker", "exact-container-id", "installed-test", RunOptions{Stderr: &diagnostic}, installedServiceExitRunner(t, "137\n", nil))
			if err == nil || !strings.Contains(err.Error(), "container exited with status 137") || !strings.Contains(err.Error(), "confirm installed service stop intent") {
				t.Fatalf("query failure hid container failure: %v", err)
			}
			if timeout && (!errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second) {
				t.Fatalf("query ignored parent deadline: elapsed=%v error=%v", time.Since(started), err)
			}
			if !timeout && !strings.Contains(diagnostic.String(), "manager unavailable") {
				t.Fatalf("lost manager diagnostic: %q", diagnostic.String())
			}
		})
	}
}
