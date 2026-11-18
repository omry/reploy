package apt

import "testing"

func TestDebianArchiveRepositoryV1RecognizesEOLBullseye(t *testing.T) {
	evidence, err := NewBaseProfileEvidenceV1(
		aptArchiveTestPlatform(t),
		map[string]string{"ID": "debian", "VERSION_ID": "11", "VERSION_CODENAME": "bullseye"},
		RequiredBaseToolsV1WithVersionsForTest(), "amd64", []string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	repository, found, err := DebianArchiveRepositoryV1(evidence)
	if err != nil || !found {
		t.Fatalf("repository=%#v found=%v err=%v", repository, found, err)
	}
	sources, err := repository.SourceListV1()
	want := "deb http://archive.debian.org/debian bullseye main contrib non-free\n" +
		"deb http://archive.debian.org/debian bullseye-updates main contrib non-free\n" +
		"deb http://archive.debian.org/debian-security bullseye-security main contrib non-free\n"
	if err != nil || sources != want {
		t.Fatalf("sources=%q err=%v", sources, err)
	}
}

func TestDebianArchiveSourceListV1MatchesRetainedReleaseSuites(t *testing.T) {
	for _, test := range []struct {
		version string
		want    string
	}{
		{"6", "deb http://archive.debian.org/debian squeeze main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security squeeze/updates main contrib non-free\n"},
		{"9", "deb http://archive.debian.org/debian stretch main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security stretch/updates main contrib non-free\n"},
		{"10", "deb http://archive.debian.org/debian buster main contrib non-free\n" +
			"deb http://archive.debian.org/debian buster-updates main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security buster/updates main contrib non-free\n"},
		{"12", "deb http://archive.debian.org/debian bookworm main contrib non-free\n" +
			"deb http://archive.debian.org/debian bookworm-updates main contrib non-free\n" +
			"deb http://archive.debian.org/debian-security bookworm-security main contrib non-free\n"},
	} {
		t.Run(test.version, func(t *testing.T) {
			evidence, err := NewBaseProfileEvidenceV1(aptArchiveTestPlatform(t),
				map[string]string{"ID": "debian", "VERSION_ID": test.version},
				RequiredBaseToolsV1WithVersionsForTest(), "amd64", []string{})
			if err != nil {
				t.Fatal(err)
			}
			repository, found, err := DebianArchiveRepositoryV1(evidence)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			got, err := repository.SourceListV1()
			if err != nil || got != test.want {
				t.Fatalf("sources=%q, want %q; err=%v", got, test.want, err)
			}
		})
	}
}

func TestDebianArchiveRepositoryV1ProvidesArchiveCandidateAfterAnyDebianRepositoryFailure(t *testing.T) {
	evidence, err := NewBaseProfileEvidenceV1(
		aptArchiveTestPlatform(t),
		map[string]string{"ID": "debian", "VERSION_ID": "12"},
		RequiredBaseToolsV1WithVersionsForTest(), "amd64", []string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	repository, found, err := DebianArchiveRepositoryV1(evidence)
	if err != nil || !found || repository.Suite != "bookworm" || repository.EOL {
		t.Fatalf("repository=%#v found=%v err=%v", repository, found, err)
	}
}
