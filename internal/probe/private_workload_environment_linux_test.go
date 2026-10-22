//go:build linux

package probe

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestApplicationLaunchProofRejectsTrustedVerifierV1(t *testing.T) {
	if err := applicationLaunchProofV1([]byte(trustedStartupVerifierExecutableV1 + "\x00sandbox-exec\x00")); err == nil {
		t.Fatal("trusted verifier process was accepted as the launched application")
	}
	if err := applicationLaunchProofV1([]byte("/usr/local/bin/application\x00")); err != nil {
		t.Fatal("application process was rejected", err)
	}
}

func TestApplicationLaunchProofRejectsMissingCommandV1(t *testing.T) {
	for _, command := range [][]byte{nil, {}, []byte("unterminated"), []byte("\x00")} {
		if err := applicationLaunchProofV1(command); err == nil {
			t.Fatal("missing process command accepted")
		}
	}
}

func TestPrivateEnvironmentLaunchProcessV1(t *testing.T) {
	if os.Getenv("REPLOY_TEST_PRIVATE_LAUNCH") != "1" {
		return
	}
	home := os.Getenv("REPLOY_TEST_PRIVATE_HOME")
	argv := []string{"/bin/sh", "-c", `test "$TOKEN" = "synthetic=value" && test "$(cat)" = "" && rm -f "$HOME"/.* 2>/dev/null; printf 'application started\n'`}
	if os.Getenv("REPLOY_TEST_PRIVATE_FAILURE") == "1" {
		argv = []string{"/does-not-exist"}
	}
	err := launchPrivateWorkloadEnvironmentWithV1(home, argv, []string{"HOME=" + home, "TOKEN=old"}, 2*time.Second,
		func(argv, env []string, ready func() error) error {
			if err := ready(); err != nil {
				return err
			}
			return syscall.Exec(argv[0], argv, env)
		})
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func TestPrivateEnvironmentHandshakeRequiresApplicationExecV1(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			home := t.TempDir()
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPrivateEnvironmentLaunchProcessV1$")
			command.Env = append(os.Environ(), "REPLOY_TEST_PRIVATE_LAUNCH=1", "REPLOY_TEST_PRIVATE_HOME="+home)
			if failure {
				command.Env = append(command.Env, "REPLOY_TEST_PRIVATE_FAILURE=1")
			}
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			err := relayPrivateWorkloadEnvironmentWithV1(home, strings.NewReader("TOKEN=synthetic=value\n\n"), 2*time.Second)
			processErr := command.Wait()
			if (err != nil) != failure || (processErr != nil) != failure {
				t.Fatalf("relay=%v process=%v diagnostics=%s", err, processErr, output.String())
			}
			if !failure && !strings.Contains(output.String(), "application started") {
				t.Fatal("application was not executed")
			}
			if failure && !strings.Contains(output.String(), "no such file") {
				t.Fatal("application execution cause missing from logs")
			}
			if strings.Contains(output.String(), "synthetic=value") {
				t.Fatal("payload leaked in diagnostics")
			}
			for _, name := range []string{".reploy-private-environment", ".reploy-private-launch"} {
				if _, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
					t.Fatal("startup channel retained", err)
				}
			}
		})
	}
}

func TestPrivateEnvironmentLauncherDeadlineAndInvalidPayloadV1(t *testing.T) {
	home := t.TempDir()
	started := time.Now()
	err := launchPrivateWorkloadEnvironmentWithV1(home, []string{"/bin/true"}, nil, 40*time.Millisecond,
		func([]string, []string, func() error) error { t.Fatal("exec before private delivery"); return nil })
	if err == nil || time.Since(started) > time.Second {
		t.Fatal("unbounded startup wait", err)
	}
	for _, payload := range []string{"", "TOKEN=value\n", "=value\n\n", "1TOKEN=value\n\n", "TOKEN=value\nTOKEN=duplicate\n\n", "TOKEN=hidden\x00\n\n", "TOKEN=hidden\r\n\n"} {
		if _, err := privateEnvironmentAssignmentsV1([]byte(payload)); err == nil || strings.Contains(err.Error(), "hidden") {
			t.Fatal("invalid payload accepted or disclosed")
		}
	}
}

func TestPrivateEnvironmentRelayBoundsIncompleteStdinV1(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	started := time.Now()
	err := relayPrivateWorkloadEnvironmentWithV1(t.TempDir(), reader, 40*time.Millisecond)
	if err == nil || time.Since(started) > time.Second {
		t.Fatal("incomplete secret input blocked the relay", err)
	}
}
