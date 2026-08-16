package python

import "testing"

func TestPackageOverrideVersionUsesPEP440ValidationAndOrdering(t *testing.T) {
	for _, value := range []string{"1.0", "2.4.0.dev3", "1!2.0.post1+local"} {
		if err := ValidatePackageVersionV1(value); err != nil {
			t.Fatalf("valid version %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "1.0; python_version < '0'", "1.0 # ignored", "not a version"} {
		if err := ValidatePackageVersionV1(value); err == nil {
			t.Fatalf("invalid version %q was accepted", value)
		}
	}
	compared, err := ComparePackageVersionsV1("2.4.0", "2.4.0.dev3")
	if err != nil {
		t.Fatal(err)
	}
	if compared <= 0 {
		t.Fatalf("final release comparison = %d, want newer than development release", compared)
	}
}

func TestValidateInterpreterVersionV1(t *testing.T) {
	for _, value := range []string{"3.11", "3.13.2"} {
		if err := ValidateInterpreterVersionV1(value); err != nil {
			t.Errorf("ValidateInterpreterVersionV1(%q): %v", value, err)
		}
	}
	for _, value := range []string{"banana", "3..11", "03.11", "3"} {
		if err := ValidateInterpreterVersionV1(value); err == nil {
			t.Errorf("ValidateInterpreterVersionV1(%q) succeeded", value)
		}
	}
}
