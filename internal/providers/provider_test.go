package providers

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestNormalizeResolveRequestIsDeterministic(t *testing.T) {
	request := NormalizeResolveRequest(ResolveRequest{
		Components:   []Component{{Name: "z"}, {Name: "a"}},
		Translations: []Translation{{Name: "two"}, {Name: "one"}},
		DirectRoots:  []string{"z", "a"},
		Executables:  []ExecutableRequest{{Name: "server"}, {Name: "check"}},
	})
	if got := []string{request.Components[0].Name, request.Components[1].Name}; !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("components = %#v", got)
	}
	if !reflect.DeepEqual(request.DirectRoots, []string{"a", "z"}) {
		t.Fatalf("roots = %#v", request.DirectRoots)
	}
	if got := []string{request.Executables[0].Name, request.Executables[1].Name}; !reflect.DeepEqual(got, []string{"check", "server"}) {
		t.Fatalf("executables = %#v", got)
	}
}

func TestValidateBundleRequiresClosedChecksummedArtifacts(t *testing.T) {
	valid := Bundle{
		Provider: blueprint.ComponentTypePython, RecipeVersion: "python-v1",
		Platform: "linux/amd64", BaseIdentity: "python@sha256:base",
		Artifacts: []Artifact{{Identifier: "demo", Kind: "wheel", Path: "demo.whl", SHA256: strings.Repeat("a", 64)}},
		Executables: map[string]ExecutableOutput{
			"demo": {Component: "application", Binary: "demo", ImagePath: "/opt/demo"},
		},
	}
	if err := ValidateBundle(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Bundle)
	}{
		{name: "missing digest", mutate: func(bundle *Bundle) { bundle.Artifacts[0].SHA256 = "" }},
		{name: "escaping path", mutate: func(bundle *Bundle) { bundle.Artifacts[0].Path = "../demo.whl" }},
		{name: "relative executable", mutate: func(bundle *Bundle) {
			output := bundle.Executables["demo"]
			output.ImagePath = "bin/demo"
			bundle.Executables["demo"] = output
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := valid
			candidate.Artifacts = append([]Artifact(nil), valid.Artifacts...)
			candidate.Executables = map[string]ExecutableOutput{"demo": valid.Executables["demo"]}
			tt.mutate(&candidate)
			if err := ValidateBundle(candidate); err == nil {
				t.Fatal("expected invalid bundle")
			}
		})
	}
}
