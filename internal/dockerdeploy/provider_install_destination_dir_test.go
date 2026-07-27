package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureProviderInstallDestinationV1CreatesAndReusesRealDirectory(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "nested", "deployment")
	created, err := ensureProviderInstallDestinationV1(destination)
	if err != nil || !created {
		t.Fatalf("first ensure created=%v error=%v", created, err)
	}
	created, err = ensureProviderInstallDestinationV1(destination)
	if err != nil || created {
		t.Fatalf("second ensure created=%v error=%v", created, err)
	}
	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination = %#v, error = %v", info, err)
	}
}

func TestCleanupFailedProviderInstallDestinationV1RemovesOnlyBootstrap(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "deployment")
	if created, err := ensureProviderInstallDestinationV1(destination); err != nil || !created {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(destination, ".reploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, ".reploy", "operation.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupFailedProviderInstallDestinationV1(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed destination remains: %v", err)
	}
}

func TestCleanupFailedProviderInstallDestinationV1RetainsChangedDestination(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "deployment")
	if created, err := ensureProviderInstallDestinationV1(destination); err != nil || !created {
		t.Fatal(err)
	}
	stateDir := filepath.Join(destination, ".reploy")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state-v1.json"), []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupFailedProviderInstallDestinationV1(destination); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(stateDir, "state-v1.json")); err != nil || string(content) != "state" {
		t.Fatalf("changed destination was not retained: content=%q error=%v", content, err)
	}
}

func TestEnsureProviderInstallDestinationV1RejectsNonDirectory(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "deployment")
	if err := os.WriteFile(destination, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ensureProviderInstallDestinationV1(destination)
	if err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("occupied destination error = %v", err)
	}
}
