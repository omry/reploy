package dockerdeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRuntimeCommandActions(t *testing.T) {
	cases := []struct {
		action string
		suffix []string
	}{
		{action: "up", suffix: []string{"up", "-d"}},
		{action: "restart", suffix: []string{"up", "-d", "--force-recreate"}},
		{action: "down", suffix: []string{"down", "--remove-orphans"}},
		{action: "ps", suffix: []string{"ps"}},
		{action: "status", suffix: []string{"ps"}},
		{action: "logs", suffix: []string{"logs", "--timestamps"}},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			spec, err := RuntimeCommand("deployment", tc.action)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Name != "docker" {
				t.Fatalf("name = %q", spec.Name)
			}
			if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(tc.suffix):], tc.suffix) {
				t.Fatalf("suffix = %#v, want %#v", spec.Args[len(spec.Args)-len(tc.suffix):], tc.suffix)
			}
		})
	}
}

func TestRuntimeCommandCanFollowLogs(t *testing.T) {
	spec, err := RuntimeCommandWithOptions("deployment", "logs", RuntimeCommandOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	suffix := []string{"logs", "--timestamps", "-f"}
	if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(suffix):], suffix) {
		t.Fatalf("suffix = %#v, want %#v", spec.Args[len(spec.Args)-len(suffix):], suffix)
	}
}

func TestRuntimeCommandRejectsUnknownAction(t *testing.T) {
	_, err := RuntimeCommand("deployment", "explode")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRuntimeInputsDoesNotRequireAppEnvFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DockerEnvFileName), []byte("REPLOY_CONFIG_DIR=./conf\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateRuntimeInputs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
