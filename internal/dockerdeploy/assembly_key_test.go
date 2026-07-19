package dockerdeploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func TestMaterializationAssemblyKeyUsesPinnedRendererInputs(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	transaction := rendererTransaction()
	key, digest, err := MaterializationAssemblyKey(transaction, platform)
	if err != nil {
		t.Fatal(err)
	}
	wantTransaction, err := providers.MaterializationTransactionDigest(transaction)
	if err != nil {
		t.Fatal(err)
	}
	if key.Parent != transaction.Upstream || key.TransactionDigest != wantTransaction || key.RendererProfile != MaterializationRendererProfile || key.Platform != platform {
		t.Fatalf("assembly key = %#v", key)
	}
	if !strings.HasSuffix(MaterializationDockerfileSyntax, "@"+string(key.DockerfileFrontend)) {
		t.Fatalf("frontend %q does not match Dockerfile syntax %q", key.DockerfileFrontend, MaterializationDockerfileSyntax)
	}
	wantDigest, err := providers.AssemblyKeyDigest(key)
	if err != nil {
		t.Fatal(err)
	}
	if digest != wantDigest {
		t.Fatalf("assembly digest = %q, want %q", digest, wantDigest)
	}
}

func TestMaterializationAssemblyKeyChangesWithTransaction(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	transaction := rendererTransaction()
	_, first, err := MaterializationAssemblyKey(transaction, platform)
	if err != nil {
		t.Fatal(err)
	}
	transaction.Argv[1].Literal = "changed"
	_, second, err := MaterializationAssemblyKey(transaction, platform)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("transaction change did not invalidate assembly key")
	}
}

func TestMaterializationAssemblyKeyRejectsInvalidInputs(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	transaction := rendererTransaction()
	transaction.Schema = "materialization-transaction-v2"
	if _, _, err := MaterializationAssemblyKey(transaction, platform); err == nil || !strings.Contains(err.Error(), "transaction") {
		t.Fatalf("error = %v", err)
	}

	transaction = rendererTransaction()
	platform.Canonical = "linux/arm64"
	if _, _, err := MaterializationAssemblyKey(transaction, platform); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("error = %v", err)
	}
}
