package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareProviderInstallFileCandidatesV1WritesNoLiveFilesAndCleansUp(t *testing.T) {
	root := t.TempDir()
	privateDir := filepath.Join(root, ".reploy")
	if err := os.Mkdir(privateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	candidates := []providerInstallFileCandidateV1{
		{Path: filepath.Join(root, ".reploy", "docker.env"), Content: []byte("IMAGE=demo\n"), Mode: 0o640},
		{Path: filepath.Join(root, ".reploy", "runtime", "compose.yaml"), Content: []byte("services: {}\n"), Mode: 0o644},
	}

	prepared, err := prepareProviderInstallFileCandidatesV1(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Files) != len(candidates) {
		t.Fatalf("prepared files = %#v", prepared.Files)
	}
	for index, file := range prepared.Files {
		if file.FinalPath != candidates[index].Path || file.Mode != candidates[index].Mode {
			t.Fatalf("prepared file %d = %#v", index, file)
		}
		if filepath.Dir(file.TemporaryPath) != privateDir || !strings.HasPrefix(filepath.Base(file.TemporaryPath), ".reploy-install-") {
			t.Fatalf("temporary file %d is not private and adjacent: %s", index, file.TemporaryPath)
		}
		content, err := os.ReadFile(file.TemporaryPath)
		if err != nil || string(content) != string(candidates[index].Content) {
			t.Fatalf("temporary content %d = %q, error=%v", index, content, err)
		}
		info, err := os.Stat(file.TemporaryPath)
		if err != nil {
			t.Fatalf("stat temporary file %d: %v", index, err)
		}
		if info.Mode().Perm() != candidates[index].Mode {
			t.Fatalf("temporary mode %d = %v", index, info.Mode())
		}
		if _, err := os.Lstat(file.FinalPath); !os.IsNotExist(err) {
			t.Fatalf("live destination %d changed: %v", index, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(privateDir, "runtime")); !os.IsNotExist(err) {
		t.Fatalf("preparation created a live parent directory: %v", err)
	}
	if err := prepared.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Cleanup(); err != nil {
		t.Fatalf("repeated cleanup failed: %v", err)
	}
	for _, file := range prepared.Files {
		if _, err := os.Lstat(file.TemporaryPath); !os.IsNotExist(err) {
			t.Fatalf("temporary file survived cleanup: %s", file.TemporaryPath)
		}
	}
}

func TestPrepareProviderInstallFileCandidatesV1RemovesEarlierCandidatesOnFailure(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "z-blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates := []providerInstallFileCandidateV1{
		{Path: filepath.Join(root, "a-file"), Content: []byte("candidate"), Mode: 0o600},
		{Path: filepath.Join(blocker, "child"), Content: []byte("candidate"), Mode: 0o600},
	}

	_, err := prepareProviderInstallFileCandidatesV1(candidates)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("invalid ancestor error = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".reploy-install-") {
			t.Fatalf("failed preparation left temporary file: %s", entry.Name())
		}
	}
}

func TestPrepareProviderInstallFileCandidatesV1RejectsExistingSymlink(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	if err := os.Symlink(filepath.Join(root, "elsewhere"), destination); err != nil {
		t.Fatal(err)
	}

	_, err := prepareProviderInstallFileCandidatesV1([]providerInstallFileCandidateV1{
		{Path: destination, Content: []byte("candidate"), Mode: 0o600},
	})
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink destination error = %v", err)
	}
}

func TestPrepareProviderInstallFileCandidatesV1RejectsIntermediateSymlink(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedDirectory := filepath.Join(root, "linked")
	if err := os.Symlink(realDirectory, linkedDirectory); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := prepareProviderInstallFileCandidatesV1([]providerInstallFileCandidateV1{
		{Path: filepath.Join(linkedDirectory, "nested", "file"), Content: []byte("value"), Mode: 0o600},
	})
	if err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("intermediate symlink error = %v", err)
	}
}

func TestPreparedProviderInstallFilesV1PublishesWithoutBackup(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a", "first")
	second := filepath.Join(root, "b", "second")
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareProviderInstallFileCandidatesV1([]providerInstallFileCandidateV1{
		{Path: first, Content: []byte("new-first"), Mode: 0o640},
		{Path: second, Content: []byte("new-second"), Mode: 0o644},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Cleanup() })
	if err := prepared.Publish(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{first: "new-first", second: "new-second"} {
		content, err := os.ReadFile(path)
		if err != nil || string(content) != want {
			t.Fatalf("published %q content=%q error=%v", path, content, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "backup") {
			t.Fatalf("unexpected backup: %s", entry.Name())
		}
	}
}

func TestPreparedProviderInstallFilesV1LeavesEarlierReplacementAfterFailure(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a")
	blockedParent := filepath.Join(root, "blocked")
	prepared, err := prepareProviderInstallFileCandidatesV1([]providerInstallFileCandidateV1{
		{Path: first, Content: []byte("published"), Mode: 0o600},
		{Path: filepath.Join(blockedParent, "second"), Content: []byte("pending"), Mode: 0o600},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Cleanup() })
	if err := os.WriteFile(blockedParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Publish(); err == nil {
		t.Fatal("expected second-file publication failure")
	}
	content, err := os.ReadFile(first)
	if err != nil || string(content) != "published" {
		t.Fatalf("first publication content=%q error=%v", content, err)
	}
	if _, err := os.Stat(prepared.Files[1].TemporaryPath); err != nil {
		t.Fatalf("unpublished candidate was not retained for cleanup: %v", err)
	}
}
