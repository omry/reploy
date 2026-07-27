package dockerdeploy

import (
	"context"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
)

func TestObserveAPTBaseProfileBuildsCanonicalEvidence(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	wantRequest := aptBaseProbeRequest()
	response := aptBaseProbeResponse()
	outputs := map[string][]byte{
		"/bin/sh\x00-c\x00" + aptOSReleaseProbeScriptV1:  []byte("ID\x00debian\x00VERSION_ID\x0013\x00"),
		"/usr/bin/apt-get\x00--version":                  []byte("apt 3.0.3 (amd64)\nSupported modules:\n"),
		"/usr/bin/dpkg\x00--version":                     []byte("Debian 'dpkg' package management program version 1.22.21 (amd64).\n"),
		"/usr/bin/dpkg-deb\x00--version":                 []byte("Debian 'dpkg-deb' package archive backend version 1.22.21 (amd64).\n"),
		"/usr/bin/dpkg-query\x00--version":               []byte("Debian dpkg-query package management program query tool version 1.22.21 (amd64).\n"),
		"/usr/bin/sha256sum\x00--version":                []byte("sha256sum (GNU coreutils) 9.5\n"),
		"/usr/bin/dpkg\x00--print-architecture":          []byte("amd64\n"),
		"/usr/bin/dpkg\x00--print-foreign-architectures": {},
	}
	commands := []string{}
	validation, err := observeAPTBaseProfile(
		context.Background(),
		descriptor.Platform,
		func(_ context.Context, request probe.RequestV1) (probe.ResponseV1, error) {
			if !reflect.DeepEqual(request, wantRequest) {
				t.Fatalf("probe request = %#v, want %#v", request, wantRequest)
			}
			return response, nil
		},
		func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
			key := executable
			for _, argument := range arguments {
				key += "\x00" + argument
			}
			commands = append(commands, key)
			output, found := outputs[key]
			if !found {
				t.Fatalf("unexpected profile command %q", key)
			}
			return output, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if validation.Profile.MatchedBy != "id" || validation.Profile.NativeArchitecture != "amd64" || len(validation.Profile.ForeignArchitectures) != 0 {
		t.Fatalf("profile = %#v", validation.Profile)
	}
	if len(commands) != 8 {
		t.Fatalf("commands = %#v", commands)
	}
	if len(validation.Executables) != len(wantRequest.Inspections) {
		t.Fatalf("executables = %#v", validation.Executables)
	}
	roles := map[string]string{}
	for _, executable := range validation.Executables {
		roles[executable.ID] = executable.Role
	}
	if roles["sh"] != providers.ExecutableRoleCarrier || roles["env"] != providers.ExecutableRoleEnvironmentLauncher || roles["apt_get"] != providers.ExecutableRoleProviderPrerequisite {
		t.Fatalf("roles = %#v", roles)
	}
}
