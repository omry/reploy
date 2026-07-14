package dockerdeploy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestInitEnvironmentBlueprintUsesDirectControlNameAndTypedDocument(t *testing.T) {
	ref, err := deploy.ParsePackRef("file:../../examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "omegaconf-inspector")); err != nil {
		t.Fatalf("direct control script is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "omegaconf-inspectorctl")); !os.IsNotExist(err) {
		t.Fatalf("legacy ctl script should not exist: %v", err)
	}
	state, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Environment == nil {
		t.Fatal("typed environment document was lost from persisted blueprint reference")
	}
	dockerEnv, err := os.ReadFile(filepath.Join(dir, DockerEnvFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(dockerEnv) == "" || containsAny(string(dockerEnv), "REPLOY_RUNTIME_DIR", "REPLOY_DEPLOYMENT_SCOPE", "REPLOY_CONTAINER_COMMAND") {
		t.Fatalf("environment bootstrap inputs retain legacy runtime protocol:\n%s", dockerEnv)
	}
	compose, err := os.ReadFile(filepath.Join(dir, ComposeFileName))
	if err != nil {
		t.Fatal(err)
	}
	if containsAny(string(compose), "pip install", "prepare_python_runtime", "REPLOY_RUNTIME_ROOT") {
		t.Fatalf("environment bootstrap Compose retains startup installer:\n%s", compose)
	}
}

func TestEnvironmentBundleBuildNeverWarmsStartupRuntime(t *testing.T) {
	ref, err := deploy.ParsePackRef("file:../../examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Init(InitOptions{Dir: dir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := BundlePrepare(BundlePrepareOptions{Dir: dir, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	if containsAny(stdout.String(), "warm Python runtime", "__reploy_runtime_warmup", "REPLOY_RUNTIME_ROOT") {
		t.Fatalf("environment bundle build retained startup runtime behavior:\n%s", stdout.String())
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
