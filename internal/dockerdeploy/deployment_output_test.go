package dockerdeploy

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestStagingOutputWriterPrefixesEachLine(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	var output strings.Builder
	writer := newDeploymentOutputWriter(&output, deploymentOutputLabel(stagingOutputPhase, "demo"), stagingOutputColor)

	if _, err := writer.Write([]byte("first\nsecond")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(" line\nthird\n")); err != nil {
		t.Fatal(err)
	}

	want := "[STAGING : demo] first\n[STAGING : demo] second line\n[STAGING : demo] third\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestDeploymentOutputPrefixTextUsesSameColors(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "always")

	var output strings.Builder
	prefix := deploymentOutputPrefixText(&output, deploymentOutputLabel(stagingOutputPhase, "demo"), stagingOutputColor)
	if prefix != "\x1b[38;5;117m[STAGING : demo]\x1b[0m" {
		t.Fatalf("prefix = %q", prefix)
	}
}

func TestDeploymentOutputHelpersReadStateV1WithoutLegacyProjection(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	dir := t.TempDir()
	current, input := runtimeCurrentBuildFixture(t)
	document := input.Document
	document.Environment.ID = "demo"
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	current.State.Blueprint = resolved
	content, err := deploy.EncodeStateV1(current.State)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".reploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	prefix, err := DeploymentOutputPrefix(dir, io.Discard)
	if err != nil || prefix != "[STAGING : demo]" {
		t.Fatalf("state-v1 prefix = %q, %v", prefix, err)
	}
	var stdout bytes.Buffer
	wrapped, _, err := DeploymentOutputWriters(dir, &stdout, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(wrapped, "ready\n"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "[STAGING : demo] ready\n" {
		t.Fatalf("state-v1 output = %q", got)
	}
}
