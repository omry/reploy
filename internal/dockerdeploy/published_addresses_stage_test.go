package dockerdeploy

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

func TestPublishedAddressesCanonicalArbiterStageImportRefreshAndSelections(t *testing.T) {
	authored, err := os.ReadFile("testdata/published-address/arbiter.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	manifest := filepath.Join(sourceDir, "arbiter.blueprint.yaml")
	if err := os.WriteFile(manifest, authored, 0600); err != nil {
		t.Fatal(err)
	}
	raw := deploy.EmptyPackageOverridesV1("arbiter")
	raw.Environment.Base = &deploy.BaseImageOverrideV1{Image: "python:3.13-slim"}
	raw.Environment.Vars["workspace_root"] = t.TempDir()
	raw.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"arbiter-server": {Path: "{{ workspace_root }}/server"}, "arbiter-imap": {Version: "0.1.0"}, "arbiter-smtp": {Version: "0.1.0"},
	}
	raw.Docker.Workload.Endpoints = map[string]deploy.DockerEndpointOverrideV1{"https": {Publish: deploy.DockerPublicationOverrideV1{Address: "0.0.0.0"}}}
	sidecar, err := deploy.EncodePackageOverridesV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, deploy.PackageOverridesFilename), sidecar, 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := deploy.ParsePackRef("file:" + manifest)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input := PackDesiredStateStageInputV1{DeploymentDir: dir, Pack: ref, ExplicitPlatform: "linux/amd64", Create: true, SkipControlSurface: true}
	staged, err := StagePackDesiredStateV1(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if staged.DesiredState.State.BlueprintSource != string(authored) {
		t.Fatal("authored source changed")
	}
	imported, found, err := deploy.ReadPackageOverridesV1(dir)
	if err != nil || !found || imported.PublishedAddressesV1()["https"] != "0.0.0.0" {
		t.Fatalf("import=%#v %v", imported, err)
	}
	if imported.Environment.Base.Image != raw.Environment.Base.Image || imported.Environment.PackageOverrides["python"]["arbiter-smtp"].Version != "0.1.0" || imported.Environment.PackageOverrides["python"]["arbiter-server"].Path != "{{ workspace_root }}/server" {
		t.Fatalf("source/base overrides changed: %#v", imported)
	}
	_, err = deploy.MutateRequestOverlayV1(t.Context(), dir, registry.ValidatePackageRequest, func(_ blueprint.Document, overlay deploy.RequestOverlayV1) (deploy.RequestOverlayV1, error) {
		return deploy.AddOverlayOptions(overlay, []deploy.QualifiedOption{{Application: "application", Option: "imap"}, {Application: "application", Option: "smtp"}}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, deploy.PackageOverridesFilename))
	if err != nil {
		t.Fatal(err)
	}
	// Updating never imports a changed source sidecar over deployment intent.
	raw.Docker.Workload.Endpoints["https"] = deploy.DockerEndpointOverrideV1{Publish: deploy.DockerPublicationOverrideV1{Address: "192.0.2.42"}}
	changed, _ := deploy.EncodePackageOverridesV1(raw)
	if err := os.WriteFile(filepath.Join(sourceDir, deploy.PackageOverridesFilename), changed, 0600); err != nil {
		t.Fatal(err)
	}
	input.Create = false
	updated, err := StagePackDesiredStateV1(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, deploy.PackageOverridesFilename))
	if !bytes.Equal(before, after) {
		t.Fatal("refresh changed deployment override sidecar")
	}
	if len(updated.DesiredState.State.Overlay.SelectedOptions) != 2 || updated.DesiredState.State.BlueprintSource != string(authored) {
		t.Fatal("refresh lost selections or source")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(updated.DesiredState.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	original, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []blueprint.Phase{blueprint.PhaseStaged, blueprint.PhaseInstalled} {
		scope := blueprint.InstallScopeUser
		context := DockerPlanContext{DeploymentDir: dir, Phase: phase, GeneratedImage: "image", UID: 1000, GID: 1000, PublishedAddresses: imported.PublishedAddressesV1()}
		if phase == blueprint.PhaseInstalled {
			context.Scope = &scope
			context.InstallTarget = t.TempDir()
		}
		plan, err := PlanDockerExecution(document, context)
		if err != nil {
			t.Fatal(err)
		}
		values, err := resolveEnvironmentOperationStrings(document, plan, []string{"{{ reploy.workload.endpoints.https.publish.address }}", "{{ reploy.workload.endpoints.https.publish.port }}"})
		if err != nil || values[0] != "0.0.0.0" {
			t.Fatalf("interpolation=%v %v", values, err)
		}
		wantPort := "18075"
		if phase == blueprint.PhaseInstalled {
			wantPort = "8075"
		}
		if values[1] != wantPort {
			t.Fatalf("phase port=%v", values)
		}
	}
	final, _ := blueprint.EncodeResolvedDocumentV1(document)
	if !reflect.DeepEqual(final, original) {
		t.Fatal("address override mutated resolved blueprint")
	}
	sourceAfter, _ := os.ReadFile(manifest)
	if !bytes.Equal(sourceAfter, authored) {
		t.Fatal("canonical blueprint bytes changed")
	}
	// An updated blueprint that removes the selected endpoint must fail clearly.
	removed := bytes.ReplaceAll(authored, []byte("endpoints.https"), []byte("endpoints.https-renamed"))
	removed = bytes.ReplaceAll(removed, []byte("      https:"), []byte("      https-renamed:"))
	removed = bytes.ReplaceAll(removed, []byte("endpoints: [https]"), []byte("endpoints: [https-renamed]"))
	if err := os.WriteFile(manifest, removed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StagePackDesiredStateV1(t.Context(), input); err == nil || !strings.Contains(err.Error(), "unknown Docker workload endpoint") {
		t.Fatalf("removed endpoint accepted: %v", err)
	}
}
