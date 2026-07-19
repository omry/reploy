package apt

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestResolveWellKnownPythonMapping(t *testing.T) {
	tests := []struct {
		name     string
		request  blueprint.APTPackageRequest
		path     string
		explicit bool
		version  string
	}{
		{
			name:    "unpinned",
			request: blueprint.APTPackageRequest{Name: "python3", Exports: map[string]blueprint.ExecutableExport{}},
			path:    "/usr/bin/python3",
		},
		{
			name: "pinned",
			request: blueprint.APTPackageRequest{
				Name: "python3", Version: "3.11.2-1+deb12u1", Exports: map[string]blueprint.ExecutableExport{},
			},
			path: "/usr/bin/python3", version: "3.11.2-1+deb12u1",
		},
		{
			name: "explicit replacement",
			request: blueprint.APTPackageRequest{
				Name: "python3", Exports: map[string]blueprint.ExecutableExport{
					"python": {Executable: "/opt/python/bin/python3"},
				},
			},
			path: "/opt/python/bin/python3", explicit: true,
		},
		{
			name: "unrelated explicit export",
			request: blueprint.APTPackageRequest{
				Name: "python3", Exports: map[string]blueprint.ExecutableExport{
					"idle": {Executable: "/usr/bin/idle3"},
				},
			},
			path: "/usr/bin/python3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapping, found, err := ResolveWellKnownToolV1(tt.request)
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatal("python3 mapping not found")
			}
			want := WellKnownToolMappingV1{
				Schema: WellKnownToolSchemaV1, Profile: WellKnownToolsProfileV1,
				PackageName: "python3", PackageVersion: tt.version,
				OutputName: "python", CandidatePath: tt.path, ConsumerKind: "python",
				ExplicitReplacement: tt.explicit,
			}
			if !reflect.DeepEqual(mapping, want) {
				t.Fatalf("mapping = %#v, want %#v", mapping, want)
			}
		})
	}
}

func TestResolveWellKnownToolV1HasNoGenericRegistry(t *testing.T) {
	request := blueprint.APTPackageRequest{Name: "curl", Exports: map[string]blueprint.ExecutableExport{}}
	mapping, found, err := ResolveWellKnownToolV1(request)
	if err != nil {
		t.Fatal(err)
	}
	if found || mapping != (WellKnownToolMappingV1{}) {
		t.Fatalf("unexpected mapping = %#v, found = %v", mapping, found)
	}
}

func TestResolveWellKnownToolV1RejectsInvalidRequest(t *testing.T) {
	request := blueprint.APTPackageRequest{Name: "python3", Exports: map[string]blueprint.ExecutableExport{
		"python": {Executable: "usr/bin/python3"},
	}}
	if _, _, err := ResolveWellKnownToolV1(request); err == nil {
		t.Fatal("expected invalid request to fail")
	}
}
