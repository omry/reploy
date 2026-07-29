package deploy

import "testing"

func TestCurrentBootSessionIDV1IsStableAndValid(t *testing.T) {
	first, err := CurrentBootSessionIDV1()
	if err != nil {
		t.Fatal(err)
	}
	second, err := CurrentBootSessionIDV1()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("boot-session identities = %q and %q", first, second)
	}
	if err := validateBootSessionIDV1(first); err != nil {
		t.Fatalf("current boot-session identity is invalid: %v", err)
	}
}
