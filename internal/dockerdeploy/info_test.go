package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestInfoReportsStateAndBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	info, err := Info(InfoOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"target: docker",
		"phase: staged",
		"blueprint: file:" + packDir,
		"bundle roots:",
		"  - python package demo-suite",
		"bundle prepared:",
		"  not built",
		"files:",
	} {
		if !strings.Contains(info, want) {
			t.Fatalf("info missing %q:\n%s", want, info)
		}
	}
	for _, unwanted := range []string{"compose:", "docker env:", "requirements:"} {
		if strings.Contains(info, unwanted) {
			t.Fatalf("info should not expose generated path %q:\n%s", unwanted, info)
		}
	}
}

func TestInfoReportsPreparedBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(deployDir, BundleDirName)
	if err := os.WriteFile(filepath.Join(bundleDir, "demo_suite-1.2.3-py3-none-any.whl"), []byte("wheel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "hydra_core-1.3.2-py3-none-any.whl"), []byte("wheel\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Info(InfoOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"bundle prepared:",
		"  - root demo-suite==1.2.3",
		"  - transitive hydra-core==1.3.2",
	} {
		if !strings.Contains(info, want) {
			t.Fatalf("info missing %q:\n%s", want, info)
		}
	}
}

func TestInfoReportsResolvedEnvironmentWithoutMaterializingIt(t *testing.T) {
	ref, err := deploy.ParsePackRef("file:../../examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	info, err := Info(InfoOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"environment: omegaconf-inspector",
		"bundle identity: unresolved",
		"bundle inputs changed: true",
		"candidate bundle identity: unresolved",
		"materialized image: unresolved",
		"phase order:",
		"  - resolve blueprint",
		"  - materialize Docker environment",
		"  - satisfy readiness requirements",
		"commands:",
		"  - config check [staging,deployed]:",
		"endpoints:",
		"  - http: http://127.0.0.1:18076 -> 0.0.0.0:8076 readiness=/_health_",
		"backend files:",
		filepath.Join(deployDir, StateFileName) + " [existing]",
		filepath.Join(deployDir, ComposeFileName) + " [existing]",
	} {
		if !strings.Contains(info, want) {
			t.Fatalf("info missing %q:\n%s", want, info)
		}
	}
	state, err := loadState(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Materialization != nil || state.Images != nil || state.Bundle.PreparedFingerprint != "" {
		t.Fatalf("info mutated deployment state: %#v", state)
	}
}
