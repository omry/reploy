package blueprint

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveVariablesResolvesDependenciesAndTypes(t *testing.T) {
	resolved, err := resolveVariables(map[string]any{
		"root":    "/workspace",
		"server":  "{{ root }}/server",
		"workers": 4,
		"copy":    "{{ workers }}",
		"nested":  []any{"{{ server }}", true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["server"] != "/workspace/server" || resolved["copy"] != 4 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if !reflect.DeepEqual(resolved["nested"], []any{"/workspace/server", true}) {
		t.Fatalf("nested = %#v", resolved["nested"])
	}
}

func TestResolveVariablesLeavesNamespacedInterpolationLazy(t *testing.T) {
	resolved, err := resolveVariables(map[string]any{"url": "{{ environment.id }}-{{ reploy.phase }}"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["url"] != "{{ environment.id }}-{{ reploy.phase }}" {
		t.Fatalf("url = %q", resolved["url"])
	}
}

func TestResolveVariablesRejectsReservedInvalidMissingAndCycles(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   string
	}{
		{name: "reserved", values: map[string]any{"blueprint": "x"}, want: "reserved root"},
		{name: "invalid", values: map[string]any{"not-valid": "x"}, want: "valid identifier"},
		{name: "missing", values: map[string]any{"a": "{{ missing }}"}, want: "unknown blueprint variable"},
		{name: "cycle", values: map[string]any{"a": "{{ b }}", "b": "{{ a }}"}, want: "variable cycle"},
		{name: "embedded collection", values: map[string]any{"a": []any{"x"}, "b": "value={{ a }}"}, want: "not scalar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveVariables(tt.values)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolveExpandsVariablesAcrossStringSchemaFields(t *testing.T) {
	source, err := Decode([]byte(minimalBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	source.Environment.Vars = map[string]any{
		"version": "2.3",
		"image":   "python:3.13",
		"color":   "APP_COLOR",
		"package": "demo-server>=2",
	}
	source.Blueprint.Version = "{{ version }}"
	source.Docker.Image = "{{ image }}"
	source.Environment.Terminal.ColorEnv = "{{ color }}"
	source.Environment.Components["application"] = ComponentSyntax{Type: "python", Requirements: []string{"{{ package }}"}}

	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if document.Blueprint.Version != "2.3" || document.Docker.Image != "python:3.13" || document.Environment.Terminal.ColorEnv != "APP_COLOR" {
		t.Fatalf("resolved document = %#v", document)
	}
	if got := document.Environment.Components["application"].Requirements[0]; got != "demo-server>=2" {
		t.Fatalf("component requirement = %q", got)
	}
}

func TestResolvePreservesWholeVariableTypesForTypedFields(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  id: demo\n", "  id: demo\n  vars: {app_port: 9090, published_port: 19090, verify: true}\n", 1)
	value = strings.Replace(value, "        port: 8080", "        port: \"{{ app_port }}\"\n        readiness: {path: /ready, tls_verify: \"{{ verify }}\"}", 1)
	value = strings.Replace(value, "staging: 18080", `staging: "{{ published_port }}"`, 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := document.Environment.Workload.Endpoints["http"]
	if endpoint.Port != 9090 || endpoint.Readiness == nil || !endpoint.Readiness.TLSVerify {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	if got := document.Docker.Workload.Endpoints["http"].Publish.Staging; got != 19090 {
		t.Fatalf("staging port = %d", got)
	}

	source.Environment.Vars["app_port"] = "9090"
	item := source.Environment.Workload.Endpoints["http"]
	item.Port = "{{ app_port }}"
	source.Environment.Workload.Endpoints["http"] = item
	if _, err := Resolve(source); err == nil || !strings.Contains(err.Error(), "must resolve to an integer") {
		t.Fatalf("type error = %v", err)
	}
}
