package dockerdeploy

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestDoctorPassesForInitializedDeployment(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: generated file matches manifest:") {
		t.Fatalf("stdout missing manifest check:\n%s", stdout.String())
	}
}

func TestDoctorFailsForEditedGeneratedFile(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ComposeFileName), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: generated file has local edits:") {
		t.Fatalf("stdout missing local edit failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallIgnoresToolBinaryDrift(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ToolBinaryFileName), []byte("new running reploy\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var normalStdout strings.Builder
	normalCode := Doctor(DoctorOptions{Dir: deployDir, Stdout: &normalStdout})
	if normalCode != 1 {
		t.Fatalf("normal doctor exit = %d\n%s", normalCode, normalStdout.String())
	}
	if !strings.Contains(normalStdout.String(), "fail: generated file has local edits:") {
		t.Fatalf("normal doctor stdout missing local edit failure:\n%s", normalStdout.String())
	}

	var preinstallStdout strings.Builder
	preinstallCode := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &preinstallStdout})
	if preinstallCode != 0 {
		t.Fatalf("preinstall doctor exit = %d\n%s", preinstallCode, preinstallStdout.String())
	}
	if !strings.Contains(preinstallStdout.String(), "ok: generated file drift ignored for preinstall; install overwrites target:") {
		t.Fatalf("preinstall doctor stdout missing install overwrite note:\n%s", preinstallStdout.String())
	}
}

func TestDoctorPreinstallStillFailsForEditedGeneratedFile(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ComposeFileName), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: generated file has local edits:") {
		t.Fatalf("stdout missing local edit failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallPassesForInitializedDeployment(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: preinstall checks passed") {
		t.Fatalf("stdout missing preinstall success:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: install owner resolves to 1000:1000 (1000:1000)") {
		t.Fatalf("stdout missing install owner success:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallFailsForMissingBundleWheel(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	sourceWheel := filepath.Join(t.TempDir(), "demo-1.0.0-py3-none-any.whl")
	if err := os.WriteFile(sourceWheel, []byte("wheel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BundleAddWheel(BundleRootOptions{Dir: deployDir, Source: sourceWheel}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(deployDir, BundleDirName, filepath.Base(sourceWheel))); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: wheel root is missing from deployment bundle:") {
		t.Fatalf("stdout missing wheel failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallRejectsUnresolvedInstallOwner(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "appuser"}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `fail: install owner must resolve to a non-root uid:gid: resolve REPLOY_INSTALL_OWNER user "appuser"`) {
		t.Fatalf("stdout missing install owner failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallRejectsMissingInstallOwner(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 1 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fail: install owner must resolve to a non-root uid:gid: REPLOY_INSTALL_OWNER is required for install") {
		t.Fatalf("stdout missing install owner failure:\n%s", stdout.String())
	}
}

func TestDoctorPreinstallAcceptsNamedInstallOwner(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{"REPLOY_INSTALL_OWNER": "appuser:appgroup"}); err != nil {
		t.Fatal(err)
	}
	oldLookupUser := installLookupUser
	oldLookupGroup := installLookupGroup
	t.Cleanup(func() {
		installLookupUser = oldLookupUser
		installLookupGroup = oldLookupGroup
	})
	installLookupUser = func(name string) (*user.User, error) {
		if name != "appuser" {
			t.Fatalf("unexpected user lookup: %s", name)
		}
		return &user.User{Username: "appuser", Uid: "997", Gid: "988"}, nil
	}
	installLookupGroup = func(name string) (*user.Group, error) {
		if name != "appgroup" {
			t.Fatalf("unexpected group lookup: %s", name)
		}
		return &user.Group{Name: "appgroup", Gid: "988"}, nil
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: install owner resolves to appuser:appgroup (997:988)") {
		t.Fatalf("stdout missing install owner success:\n%s", stdout.String())
	}
}

func TestDoctorQuietSuppressesPassingChecks(t *testing.T) {
	disableDoctorColor(t)
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Quiet: true, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestDoctorStatusColors(t *testing.T) {
	colors := doctorColors{enabled: true}
	if got := colors.status("ok"); got != "\x1b[32mok\x1b[0m" {
		t.Fatalf("ok color = %q", got)
	}
	if got := colors.status("fail"); got != "\x1b[31mfail\x1b[0m" {
		t.Fatalf("fail color = %q", got)
	}
	if got := colors.status("unknown"); got != "unknown" {
		t.Fatalf("unknown color = %q", got)
	}
}

func TestDoctorCapturedOutputStaysPlain(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "auto")
	t.Setenv("TERM", "xterm-256color")
	var stdout strings.Builder
	colors := doctorStatusColors(&stdout)
	if got := colors.status("ok"); got != "ok" {
		t.Fatalf("captured ok color = %q", got)
	}
}

func disableDoctorColor(t *testing.T) {
	t.Helper()
	t.Setenv("REPLOY_COLOR", "never")
}
