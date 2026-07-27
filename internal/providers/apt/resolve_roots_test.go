package apt

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestResolveRootOperandsV1SortsDeduplicatesAndIgnoresExports(t *testing.T) {
	request, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{
		{Component: "tools", Packages: []blueprint.APTPackageRequest{
			{Name: "zlib1g", Exports: map[string]blueprint.ExecutableExport{}},
			{Name: "curl", Version: "7.88.1-10+deb12u12", Exports: map[string]blueprint.ExecutableExport{"curl": {Executable: "/usr/bin/curl"}}},
		}},
		{Component: "system", Packages: []blueprint.APTPackageRequest{
			{Name: "zlib1g", Exports: map[string]blueprint.ExecutableExport{}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	operands, err := ResolveRootOperandsV1(request)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"curl=7.88.1-10+deb12u12", "zlib1g"}
	if !reflect.DeepEqual(operands, want) {
		t.Fatalf("operands = %#v", operands)
	}
}

func TestResolveRootOperandsV1RejectsNonCanonicalRequest(t *testing.T) {
	request := providers.CanonicalProviderRequest{
		Schema: ProviderRequestSchemaV1, Provider: blueprint.ComponentTypeAPT,
		Value: canonical.Object{"components": []any{}},
	}
	if _, err := ResolveRootOperandsV1(request); err == nil {
		t.Fatal("invalid request was accepted")
	}
}
