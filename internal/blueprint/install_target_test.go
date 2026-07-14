package blueprint

import (
	"strings"
	"testing"
)

func testInstallContext(host HostOS, scope InstallScope) InstallTargetContext {
	return InstallTargetContext{
		Host:  host,
		Scope: scope,
		Paths: HostPaths{
			Home:       "/home/demo",
			UserData:   "/home/demo/.local/share",
			LocalData:  `C:\Users\demo\AppData\Local`,
			SystemData: "/var/lib",
		},
	}
}

func TestResolveInstallTargetUsesPrecedence(t *testing.T) {
	target := InstallTargetSyntax{
		DefaultPath: "/global/{{ environment.id }}",
		DefaultPaths: map[string]string{
			"linux":        "/linux/{{ environment.id }}",
			"system.linux": "/system/{{ environment.id }}",
		},
	}
	context := testInstallContext(HostLinux, InstallScopeSystem)
	resolved, err := resolveInstallTarget(target, "arbiter", context)
	if err != nil || resolved != "/system/arbiter" {
		t.Fatalf("resolved/error = %q/%v", resolved, err)
	}
	context.Override = "/override/arbiter"
	resolved, err = resolveInstallTarget(target, "arbiter", context)
	if err != nil || resolved != "/override/arbiter" {
		t.Fatalf("override/error = %q/%v", resolved, err)
	}
}

func TestResolveInstallTargetUsesBuiltInDefaults(t *testing.T) {
	tests := []struct {
		name    string
		context InstallTargetContext
		want    string
	}{
		{name: "linux system", context: testInstallContext(HostLinux, InstallScopeSystem), want: "/opt/arbiter"},
		{name: "linux user", context: testInstallContext(HostLinux, InstallScopeUser), want: "/home/demo/.local/share/Reploy/installs/arbiter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolveInstallTarget(InstallTargetSyntax{}, "arbiter", tt.context)
			if err != nil || resolved != tt.want {
				t.Fatalf("resolved/error = %q/%v, want %q", resolved, err, tt.want)
			}
		})
	}
}

func TestResolveInstallTargetAllowsInactiveForeignPath(t *testing.T) {
	target := InstallTargetSyntax{DefaultPaths: map[string]string{
		"linux":        "/opt/{{ environment.id }}",
		"user.windows": `C:\Apps\{{ environment.id }}`,
	}}
	_, err := resolveInstallTarget(target, "arbiter", testInstallContext(HostLinux, InstallScopeUser))
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveInstallTargetRejectsInvalidPlatformAndPaths(t *testing.T) {
	tests := []struct {
		name    string
		target  InstallTargetSyntax
		context InstallTargetContext
		want    string
	}{
		{name: "mac system", context: testInstallContext(HostMacOS, InstallScopeSystem), want: "not supported"},
		{name: "unknown key", target: InstallTargetSyntax{DefaultPaths: map[string]string{"darwin": "/tmp"}}, context: testInstallContext(HostLinux, InstallScopeUser), want: "unknown key"},
		{name: "relative", target: InstallTargetSyntax{DefaultPath: "relative"}, context: testInstallContext(HostLinux, InstallScopeUser), want: "absolute"},
		{name: "unknown variable", target: InstallTargetSyntax{DefaultPath: "/opt/{{ missing }}"}, context: testInstallContext(HostLinux, InstallScopeUser), want: "unknown blueprint variable"},
		{name: "interpolated traversal", target: InstallTargetSyntax{DefaultPath: "/opt/{{ suffix }}"}, context: func() InstallTargetContext {
			context := testInstallContext(HostLinux, InstallScopeUser)
			context.Variables = map[string]any{"suffix": "../escape"}
			return context
		}(), want: "parent-directory traversal"},
		{name: "windows traversal", target: InstallTargetSyntax{DefaultPath: `C:\\Apps\\..\\escape`}, context: testInstallContext(HostWindows, InstallScopeUser), want: "parent-directory traversal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveInstallTarget(tt.target, "arbiter", tt.context)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
