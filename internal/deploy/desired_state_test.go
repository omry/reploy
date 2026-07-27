package deploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func desiredStateTestPlatform(t *testing.T, value string) blueprint.Platform {
	t.Helper()
	platform, err := blueprint.ParsePlatform(value)
	if err != nil {
		t.Fatal(err)
	}
	return platform
}

func TestSetDesiredStateV1CreatesUnbuiltDeployment(t *testing.T) {
	dir := t.TempDir()
	document := overlayTestDocument()
	platform := desiredStateTestPlatform(t, "linux/amd64")

	result, err := SetDesiredStateV1(context.Background(), dir, document, platform, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.State.Current != nil || !reflect.DeepEqual(result.State.Overlay, EmptyRequestOverlayV1()) {
		t.Fatalf("result = %#v", result)
	}
	if result.State.Platform != platform {
		t.Fatalf("platform = %#v, want %#v", result.State.Platform, platform)
	}
	decoded, err := blueprint.DecodeResolvedDocumentV1(result.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, document) {
		t.Fatalf("blueprint = %#v, want %#v", decoded, document)
	}

	path := filepath.Join(dir, ".reploy", stateFilenameV1)
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := EncodeStateV1(result.State)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persisted, want) {
		t.Fatal("persisted state differs from result")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}
}

func TestCreateDesiredStateV1CreatesOnlyWhenStateIsMissing(t *testing.T) {
	dir := t.TempDir()
	document := overlayTestDocument()
	platform := desiredStateTestPlatform(t, "linux/amd64")

	created, err := CreateDesiredStateV1(context.Background(), dir, document, platform, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Changed || created.State.Current != nil {
		t.Fatalf("created result = %#v", created)
	}
	path := filepath.Join(dir, ".reploy", stateFilenameV1)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	document.Blueprint.Version = "replacement"
	_, err = CreateDesiredStateV1(context.Background(), dir, document, platform, overlayTestPackageValidator)
	if !errors.Is(err, ErrDesiredStateAlreadyExists) {
		t.Fatalf("second create error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("rejected create changed existing state")
	}
}

func TestSetDesiredStateV1NoopDoesNotRewriteState(t *testing.T) {
	dir := t.TempDir()
	document := overlayTestDocument()
	platform := desiredStateTestPlatform(t, "linux/amd64")
	if _, err := SetDesiredStateV1(context.Background(), dir, document, platform, overlayTestPackageValidator); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", stateFilenameV1)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	result, err := SetDesiredStateV1(context.Background(), dir, document, platform, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatalf("result = %#v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("no-op stage rewrote state")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("no-op stage replaced state: mode = %o", info.Mode().Perm())
	}
}

func TestSetDesiredStateV1PreservesOverlayAndCurrentGeneration(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	if _, err := MutateRequestOverlayV1(context.Background(), dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		overlay.SelectedOptions = append(overlay.SelectedOptions, QualifiedOption{Application: "app", Option: "debug"})
		return overlay, nil
	}); err != nil {
		t.Fatal(err)
	}
	before := readOverlayTestState(t, statePath)
	document := overlayTestDocument()
	document.Blueprint.Version = "2"

	result, err := SetDesiredStateV1(context.Background(), dir, document, before.Platform, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("changed blueprint was reported as a no-op")
	}
	if !reflect.DeepEqual(result.State.Overlay, before.Overlay) {
		t.Fatalf("overlay = %#v, want %#v", result.State.Overlay, before.Overlay)
	}
	if !reflect.DeepEqual(result.State.Current, before.Current) {
		t.Fatalf("current generation = %#v, want %#v", result.State.Current, before.Current)
	}
	decoded, err := blueprint.DecodeResolvedDocumentV1(result.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Blueprint.Version != "2" {
		t.Fatalf("blueprint version = %q", decoded.Blueprint.Version)
	}
}

func TestSetDesiredStateV1RejectsDifferentEnvironmentWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	document := overlayTestDocument()
	document.Environment.ID = "demo"
	platform := desiredStateTestPlatform(t, "linux/amd64")
	if _, err := SetDesiredStateV1(t.Context(), dir, document, platform, overlayTestPackageValidator); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, ".reploy", stateFilenameV1)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	replacement := document
	replacement.Environment.ID = "other"
	_, err = SetDesiredStateV1(t.Context(), dir, replacement, platform, overlayTestPackageValidator)
	if err == nil || !strings.Contains(err.Error(), `staging directory belongs to environment "demo"`) || !strings.Contains(err.Error(), "different staging directory") {
		t.Fatalf("different environment error = %v", err)
	}
	after, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("rejected environment replacement changed existing state")
	}
}

func TestSetDesiredStateV1PreservesDeploymentLocalState(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	before := readOverlayTestState(t, statePath)
	before.Deployment = &DeploymentStateV1{
		Schema: DeploymentStateSchemaV1,
		Installation: InstallationStateV1{
			Schema: InstallationSchemaV1, Status: InstallationStatusReady,
			TargetDir: "/opt/demo", Scope: "system", Service: "demo",
			UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo-1",
			ContainerName: "demo", NetworkName: "demo", Ports: []InstallationPortBindingV1{},
		},
	}
	content, err := EncodeStateV1(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	document := overlayTestDocument()
	document.Blueprint.Version = "2"

	result, err := SetDesiredStateV1(t.Context(), dir, document, before.Platform, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.State.Deployment, before.Deployment) {
		t.Fatalf("deployment-local state changed: before=%#v after=%#v", before.Deployment, result.State.Deployment)
	}
}

func TestSetDesiredStateV1RejectsIncompatibleOverlayWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	if _, err := MutateRequestOverlayV1(context.Background(), dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		overlay.SelectedOptions = append(overlay.SelectedOptions, QualifiedOption{Application: "app", Option: "debug"})
		return overlay, nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	document := overlayTestDocument()
	application := document.Environment.Applications["app"]
	application.Options = map[string]blueprint.ApplicationOption{}
	document.Environment.Applications["app"] = application
	if err := document.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	_, err = SetDesiredStateV1(context.Background(), dir, document, desiredStateTestPlatform(t, "linux/amd64"), overlayTestPackageValidator)
	if err == nil || !strings.Contains(err.Error(), "missing option") {
		t.Fatalf("error = %v", err)
	}
	after, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("failed stage changed state")
	}
}

func TestSetDesiredStateV1ChangesPlatformAndKeepsPriorGeneration(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	before := readOverlayTestState(t, statePath)
	document := overlayTestDocument()
	compatibility, err := blueprint.ParseCompatibility([]string{"linux/arm64", "linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	document.Blueprint.Compatibility = compatibility
	arm64 := desiredStateTestPlatform(t, "linux/arm64")

	result, err := SetDesiredStateV1(context.Background(), dir, document, arm64, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.State.Platform != arm64 {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(result.State.Current, before.Current) || result.State.Current.Platform == arm64 {
		t.Fatalf("prior generation was not retained as stale: %#v", result.State.Current)
	}
}

func TestSetDesiredStateV1RejectsUndeclaredPlatformWithoutCreatingState(t *testing.T) {
	dir := t.TempDir()
	_, err := SetDesiredStateV1(context.Background(), dir, overlayTestDocument(), desiredStateTestPlatform(t, "linux/arm64"), overlayTestPackageValidator)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".reploy", stateFilenameV1)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state exists after rejected stage: %v", statErr)
	}
}

func TestSetDesiredStateV1RejectsLegacyStateWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".reploy")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, stateFilenameV1)
	legacy := []byte(`{"schema_version":1,"bundle":{"prepared_fingerprint":"old"}}`)
	if err := os.WriteFile(path, legacy, 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := SetDesiredStateV1(context.Background(), dir, overlayTestDocument(), desiredStateTestPlatform(t, "linux/amd64"), overlayTestPackageValidator)
	if !errors.Is(err, ErrLegacyStateUnsupported) {
		t.Fatalf("error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, legacy) {
		t.Fatal("legacy state was mutated")
	}
}

func TestSetDesiredPlatformV1ChangesOnlySelectedPlatform(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlayTestState(t, dir)
	before := readOverlayTestState(t, path)
	document, err := blueprint.DecodeResolvedDocumentV1(before.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	compatibility, err := blueprint.ParseCompatibility([]string{"linux/amd64", "linux/arm64"})
	if err != nil {
		t.Fatal(err)
	}
	document.Blueprint.Compatibility = compatibility
	before.Blueprint, err = blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	content, err := EncodeStateV1(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o640); err != nil {
		t.Fatal(err)
	}

	arm64 := desiredStateTestPlatform(t, "linux/arm64")
	result, err := SetDesiredPlatformV1(t.Context(), dir, arm64, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.State.Platform != arm64 {
		t.Fatalf("result = %#v", result)
	}
	if result.State.Blueprint != before.Blueprint || !reflect.DeepEqual(result.State.Overlay, before.Overlay) || !reflect.DeepEqual(result.State.Current, before.Current) {
		t.Fatalf("platform update changed retained state: before=%#v after=%#v", before, result.State)
	}
	if result.State.Current == nil || result.State.Current.Platform == arm64 {
		t.Fatalf("prior generation was not retained as stale: %#v", result.State.Current)
	}
}

func TestSetDesiredPlatformV1RejectsUndeclaredPlatformWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlayTestState(t, dir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = SetDesiredPlatformV1(t.Context(), dir, desiredStateTestPlatform(t, "linux/arm64"), overlayTestPackageValidator)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("rejected platform update changed state")
	}
}

func TestSetDesiredPlatformV1NoopDoesNotRewriteState(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlayTestState(t, dir)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	result, err := SetDesiredPlatformV1(t.Context(), dir, desiredStateTestPlatform(t, "linux/amd64"), overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("unchanged platform was reported as changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("no-op platform update replaced state: mode = %o", info.Mode().Perm())
	}
}

func TestSelectDesiredPlatformV1PassesStoredBlueprintAndPreservesStateOnSelectionFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlayTestState(t, dir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("selection failed")
	_, err = SelectDesiredPlatformV1(t.Context(), dir, func(document blueprint.Document) (blueprint.Platform, error) {
		if document.Environment.Components["base"].Base.Image != "debian:13" {
			t.Fatalf("selector document = %#v", document)
		}
		return blueprint.Platform{}, wantErr
	}, overlayTestPackageValidator)
	if !errors.Is(err, wantErr) {
		t.Fatalf("selection error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("failed selection changed state")
	}
}
