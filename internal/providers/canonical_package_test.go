package providers

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestCanonicalPackageRequestBytes(t *testing.T) {
	request := CanonicalPackageRequest{
		Schema: "apt-package-request-v1",
		Value:  canonical.Object{"version": "1.0", "name": "demo"},
	}
	encoded, err := CanonicalPackageRequestBytes(request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"schema":"apt-package-request-v1","value":{"name":"demo","version":"1.0"}}`; got != want {
		t.Fatalf("canonical package = %s, want %s", got, want)
	}
}

func TestCanonicalPackageRequestBytesRejectsMissingValue(t *testing.T) {
	_, err := CanonicalPackageRequestBytes(CanonicalPackageRequest{Schema: "apt-package-request-v1"})
	if err == nil || !strings.Contains(err.Error(), "must be an object") {
		t.Fatalf("error = %v", err)
	}
}
