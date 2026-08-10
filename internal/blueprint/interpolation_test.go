package blueprint

import (
	"reflect"
	"strings"
	"testing"
)

func TestInterpolationContextResolvesPhaseScopeHostAndWorkload(t *testing.T) {
	scope := InstallScopeUser
	context := newInterpolationContext(map[string]any{"name": "arbiter"}, PhaseInstalled, &scope)
	context = context.WithRoot("environment", map[string]any{"id": "arbiter"})
	context = context.WithRoot("user", map[string]any{"data": "/home/user/.local/share"})
	context = context.WithReployValue("workload", map[string]any{
		"endpoints": map[string]any{
			"https": map[string]any{
				"publish": map[string]any{"address": "127.0.0.1", "port": 8075},
			},
		},
	})

	value, err := interpolate([]string{
		"{{ name }}",
		"{{ reploy.phase }}",
		"{{ reploy.scope }}",
		"{{ user.data }}/{{ environment.id }}",
		"{{ reploy.workload.endpoints.https.publish.address }}:{{ reploy.workload.endpoints.https.publish.port }}",
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"arbiter", "installed", "user", "/home/user/.local/share/arbiter", "127.0.0.1:8075"}
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("value = %#v, want %#v", value, want)
	}
}

func TestInterpolationContextRejectsScopeDuringStaging(t *testing.T) {
	context := newInterpolationContext(nil, PhaseStaged, nil)
	_, err := interpolate("{{ reploy.scope }}", context)
	if err == nil || !strings.Contains(err.Error(), "reploy.scope") {
		t.Fatalf("error = %v", err)
	}
}

func TestInterpolationContextPreservesWholeValueType(t *testing.T) {
	context := newInterpolationContext(map[string]any{"port": 8075}, PhaseStaged, nil)
	value, err := interpolate("{{ port }}", context)
	if err != nil {
		t.Fatal(err)
	}
	if value != 8075 {
		t.Fatalf("value = %#v", value)
	}
}

func TestInterpolationContextRejectsUnavailableWorkload(t *testing.T) {
	context := newInterpolationContext(nil, PhaseStaged, nil)
	_, err := interpolate("{{ reploy.workload.endpoints.http.publish.port }}", context)
	if err == nil || !strings.Contains(err.Error(), "reploy.workload") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveOperationStringsStringifiesWholeScalarReferences(t *testing.T) {
	values, err := ResolveOperationStrings([]string{"{{ reploy.workload.endpoints.http.bind.port }}"}, nil, PhaseStaged, nil, map[string]any{
		"reploy.workload": map[string]any{"endpoints": map[string]any{"http": map[string]any{"bind": map[string]any{"port": 8075}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "8075" {
		t.Fatalf("values = %#v", values)
	}
}

func TestResolveOperationStringsResolvesDottedEndpointNames(t *testing.T) {
	values, err := ResolveOperationStrings([]string{
		"{{ reploy.workload.endpoints.api.v1.publish.port }}",
		"{{ reploy.workload.endpoints.api.publish.port }}",
	}, nil, PhaseStaged, nil, map[string]any{
		"reploy.workload": map[string]any{"endpoints": map[string]any{
			"api.v1": map[string]any{"publish": map[string]any{"port": 8443}},
			"api":    map[string]any{"publish": map[string]any{"port": 8080}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"8443", "8080"}; !reflect.DeepEqual(values, want) {
		t.Fatalf("values = %#v, want %#v", values, want)
	}
}

func TestInterpolationContextPrefersLongestAvailableDottedMapKey(t *testing.T) {
	context := InterpolationContext{Roots: map[string]any{
		"root": map[string]any{
			"api":    map[string]any{"v1": "nested"},
			"api.v1": "dotted",
		},
	}}
	value, err := interpolate("{{ root.api.v1 }}", context)
	if err != nil {
		t.Fatal(err)
	}
	if value != "dotted" {
		t.Fatalf("value = %#v, want dotted key", value)
	}
}
