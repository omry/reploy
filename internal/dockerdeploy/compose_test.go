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
