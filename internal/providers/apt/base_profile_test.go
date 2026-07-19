package apt

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func aptBasePlatform(t *testing.T, value string) blueprint.Platform {
	t.Helper()
	platform, err := blueprint.ParsePlatform(value)
	if err != nil {
		t.Fatal(err)
	}
	return platform
}

func aptBaseTools() []RequiredToolEvidenceV1 {
	tools := RequiredBaseToolsV1()
	for index := range tools {
		if tools[index].Name != "env" && tools[index].Name != "sh" {
			tools[index].Version = tools[index].Name + " 1.2.3"
		}
	}
	return tools
}

func TestNewBaseProfileEvidenceAcceptsExactFamilyIdentityWithoutReleaseAllowlist(t *testing.T) {
	for _, test := range []struct {
		name      string
		platform  string
		osRelease map[string]string
		arch      string
		matchedBy string
	}{
		{name: "old Debian", platform: "linux/amd64", osRelease: map[string]string{"ID": "debian", "VERSION_ID": "8", "PRETTY_NAME": "Debian GNU/Linux 8", "OPTIONAL": ""}, arch: "amd64", matchedBy: "id"},
		{name: "future Ubuntu", platform: "linux/arm64/v8", osRelease: map[string]string{"ID": "ubuntu", "VERSION_ID": "30.04"}, arch: "arm64", matchedBy: "id"},
		{name: "derived Debian", platform: "linux/arm/v7", osRelease: map[string]string{"ID": "example", "ID_LIKE": "gnu/linux debian", "VERSION_ID": "1"}, arch: "armhf", matchedBy: "id_like"},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence, err := NewBaseProfileEvidenceV1(aptBasePlatform(t, test.platform), test.osRelease, aptBaseTools(), test.arch, []string{})
			if err != nil {
				t.Fatal(err)
			}
			if evidence.MatchedBy != test.matchedBy || evidence.NativeArchitecture != test.arch || len(evidence.OSRelease) != len(test.osRelease) {
				t.Fatalf("evidence = %#v", evidence)
			}
			first, err := canonical.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			second, err := canonical.Marshal(evidence)
			if err != nil || string(first) != string(second) {
				t.Fatalf("evidence is not canonically stable: %v", err)
			}
		})
	}
}

func TestBaseProfileUsesExactIDLikeTokens(t *testing.T) {
	for _, idLike := range []string{"notdebian", "ubuntuish", "gnu/linux-debian"} {
		_, err := NewBaseProfileEvidenceV1(
			aptBasePlatform(t, "linux/amd64"),
			map[string]string{"ID": "example", "ID_LIKE": idLike, "VERSION_ID": "1"},
			aptBaseTools(), "amd64", []string{},
		)
		if err == nil || !strings.Contains(err.Error(), "do not identify") {
			t.Fatalf("ID_LIKE %q: err = %v", idLike, err)
		}
	}
}

func TestBaseProfileRejectsMissingVersionToolsAndArchitectureMismatch(t *testing.T) {
	validOS := map[string]string{"ID": "debian", "VERSION_ID": "13"}
	platform := aptBasePlatform(t, "linux/amd64")
	tests := []struct {
		name    string
		os      map[string]string
		tools   []RequiredToolEvidenceV1
		native  string
		foreign []string
		want    string
	}{
		{name: "missing VERSION_ID", os: map[string]string{"ID": "debian"}, tools: aptBaseTools(), native: "amd64", foreign: []string{}, want: "VERSION_ID"},
		{name: "missing tool", os: validOS, tools: aptBaseTools()[:5], native: "amd64", foreign: []string{}, want: "exact required tool set"},
		{name: "wrong tool path", os: validOS, tools: func() []RequiredToolEvidenceV1 { tools := aptBaseTools(); tools[0].Path = "/bin/apt-get"; return tools }(), native: "amd64", foreign: []string{}, want: "does not match"},
		{name: "missing tool version", os: validOS, tools: func() []RequiredToolEvidenceV1 { tools := aptBaseTools(); tools[0].Version = ""; return tools }(), native: "amd64", foreign: []string{}, want: "version evidence"},
		{name: "native mismatch", os: validOS, tools: aptBaseTools(), native: "arm64", foreign: []string{}, want: "does not match"},
		{name: "foreign architecture", os: validOS, tools: aptBaseTools(), native: "amd64", foreign: []string{"i386"}, want: "unsupported foreign"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewBaseProfileEvidenceV1(platform, test.os, test.tools, test.native, test.foreign)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDebianArchitectureForPlatformV1(t *testing.T) {
	for platform, want := range map[string]string{
		"linux/amd64": "amd64", "linux/amd64/v3": "amd64",
		"linux/arm64": "arm64", "linux/arm64/v8": "arm64",
		"linux/arm/v7": "armhf",
	} {
		got, err := DebianArchitectureForPlatformV1(aptBasePlatform(t, platform))
		if err != nil || got != want {
			t.Fatalf("%s: got %q, err %v", platform, got, err)
		}
	}
	for _, platform := range []string{"darwin/amd64", "linux/386", "linux/arm", "linux/arm/v6"} {
		if _, err := DebianArchitectureForPlatformV1(aptBasePlatform(t, platform)); err == nil {
			t.Fatalf("unsupported platform %s was accepted", platform)
		}
	}
}

func TestValidateBaseProfileEvidenceRejectsNonCanonicalRecords(t *testing.T) {
	evidence, err := NewBaseProfileEvidenceV1(
		aptBasePlatform(t, "linux/amd64"),
		map[string]string{"ID": "debian", "VERSION_ID": "13"},
		aptBaseTools(), "amd64", []string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	evidence.OSRelease[0], evidence.OSRelease[1] = evidence.OSRelease[1], evidence.OSRelease[0]
	if err := ValidateBaseProfileEvidenceV1(evidence); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("err = %v", err)
	}
}
