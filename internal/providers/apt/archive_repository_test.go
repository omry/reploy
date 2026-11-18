package apt

import (
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestDebianArchiveSourcesV1(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	tools := RequiredBaseToolsV1()
	for index := range tools {
		if requiredToolVersionV1(tools[index].Name) {
			tools[index].Version = "test"
		}
	}
	for _, test := range []struct {
		version, suite, want string
		eol                  bool
	}{
		{"6", "squeeze", "deb http://archive.debian.org/debian squeeze main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security squeeze/updates main contrib non-free\n", true},
		{"9", "stretch", "deb http://archive.debian.org/debian stretch main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security stretch/updates main contrib non-free\n", true},
		{"10", "buster", "deb http://archive.debian.org/debian buster main contrib non-free\n" +
			"deb http://archive.debian.org/debian buster-updates main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security buster/updates main contrib non-free\n", true},
		{"11", "bullseye", "deb http://archive.debian.org/debian bullseye main contrib non-free\n" +
			"deb http://archive.debian.org/debian bullseye-updates main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security bullseye-security main contrib non-free\n", true},
		{"12", "bookworm", "deb http://archive.debian.org/debian bookworm main contrib non-free\n" +
			"deb http://archive.debian.org/debian bookworm-updates main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security bookworm-security main contrib non-free\n", false},
	} {
		t.Run(test.version, func(t *testing.T) {
			evidence, err := NewBaseProfileEvidenceV1(platform,
				map[string]string{"ID": "debian", "VERSION_ID": test.version}, tools, "amd64", []string{})
			if err != nil {
				t.Fatal(err)
			}
			suite, sources, eol, err := DebianArchiveSourcesV1(evidence)
			if err != nil || suite != test.suite || sources != test.want || eol != test.eol {
				t.Fatalf("suite=%q sources=%q eol=%v err=%v", suite, sources, eol, err)
			}
		})
	}
}
