package deploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/identitymapping"
	"gopkg.in/yaml.v3"
)

func geometryDocumentForTest(t *testing.T, input string) blueprint.Document {
	t.Helper()
	var mapping blueprint.IdentityMappingV1
	if err := yaml.Unmarshal([]byte(input), &mapping); err != nil {
		t.Fatal(err)
	}
	return blueprint.Document{Environment: blueprint.Environment{ID: "demo", Runtime: blueprint.EnvironmentRuntime{IdentityMapping: &mapping}}}
}

func TestResolveMappingGeometryUsesTypedOverrideFirstInput(t *testing.T) {
	document := geometryDocumentForTest(t, `{schema: 1, profile: "???", uids: [{container_start: 0, host_start: "???", count: 1}], gids: [{container_start: 0, host_start: 400000, count: 2}]}`)
	overrides := decodeMappingSidecarV1(t, mappingSidecarV1(`{profile: exact, uids: [{container_start: 5, host_start: 300005, count: 2}, {container_start: 3, host_start: 300003, count: 2}]}`))
	before, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveIdentityMappingGeometryV1(document, overrides.IdentityMappingOverride())
	want := identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{{ContainerStart: 3, HostStart: 300003, Count: 4}}, GIDs: []identitymapping.Interval{{ContainerStart: 0, HostStart: 400000, Count: 2}}}
	if err != nil || got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("numeric operation candidate = %#v / %v", got, err)
	}
	after, err := yaml.Marshal(document)
	if err != nil || string(before) != string(after) {
		t.Fatalf("numeric resolution mutated source authority: %v", err)
	}
	// Converting input does not accept or replace retained assignments.
	retained := want
	retained.UIDs = []identitymapping.Interval{{ContainerStart: 3, HostStart: 500003, Count: 4}}
	if _, err := identitymapping.RetainGeometry(*got, &retained); err == nil {
		t.Fatal("resolved input silently replaced established assignment")
	}
}

func TestResolveMappingGeometryPreservesVariableKindsAndMissingPaths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		kind  string
		text  string
		valid bool
	}{
		{"integer", 9, "integer", "9", true},
		{"integral float", 9.0, "float", "9", false},
		{"numeric string", "9", "string", "9", false},
		{"boolean", true, "bool", "true", false},
		{"missing", "???", "missing", "???", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := geometryDocumentForTest(t, `{schema: 1, profile: exact, uids: [{container_start: "{{ start }}", host_start: 300000, count: 2}], gids: [{container_start: 0, host_start: 400000, count: 2}]}`)
			document.Environment.Vars = map[string]any{"start": tc.value}
			document.Environment.Runtime.IdentityMapping.Variables = map[string]blueprint.MappingScalarV1{"start": {Kind: tc.kind, Value: tc.text}}
			got, err := ResolveIdentityMappingGeometryV1(document, nil)
			if (err == nil) != tc.valid || !tc.valid && got != nil {
				t.Fatalf("incorrect type acceptance: %#v / %v", got, err)
			}
			if tc.valid && got.UIDs[0].ContainerStart != 9 {
				t.Fatalf("integer binding lost: %#v", got)
			}
			if tc.kind == "missing" && !strings.Contains(err.Error(), "uids[0].container_start") {
				t.Fatalf("missing consumer path lost: %v", err)
			}
		})
	}
}

func TestResolveMappingGeometryOrdinaryAndUndeclaredControls(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{ID: "demo"}}
	if got, err := ResolveIdentityMappingGeometryV1(document, nil); err != nil || got != nil {
		t.Fatalf("ordinary input changed: %#v / %v", got, err)
	}
	overrides := decodeMappingSidecarV1(t, mappingSidecarV1(`{profile: exact}`))
	if got, err := ResolveIdentityMappingGeometryV1(document, overrides.IdentityMappingOverride()); err == nil || got != nil {
		t.Fatalf("undeclared input accepted: %#v / %v", got, err)
	}
}
