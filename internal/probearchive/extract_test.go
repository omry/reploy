package probearchive

import (
	"context"
	"crypto/sha256"
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
	if err := Append(executable, inputs); err != nil {
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
	if err := Append(executable, testHelpers(t, dir)); err != nil {
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
	if err := Append(executable, testHelpers(t, dir)); err != nil {
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
	if _, err := writeExtracted(ctx, workspace, entry, reader); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("mid-stream cancellation error = %v", err)
	}
	assertNoExtractedProbe(t, workspace)
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
