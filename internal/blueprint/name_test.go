package blueprint

import (
	"strings"
	"testing"
)

func TestResolveNamesDefaultsControlScriptToAppctl(t *testing.T) {
	id, control, err := resolveNames(EnvironmentSyntax{ID: "arbiter"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "arbiter" || control != "appctl" {
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

func TestValidateProviderIdentifier(t *testing.T) {
	for _, name := range []string{"application", "python_env_3", "apt-tools", "a0"} {
		if err := validateProviderIdentifier("component", name); err != nil {
			t.Fatalf("validateProviderIdentifier(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "Application", "3python", "python.env", "python/env", "python env"} {
		if err := validateProviderIdentifier("component", name); err == nil {
			t.Fatalf("validateProviderIdentifier(%q) succeeded", name)
		}
	}
}

func TestValidatePythonDistributionName(t *testing.T) {
	for _, name := range []string{"demo", "Demo_Server", "demo.pkg", "demo-pkg2"} {
		if err := ValidatePythonDistributionName("distribution", name); err != nil {
			t.Fatalf("ValidatePythonDistributionName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "demo/pkg", `demo\pkg`, " demo", "demo-", ".demo"} {
		if err := ValidatePythonDistributionName("distribution", name); err == nil {
			t.Fatalf("ValidatePythonDistributionName(%q) succeeded", name)
		}
	}
}

func TestValidateNonBaseComponentIdentifier(t *testing.T) {
	if err := validateNonBaseComponentIdentifier("component", "application"); err != nil {
		t.Fatal(err)
	}
	if err := validateNonBaseComponentIdentifier("component", "base"); err == nil || !strings.Contains(err.Error(), "reserved root") {
		t.Fatalf("reserved base error = %v", err)
	}
}
