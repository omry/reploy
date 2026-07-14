package blueprint

import (
	"strings"
	"testing"
)

func TestResolveNamesDefaultsControlScriptToEnvironmentID(t *testing.T) {
	id, control, err := resolveNames(EnvironmentSyntax{ID: "arbiter"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "arbiter" || control != "arbiter" {
		t.Fatalf("id/control = %q/%q", id, control)
	}
}

func TestResolveNamesAllowsExplicitControlScript(t *testing.T) {
	_, control, err := resolveNames(EnvironmentSyntax{ID: "arbiter", ControlScript: "arbiter-dev"})
	if err != nil {
		t.Fatal(err)
	}
	if control != "arbiter-dev" {
		t.Fatalf("control = %q", control)
	}
}

func TestResolveNamesRejectsUnsafeAndReservedNames(t *testing.T) {
	tests := []struct {
		name        string
		environment EnvironmentSyntax
		want        string
	}{
		{name: "empty id", environment: EnvironmentSyntax{}, want: "environment.id"},
		{name: "path id", environment: EnvironmentSyntax{ID: "acme/demo"}, want: "portable basename"},
		{name: "trailing dot", environment: EnvironmentSyntax{ID: "demo."}, want: "portable basename"},
		{name: "windows device", environment: EnvironmentSyntax{ID: "CON.txt"}, want: "platform-reserved"},
		{name: "unsafe control", environment: EnvironmentSyntax{ID: "demo", ControlScript: "../demo"}, want: "environment.control_script"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolveNames(tt.environment)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
