package python

import (
	"strconv"
	"testing"

	providerapi "github.com/omry/reploy/internal/providers"
)

func TestPythonRequiresPythonClaimCoverageRejectsMinorSuccessorOverflowV1(t *testing.T) {
	claim := "3." + strconv.Itoa(int(^uint(0)>>1))
	if _, err := PythonRequiresPythonCoversClaimV1(">=3", claim); err == nil {
		t.Fatalf("coverage accepted unrepresentable minor successor %q", claim)
	}
	if _, err := PythonRequiresPythonIntersectsClaimV1(">=3", claim); err == nil {
		t.Fatalf("intersection accepted unrepresentable minor successor %q", claim)
	}
}

func TestPythonRequiresPythonClaimCoverageV1(t *testing.T) {
	cases := []struct {
		requires string
		claim    string
		want     bool
	}{
		{">=3.14,<3.15", "3.14", true},
		{"==3.14.*", "3.14", true},
		{"==3.*", "3.14", true},
		{"~=3.14", "3.14", true},
		{"~=3.14.0", "3.14", true},
		{"==3.14.*,<3.15", "3.14", true},
		{">=3.14,<3.14.1", "3.14", false},
		{"~=3.14.1", "3.14", false},
		{"==3.13.*", "3.14", false},
		{"==3.15.*", "3.14", false},
		{"~=3.13.0", "3.14", false},
		{"~=3.15.0", "3.14", false},
		{"==3.14.*,>3.14", "3.14", false},
		{"==3.14.*,<=3.14", "3.14", false},
		{"==3.14.*,<3.14.1", "3.14", false},
		{">=3.14,!=3.14.1", "3.14", false},
		{">=3.14,<3.15,!=3.14.1.2", "3.14", true},
		{">=3.14,<3.15,!=3.14.1.2.*", "3.14", true},
		{">=3.14,<3.15,!=3.14.1.0.*", "3.14", false},
		{">=3.14,<3.15,!=3.14.1.0", "3.14", false},
		{">=3.14,<3.15,!=3.14.1.*", "3.14", false},
		{">=3.14,<3.15", "3.14.1", true},
		{">=3.14,<3.15", "3.15.1", false},
		{"==3.14.1.0.*", "3.14.1", true},
		{"!=3.14.1.0.*", "3.14.1", false},
	}
	for _, test := range cases {
		got, err := PythonRequiresPythonCoversClaimV1(test.requires, test.claim)
		if err != nil {
			t.Fatalf("coverage %q/%q: %v", test.requires, test.claim, err)
		}
		if got != test.want {
			t.Errorf("coverage %q/%q = %v, want %v", test.requires, test.claim, got, test.want)
		}
	}
}

func TestPythonRequiresPythonClaimIntersectionUsesCanonicalRuntimeReleasesV1(t *testing.T) {
	cases := []struct {
		requires string
		claim    string
		want     bool
	}{
		{">3.14.0,<3.14.1", "3.14", false},
		{">=3.14,<3.14.1,!=3.14", "3.14", false},
		{"===3.14", "3.14", false},
		{"===3.14.0", "3.14", true},
		{">=3.14,<3.15,!=3.14.1.2", "3.14", true},
		{">=3.14,<3.15,!=3.14.1.2.*", "3.14", true},
		{"==3.14.1.0.*", "3.14", true},
		{">=3.14,<3.14.1", "3.14", true},
	}
	for _, test := range cases {
		got, err := PythonRequiresPythonIntersectsClaimV1(test.requires, test.claim)
		if err != nil {
			t.Fatalf("intersection %q/%q: %v", test.requires, test.claim, err)
		}
		if got != test.want {
			t.Errorf("intersection %q/%q = %v, want %v", test.requires, test.claim, got, test.want)
		}
	}
}

func TestCompatiblePythonReleaseEqualitiesRemainAConjunctionV1(t *testing.T) {
	for _, constraint := range []string{
		"==3.14,==3.14.*",
		"==3.14.*,==3.14",
		"==3.14.*,<=3.14",
		"==3.14.*,==3.14.0.*",
	} {
		matches, err := InterpreterVersionSatisfies(constraint, "3.14.0")
		if err != nil {
			t.Errorf("constraint %q: %v", constraint, err)
			continue
		}
		if !matches {
			t.Errorf("constraint %q rejected 3.14.0", constraint)
		}
	}
	for _, constraint := range []string{
		"==3.14.1,==3.14.1.0.*",
		"==3.14.1.0.*,==3.14.1",
	} {
		if matches, supported := versionSpecifiersAllowVersion(constraint, "3.14.1"); !supported || !matches {
			t.Errorf("constraint %q rejected its zero-padded three-component runtime", constraint)
		}
	}
	if matches, supported := versionSpecifiersAllowVersion("==3.14.*,==3.14.0.*", "3.14.1"); !supported || matches {
		t.Error("conjoined wildcard prefixes admitted a release outside the narrower literal prefix")
	}
}

func TestPythonReleaseWildcardPrefixesUseZeroPaddingV1(t *testing.T) {
	for _, test := range []struct {
		constraint string
		version    string
		want       bool
	}{
		{"==3.14.1.0.*", "3.14.1", true},
		{"==3.14.1.0.*", "3.14.2", false},
		{"!=3.14.1.0.*", "3.14.1", false},
		{"!=3.14.1.0.*", "3.14.2", true},
		{"==3.14.1.2.*", "3.14.1", false},
		{"!=3.14.1.2.*", "3.14.1", true},
	} {
		got, err := InterpreterVersionSatisfies(test.constraint, test.version)
		if err != nil {
			t.Fatalf("constraint %q/version %q: %v", test.constraint, test.version, err)
		}
		if got != test.want {
			t.Errorf("constraint %q/version %q = %v, want %v", test.constraint, test.version, got, test.want)
		}
	}
}

func TestPythonReleaseConstraintsRejectLocalVersionSpecifiersV1(t *testing.T) {
	for _, constraint := range []string{"==3.14.1+vendor", "!=3.14.1+vendor", "===3.14.1+vendor"} {
		if matches, supported := versionSpecifiersAllowVersion(constraint, "3.14.1"); supported || matches {
			t.Errorf("constraint %q was admitted to the normalized runtime subset", constraint)
		}
		if _, err := InterpreterVersionSatisfies(constraint, "3.14.1"); err == nil {
			t.Errorf("runtime constraint %q did not fail closed", constraint)
		}
		if _, err := PythonRequiresPythonCoversClaimV1(constraint, "3.14.1"); err == nil {
			t.Errorf("coverage constraint %q did not fail closed", constraint)
		}
		if _, err := PythonRequiresPythonIntersectsClaimV1(constraint, "3.14.1"); err == nil {
			t.Errorf("intersection constraint %q did not fail closed", constraint)
		}
	}
}

func TestStrictPythonReleaseConstraintRemainsAConjunctionV1(t *testing.T) {
	for _, specifier := range []string{"===3.14.1,<3.14", ">3.14.1,===3.14.1", "===not-a-release"} {
		if matches, valid := versionSpecifiersAllowVersion(specifier, "3.14.1"); valid && matches {
			t.Errorf("constraint %q admitted 3.14.1", specifier)
		}
	}
}

func TestStrictPythonPackageRequirementsPreserveRawEqualityV1(t *testing.T) {
	for _, test := range []struct {
		version string
		want    bool
	}{
		{version: "1.0+vendor", want: true},
		{version: "1.0+other", want: false},
		{version: "1.0", want: false},
	} {
		got, checked := requirementAllowsVersion("demo===1.0+vendor", test.version)
		if !checked || got != test.want {
			t.Errorf("requirementAllowsVersion(strict local, %q) = (%v, %v), want (%v, true)", test.version, got, checked, test.want)
		}
	}

	packageRequest, err := CanonicalPackageRequestV1("demo===1.0+vendor")
	if err != nil {
		t.Fatal(err)
	}
	request := PythonProviderRequestV1{
		Component:    "application",
		Requirements: []providerapi.CanonicalPackageRequest{packageRequest},
	}
	if err := validateCanonicalRequestedDistributions(request, map[string]inspectedWheel{
		"demo": {Distribution: "demo", Version: "1.0+other"},
	}); err == nil {
		t.Fatal("prepared-bundle validation accepted a wheel that violates strict local-version equality")
	}
}

func TestContradictoryPythonReleaseConstraintsRemainCheckedV1(t *testing.T) {
	for _, test := range []struct {
		requirement string
		specifiers  string
	}{
		{requirement: "demo>=2,<1", specifiers: ">=2,<1"},
		{requirement: "demo==1,==2", specifiers: "==1,==2"},
	} {
		requirement := test.requirement
		if satisfied, checked := requirementAllowsVersion(requirement, "1.5"); !checked || satisfied {
			t.Errorf("requirementAllowsVersion(%q, %q) = (%v, %v), want (false, true)", requirement, "1.5", satisfied, checked)
		}
		if covered, err := PythonRequiresPythonCoversClaimV1(test.specifiers, "1.5"); err != nil || covered {
			t.Errorf("PythonRequiresPythonCoversClaimV1(%q, %q) = (%v, %v), want (false, nil)", test.specifiers, "1.5", covered, err)
		}
		if intersects, err := PythonRequiresPythonIntersectsClaimV1(test.specifiers, "1.5"); err != nil || intersects {
			t.Errorf("PythonRequiresPythonIntersectsClaimV1(%q, %q) = (%v, %v), want (false, nil)", test.specifiers, "1.5", intersects, err)
		}

		packageRequest, err := CanonicalPackageRequestV1(requirement)
		if err != nil {
			t.Fatalf("canonicalize %q: %v", requirement, err)
		}
		request := PythonProviderRequestV1{
			Component:    "application",
			Requirements: []providerapi.CanonicalPackageRequest{packageRequest},
		}
		artifacts := map[string]inspectedWheel{
			"demo": {Distribution: "demo", Version: "1.5"},
		}
		if err := validateCanonicalRequestedDistributions(request, artifacts); err == nil {
			t.Errorf("prepared-bundle validation accepted contradictory requirement %q", requirement)
		}
	}
}
