package providers

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestCanonicalBaseProviderRequestAndNodeOutputsAreSorted(t *testing.T) {
	request, err := CanonicalBaseProviderRequestV1(BaseProviderRequestV1{
		Image: "debian:13",
		Exports: map[string]blueprint.BaseExecutableExport{
			"zsh":    {Executable: "/bin/zsh"},
			"python": {Executable: "/usr/bin/python3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanonicalBaseProviderRequestV1(request); err != nil {
		t.Fatal(err)
	}
	node, err := BaseNodeSpec(request)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{node.OutputDeclarations[0].Name, node.OutputDeclarations[1].Name}
	if !reflect.DeepEqual(names, []string{"python", "zsh"}) {
		t.Fatalf("output names = %#v", names)
	}
	if node.OutputDeclarations[0].Provenance.Schema != BaseExportSchemaV1 {
		t.Fatalf("output provenance = %#v", node.OutputDeclarations[0].Provenance)
	}
}

func TestCanonicalBaseProviderRequestRejectsMalformedValues(t *testing.T) {
	for name, request := range map[string]BaseProviderRequestV1{
		"empty image":     {Exports: map[string]blueprint.BaseExecutableExport{}},
		"relative export": {Image: "debian:13", Exports: map[string]blueprint.BaseExecutableExport{"python": {Executable: "usr/bin/python3"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalBaseProviderRequestV1(request); err == nil {
				t.Fatal("malformed base provider request was accepted")
			}
		})
	}
}
