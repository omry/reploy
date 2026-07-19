package deploy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func overlayTestDocument() blueprint.Document {
	return blueprint.Document{Environment: blueprint.Environment{Components: map[string]blueprint.Component{
		"base": {Type: blueprint.ComponentTypeBase, Base: &blueprint.BaseComponent{Image: "debian:13"}},
		"app": {
			Type: blueprint.ComponentTypePython, Python: &blueprint.PythonComponent{Requirements: []string{"demo"}},
			Options: map[string]blueprint.ComponentOption{"debug": {Description: "Debug", PythonRequirements: []string{"debugpy"}}},
		},
		"system": {
			Type: blueprint.ComponentTypeAPT, APT: &blueprint.APTComponent{Packages: []blueprint.APTPackageRequest{{Name: "curl"}}},
			Options: map[string]blueprint.ComponentOption{"git": {Description: "Git", APTPackages: []blueprint.APTPackageRequest{{Name: "git"}}}},
		},
	}}}
}

func overlayTestPackageValidator(componentType blueprint.ComponentType, request providers.CanonicalPackageRequest) error {
	expected := map[blueprint.ComponentType]string{
		blueprint.ComponentTypeAPT:    "apt-package-request-v1",
		blueprint.ComponentTypePython: "python-package-request-v1",
	}[componentType]
	if expected == "" {
		return fmt.Errorf("component type %q does not support direct packages", componentType)
	}
	if request.Schema != expected {
		return fmt.Errorf("component type %q requires package schema %q", componentType, expected)
	}
	if componentType == blueprint.ComponentTypeAPT {
		if _, ok := request.Value["exports"].([]any); !ok {
			return fmt.Errorf("APT package exports must be an array")
		}
	}
	if componentType == blueprint.ComponentTypePython {
		if _, ok := request.Value["requirement"].(string); !ok || len(request.Value) != 1 {
			return fmt.Errorf("Python package request is malformed")
		}
	}
	_, err := providers.CanonicalPackageRequestBytes(request)
	return err
}

func TestNormalizeEmptyRequestOverlayV1(t *testing.T) {
	normalized, err := NormalizeRequestOverlayV1(overlayTestDocument(), RequestOverlayV1{}, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	want := EmptyRequestOverlayV1()
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("overlay = %#v, want %#v", normalized, want)
	}
	digest, err := RequestOverlayDigestV1(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := digest, canonical.Digest("sha256:1fbde17a76c7852027e8e1a5a3871876f2a4d8bced6a3d4fe8848228dfb8b149"); got != want {
		t.Fatalf("empty overlay digest = %q, want %q", got, want)
	}
}

func TestNormalizeRequestOverlayV1SortsAndDeduplicates(t *testing.T) {
	aptCurl := providers.CanonicalPackageRequest{Schema: "apt-package-request-v1", Value: canonical.Object{"name": "curl", "exports": []any{}}}
	aptGit := providers.CanonicalPackageRequest{Schema: "apt-package-request-v1", Value: canonical.Object{"name": "git", "exports": []any{}}}
	pythonDebug := providers.CanonicalPackageRequest{Schema: "python-package-request-v1", Value: canonical.Object{"requirement": "debugpy==1.8.0"}}
	overlay := RequestOverlayV1{
		Schema: RequestOverlaySchemaV1,
		SelectedOptions: []QualifiedOption{
			{Component: "system", Option: "git"},
			{Component: "app", Option: "debug"},
			{Component: "app", Option: "debug"},
		},
		DirectPackages: []DirectPackageRequest{
			{Component: "system", Package: aptGit},
			{Component: "app", Package: pythonDebug},
			{Component: "system", Package: aptCurl},
			{Component: "system", Package: aptCurl},
		},
	}
	normalized, err := NormalizeRequestOverlayV1(overlayTestDocument(), overlay, overlayTestPackageValidator)
	if err != nil {
		t.Fatal(err)
	}
	wantOptions := []QualifiedOption{{Component: "app", Option: "debug"}, {Component: "system", Option: "git"}}
	if !reflect.DeepEqual(normalized.SelectedOptions, wantOptions) {
		t.Fatalf("selected options = %#v", normalized.SelectedOptions)
	}
	if len(normalized.DirectPackages) != 3 || normalized.DirectPackages[0].Component != "app" || normalized.DirectPackages[1].Package.Value["name"] != "curl" || normalized.DirectPackages[2].Package.Value["name"] != "git" {
		t.Fatalf("direct packages = %#v", normalized.DirectPackages)
	}
	if _, err := RequestOverlayDigestV1(normalized); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeRequestOverlayV1RejectsInvalidIntent(t *testing.T) {
	apt := providers.CanonicalPackageRequest{Schema: "apt-package-request-v1", Value: canonical.Object{"name": "curl", "exports": []any{}}}
	for _, test := range []struct {
		name    string
		overlay RequestOverlayV1
		want    string
	}{
		{name: "schema", overlay: RequestOverlayV1{Schema: "overlay-v2"}, want: "schema"},
		{name: "missing component option", overlay: RequestOverlayV1{Schema: RequestOverlaySchemaV1, SelectedOptions: []QualifiedOption{{Component: "missing", Option: "debug"}}}, want: "missing component"},
		{name: "missing option", overlay: RequestOverlayV1{Schema: RequestOverlaySchemaV1, SelectedOptions: []QualifiedOption{{Component: "app", Option: "missing"}}}, want: "missing option"},
		{name: "base package", overlay: RequestOverlayV1{Schema: RequestOverlaySchemaV1, DirectPackages: []DirectPackageRequest{{Component: "base", Package: apt}}}, want: "does not support"},
		{name: "wrong package schema", overlay: RequestOverlayV1{Schema: RequestOverlaySchemaV1, DirectPackages: []DirectPackageRequest{{Component: "app", Package: apt}}}, want: "requires package schema"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeRequestOverlayV1(overlayTestDocument(), test.overlay, overlayTestPackageValidator)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRequestOverlayDigestV1RejectsNoncanonicalCollections(t *testing.T) {
	aptCurl := providers.CanonicalPackageRequest{Schema: "apt-package-request-v1", Value: canonical.Object{"name": "curl", "exports": []any{}}}
	aptGit := providers.CanonicalPackageRequest{Schema: "apt-package-request-v1", Value: canonical.Object{"name": "git", "exports": []any{}}}
	for _, overlay := range []RequestOverlayV1{
		{Schema: RequestOverlaySchemaV1},
		{Schema: RequestOverlaySchemaV1, SelectedOptions: []QualifiedOption{{Component: "system", Option: "git"}, {Component: "app", Option: "debug"}}, DirectPackages: []DirectPackageRequest{}},
		{Schema: RequestOverlaySchemaV1, SelectedOptions: []QualifiedOption{{Component: "app", Option: "debug"}, {Component: "app", Option: "debug"}}, DirectPackages: []DirectPackageRequest{}},
		{Schema: RequestOverlaySchemaV1, SelectedOptions: []QualifiedOption{}, DirectPackages: []DirectPackageRequest{{Component: "system", Package: aptGit}, {Component: "system", Package: aptCurl}}},
		{Schema: RequestOverlaySchemaV1, SelectedOptions: []QualifiedOption{}, DirectPackages: []DirectPackageRequest{{Component: "system", Package: aptCurl}, {Component: "system", Package: aptCurl}}},
	} {
		if _, err := RequestOverlayDigestV1(overlay); err == nil {
			t.Fatalf("RequestOverlayDigestV1(%#v) succeeded", overlay)
		}
	}
}

func TestRequestOverlayDigestV1ChangesWithIntent(t *testing.T) {
	empty := EmptyRequestOverlayV1()
	selected := EmptyRequestOverlayV1()
	selected.SelectedOptions = append(selected.SelectedOptions, QualifiedOption{Component: "app", Option: "debug"})
	emptyDigest, err := RequestOverlayDigestV1(empty)
	if err != nil {
		t.Fatal(err)
	}
	selectedDigest, err := RequestOverlayDigestV1(selected)
	if err != nil {
		t.Fatal(err)
	}
	if emptyDigest == selectedDigest {
		t.Fatal("overlay intent did not affect digest")
	}
}
