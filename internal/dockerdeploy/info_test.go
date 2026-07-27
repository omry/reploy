package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestInfoReadsUnbuiltStateV1(t *testing.T) {
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	document.Environment.ID = "demo"
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	current.State.Current = nil
	current.State.BlueprintSource = "blueprint:\n  schema: 1\n"
	current.State.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	current.State.Deployment = nil
	dir := t.TempDir()
	writeInfoStateV1(t, dir, current.State)

	if err := RequireStagingDeployment(dir); err != nil {
		t.Fatal(err)
	}
	info, err := Info(InfoOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"runtime: docker", "phase: staged", "environment: demo", "platform: linux/amd64",
		"bundle: not built", "image: not built", "request overlay:", "  (empty)",
	} {
		if !strings.Contains(info, want) {
			t.Fatalf("state-v1 info missing %q:\n%s", want, info)
		}
	}
}

func TestInfoReportsCurrentStateV1Build(t *testing.T) {
	current, _ := runtimeCurrentBuildFixture(t)
	dir := t.TempDir()
	writeInfoStateV1(t, dir, current.State)

	info, err := Info(InfoOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"bundle: built",
		"image: built",
	} {
		if !strings.Contains(info, want) {
			t.Fatalf("state-v1 info missing %q:\n%s", want, info)
		}
	}
	if strings.Contains(info, string(current.Generation.BuildLockDigest)) || strings.Contains(info, current.Generation.Reference) {
		t.Fatalf("default info leaked internal build identities:\n%s", info)
	}
}

func TestInfoRejectsLegacyState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StateFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"phase":"installed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Info(InfoOptions{Dir: dir}); err == nil || !strings.Contains(err.Error(), "expected \"state-v1\"") {
		t.Fatalf("legacy state error = %v", err)
	}
}

func writeInfoStateV1(t *testing.T, dir string, state deploy.StateV1) {
	t.Helper()
	content, err := deploy.EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, StateFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
