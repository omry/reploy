package blueprint

import (
	"strings"
	"testing"
)

func TestResolveExtendsCopiesEnvironmentObjects(t *testing.T) {
	source, err := Decode([]byte(minimalBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveExtends(source)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Mounts["data"].Contract.Target != "/data" {
		t.Fatalf("mount = %#v", resolved.Mounts["data"])
	}
	if resolved.Endpoints["http"].Endpoint.Port != 8080 {
		t.Fatalf("endpoint = %#v", resolved.Endpoints["http"])
	}
}

func TestResolveExtendsRejectsInvalidReferences(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "cross kind", old: "environment.mounts.data", new: "environment.workload.endpoints.http", want: "must reference environment.mounts"},
		{name: "missing", old: "environment.mounts.data", new: "environment.mounts.missing", want: "missing environment mount"},
		{name: "nested", old: "environment.mounts.data", new: "environment.mounts.data.child", want: "one named object"},
		{name: "missing extends", old: "      extends: environment.mounts.data\n", new: "", want: "extends is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := Decode([]byte(strings.Replace(minimalBlueprint, tt.old, tt.new, 1)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolveExtends(source)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
