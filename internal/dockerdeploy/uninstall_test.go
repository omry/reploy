package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListReploySystemdServices(t *testing.T) {
	unitDir := t.TempDir()
	oldSystemdUnitDir := uninstallSystemdUnitDir
	t.Cleanup(func() { uninstallSystemdUnitDir = oldSystemdUnitDir })
	uninstallSystemdUnitDir = unitDir

	unit := `[Unit]
Description=Reploy Docker service (demo2)
# Managed-By: reploy
# Reploy-Service: demo2
# Reploy-Target: /opt/demo2
# Reploy-Compose-Project: demo2-abcd

[Service]
WorkingDirectory=/opt/demo2
ExecStart=/usr/bin/docker compose --project-name demo2-abcd up
`
	if err := os.WriteFile(filepath.Join(unitDir, "demo2.service"), []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "other.service"), []byte("[Unit]\nDescription=Other\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	services, err := ListReploySystemdServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("services = %#v", services)
	}
	if services[0].ServiceName != "demo2" || services[0].TargetDir != "/opt/demo2" || services[0].ComposeProject != "demo2-abcd" {
		t.Fatalf("service = %#v", services[0])
	}

	var stdout strings.Builder
	if err := PrintReploySystemdServices(&stdout); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SERVICE", "demo2", "/opt/demo2", "demo2-abcd"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestPrintReploySystemdServicesRejectsDockerManagedPlatforms(t *testing.T) {
	restorePlatform := stubHostPlatform(t, hostPlatform{GOOS: "windows"})
	defer restorePlatform()
	var stdout strings.Builder
	err := PrintReploySystemdServices(&stdout)
	if err == nil || !strings.Contains(err.Error(), "Linux/systemd-only") || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout should be empty: %q", stdout.String())
	}
}
