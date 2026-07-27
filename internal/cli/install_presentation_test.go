package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/dockerdeploy"
)

func TestWriteInstallResultClassifiesActualUpdateActions(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo")
	var output bytes.Buffer
	writeInstallResult(&output, dockerdeploy.ProviderInstallResultV1{
		Environment: "demo", TargetDir: target, ControlScript: "demo",
		Service: "demo", Updated: true, ImageReused: true, Started: true,
		PathUpdates: []dockerdeploy.PathUpdateAction{
			{Name: "config", Kind: dockerdeploy.PathReplaceManagedBind, Target: filepath.Join(target, "config")},
			{Name: "data", Kind: dockerdeploy.PathPreserveManagedBind, Target: filepath.Join(target, "data")},
			{Name: "exports", Kind: dockerdeploy.PathValidateUnmanaged, Target: "/srv/demo-exports"},
		},
	}, []string{"inspector url: http://127.0.0.1:19076"})

	got := output.String()
	for _, want := range []string{
		"[DEPLOYED : demo] update completed successfully",
		"[DEPLOYED : demo] preserved: data (" + filepath.Join(target, "data") + ")",
		"[DEPLOYED : demo] replaced: config (" + filepath.Join(target, "config") + ")",
		"[DEPLOYED : demo] retained: exports (/srv/demo-exports)",
		"[DEPLOYED : demo] replaced: service instance",
		"[DEPLOYED : demo] replaced: deployment files",
		"[DEPLOYED : demo] reused: environment image",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("install result missing %q:\n%s", want, got)
		}
	}
}

func TestWriteInstallResultReportsChangedEnvironmentImage(t *testing.T) {
	var output bytes.Buffer
	writeInstallResult(&output, dockerdeploy.ProviderInstallResultV1{
		Environment: "demo", TargetDir: "/opt/demo", Updated: true,
	}, nil)
	if !strings.Contains(output.String(), "[DEPLOYED : demo] replaced: environment image") {
		t.Fatalf("changed-generation result missing:\n%s", output.String())
	}
}

func TestWriteUninstallResultDistinguishesRetainedDirectory(t *testing.T) {
	var output bytes.Buffer
	writeUninstallResult(&output, dockerdeploy.ProviderUninstallResultV1{
		DeploymentDir: "/opt/demo", Environment: "demo", Service: "demo",
		RetainedDirectory: true,
	})
	got := output.String()
	if !strings.Contains(got, "[DEPLOYED : demo] retained: installation directory /opt/demo") ||
		strings.Contains(got, "removed: installation directory") {
		t.Fatalf("retained-directory uninstall result = %q", got)
	}
}

func TestInstallFailureDiagnosticRetainsLifecycleFailureWithoutDockerChatter(t *testing.T) {
	got := installFailureDiagnostic(assertiveError("startup failed"), strings.Join([]string{
		" Network demo Creating",
		" Container demo Starting",
		`{"check":"fail","reason":"database unavailable"}`,
	}, "\n"))
	if !strings.Contains(got, "startup failed") ||
		!strings.Contains(got, `"reason":"database unavailable"`) {
		t.Fatalf("failure diagnostic lost useful output:\n%s", got)
	}
	if strings.Contains(got, "Network demo") || strings.Contains(got, "Container demo") {
		t.Fatalf("failure diagnostic leaked Docker lifecycle chatter:\n%s", got)
	}
}

type assertiveError string

func (err assertiveError) Error() string { return string(err) }
