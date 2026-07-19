package python

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	providerapi "github.com/omry/reploy/internal/providers"
)

func TestClassifyRootAcceptsContainerAbsoluteWheel(t *testing.T) {
	root, err := ClassifyRoot("/bundle/demo-1.0.0-py3-none-any.whl")
	if err != nil {
		t.Fatal(err)
	}
	if root.Provider != ProviderName || root.Kind != "wheel" || root.Source != "/bundle/demo-1.0.0-py3-none-any.whl" {
		t.Fatalf("root = %#v", root)
	}
}

func TestResolveRequestSelectsOptionalComponents(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{
		Components: map[string]blueprint.Component{
			"application": {Type: blueprint.ComponentTypePython, Requirements: []string{"demo-server"}},
			"imap": {
				Type: blueprint.ComponentTypePython, Requirements: []string{"demo-imap"},
				Optional: &blueprint.OptionalComponent{Group: "plugins", Description: "IMAP plugin"},
			},
		},
		Executables: map[string]blueprint.Executable{
			"server": {Component: "application", Binary: "demo-server"},
		},
	}}
	request, err := ResolveRequest(document, Selection{
		OptionalComponents: map[string]bool{"imap": true}, DirectRoots: []string{"debugpy==1.8.0"},
	}, "linux/amd64", "python@sha256:demo")
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{request.Components[0].Name, request.Components[1].Name}; !reflect.DeepEqual(got, []string{"application", "imap"}) {
		t.Fatalf("components = %#v", got)
	}
	if !reflect.DeepEqual(request.DirectRoots, []string{"debugpy==1.8.0"}) {
		t.Fatalf("direct roots = %#v", request.DirectRoots)
	}
}

func TestResolveRequestRejectsInactiveComponentReference(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{
		Components: map[string]blueprint.Component{
			"imap": {
				Type: blueprint.ComponentTypePython, Requirements: []string{"demo-imap"},
				Optional: &blueprint.OptionalComponent{Description: "IMAP plugin"},
			},
		},
		Executables: map[string]blueprint.Executable{"imap": {Component: "imap", Binary: "demo-imap"}},
	}}
	_, err := ResolveRequest(document, Selection{}, "linux/amd64", "python@sha256:demo")
	if err == nil || !strings.Contains(err.Error(), "inactive optional component") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveRequestNormalizesExplicitTranslations(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{
		Components: map[string]blueprint.Component{
			"application": {Type: blueprint.ComponentTypePython, Requirements: []string{"demo-server"}},
		},
		Translations: map[string]blueprint.Translation{
			"workspace": {
				Type: blueprint.ComponentTypePython, Scope: blueprint.TranslationScopeDevelopment,
				Root: "../..", Mappings: map[string]string{"Demo_Server": "server"},
			},
		},
		Executables: map[string]blueprint.Executable{},
	}}
	request, err := ResolveRequest(document, Selection{}, "linux/amd64", "python@sha256:demo")
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Translations[0].Mappings["demo-server"]; got != "server" {
		t.Fatalf("normalized mapping = %q", got)
	}
}

func TestResolveRequestRejectsDuplicateNormalizedTranslation(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{
		Components: map[string]blueprint.Component{
			"application": {Type: blueprint.ComponentTypePython, Requirements: []string{"demo-server"}},
		},
		Translations: map[string]blueprint.Translation{
			"one": {Type: blueprint.ComponentTypePython, Scope: blueprint.TranslationScopeDevelopment, Root: ".", Mappings: map[string]string{"demo_server": "one"}},
			"two": {Type: blueprint.ComponentTypePython, Scope: blueprint.TranslationScopeDevelopment, Root: ".", Mappings: map[string]string{"demo-server": "two"}},
		},
		Executables: map[string]blueprint.Executable{},
	}}
	_, err := ResolveRequest(document, Selection{}, "linux/amd64", "python@sha256:demo")
	if err == nil || !strings.Contains(err.Error(), "both map normalized distribution") {
		t.Fatalf("error = %v", err)
	}
}

type fakeBundleResolver struct {
	resolved ResolvedSet
	err      error
}

func (resolver fakeBundleResolver) ResolvePython(context.Context, providerapi.ResolveRequest) (ResolvedSet, error) {
	return resolver.resolved, resolver.err
}

func TestComponentProviderResolvesExecutableAndOfflineRecipe(t *testing.T) {
	provider := ComponentProvider{Resolver: fakeBundleResolver{resolved: ResolvedSet{
		BaseIdentity: "python@sha256:base",
		Artifacts: []providerapi.Artifact{{
			Identifier: "demo-server", Version: "1.0", Kind: "wheel",
			Path: "demo_server-1.0-py3-none-any.whl", SHA256: strings.Repeat("a", 64),
		}},
		ConsoleScripts: map[string]string{"demo-server": "demo-server"},
	}}}
	bundle, err := provider.Resolve(context.Background(), providerapi.ResolveRequest{
		Platform: "linux/amd64", BaseImage: "python:3.13-slim",
		Components:  []providerapi.Component{{Name: "application", Requirements: []string{"demo-server"}}},
		Executables: []providerapi.ExecutableRequest{{Name: "server", Component: "application", Binary: "demo-server"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := bundle.Executables["server"].ImagePath; got != "/opt/reploy/providers/python/bin/demo-server" {
		t.Fatalf("image path = %q", got)
	}
	materialization, err := provider.Materialize(providerapi.MaterializeRequest{Bundle: bundle})
	if err != nil {
		t.Fatal(err)
	}
	install := materialization.Steps[1].Argv
	joined := strings.Join(install, " ")
	for _, want := range []string{"--no-index", "--no-deps", "/reploy-bundle/demo_server-1.0-py3-none-any.whl"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("install argv %q does not contain %q", joined, want)
		}
	}
}

func TestComponentProviderRejectsMissingConsoleScript(t *testing.T) {
	provider := ComponentProvider{Resolver: fakeBundleResolver{resolved: ResolvedSet{
		BaseIdentity: "python@sha256:base",
		Artifacts: []providerapi.Artifact{{
			Identifier: "demo", Kind: "wheel", Path: "demo.whl", SHA256: strings.Repeat("a", 64),
		}},
		ConsoleScripts: map[string]string{},
	}}}
	_, err := provider.Resolve(context.Background(), providerapi.ResolveRequest{
		Platform: "linux/amd64", BaseImage: "python:3.13-slim",
		Executables: []providerapi.ExecutableRequest{{Name: "server", Component: "application", Binary: "missing"}},
	})
	if err == nil || !strings.Contains(err.Error(), "does not provide console script") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidatePythonVersion(t *testing.T) {
	if err := ValidatePythonVersion("Python 3.13.2\n"); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePythonVersion("Python 2.7.18"); err == nil {
		t.Fatal("expected Python 2 to fail")
	}
}

func TestComponentProviderContractIsDeterministic(t *testing.T) {
	provider := ComponentProvider{}
	prerequisites := provider.Prerequisites(providerapi.ResolveRequest{})
	if len(prerequisites) != 2 || prerequisites[0].Source != providerapi.PrerequisiteBaseImage || prerequisites[0].ProbeArgv[0] != "python" {
		t.Fatalf("prerequisites = %#v", prerequisites)
	}
	bundle := providerapi.Bundle{
		Provider: blueprint.ComponentTypePython, RecipeVersion: RecipeVersion,
		Platform: "linux/amd64", BaseIdentity: "python@sha256:base",
		Artifacts: []providerapi.Artifact{
			{Identifier: "z", Kind: "wheel", Path: "z.whl", SHA256: strings.Repeat("a", 64)},
			{Identifier: "a", Kind: "wheel", Path: "a.whl", SHA256: strings.Repeat("b", 64)},
		},
		Executables: map[string]providerapi.ExecutableOutput{},
	}
	materialization, err := provider.Materialize(providerapi.MaterializeRequest{Bundle: bundle})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(materialization.Steps[1].Argv, " ")
	if strings.Index(joined, "/reploy-bundle/a.whl") > strings.Index(joined, "/reploy-bundle/z.whl") {
		t.Fatalf("wheel order is not deterministic: %s", joined)
	}
	if _, err := provider.Materialize(providerapi.MaterializeRequest{Bundle: providerapi.Bundle{
		Provider: blueprint.ComponentTypePython, RecipeVersion: "python-v2",
	}}); err == nil || !strings.Contains(err.Error(), "unsupported Python recipe") {
		t.Fatalf("recipe error = %v", err)
	}
}
