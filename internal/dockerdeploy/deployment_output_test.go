package dockerdeploy

import (
	"strings"
	"testing"

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

func TestStagingOutputWritersWrapDeploymentPhases(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	var staged strings.Builder
	stdout, _ := deploymentOutputWritersForState(deploy.DeploymentState{Phase: deploy.PhaseStaged, AppID: "demo"}, &staged, nil)
	if _, err := stdout.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if staged.String() != "[STAGING : demo] hello\n" {
		t.Fatalf("staged output = %q", staged.String())
	}

	var installed strings.Builder
	stdout, _ = deploymentOutputWritersForState(deploy.DeploymentState{Phase: deploy.PhaseInstalled, AppID: "demo"}, &installed, nil)
	if _, err := stdout.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if installed.String() != "[DEPLOYED : demo] hello\n" {
		t.Fatalf("installed output = %q", installed.String())
	}
}

func TestDeploymentOutputWriterUsesPhaseColors(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "always")

	var staged strings.Builder
	stdout, _ := deploymentOutputWritersForState(deploy.DeploymentState{Phase: deploy.PhaseStaged, AppID: "demo"}, &staged, nil)
	if _, err := stdout.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if staged.String() != "\x1b[38;5;117m[STAGING : demo]\x1b[0m hello\n" {
		t.Fatalf("staged output = %q", staged.String())
	}

	var deployed strings.Builder
	stdout, _ = deploymentOutputWritersForState(deploy.DeploymentState{Phase: deploy.PhaseInstalled, AppID: "demo"}, &deployed, nil)
	if _, err := stdout.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if deployed.String() != "\x1b[38;5;208m[DEPLOYED : demo]\x1b[0m hello\n" {
		t.Fatalf("deployed output = %q", deployed.String())
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
