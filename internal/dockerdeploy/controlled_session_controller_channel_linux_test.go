//go:build linux

package dockerdeploy

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequirePreparedControlledSessionControllerChannelV1(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	source := shortControlledSessionControllerChannelSourceV1(t, &plan)
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(source, controlledSessionChannelSocketNameV1))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := requirePreparedControlledSessionControllerChannelV1(plan); err != nil {
		t.Fatal(err)
	}
}

func TestRequirePreparedControlledSessionControllerChannelV1RejectsMissingOrNonSocketPath(t *testing.T) {
	for _, test := range []struct {
		name        string
		preparePath func(*testing.T, string)
		want        string
	}{
		{name: "missing socket", preparePath: func(t *testing.T, source string) {
			t.Helper()
			if err := os.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "inspect private channel socket"},
		{name: "regular file", preparePath: func(t *testing.T, source string) {
			t.Helper()
			if err := os.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, controlledSessionChannelSocketNameV1), []byte("not a socket"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "is not a Unix socket"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := controlledSessionControllerPlanFixtureV1(t)
			source := shortControlledSessionControllerChannelSourceV1(t, &plan)
			test.preparePath(t, source)
			err := requirePreparedControlledSessionControllerChannelV1(plan)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readiness error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRequirePreparedControlledSessionControllerChannelV1RejectsWrongMountShape(t *testing.T) {
	plan := controlledSessionControllerPlanFixtureV1(t)
	for index := range plan.Mounts {
		if plan.Mounts[index].Name == "session-channel" {
			plan.Mounts[index].ReadOnly = false
			break
		}
	}
	err := requirePreparedControlledSessionControllerChannelV1(plan)
	if err == nil || !strings.Contains(err.Error(), "private read-only bind contract") {
		t.Fatalf("mount-shape error = %v", err)
	}
}

func shortControlledSessionControllerChannelSourceV1(t *testing.T, plan *ControlledSessionContainerPlanV1) string {
	t.Helper()
	source, err := os.MkdirTemp("", "reploy-csc-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(source) })
	for index := range plan.Mounts {
		if plan.Mounts[index].Name == "session-channel" {
			plan.Mounts[index].Source = source
			return source
		}
	}
	t.Fatal("controller plan has no session-channel mount")
	return ""
}
