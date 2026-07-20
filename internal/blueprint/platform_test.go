package blueprint

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestParsePlatform(t *testing.T) {
	for _, test := range []struct {
		input string
		want  Platform
	}{
		{input: "linux/amd64", want: Platform{OS: "linux", Architecture: "amd64", Canonical: "linux/amd64"}},
		{input: "linux/arm/v7", want: Platform{OS: "linux", Architecture: "arm", Variant: "v7", Canonical: "linux/arm/v7"}},
		{input: "custom-os/arch_1/v1.2", want: Platform{OS: "custom-os", Architecture: "arch_1", Variant: "v1.2", Canonical: "custom-os/arch_1/v1.2"}},
	} {
		platform, err := ParsePlatform(test.input)
		if err != nil {
			t.Fatalf("ParsePlatform(%q): %v", test.input, err)
		}
		if platform != test.want {
			t.Fatalf("ParsePlatform(%q) = %#v, want %#v", test.input, platform, test.want)
		}
	}
}

func TestParsePlatformRejectsNoncanonicalValues(t *testing.T) {
	for _, value := range []string{
		"", "linux", "linux/amd64/", "/amd64", "linux//v7",
		"linux/amd64/extra/value", "Linux/amd64", "linux/AMD64",
		" linux/amd64", "linux/amd64 ", "linux/@amd64", "linux/-amd64",
	} {
		if _, err := ParsePlatform(value); err == nil {
			t.Fatalf("ParsePlatform(%q) succeeded", value)
		}
	}
}

func TestPlatformValidateRejectsCanonicalDisagreement(t *testing.T) {
	platform := Platform{OS: "linux", Architecture: "amd64", Canonical: "linux/arm64"}
	if err := platform.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPlatformCanonicalEncodingUsesStableWireNames(t *testing.T) {
	platform, err := ParsePlatform("linux/arm/v7")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := canonical.Marshal(platform)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"architecture":"arm","canonical":"linux/arm/v7","os":"linux","variant":"v7"}`
	if string(encoded) != want {
		t.Fatalf("platform = %s, want %s", encoded, want)
	}
}

func TestParseCompatibilityRequiresUniqueSortedPlatforms(t *testing.T) {
	compatibility, err := ParseCompatibility([]string{"linux/arm/v7", "linux/amd64", "linux/arm64"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Platform{
		{OS: "linux", Architecture: "amd64", Canonical: "linux/amd64"},
		{OS: "linux", Architecture: "arm", Variant: "v7", Canonical: "linux/arm/v7"},
		{OS: "linux", Architecture: "arm64", Canonical: "linux/arm64"},
	}
	if !reflect.DeepEqual(compatibility.Platforms, want) {
		t.Fatalf("platforms = %#v, want %#v", compatibility.Platforms, want)
	}
	if _, err := ParseCompatibility(nil); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty compatibility error = %v", err)
	}
	if _, err := ParseCompatibility([]string{"linux/amd64", "linux/amd64"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate compatibility error = %v", err)
	}
}

func TestValidateSelectedPlatformUsesDeclaredCompatibility(t *testing.T) {
	compatibility, err := ParseCompatibility([]string{"linux/arm/v7", "linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	document := Document{Blueprint: Metadata{Compatibility: compatibility}}
	for _, value := range []string{"linux/amd64", "linux/arm/v7"} {
		selected, err := ParsePlatform(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateSelectedPlatform(document, selected); err != nil {
			t.Fatalf("%s: %v", value, err)
		}
	}
	undeclared, err := ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSelectedPlatform(document, undeclared); err == nil {
		t.Fatal("undeclared platform accepted")
	}
}

func TestValidateSelectedPlatformAllowsConcreteDeclaredArchitectureVariant(t *testing.T) {
	compatibility, err := ParseCompatibility([]string{"linux/arm"})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ParsePlatform("linux/arm/v7")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSelectedPlatform(Document{Blueprint: Metadata{Compatibility: compatibility}}, selected); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSelectedPlatformRejectsMalformedCompatibilitySet(t *testing.T) {
	selected, err := ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, platforms := range [][]Platform{
		nil,
		{selected, selected},
	} {
		document := Document{Blueprint: Metadata{Compatibility: Compatibility{Platforms: platforms}}}
		if err := ValidateSelectedPlatform(document, selected); err == nil {
			t.Fatalf("platforms unexpectedly accepted: %#v", platforms)
		}
	}
}
