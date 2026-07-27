package python

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type testSourceDistributionEntry struct {
	name     string
	kind     byte
	content  string
	linkname string
}

func TestSourceDistributionValidationAndExtraction(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "demo_pkg-1.2.3.tar.gz")
	entries := []testSourceDistributionEntry{
		{name: "demo_pkg-1.2.3/", kind: tar.TypeDir},
		{name: "demo_pkg-1.2.3/pyproject.toml", kind: tar.TypeReg, content: "[build-system]\n"},
		{name: "demo_pkg-1.2.3/PKG-INFO", kind: tar.TypeReg, content: "Name: Demo-Pkg\nVersion: 1.2.3\n\n"},
		{name: "demo_pkg-1.2.3/demo.py", kind: tar.TypeReg, content: "value = 1\n"},
	}
	if runtime.GOOS != "windows" {
		entries = append(entries, testSourceDistributionEntry{
			name: "demo_pkg-1.2.3/link.py", kind: tar.TypeSymlink, linkname: "demo.py",
		})
	}
	writeTestSourceDistribution(t, archive, entries)
	descriptor, metadata, err := DescribeSourceDistributionFileV1(
		archive, "sdists/demo_pkg-1.2.3.tar.gz",
	)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Kind != "sdist" || metadata.Distribution != "demo-pkg" ||
		metadata.Version != "1.2.3" || metadata.Root != "demo_pkg-1.2.3" {
		t.Fatalf("descriptor/metadata = %#v / %#v", descriptor, metadata)
	}
	destination := t.TempDir()
	extracted, err := ExtractSourceDistributionFileV1(archive, destination)
	if err != nil {
		t.Fatal(err)
	}
	if extracted != metadata {
		t.Fatalf("extracted metadata = %#v, want %#v", extracted, metadata)
	}
	content, err := os.ReadFile(filepath.Join(destination, metadata.Root, "demo.py"))
	if err != nil || string(content) != "value = 1\n" {
		t.Fatalf("extracted file = %q, %v", content, err)
	}
	if runtime.GOOS != "windows" {
		target, err := os.Readlink(filepath.Join(destination, metadata.Root, "link.py"))
		if err != nil || target != "demo.py" {
			t.Fatalf("extracted link = %q, %v", target, err)
		}
	}
}

func TestSourceDistributionRelativePathsV1ReturnsValidatedPathsBelowRoot(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "demo-1.tar.gz")
	writeTestSourceDistribution(t, archive, []testSourceDistributionEntry{
		{name: "demo-1/", kind: tar.TypeDir},
		{name: "demo-1/pyproject.toml", kind: tar.TypeReg, content: "[build-system]\n"},
		{name: "demo-1/PKG-INFO", kind: tar.TypeReg, content: "Name: demo\nVersion: 1\n\n"},
		{name: "demo-1/src/", kind: tar.TypeDir},
		{name: "demo-1/src/demo.py", kind: tar.TypeReg, content: "value = 1\n"},
	})
	paths, metadata, err := SourceDistributionRelativePathsV1(archive)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PKG-INFO", "pyproject.toml", "src", "src/demo.py"}
	if !reflect.DeepEqual(paths, want) || metadata.Root != "demo-1" {
		t.Fatalf("paths/metadata = %#v/%#v", paths, metadata)
	}
}

func TestSourceDistributionRejectsUnsafeArchiveShapes(t *testing.T) {
	tests := []struct {
		name  string
		entry testSourceDistributionEntry
		want  string
	}{
		{
			name: "traversal",
			entry: testSourceDistributionEntry{
				name: "demo-1/../outside", kind: tar.TypeReg, content: "outside",
			},
			want: "unsafe path",
		},
		{
			name: "escaping symlink",
			entry: testSourceDistributionEntry{
				name: "demo-1/link", kind: tar.TypeSymlink, linkname: "../../outside",
			},
			want: "escapes",
		},
		{
			name: "hard link",
			entry: testSourceDistributionEntry{
				name: "demo-1/hard", kind: tar.TypeLink, linkname: "demo-1/module.py",
			},
			want: "hard link",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "demo-1.tar.gz")
			writeTestSourceDistribution(t, archive, []testSourceDistributionEntry{
				{name: "demo-1/", kind: tar.TypeDir},
				{name: "demo-1/pyproject.toml", kind: tar.TypeReg, content: "[build-system]\n"},
				{name: "demo-1/PKG-INFO", kind: tar.TypeReg, content: "Name: demo\nVersion: 1\n\n"},
				test.entry,
			})
			if _, _, err := DescribeSourceDistributionFileV1(
				archive, "sdists/demo-1.tar.gz",
			); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSourceDistributionPKGINFOErrorsNameTheSdistMetadata(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "demo-1.tar.gz")
	writeTestSourceDistribution(t, archive, []testSourceDistributionEntry{
		{name: "demo-1/", kind: tar.TypeDir},
		{name: "demo-1/pyproject.toml", kind: tar.TypeReg, content: "[build-system]\n"},
		{name: "demo-1/PKG-INFO", kind: tar.TypeReg, content: " malformed continuation\n\n"},
	})
	_, _, err := DescribeSourceDistributionFileV1(archive, "sdists/demo-1.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "source distribution PKG-INFO") ||
		strings.Contains(err.Error(), "wheel metadata") {
		t.Fatalf("PKG-INFO error = %v", err)
	}
}

func writeTestSourceDistribution(
	t *testing.T,
	filename string,
	entries []testSourceDistributionEntry,
) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		mode := int64(0o644)
		size := int64(len(entry.content))
		if entry.kind == tar.TypeDir {
			mode = 0o755
			size = 0
		}
		if entry.kind == tar.TypeSymlink || entry.kind == tar.TypeLink {
			size = 0
		}
		header := &tar.Header{
			Name: entry.name, Typeflag: entry.kind, Mode: mode, Size: size,
			Linkname: entry.linkname, Format: tar.FormatPAX,
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.kind == tar.TypeReg {
			if _, err := archive.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
