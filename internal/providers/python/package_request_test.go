package python

import (
	"testing"

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
