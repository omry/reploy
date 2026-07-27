package registry

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestNormalizePackageOverrideV1(t *testing.T) {
	normalized, err := NormalizePackageOverrideV1("python", "Demo_Pkg", deploy.PackageOverrideChoiceV1{Path: "../demo"})
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "demo-pkg" {
		t.Fatalf("normalized = %q", normalized)
	}
	for _, provider := range []string{"apt", "go"} {
		if _, err := NormalizePackageOverrideV1(provider, "demo", deploy.PackageOverrideChoiceV1{Version: "1"}); err == nil || !strings.Contains(err.Error(), "not support") {
			t.Fatalf("%s error = %v", provider, err)
		}
	}
	for _, invalid := range []string{"1.0; python_version < '0'", "1.0 # ignored", "not a version"} {
		if _, err := NormalizePackageOverrideV1(
			"python", "demo", deploy.PackageOverrideChoiceV1{Version: invalid},
		); err == nil || !strings.Contains(err.Error(), "PEP 440") {
			t.Fatalf("invalid Python version %q error = %v", invalid, err)
		}
	}
}
