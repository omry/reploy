package deploy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestParseQualifiedOptionGroups(t *testing.T) {
	options, err := ParseQualifiedOptionGroups([]string{"system/git,curl", "app/debug", "system/git"})
	if err != nil {
		t.Fatal(err)
	}
	want := []QualifiedOption{
		{Component: "app", Option: "debug"},
		{Component: "system", Option: "curl"},
		{Component: "system", Option: "git"},
	}
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("options = %#v, want %#v", options, want)
	}
}

func TestParseQualifiedOptionGroupsRejectsInvalidGrammar(t *testing.T) {
	for _, arguments := range [][]string{
		nil,
		{"app"},
		{"app/"},
		{"app/debug/extra"},
		{"app/debug,"},
		{"app/debug option"},
		{"App/debug"},
	} {
		if _, err := ParseQualifiedOptionGroups(arguments); err == nil {
			t.Fatalf("ParseQualifiedOptionGroups(%#v) succeeded", arguments)
		}
	}
}

func testPackageParser(componentType blueprint.ComponentType, requirement string) (providers.CanonicalPackageRequest, error) {
	if strings.TrimSpace(requirement) == "" {
		return providers.CanonicalPackageRequest{}, fmt.Errorf("empty requirement")
	}
	schema := "python-package-request-v1"
	if componentType == blueprint.ComponentTypeAPT {
		schema = "apt-package-request-v1"
	}
	return providers.CanonicalPackageRequest{Schema: schema, Value: canonical.Object{"requirement": strings.TrimSpace(requirement)}}, nil
}

func TestParseDirectPackageRequestsTargetsSortsAndDeduplicates(t *testing.T) {
	requests, err := ParseDirectPackageRequests(overlayTestDocument(), "app", []string{"z==1", "a==1", "z==1"}, testPackageParser)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0].Component != "app" || requests[0].Package.Value["requirement"] != "a==1" || requests[1].Package.Value["requirement"] != "z==1" {
		t.Fatalf("requests = %#v", requests)
	}
	for _, test := range []struct {
		component    string
		requirements []string
		want         string
	}{
		{component: "missing", requirements: []string{"demo"}, want: "does not exist"},
		{component: "base", requirements: []string{"demo"}, want: "does not support"},
		{component: "app", want: "at least one"},
	} {
		if _, err := ParseDirectPackageRequests(overlayTestDocument(), test.component, test.requirements, testPackageParser); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("component %q error = %v, want %q", test.component, err, test.want)
		}
	}
}

func TestOverlayOptionMutationsAreAllOrNothing(t *testing.T) {
	overlay := EmptyRequestOverlayV1()
	overlay.SelectedOptions = []QualifiedOption{{Component: "app", Option: "debug"}, {Component: "system", Option: "git"}}
	before := append([]QualifiedOption(nil), overlay.SelectedOptions...)
	if _, err := RemoveOverlayOptions(overlay, []QualifiedOption{{Component: "app", Option: "debug"}, {Component: "system", Option: "missing"}}); err == nil {
		t.Fatal("expected missing option removal to fail")
	}
	if !reflect.DeepEqual(overlay.SelectedOptions, before) {
		t.Fatal("failed removal mutated input")
	}
	updated, err := RemoveOverlayOptions(overlay, []QualifiedOption{{Component: "app", Option: "debug"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.SelectedOptions, []QualifiedOption{{Component: "system", Option: "git"}}) {
		t.Fatalf("selected options = %#v", updated.SelectedOptions)
	}
}

func TestOverlayPackageMutationsRemoveExactCanonicalRequest(t *testing.T) {
	requests, err := ParseDirectPackageRequests(overlayTestDocument(), "app", []string{"demo==1", "debug==1"}, testPackageParser)
	if err != nil {
		t.Fatal(err)
	}
	overlay := AddOverlayPackages(EmptyRequestOverlayV1(), requests)
	missing, err := ParseDirectPackageRequests(overlayTestDocument(), "app", []string{"demo==2"}, testPackageParser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveOverlayPackages(overlay, append(requests[:1], missing...)); err == nil {
		t.Fatal("expected exact multi-remove to fail")
	}
	if len(overlay.DirectPackages) != 2 {
		t.Fatal("failed package removal mutated input")
	}
	updated, err := RemoveOverlayPackages(overlay, requests[:1])
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.DirectPackages) != 1 {
		t.Fatalf("direct packages = %#v", updated.DirectPackages)
	}
}
