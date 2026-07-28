package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestRecoverLegacyComponentsStagingStateV1PreservesIntentAndFiles(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	content, current := legacyComponentsStagingStateFixtureV1(t)
	statePath := filepath.Join(dir, ".reploy", stateFilenameV1)
	if err := os.WriteFile(statePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	overrides := []byte(
		"environment:\n" +
			"  id: legacy-demo\n" +
			"  vars: {}\n" +
			"  package_overrides: {}\n",
	)
	if err := os.WriteFile(filepath.Join(dir, PackageOverridesFilename), overrides, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "value"), []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	privateEnvironment := []byte("TOKEN=preserved\n")
	if err := os.WriteFile(filepath.Join(dir, ".env"), privateEnvironment, 0o600); err != nil {
		t.Fatal(err)
	}
	platformSelectionCalls := 0
	recovery, err := operation.PrepareLegacyComponentsStagingRecoveryV1(
		func(document blueprint.Document) (blueprint.Platform, error) {
			platformSelectionCalls++
			return document.Blueprint.Compatibility.Platforms[0], nil
		},
		func(componentType blueprint.ComponentType, request providers.CanonicalPackageRequest) error {
			if componentType != blueprint.ComponentTypePython || request.Schema != "test-package-v1" {
				t.Fatalf("package validation = %q/%#v", componentType, request)
			}
			return nil
		},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if platformSelectionCalls != 0 {
		t.Fatalf("supported selected platform was reselected %d times", platformSelectionCalls)
	}
	if recovery.State.Current != nil ||
		!reflect.DeepEqual(recovery.PreviousCurrent, current) ||
		recovery.PreviousEnvironment != "legacy-demo" {
		t.Fatalf("recovery plan = %#v", recovery)
	}
	if !reflect.DeepEqual(recovery.State.Overlay.SelectedOptions, []QualifiedOption{{
		Application: "application",
		Option:      "debug",
	}}) {
		t.Fatalf("recovered options = %#v", recovery.State.Overlay.SelectedOptions)
	}
	if len(recovery.State.Overlay.DirectPackages) != 1 ||
		recovery.State.Overlay.DirectPackages[0].Contribution != "application/application/python" {
		t.Fatalf("recovered direct packages = %#v", recovery.State.Overlay.DirectPackages)
	}
	if err := operation.CommitLegacyComponentsStagingRecoveryV1(recovery); err != nil {
		t.Fatal(err)
	}
	reloaded, found, err := operation.ReadStateV1()
	if err != nil || !found {
		t.Fatalf("read recovered state: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(reloaded, recovery.State) ||
		strings.Contains(reloaded.BlueprintSource, "components:") ||
		!strings.Contains(reloaded.BlueprintSource, "applications:") {
		t.Fatalf("reloaded recovery = %#v", reloaded)
	}
	gotOverrides, err := os.ReadFile(filepath.Join(dir, PackageOverridesFilename))
	if err != nil || !reflect.DeepEqual(gotOverrides, overrides) {
		t.Fatalf("overrides after recovery = %q, %v", gotOverrides, err)
	}
	gotData, err := os.ReadFile(filepath.Join(dir, "data", "value"))
	if err != nil || string(gotData) != "preserved" {
		t.Fatalf("managed data after recovery = %q, %v", gotData, err)
	}
	gotEnvironment, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil || !reflect.DeepEqual(gotEnvironment, privateEnvironment) {
		t.Fatalf("private environment after recovery = %q, %v", gotEnvironment, err)
	}
}

func TestPrepareLegacyComponentsStagingRecoveryV1RejectsIncompatiblePreservedOverrides(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	content, _ := legacyComponentsStagingStateFixtureV1(t)
	statePath := filepath.Join(dir, ".reploy", stateFilenameV1)
	if err := os.WriteFile(statePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	overrides, err := EncodePackageOverridesV1(
		EmptyPackageOverridesV1("different-environment"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, PackageOverridesFilename),
		overrides,
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	_, err = operation.PrepareLegacyComponentsStagingRecoveryV1(
		func(document blueprint.Document) (blueprint.Platform, error) {
			return document.Blueprint.Compatibility.Platforms[0], nil
		},
		func(blueprint.ComponentType, providers.CanonicalPackageRequest) error {
			return nil
		},
		true,
	)
	if err == nil ||
		!strings.Contains(err.Error(), "retain package overrides") ||
		!strings.Contains(err.Error(), `target environment "different-environment"`) {
		t.Fatalf("incompatible preserved overrides error = %v", err)
	}
	got, readErr := os.ReadFile(statePath)
	if readErr != nil || !reflect.DeepEqual(got, content) {
		t.Fatalf("legacy state changed after rejected recovery = %v/%q", readErr, got)
	}
}

func TestCommitLegacyComponentsStagingRecoveryV1RejectsChangedSnapshot(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	content, _ := legacyComponentsStagingStateFixtureV1(t)
	statePath := filepath.Join(dir, ".reploy", stateFilenameV1)
	if err := os.WriteFile(statePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	recovery, err := operation.PrepareLegacyComponentsStagingRecoveryV1(
		func(document blueprint.Document) (blueprint.Platform, error) {
			return document.Blueprint.Compatibility.Platforms[0], nil
		},
		func(blueprint.ComponentType, providers.CanonicalPackageRequest) error {
			return nil
		},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte(nil), content...), '\n')
	if err := os.WriteFile(statePath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	err = operation.CommitLegacyComponentsStagingRecoveryV1(recovery)
	if err == nil || !strings.Contains(err.Error(), "changed before forced recovery") {
		t.Fatalf("changed-snapshot commit error = %v", err)
	}
	got, readErr := os.ReadFile(statePath)
	if readErr != nil || !reflect.DeepEqual(got, changed) {
		t.Fatalf("changed snapshot after rejected commit = %v/%q", readErr, got)
	}
}

func TestDecodeStateV1ClassifiesLegacyComponentsStateWithRecoveryGuidance(t *testing.T) {
	content, _ := legacyComponentsStagingStateFixtureV1(t)
	_, err := DecodeStateV1(content)
	if err == nil ||
		!strings.Contains(err.Error(), "stage --update --force") ||
		!strings.Contains(err.Error(), "components-based") {
		t.Fatalf("legacy state error = %v", err)
	}
}

func legacyComponentsStagingStateFixtureV1(
	t *testing.T,
) ([]byte, *EnvironmentGenerationState) {
	t.Helper()
	const legacySource = `blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: legacy-demo
  components:
    base:
      image: python:3.13-slim
    application:
      type: python
      requirements: [demo]
      options:
        debug:
          description: Install debugging support.
          requirements: [debugpy]
      executables:
        server:
          binary: demo
  commands:
    serve:
      executable: application.server
      argv: [serve]
docker: {}
`
	const currentSource = `blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: legacy-demo
  base:
    image: python:3.13-slim
  applications:
    application:
      packages:
        python:
          requirements: [demo]
      options:
        debug:
          description: Install debugging support.
          packages:
            python:
              requirements: [debugpy]
      executables:
        server:
          source: python
          binary: demo
  commands:
    serve:
      executable: application.server
      argv: [serve]
docker: {}
`
	syntax, err := blueprint.Decode([]byte(currentSource))
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.Resolve(syntax)
	if err != nil {
		t.Fatal(err)
	}
	documentContent, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var documentMap map[string]any
	if err := json.Unmarshal(documentContent, &documentMap); err != nil {
		t.Fatal(err)
	}
	environment := documentMap["Environment"].(map[string]any)
	components := map[string]any{}
	componentContent, err := json.Marshal(document.Environment.Components)
	if err != nil {
		t.Fatal(err)
	}
	var allComponents map[string]any
	if err := json.Unmarshal(componentContent, &allComponents); err != nil {
		t.Fatal(err)
	}
	components["base"] = allComponents["base"]
	application := allComponents["application/application/python"].(map[string]any)
	executables := application["Executables"].(map[string]any)
	for _, value := range executables {
		delete(value.(map[string]any), "Source")
	}
	components["application"] = application
	delete(environment, "Base")
	delete(environment, "Packages")
	delete(environment, "Applications")
	environment["Components"] = components
	resolvedContent, err := json.Marshal(map[string]any{
		"schema":   blueprint.ResolvedDocumentSchemaV1,
		"document": documentMap,
	})
	if err != nil {
		t.Fatal(err)
	}
	platform := document.Blueprint.Compatibility.Platforms[0]
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	current := &EnvironmentGenerationState{
		Reference: "reploy/env/legacy-demo:g-current", ImageDigest: digest,
		RootFSSubject: digest, BuildLockDigest: digest, Platform: platform,
		RuntimePolicyDigest: digest,
	}
	packageRequest := providers.CanonicalPackageRequest{
		Schema: "test-package-v1",
		Value:  canonical.Object{},
	}
	state := map[string]any{
		"schema":           StateSchemaV1,
		"blueprint":        string(resolvedContent),
		"blueprint_source": legacySource,
		"platform":         platform,
		"overlay": map[string]any{
			"schema": RequestOverlaySchemaV1,
			"selected_options": []any{
				map[string]any{"component": "application", "option": "debug"},
			},
			"direct_packages": []any{
				map[string]any{"component": "application", "package": packageRequest},
			},
		},
		"current":    current,
		"staging":    StagingStateV1{Schema: StagingStateSchemaV1},
		"deployment": nil,
	}
	content, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return content, current
}
