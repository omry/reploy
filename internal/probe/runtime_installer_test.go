package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRuntimeVerifierReplacesInheritedSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	content := []byte("trusted runtime verifier")
	if err := os.WriteFile(source, content, 0o555); err != nil {
		t.Fatal(err)
	}
	attackerDirectory := filepath.Join(root, "attacker")
	if err := os.Mkdir(attackerDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	attackerPath := filepath.Join(attackerDirectory, "reploy-probe")
	if err := os.WriteFile(attackerPath, []byte("attacker"), 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "reploy-probe")
	if err := os.Symlink(attackerPath, destination); err != nil {
		t.Fatal(err)
	}

	if err := installRuntimeVerifier(source, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := os.ReadFile(attackerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o555 || string(installed) != string(content) || string(attacker) != "attacker" {
		t.Fatalf("installed mode=%v content=%q attacker=%q", info.Mode(), installed, attacker)
	}
}
