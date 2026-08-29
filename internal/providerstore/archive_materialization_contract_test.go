package providerstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestArchiveMaterializationContractNormalizesPaths(t *testing.T) {
	maxUTF8Component := strings.Repeat("é", 127) + "a"
	maxASCIIComponent := strings.Repeat("a", CoreMaxArchiveComponentBytes)
	overTotalPath := strings.Repeat(maxASCIIComponent+"/", 16) + maxASCIIComponent
	tests := []struct {
		name      string
		value     string
		directory bool
		want      string
		wantErr   string
	}{
		{name: "file", value: "bin/tool", want: "bin/tool"},
		{name: "leading-dot-directory", value: "./bin/", directory: true, want: "bin"},
		{name: "root-directory", value: ".", directory: true, want: "."},
		{name: "traversal", value: "../escape", wantErr: "invalid"},
		{name: "absolute", value: "/escape", wantErr: "relative"},
		{name: "backslash", value: `bin\tool`, wantErr: "relative"},
		{name: "empty-component", value: "bin//tool", wantErr: "invalid"},
		{name: "regular-trailing-slash", value: "bin/", wantErr: "regular-file"},
		{name: "windows-forbidden", value: "bin/tool?", wantErr: "unsafe Windows"},
		{name: "component-byte-limit", value: maxUTF8Component, want: maxUTF8Component},
		{name: "component-byte-limit-exceeded", value: maxUTF8Component + "a", wantErr: "component exceeds"},
		{name: "path-byte-limit", value: overTotalPath, wantErr: "path exceeds"},
		{name: "command-looking-data", value: "bin;echo rm -rf", want: "bin;echo rm -rf"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeArchivePath(test.value, test.directory)
			if test.wantErr == "" {
				if err != nil || got != test.want {
					t.Fatalf("normalizeArchivePath() = %q, %v, want %q", got, err, test.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("normalizeArchivePath() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestArchiveMaterializationContractPortableDestinationKeys(t *testing.T) {
	aliases := [][2]string{
		{"Bin/Tool", "bin/tool"},
		{"café/bin", "café/bin"},
		{"Σ/bin", "ς/bin"},
		{"S/bin", "ſ/bin"},
	}
	for _, alias := range aliases {
		if got, want := portableArchiveDestinationKey(alias[0]), portableArchiveDestinationKey(alias[1]); got != want {
			t.Errorf("portable keys for %q and %q differ: %q != %q", alias[0], alias[1], got, want)
		}
	}
	nonAliases := [][2]string{
		{"alpha/bin", "beta/bin"},
		{"café/bin", "cafe/bin"},
	}
	for _, pair := range nonAliases {
		if got, want := portableArchiveDestinationKey(pair[0]), portableArchiveDestinationKey(pair[1]); got == want {
			t.Errorf("portable keys for non-aliases %q and %q unexpectedly match: %q", pair[0], pair[1], got)
		}
	}
}

func TestArchiveMaterializationContractRejectsWindowsReservedNames(t *testing.T) {
	for _, value := range []string{"COM¹", "LPT².txt", "COM³.log"} {
		t.Run(value, func(t *testing.T) {
			if _, err := normalizeArchivePath(value, false); err == nil || !strings.Contains(err.Error(), "unsafe Windows") {
				t.Fatalf("normalizeArchivePath(%q) error = %v, want Windows-unsafe rejection", value, err)
			}
		})
	}
}

func TestArchiveMaterializationContractLimits(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		maximum uint64
		want    uint64
		valid   bool
	}{
		{name: "one", value: "1", maximum: 10, want: 1, valid: true},
		{name: "maximum", value: "10", maximum: 10, want: 10, valid: true},
		{name: "zero", value: "0", maximum: 10},
		{name: "leading-zero", value: "01", maximum: 10},
		{name: "negative", value: "-1", maximum: 10},
		{name: "fraction", value: "1.0", maximum: 10},
		{name: "over-limit", value: "11", maximum: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseArchiveLimit("limit", test.value, test.maximum)
			if test.valid {
				if err != nil || got != test.want {
					t.Fatalf("parseArchiveLimit() = %d, %v, want %d", got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseArchiveLimit(%q) unexpectedly succeeded", test.value)
			}
		})
	}
}

func TestArchiveMaterializationContractRootsAndInstallDirectory(t *testing.T) {
	maxUTF8Component := strings.Repeat("é", 127) + "a"
	root := t.TempDir()
	if err := validateArchiveDestinationRoot(root); err != nil {
		t.Fatalf("valid destination root rejected: %v", err)
	}
	file := filepath.Join(root, "file")
	if err := writeContractTestFile(file); err != nil {
		t.Fatal(err)
	}
	if err := validateArchiveDestinationRoot(file); err == nil {
		t.Fatal("regular file accepted as destination root")
	}
	if err := validateArchiveDestinationRoot(root + string(filepath.Separator) + "."); err == nil {
		t.Fatal("unclean destination root accepted")
	}

	for _, value := range []string{"install", "payload-1", maxUTF8Component} {
		if err := validateInstallDirectory(value); err != nil {
			t.Errorf("valid install directory %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", ".", "..", "install/child", "CON", "install:name", maxUTF8Component + "a"} {
		if err := validateInstallDirectory(value); err == nil {
			t.Errorf("invalid install directory %q accepted", value)
		}
	}

	for _, value := range []string{".", "payload/bin"} {
		got, err := validateArchiveRoot(value)
		if err != nil || got != value {
			t.Errorf("valid archive root %q = %q, %v", value, got, err)
		}
	}
	for _, value := range []string{"./payload", "payload/", "../payload", "payload\\bin"} {
		if _, err := validateArchiveRoot(value); err == nil {
			t.Errorf("invalid archive root %q accepted", value)
		}
	}
}

func TestArchiveMaterializationContractRequestValidation(t *testing.T) {
	root := t.TempDir()
	valid := ArchiveMaterializationRequest{
		Artifact: ArtifactDescriptor{
			LogicalPath: "archives/payload.tar.gz",
			Kind:        "archive",
			Size:        "1",
			SHA256:      canonical.Digest("sha256:" + strings.Repeat("a", 64)),
		},
		Format:               ArchiveFormatTarGz,
		DestinationRoot:      root,
		InstallDirectory:     "install",
		ArchiveRoot:          "payload",
		ExpectedEntryCount:   "1",
		ExpectedUnpackedSize: "1",
		ExecutablePaths:      []string{"payload/bin"},
	}
	validated, err := validateArchiveMaterializationRequest(valid)
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if validated.archiveRoot != "payload" || validated.entryLimit != 1 || validated.sizeLimit != 1 {
		t.Fatalf("validated request = %#v", validated)
	}
	mutationRequest := valid
	mutationPaths := []string{"payload/bin"}
	mutationRequest.ExecutablePaths = mutationPaths
	mutationValidated, err := validateArchiveMaterializationRequest(mutationRequest)
	if err != nil {
		t.Fatalf("mutation request rejected: %v", err)
	}
	mutationPaths[0] = "payload/changed"
	if len(mutationValidated.ExecutablePaths) != 1 || mutationValidated.ExecutablePaths[0] != "payload/bin" {
		t.Fatalf("validated executable paths changed with caller mutation: %#v", mutationValidated.ExecutablePaths)
	}
	dotRootRequest := valid
	dotRootRequest.ArchiveRoot = "."
	dotRootRequest.ExecutablePaths = []string{"payload/bin"}
	if _, err := validateArchiveMaterializationRequest(dotRootRequest); err != nil {
		t.Fatalf("normalized executable path rejected for dot archive root: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ArchiveMaterializationRequest)
	}{
		{name: "unsupported-format", mutate: func(request *ArchiveMaterializationRequest) { request.Format = "rar" }},
		{name: "unclean-destination", mutate: func(request *ArchiveMaterializationRequest) {
			request.DestinationRoot += string(filepath.Separator) + "."
		}},
		{name: "multi-component-install", mutate: func(request *ArchiveMaterializationRequest) { request.InstallDirectory = "install/child" }},
		{name: "non-normalized-root", mutate: func(request *ArchiveMaterializationRequest) { request.ArchiveRoot = "./payload" }},
		{name: "archive-root-executable", mutate: func(request *ArchiveMaterializationRequest) {
			request.ExecutablePaths = []string{"payload"}
		}},
		{name: "leading-zero-count", mutate: func(request *ArchiveMaterializationRequest) { request.ExpectedEntryCount = "01" }},
		{name: "zero-size", mutate: func(request *ArchiveMaterializationRequest) { request.ExpectedUnpackedSize = "0" }},
		{name: "unsorted-executables", mutate: func(request *ArchiveMaterializationRequest) {
			request.ExpectedEntryCount = "2"
			request.ExecutablePaths = []string{"payload/z", "payload/a"}
		}},
		{name: "duplicate-executables", mutate: func(request *ArchiveMaterializationRequest) {
			request.ExpectedEntryCount = "2"
			request.ExecutablePaths = []string{"payload/bin", "payload/bin"}
		}},
		{name: "too-many-executables", mutate: func(request *ArchiveMaterializationRequest) {
			request.ExecutablePaths = []string{"payload/a", "payload/bin"}
		}},
		{name: "case-folded-portable-aliases", mutate: func(request *ArchiveMaterializationRequest) {
			request.ExpectedEntryCount = "2"
			request.ExecutablePaths = []string{"payload/Bin", "payload/bin"}
		}},
		{name: "nfc-equivalent-portable-aliases", mutate: func(request *ArchiveMaterializationRequest) {
			request.ExpectedEntryCount = "2"
			request.ExecutablePaths = []string{"payload/cafe\u0301", "payload/café"}
		}},
		{name: "ancestor-descendant-executables", mutate: func(request *ArchiveMaterializationRequest) {
			request.ExpectedEntryCount = "2"
			request.ExecutablePaths = []string{"payload/bin", "payload/bin/tool"}
		}},
		{name: "case-folded-ancestor-descendant-executables", mutate: func(request *ArchiveMaterializationRequest) {
			request.ExpectedEntryCount = "2"
			request.ExecutablePaths = []string{"payload/BIN/tool", "payload/bin"}
		}},
		{name: "outside-root-executable", mutate: func(request *ArchiveMaterializationRequest) { request.ExecutablePaths = []string{"other/bin"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if _, err := validateArchiveMaterializationRequest(candidate); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func writeContractTestFile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	return file.Close()
}
