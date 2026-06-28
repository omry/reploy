package dockerdeploy

import (
	"strings"
	"testing"
)

func TestRunCommandForwardsStdin(t *testing.T) {
	var stdout strings.Builder
	err := runCommand(
		CommandSpec{Name: "sh", Args: []string{"-c", "cat"}},
		RunOptions{Stdin: strings.NewReader("hello\n"), Stdout: &stdout},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "hello\n" {
		t.Fatalf("stdout = %q, want stdin echoed", stdout.String())
	}
}

func TestRunCommandCapturesSuppressedOutputOnFailure(t *testing.T) {
	err := runCommand(
		CommandSpec{Name: "sh", Args: []string{"-c", "echo useful failure; exit 1"}},
		RunOptions{},
	)
	if err == nil {
		t.Fatal("err = nil, want failure")
	}
	for _, want := range []string{
		"sh failed: exit status 1",
		"command output:",
		"useful failure",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%s", want, err)
		}
	}
}
