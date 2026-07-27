package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

const overlayCommandBlueprint = `
blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: overlay-test
  components:
    base:
      image: python:3.13-slim
    application:
      type: python
      requirements: [demo]
      options:
        debug:
          description: Install debug support.
          requirements: [debugpy]
docker: {}
`

func writeOverlayCommandDeployment(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	syntax, err := blueprint.Decode([]byte(overlayCommandBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.Resolve(syntax)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "deployment")
	if err := os.MkdirAll(filepath.Join(dir, ".reploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := deploy.EncodeStateV1(deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: payload,
		Platform: document.Blueprint.Compatibility.Platforms[0], Overlay: deploy.EmptyRequestOverlayV1(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRequestOverlayOptionCommandsUseLockedBlueprint(t *testing.T) {
	dir := writeOverlayCommandDeployment(t)
	result, err := AddRequestOverlayOptions(context.Background(), dir, []string{"application/debug"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !reflect.DeepEqual(result.Overlay.SelectedOptions, []deploy.QualifiedOption{{Component: "application", Option: "debug"}}) {
		t.Fatalf("result = %#v", result)
	}
	result, err = AddRequestOverlayOptions(context.Background(), dir, []string{"application/debug"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("duplicate option selection changed state")
	}
	result, err = RemoveRequestOverlayOptions(context.Background(), dir, []string{"application/debug"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || len(result.Overlay.SelectedOptions) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRequestOverlayPackageCommandsStoreTypedIntentOnly(t *testing.T) {
	dir := writeOverlayCommandDeployment(t)
	result, err := AddRequestOverlayPackages(context.Background(), dir, "application", []string{"debugpy==1.8.0", "rich>=13"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || len(result.Overlay.DirectPackages) != 2 {
		t.Fatalf("result = %#v", result)
	}
	for _, request := range result.Overlay.DirectPackages {
		if request.Component != "application" || request.Package.Schema != pythonprovider.PackageRequestSchemaV1 {
			t.Fatalf("request = %#v", request)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".reploy", providerstore.StoreDirName)); !os.IsNotExist(err) {
		t.Fatalf("package intent unexpectedly prepared a bundle: %v", err)
	}
	result, err = RemoveRequestOverlayPackages(context.Background(), dir, "application", []string{"debugpy==1.8.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Overlay.DirectPackages) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRequestOverlayInspectionUsesResolvedBlueprintAndCanonicalState(t *testing.T) {
	dir := writeOverlayCommandDeployment(t)
	options, err := ListRequestOverlayOptions(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	wantOptions := []RequestOverlayOptionEntry{{Name: "application/debug", Description: "Install debug support."}}
	if !reflect.DeepEqual(options, wantOptions) {
		t.Fatalf("options = %#v, want %#v", options, wantOptions)
	}
	if _, err := AddRequestOverlayOptions(context.Background(), dir, []string{"application/debug"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddRequestOverlayPackages(context.Background(), dir, "application", []string{"rich>=13"}); err != nil {
		t.Fatal(err)
	}
	entries, err := ListRequestOverlay(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	wantEntries := []RequestOverlayEntry{
		{Kind: "option", Component: "application", Value: "application/debug"},
		{Kind: "package", Component: "application", Value: "rich>=13"},
	}
	if !reflect.DeepEqual(entries, wantEntries) {
		t.Fatalf("entries = %#v, want %#v", entries, wantEntries)
	}
}

func TestRequestOverlayCommandFailureWritesNothing(t *testing.T) {
	dir := writeOverlayCommandDeployment(t)
	statePath := filepath.Join(dir, StateFileName)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = AddRequestOverlayOptions(context.Background(), dir, []string{"application/debug,missing"})
	if err == nil || !strings.Contains(err.Error(), "missing option") {
		t.Fatalf("error = %v", err)
	}
	after, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("failed command changed state")
	}
}

func TestOverlayPackageParserUsesStrictProviderGrammar(t *testing.T) {
	aptRequest, err := parseOverlayPackageRequest(blueprint.ComponentTypeAPT, "python3=3.11.2-1+deb12u1")
	if err != nil {
		t.Fatal(err)
	}
	if aptRequest.Schema != aptprovider.PackageRequestSchemaV1 || aptRequest.Value["name"] != "python3" || aptRequest.Value["version"] != "3.11.2-1+deb12u1" {
		t.Fatalf("APT request = %#v", aptRequest)
	}
	if err := validateOverlayPackageRequest(blueprint.ComponentTypeAPT, aptRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := parseOverlayPackageRequest(blueprint.ComponentTypeAPT, "python3 --no-install-recommends"); err == nil {
		t.Fatal("APT option-like expression was accepted")
	}
	pythonRequest, err := parseOverlayPackageRequest(blueprint.ComponentTypePython, " debugpy==1.8.0 ")
	if err != nil {
		t.Fatal(err)
	}
	if pythonRequest.Schema != pythonprovider.PackageRequestSchemaV1 || pythonRequest.Value["requirement"] != "debugpy==1.8.0" {
		t.Fatalf("Python request = %#v", pythonRequest)
	}
}
