package apt

import (
	"archive/tar"
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestInspectArchiveFileListV1MatchesCanonicalDigest(t *testing.T) {
	members := []ArchiveMemberV1{
		{Path: "/usr", Kind: "directory", LinkTarget: ""},
		{Path: "/usr/bin/demo", Kind: "regular", LinkTarget: ""},
		{Path: "/usr/bin/demo-link", Kind: "symlink", LinkTarget: "../lib/demo"},
		{Path: "/usr/bin/demo-hard", Kind: "hardlink", LinkTarget: "/usr/bin/demo"},
	}
	stream := archiveInspectionTar(t, []tar.Header{
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./usr/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./usr/bin/demo", Typeflag: tar.TypeReg, Mode: 0o755, Size: 4},
		{Name: "./usr/bin/demo-link", Typeflag: tar.TypeSymlink, Linkname: "../lib/demo"},
		{Name: "./usr/bin/demo-hard", Typeflag: tar.TypeLink, Linkname: "./usr/bin/demo"},
	}, []string{"", "", "demo", "", ""})
	got, err := InspectArchiveFileListV1(context.Background(), bytes.NewReader(stream), []string{"/opt/reploy/providers/python/app"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := ArchiveFileListDigestV1(members)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

func TestInspectArchiveFileListV1RejectsProtectedAndMalformedMembers(t *testing.T) {
	for _, test := range []struct {
		name   string
		header tar.Header
		want   string
	}{
		{name: "build root", header: tar.Header{Name: "./.reploy-build/script", Typeflag: tar.TypeReg}, want: "protected root"},
		{name: "mount root", header: tar.Header{Name: "mnt/data", Typeflag: tar.TypeDir}, want: "protected root"},
		{name: "exclusive root", header: tar.Header{Name: "opt/reploy/providers/python/app/bin/x", Typeflag: tar.TypeReg}, want: "protected root"},
		{name: "absolute", header: tar.Header{Name: "/usr/bin/x", Typeflag: tar.TypeReg}, want: "relative UTF-8"},
		{name: "parent", header: tar.Header{Name: "usr/../etc/x", Typeflag: tar.TypeReg}, want: "invalid component"},
		{name: "empty component", header: tar.Header{Name: "usr//bin/x", Typeflag: tar.TypeReg}, want: "invalid component"},
		{name: "device", header: tar.Header{Name: "dev/x", Typeflag: tar.TypeChar}, want: "unsupported"},
		{name: "escaping hardlink", header: tar.Header{Name: "usr/bin/x", Typeflag: tar.TypeLink, Linkname: "../etc/x"}, want: "invalid component"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := archiveInspectionTar(t, []tar.Header{test.header}, []string{""})
			_, err := InspectArchiveFileListV1(context.Background(), bytes.NewReader(stream), []string{"/opt/reploy/providers/python/app"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestInspectArchiveFileListV1IncludesRepeatedPathsInOrder(t *testing.T) {
	stream := archiveInspectionTar(t, []tar.Header{
		{Name: "./usr/bin/x", Typeflag: tar.TypeReg, Size: 1},
		{Name: "./usr/bin/x", Typeflag: tar.TypeReg, Size: 1},
	}, []string{"a", "b"})
	got, err := InspectArchiveFileListV1(context.Background(), bytes.NewReader(stream), []string{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := ArchiveFileListDigestV1([]ArchiveMemberV1{
		{Path: "/usr/bin/x", Kind: "regular", LinkTarget: ""},
		{Path: "/usr/bin/x", Kind: "regular", LinkTarget: ""},
	})
	if err != nil || got != want {
		t.Fatalf("got %s, want %s, err %v", got, want, err)
	}
}

func TestInspectArchiveFileListV1HonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectArchiveFileListV1(ctx, bytes.NewReader(nil), []string{}); err == nil {
		t.Fatal("canceled inspection succeeded")
	}
}

func TestInspectArchiveFileListV1RejectsTruncatedMemberBody(t *testing.T) {
	stream := archiveInspectionTar(t, []tar.Header{{Name: "usr/share/demo", Typeflag: tar.TypeReg, Size: 16}}, []string{"0123456789abcdef"})
	stream = stream[:tarBlockSize+8]
	if _, err := InspectArchiveFileListV1(context.Background(), bytes.NewReader(stream), []string{}); err == nil {
		t.Fatal("truncated archive succeeded")
	}
}

const tarBlockSize = 512

func archiveInspectionTar(t *testing.T, headers []tar.Header, bodies []string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for index := range headers {
		header := headers[index]
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if bodies[index] != "" {
			if _, err := writer.Write([]byte(bodies[index])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
