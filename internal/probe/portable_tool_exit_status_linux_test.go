//go:build linux

package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestParsePortableToolExitStatusV1AcceptsOnlyCanonicalStatusLines(t *testing.T) {
	for _, test := range []struct {
		content string
		want    int
	}{
		{content: "0\n", want: 0},
		{content: "7\n", want: 7},
		{content: "255\n", want: 255},
	} {
		t.Run(test.content, func(t *testing.T) {
			got, err := parsePortableToolExitStatusV1([]byte(test.content))
			if err != nil || got != test.want {
				t.Fatalf("status = %d, error = %v, want %d", got, err, test.want)
			}
		})
	}

	for _, content := range []string{"00\n", "07\n", "256\n", "-1\n", "1", "1\n2", "1\r\n", "1\n\n", " 1\n"} {
		t.Run("reject-"+strings.ReplaceAll(content, "\n", "\\n"), func(t *testing.T) {
			if _, err := parsePortableToolExitStatusV1([]byte(content)); err == nil {
				t.Fatalf("status %q was accepted", content)
			}
		})
	}
}

func TestReservePortableToolExitStatusV1KeepsCanonicalFinalBytesAndSize(t *testing.T) {
	for _, test := range []struct {
		content string
		want    int
	}{
		{content: "0\n", want: 0},
		{content: "7\n", want: 7},
		{content: "255\n", want: 255},
	} {
		t.Run(strings.TrimSpace(test.content), func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "portable-tool-status-")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := reservePortableToolExitStatusV1(int(file.Fd())); err != nil {
				t.Fatal(err)
			}
			var reserved unix.Stat_t
			if err := unix.Fstat(int(file.Fd()), &reserved); err != nil {
				t.Fatal(err)
			}
			if reserved.Size != 0 {
				t.Fatalf("reserved status file size = %d, want keep-size zero", reserved.Size)
			}
			if reserved.Blocks == 0 {
				t.Fatal("status reservation allocated no filesystem blocks")
			}
			written, err := file.Write([]byte(test.content))
			if err != nil {
				t.Fatal(err)
			}
			if written != len(test.content) {
				t.Fatalf("status write = %d bytes, want %d", written, len(test.content))
			}
			if err := file.Sync(); err != nil {
				t.Fatal(err)
			}
			var final unix.Stat_t
			if err := unix.Fstat(int(file.Fd()), &final); err != nil {
				t.Fatal(err)
			}
			if final.Size != int64(len(test.content)) {
				t.Fatalf("final status file size = %d, want %d", final.Size, len(test.content))
			}
			got := make([]byte, len(test.content))
			if read, err := file.ReadAt(got, 0); err != nil || read != len(got) {
				t.Fatalf("final status bytes = %q, read = %d, error = %v", got, read, err)
			}
			if string(got) != test.content {
				t.Fatalf("final status bytes = %q, want %q", got, test.content)
			}
			if parsed, err := parsePortableToolExitStatusV1(got); err != nil || parsed != test.want {
				t.Fatalf("final status parse = %d/%v, want %d", parsed, err, test.want)
			}
		})
	}
}

func TestReservePortableToolExitStatusV1RejectsInvalidDescriptor(t *testing.T) {
	if err := reservePortableToolExitStatusV1(-1); err == nil || !strings.Contains(err.Error(), "reserve fixed portable-tool exit status") {
		t.Fatalf("invalid-descriptor reservation error = %v", err)
	}
}

func TestCreatePortableToolExitStatusFileV1CreatesReservedEmptyFixedFile(t *testing.T) {
	file, err := createPortableToolExitStatusFileV1()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = file.Close()
		_ = unix.Unlink(portableToolExitStatusPathV1)
	})
	var status unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &status); err != nil {
		t.Fatal(err)
	}
	if status.Size != 0 {
		t.Fatalf("created status file size = %d, want zero", status.Size)
	}
	if status.Blocks == 0 {
		t.Fatal("created status file has no reserved filesystem blocks")
	}
}

func TestPortableToolObservedExecArgvV1BuildsFixedDirectArgv(t *testing.T) {
	plan := sandboxExecPlanV1{UID: 65532, GID: 65532, Groups: []uint32{33, 44}}
	application := []string{"/opt/demo/bin/demo", "literal;$(touch /tmp/pwned)", "$(id)", "a|b"}
	got := portableToolObservedExecArgvV1(9, plan, application)
	want := []string{
		"/proc/self/exe", "portable-tool-observed-exec-v1",
		"--status-fd", "9", "--uid", "65532", "--gid", "65532", "--groups", "33,44", "--",
		"/opt/demo/bin/demo", "literal;$(touch /tmp/pwned)", "$(id)", "a|b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("observed-exec argv = %#v, want %#v", got, want)
	}
}

func TestPortableToolProcessStatusV1PreservesSignalAndOrdinaryExitStatus(t *testing.T) {
	for _, test := range []struct {
		name    string
		command string
		want    int
	}{
		{name: "ordinary-exit", command: "exit 7", want: 7},
		{name: "sigabrt", command: "kill -ABRT $$", want: 128 + int(syscall.SIGABRT)},
		{name: "sigsegv", command: "kill -SEGV $$", want: 128 + int(syscall.SIGSEGV)},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := exec.Command("sh", "-c", test.command).Run()
			if err == nil {
				t.Fatal("signal/exit helper unexpectedly succeeded")
			}
			got, statusErr := portableToolProcessStatusV1(err)
			if statusErr != nil {
				t.Fatalf("portable-tool status conversion error = %v, want trusted status %d", statusErr, test.want)
			}
			if got != test.want {
				t.Fatalf("portable-tool status = %d, want %d", got, test.want)
			}
			encoded := []byte(strconv.Itoa(got) + "\n")
			if parsed, parseErr := parsePortableToolExitStatusV1(encoded); parseErr != nil || parsed != got {
				t.Fatalf("trusted status evidence = %q parsed as %d/%v, want %d", encoded, parsed, parseErr, got)
			}
		})
	}
}

func TestPortableToolProcessStatusV1MapsLaunchFailuresToConventionalStatus(t *testing.T) {
	for _, test := range []struct {
		name string
		mode os.FileMode
		body string
		want int
	}{
		{name: "missing-executable", want: 127},
		{name: "non-executable", mode: 0o644, body: "#!/bin/sh\nexit 0\n", want: 126},
		{name: "bad-format", mode: 0o755, body: "not a native executable\n", want: 126},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tool")
			if test.body != "" {
				if err := os.WriteFile(path, []byte(test.body), test.mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, test.mode); err != nil {
					t.Fatal(err)
				}
			}
			err := exec.Command(path).Run()
			if err == nil {
				t.Fatal("launch helper unexpectedly succeeded")
			}
			got, statusErr := portableToolProcessStatusV1(err)
			if statusErr != nil {
				t.Fatalf("portable-tool launch status error = %v, want trusted status %d", statusErr, test.want)
			}
			if got != test.want {
				t.Fatalf("portable-tool launch status = %d, want %d", got, test.want)
			}
			encoded := []byte(strconv.Itoa(got) + "\n")
			if parsed, parseErr := parsePortableToolExitStatusV1(encoded); parseErr != nil || parsed != got {
				t.Fatalf("trusted launch status evidence = %q parsed as %d/%v, want %d", encoded, parsed, parseErr, got)
			}
		})
	}
}
