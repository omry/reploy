package python

import (
	"fmt"
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

func TestPackageRootDistributionNameV1(t *testing.T) {
	for _, accepted := range []struct {
		requirement string
		want        string
	}{
		{requirement: "demo", want: "demo"},
		{requirement: "demo>=1.2,<2", want: "demo"},
		{requirement: "demo==1.2.3", want: "demo"},
		{requirement: "d", want: "d"},
	} {
		name, err := PackageRootDistributionNameV1(accepted.requirement)
		if err != nil {
			t.Errorf("PackageRootDistributionNameV1(%q): %v", accepted.requirement, err)
		}
		if name != accepted.want {
			t.Errorf("PackageRootDistributionNameV1(%q) = %q, want %q", accepted.requirement, name, accepted.want)
		}
	}
	for _, testCase := range []struct {
		name        string
		requirement string
	}{
		{name: "whitespace", requirement: "demo ???"},
		{name: "trailing dash", requirement: "demo-"},
		{name: "trailing dots", requirement: "demo.."},
		{name: "invalid extra", requirement: "demo[http-]"},
		{name: "unterminated extras", requirement: "demo["},
		{name: "empty extras", requirement: "demo[]"},
		{name: "extras", requirement: "demo[http]"},
		{name: "extras with specifier", requirement: "demo[http]>=1.2,<2"},
		{name: "multiple extras", requirement: "demo[a,b,c]"},
		{name: "unsorted extras", requirement: "demo[b,a]"},
		{name: "duplicate extras", requirement: "demo[a,a]"},
		{name: "direct URL", requirement: "demo @ https://example.invalid/demo.whl"},
		{name: "environment marker", requirement: "demo; python_version > '3'"},
		{name: "empty", requirement: ""},
	} {
		if _, err := PackageRootDistributionNameV1(testCase.requirement); err == nil {
			t.Errorf("%s: PackageRootDistributionNameV1(%q) succeeded", testCase.name, testCase.requirement)
		}
	}
}

func TestPackageRootDistributionNameV1Limits(t *testing.T) {
	longName := strings.Repeat("a", 4096)
	if name, err := PackageRootDistributionNameV1(longName); err != nil || name != longName {
		t.Errorf("long distribution name = %q, %v", name, err)
	}
	manyExtras := make([]string, 128)
	for index := range manyExtras {
		manyExtras[index] = fmt.Sprintf("e%04d", index)
	}
	requirement := "demo[" + strings.Join(manyExtras, ",") + "]"
	if _, err := PackageRootDistributionNameV1(requirement); err == nil {
		t.Error("many extras succeeded")
	}
	longSpecifier := "demo" + strings.Repeat(">=1,", 64) + ">=1"
	if _, err := PackageRootDistributionNameV1(longSpecifier); err != nil {
		t.Errorf("long specifier set = %v", err)
	}
}

func TestPackageRootDistributionNameV1NormalizesIdentically(t *testing.T) {
	for _, requirement := range []string{"Demo", "DEMO", "demo", "De_mo", "De-mo", "de.mo"} {
		name, err := PackageRootDistributionNameV1(requirement)
		if err != nil {
			t.Fatalf("PackageRootDistributionNameV1(%q): %v", requirement, err)
		}
		if name != NormalizeDistributionName(requirement) {
			t.Errorf("PackageRootDistributionNameV1(%q) = %q, want %q", requirement, name, NormalizeDistributionName(requirement))
		}
	}
}

func TestPackageRootRequirementsCompatibleV1(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		requirements []string
		want         bool
	}{
		{name: "compatible bounds", requirements: []string{"demo>=1", "demo<3"}, want: true},
		{name: "compatible prefix and bound", requirements: []string{"demo==1.*", "demo>=1.5"}, want: true},
		{name: "incompatible bounds", requirements: []string{"demo>=3", "demo<3"}, want: false},
		{name: "incompatible exact pins", requirements: []string{"demo==1", "demo==2"}, want: false},
		{name: "excluded exact pin", requirements: []string{"demo==1", "demo!=1"}, want: false},
		{name: "exact pin in excluded prefix", requirements: []string{"demo==1.2", "demo!=1.*"}, want: false},
		{name: "excluded prefix", requirements: []string{"demo>=1", "demo<2", "demo!=1.*"}, want: false},
		{name: "tiled excluded prefixes", requirements: []string{"demo>=1", "demo<3", "demo!=1.*", "demo!=2.*"}, want: false},
		{name: "unsupported form stays resolver authority", requirements: []string{"demo==1rc1", "demo==2rc1"}, want: true},
		{name: "local labels stay resolver authority", requirements: []string{"demo==1+abc", "demo!=1+def"}, want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			compatible, err := PackageRootRequirementsCompatibleV1(testCase.requirements)
			if err != nil {
				t.Fatal(err)
			}
			if compatible != testCase.want {
				t.Errorf("compatible = %t, want %t", compatible, testCase.want)
			}
		})
	}
	if _, err := PackageRootRequirementsCompatibleV1([]string{"demo>=1", "other<3"}); err == nil {
		t.Fatal("different distributions succeeded")
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
