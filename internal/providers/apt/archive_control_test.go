package apt

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestArchiveInspectionArgvV1IsFixedAndPathIsPositional(t *testing.T) {
	archive := "/tmp/reploy-apt-resolve/archives/hello_2.10-3_amd64.deb"
	argv, err := ArchiveInspectionArgvV1(archive)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/bin/sh", "-c", ArchiveInspectionScriptV1, "apt-archive-inspect-v1", archive}
	if !reflect.DeepEqual(argv, want) || strings.Contains(ArchiveInspectionScriptV1, archive) {
		t.Fatalf("argv = %#v", argv)
	}
	for _, invalid := range []string{"hello.deb", "/tmp/reploy-apt-resolve/output/x.deb", "/tmp/reploy-apt-resolve/archives/../x.deb", "/tmp/reploy-apt-resolve/archives/x.txt"} {
		if _, err := ArchiveInspectionArgvV1(invalid); err == nil {
			t.Fatalf("invalid path %q was accepted", invalid)
		}
	}
}

func TestReadArchiveInspectionHeaderV1LeavesTarStreamUnread(t *testing.T) {
	payload := []byte("tar payload bytes")
	stream := append([]byte("Package: hello\nVersion: 2.10-3\nArchitecture: amd64\x00"), payload...)
	tuple, remainder, err := ReadArchiveInspectionHeaderV1(bytes.NewReader(stream), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	want := PackageTuple{Name: "hello", Version: "2.10-3", Architecture: "amd64", Status: InstalledPackageStatusV1}
	if tuple != want {
		t.Fatalf("tuple = %#v, want %#v", tuple, want)
	}
	got, err := io.ReadAll(remainder)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("remainder = %q, err = %v", got, err)
	}
}

func TestReadArchiveInspectionHeaderV1AcceptsAllAndRejectsMalformedFrames(t *testing.T) {
	valid := []byte("Package: perl-modules\nVersion: 5.38\nArchitecture: all\x00tar")
	if tuple, _, err := ReadArchiveInspectionHeaderV1(bytes.NewReader(valid), "amd64"); err != nil || tuple.Architecture != "all" {
		t.Fatalf("tuple = %#v, err = %v", tuple, err)
	}
	for _, input := range [][]byte{
		[]byte("Package: hello\nVersion: 1\nArchitecture: amd64"),
		[]byte("Version: 1\nPackage: hello\nArchitecture: amd64\x00"),
		[]byte("Package: hello\nVersion: 1\nArchitecture: arm64\x00"),
		[]byte("Package: H\nVersion: 1\nArchitecture: amd64\x00"),
		[]byte("Package: hello\nVersion: 1\nArchitecture: amd64\nExtra: x\x00"),
	} {
		if _, _, err := ReadArchiveInspectionHeaderV1(bytes.NewReader(input), "amd64"); err == nil {
			t.Fatalf("malformed frame %q was accepted", input)
		}
	}
}
