package deploy

import "testing"

func TestLoadPackUsesResolvedEnvironmentModel(t *testing.T) {
	ref, err := ParsePackRef("file:../../examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Environment == nil || pack.Environment.Environment.ID != "omegaconf-inspector" {
		t.Fatalf("environment = %#v", pack.Environment)
	}
	if pack.App.Provider.Identifier != "omegaconf-inspector" || len(pack.Docker.Commands) == 0 {
		t.Fatalf("compatibility projection = %#v", pack)
	}
}
