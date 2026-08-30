//go:build linux

package probe

import (
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
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
