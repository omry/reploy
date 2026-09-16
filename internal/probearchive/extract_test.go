package probearchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExtractWritesOnlySelectedVerifiedProbe(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("prefix"), 0o755)
	inputs := testHelpers(t, dir)
	if err := Append(executable, testRelease(), inputs, testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Extract(context.Background(), executable, "linux/arm/v7", workspace)
	if err != nil {
		t.Fatal(err)
	}
	wantContent, err := os.ReadFile(inputs[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(wantContent))
	if string(content) != string(wantContent) || result.Platform != "linux/arm/v7" || result.Path != filepath.Join(workspace, ExtractedFileName) || result.Size != fmt.Sprint(len(wantContent)) || string(result.SHA256) != wantDigest {
		t.Fatalf("result = %#v; content = %q", result, content)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ExtractedFileName {
		t.Fatalf("workspace entries = %#v", entries)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(result.Path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o555 {
			t.Fatalf("extracted mode = %04o", info.Mode().Perm())
		}
	}
}

func TestExtractRejectsUnsupportedPlatformWorkspaceSymlinkAndExistingTarget(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("prefix"), 0o755)
	if err := Append(executable, testRelease(), testHelpers(t, dir), testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), executable, "linux/riscv64", workspace); err == nil || !strings.Contains(err.Error(), "supports") {
		t.Fatalf("unsupported platform error = %v", err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(dir, "workspace-link")
		if err := os.Symlink(workspace, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Extract(context.Background(), executable, "linux/amd64", link); err == nil || !strings.Contains(err.Error(), "real directory") {
			t.Fatalf("workspace symlink error = %v", err)
		}
	}
	existing := writeTestFile(t, workspace, ExtractedFileName, []byte("keep"), 0o600)
	if _, err := Extract(context.Background(), executable, "linux/amd64", workspace); err == nil {
		t.Fatal("existing extraction target was overwritten")
	}
	content, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep" {
		t.Fatalf("existing target = %q", content)
	}
}

func TestExtractCancellationAndCorruptionLeaveNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("prefix"), 0o755)
	if err := Append(executable, testRelease(), testHelpers(t, dir), testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "cancelled")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Extract(ctx, executable, "linux/amd64", workspace); err == nil {
		t.Fatal("cancelled extraction succeeded")
	}
	assertNoExtractedProbe(t, workspace)

	archive, err := open(executable)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := archive.entries[helperArchivePath("linux/amd64")].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(executable, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{0xff}, offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	corruptWorkspace := filepath.Join(dir, "corrupt")
	if err := os.Mkdir(corruptWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), executable, "linux/amd64", corruptWorkspace); err == nil {
		t.Fatal("corrupted helper extraction succeeded")
	}
	assertNoExtractedProbe(t, corruptWorkspace)
}

func TestWriteExtractedRemovesFileAfterMidStreamCancellation(t *testing.T) {
	workspace := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterRead{cancel: cancel, content: []byte("partial helper bytes")}
	entry := EntryV1{Platform: "linux/amd64", Size: "999"}
	if _, err := writeExtracted(ctx, workspace, ExtractedFileName, "probe", entry, reader); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("mid-stream cancellation error = %v", err)
	}
	assertNoExtractedProbe(t, workspace)
}

func TestExtractSessionClientWritesMatchingReleaseExecutable(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("prefix"), 0o755)
	sessionClients := testSessionClients(t, dir)
	if err := Append(executable, testRelease(), testHelpers(t, dir), sessionClients); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "controller-workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := ExtractSessionClient(context.Background(), executable, "linux/arm64", workspace)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(sessionClients[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) || result.Platform != "linux/arm64" || result.Path != filepath.Join(workspace, ExtractedSessionClientFileName) || result.Release != testRelease() {
		t.Fatalf("session client extraction = %#v; content = %q", result, got)
	}
}

func TestRuntimeArchiveCallersPreflightCentralRecordMetadata(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("prefix"), 0o755)
	if err := Append(executable, testRelease(), testHelpers(t, dir), testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	archive = appendCentralRecordExtra(t, archive)
	if err := os.WriteFile(executable, archive, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(executable); err == nil || !strings.Contains(err.Error(), "central directory") {
		t.Fatalf("Verify over-limit archive error = %v", err)
	}
	workspace := filepath.Join(dir, "probe-workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), executable, "linux/amd64", workspace); err == nil || !strings.Contains(err.Error(), "central directory") {
		t.Fatalf("Extract over-limit archive error = %v", err)
	}
	clientWorkspace := filepath.Join(dir, "client-workspace")
	if err := os.Mkdir(clientWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractSessionClient(context.Background(), executable, "linux/amd64", clientWorkspace); err == nil || !strings.Contains(err.Error(), "central directory") {
		t.Fatalf("ExtractSessionClient over-limit archive error = %v", err)
	}
	if err := Append(executable, testRelease(), testHelpers(t, dir), testSessionClients(t, dir)); err == nil || !strings.Contains(err.Error(), "central directory") {
		t.Fatalf("Append over-limit archive error = %v", err)
	}
	got, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, archive) {
		t.Fatal("Append changed an existing over-limit archive")
	}
}

func appendCentralRecordExtra(t *testing.T, archive []byte) []byte {
	t.Helper()
	const (
		eocdSignature          = uint32(0x06054b50)
		centralHeaderSignature = uint32(0x02014b50)
		extraPayloadSize       = 4094
	)
	eocd := bytes.LastIndex(archive, []byte{'P', 'K', 5, 6})
	if eocd < 0 || binary.LittleEndian.Uint32(archive[eocd:eocd+4]) != eocdSignature {
		t.Fatal("runtime archive has no EOCD")
	}
	centralOffset := int(binary.LittleEndian.Uint32(archive[eocd+16 : eocd+20]))
	if centralOffset < 0 || centralOffset+46 > eocd || binary.LittleEndian.Uint32(archive[centralOffset:centralOffset+4]) != centralHeaderSignature {
		t.Fatal("runtime archive has no central record")
	}
	nameSize := int(binary.LittleEndian.Uint16(archive[centralOffset+28 : centralOffset+30]))
	extraSize := int(binary.LittleEndian.Uint16(archive[centralOffset+30 : centralOffset+32]))
	extraOffset := centralOffset + 46 + nameSize
	if extraOffset+extraSize > eocd {
		t.Fatal("runtime archive central record is truncated")
	}
	const fieldID = uint16(0xcafe)
	extra := make([]byte, 4+extraPayloadSize)
	binary.LittleEndian.PutUint16(extra[0:2], fieldID)
	binary.LittleEndian.PutUint16(extra[2:4], extraPayloadSize)
	result := make([]byte, 0, len(archive)+len(extra))
	result = append(result, archive[:extraOffset]...)
	result = append(result, extra...)
	result = append(result, archive[extraOffset:]...)
	newEOCD := eocd + len(extra)
	binary.LittleEndian.PutUint16(result[centralOffset+30:centralOffset+32], uint16(extraSize+len(extra)))
	directorySize := binary.LittleEndian.Uint32(result[newEOCD+12 : newEOCD+16])
	binary.LittleEndian.PutUint32(result[newEOCD+12:newEOCD+16], directorySize+uint32(len(extra)))
	return result
}

type cancelAfterRead struct {
	cancel  context.CancelFunc
	content []byte
	done    bool
}

func (reader *cancelAfterRead) Read(buffer []byte) (int, error) {
	if reader.done {
		return 0, io.EOF
	}
	reader.done = true
	count := copy(buffer, reader.content)
	reader.cancel()
	return count, nil
}

func assertNoExtractedProbe(t *testing.T, workspace string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(workspace, ExtractedFileName)); !os.IsNotExist(err) {
		t.Fatalf("partial extracted probe remains: %v", err)
	}
}
