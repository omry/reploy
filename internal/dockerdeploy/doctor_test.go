package dockerdeploy

import (
	"os"
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

	var stdout strings.Builder
	code := Doctor(DoctorOptions{Dir: deployDir, Preinstall: true, Stdout: &stdout})
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok: preinstall checks passed") {
		t.Fatalf("stdout missing preinstall success:\n%s", stdout.String())
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
