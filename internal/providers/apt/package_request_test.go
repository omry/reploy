package apt

import (
	"encoding/json"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestCanonicalPackageRequestV1RoundTrip(t *testing.T) {
	request, err := CanonicalPackageRequestV1(blueprint.APTPackageRequest{
		Name: "python3", Version: "3.11.2-1+deb12u1", Exports: map[string]blueprint.ExecutableExport{
			"python": {Executable: "/usr/bin/python3"},
		},
	})
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
	want := `{"schema":"apt-package-request-v1","value":{"exports":[{"executable":"/usr/bin/python3","name":"python"}],"name":"python3","version":"3.11.2-1+deb12u1"}}`
	if string(encoded) != want {
		t.Fatalf("request = %s, want %s", encoded, want)
	}
}

func TestValidateCanonicalPackageRequestV1AfterJSONRoundTrip(t *testing.T) {
	original, err := CanonicalPackageRequestV1(blueprint.APTPackageRequest{
		Name: "python3", Exports: map[string]blueprint.ExecutableExport{"python": {Executable: "/usr/bin/python3"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded providers.CanonicalPackageRequest
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanonicalPackageRequestV1(decoded); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCanonicalPackageRequestV1RejectsMalformedValue(t *testing.T) {
	request := providers.CanonicalPackageRequest{Schema: PackageRequestSchemaV1, Value: canonical.Object{
		"name": "python3", "exports": []any{}, "extra": true,
	}}
	if err := ValidateCanonicalPackageRequestV1(request); err == nil {
		t.Fatal("expected malformed request to fail")
	}
}
