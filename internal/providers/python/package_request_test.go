package python

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestCanonicalPackageRequestV1(t *testing.T) {
	request, err := CanonicalPackageRequestV1("  demo[http]>=1.2  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanonicalPackageRequestV1(request); err != nil {
		t.Fatal(err)
	}
	encoded, err := providers.CanonicalPackageRequestBytes(request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"schema":"python-package-request-v1","value":{"requirement":"demo[http]>=1.2"}}`; got != want {
		t.Fatalf("request = %s, want %s", got, want)
	}
}

func TestValidateCanonicalPackageRequestV1RejectsMalformedValue(t *testing.T) {
	request := providers.CanonicalPackageRequest{Schema: PackageRequestSchemaV1, Value: canonical.Object{"requirement": " demo "}}
	if err := ValidateCanonicalPackageRequestV1(request); err == nil {
		t.Fatal("expected noncanonical request to fail")
	}
}

func TestCanonicalPackageRequestV1RejectsInvalidUTF8(t *testing.T) {
	if _, err := CanonicalPackageRequestV1(string([]byte{0xff})); err == nil {
		t.Fatal("expected invalid UTF-8 requirement to fail")
	}
}

func TestCanonicalPackageRequestV1RejectsPackageManagerOptions(t *testing.T) {
	for _, requirement := range []string{"--no-deps", " --index-url=https://example.invalid/simple ", "-r requirements.txt"} {
		if _, err := CanonicalPackageRequestV1(requirement); err == nil {
			t.Fatalf("CanonicalPackageRequestV1(%q) succeeded", requirement)
		}
		request := providers.CanonicalPackageRequest{
			Schema: PackageRequestSchemaV1,
			Value:  canonical.Object{"requirement": requirement},
		}
		if err := ValidateCanonicalPackageRequestV1(request); err == nil {
			t.Fatalf("ValidateCanonicalPackageRequestV1(%q) succeeded", requirement)
		}
	}
}

func TestValidatePackageRootRequirementV1(t *testing.T) {
	for _, requirement := range []string{"demo", "demo[http]>=1.2,<2", "demo==1.2.3"} {
		if err := ValidatePackageRootRequirementV1(requirement); err != nil {
			t.Errorf("ValidatePackageRootRequirementV1(%q): %v", requirement, err)
		}
		if name, err := PackageRootDistributionNameV1(requirement); err != nil || name != "demo" {
			t.Errorf("PackageRootDistributionNameV1(%q) = %q, %v", requirement, name, err)
		}
	}
	for _, requirement := range []string{"demo ???", "demo-", "demo..", "demo[http-]", "demo[", "demo @ https://example.invalid/demo.whl", "demo; python_version > '3'"} {
		if err := ValidatePackageRootRequirementV1(requirement); err == nil {
			t.Errorf("ValidatePackageRootRequirementV1(%q) succeeded", requirement)
		}
	}
}

func TestProviderRequestDistributionsV1ReturnsSortedDirectRoots(t *testing.T) {
	zeta, err := CanonicalPackageRequestV1("Zeta[extra]>=1")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := CanonicalPackageRequestV1("alpha_pkg==2")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application",
		Interpreter: blueprint.CommandRequirement{
			Command: "python",
		},
		Requirements: []providers.CanonicalPackageRequest{zeta, alpha, alpha},
		Overrides:    []PythonPackageOverrideV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	distributions, err := ProviderRequestDistributionsV1(request)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(distributions, ","); got != "alpha-pkg,zeta" {
		t.Fatalf("distributions = %q", got)
	}
}

func TestFilterProviderRequestOverridesV1KeepsOnlyClosureDistributions(t *testing.T) {
	requirement, err := CanonicalPackageRequestV1("demo")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component:    "application",
		Interpreter:  blueprint.CommandRequirement{Command: "python", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
		Overrides: []PythonPackageOverrideV1{
			{Distribution: "demo", Kind: "version", Version: "2"},
			{Distribution: "unused", Kind: "local"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := FilterProviderRequestOverridesV1(request, []string{"demo", "dependency"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCanonicalProviderRequestV1(filtered)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Overrides, []PythonPackageOverrideV1{{
		Distribution: "demo", Kind: "version", Version: "2",
	}}) {
		t.Fatalf("filtered overrides = %#v", decoded.Overrides)
	}
}
