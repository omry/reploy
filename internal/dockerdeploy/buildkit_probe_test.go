package dockerdeploy

import (
	"context"
	"fmt"
	"testing"
)

func TestProbeBuildKitCapabilitiesLinuxAndDesktop(t *testing.T) {
	tests := []struct {
		name string
		info string
		ctx  string
		kind DockerEngineKind
	}{
		{name: "minimum engine", info: "24.0.0\tlinux\tDebian GNU/Linux 12\n", ctx: "default\n", kind: DockerEngineLinux},
		{name: "linux engine", info: "27.5.1\tlinux\tUbuntu 24.04 LTS\n", ctx: "default\n", kind: DockerEngineLinux},
		{name: "desktop", info: "29.6.1\tlinux\tDocker Desktop\n", ctx: "desktop-linux\n", kind: DockerEngineDesktop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := func(_ context.Context, args ...string) (string, error) {
				if args[0] == "info" {
					return tt.info, nil
				}
				if args[0] == "context" {
					return tt.ctx, nil
				}
				return "", fmt.Errorf("unexpected args: %v", args)
			}
			capabilities, err := probeBuildKitCapabilities(context.Background(), run)
			if err != nil {
				t.Fatal(err)
			}
			if capabilities.Engine != tt.kind || capabilities.ServerOS != "linux" {
				t.Fatalf("capabilities = %#v", capabilities)
			}
		})
	}
}

func TestProbeBuildKitCapabilitiesRejectsUnsupportedDaemon(t *testing.T) {
	tests := []string{"23.0.6\tlinux\tUbuntu", "29.0.0\twindows\tDocker Desktop"}
	for _, info := range tests {
		run := func(_ context.Context, args ...string) (string, error) {
			if args[0] == "info" {
				return info, nil
			}
			return "default", nil
		}
		if _, err := probeBuildKitCapabilities(context.Background(), run); err == nil {
			t.Fatalf("expected %q to fail", info)
		}
	}
}
