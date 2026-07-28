package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPyPICachePathsContainUntrustedVersion(t *testing.T) {
	cacheRoot := t.TempDir()
	version := "../../../../outside"
	paths := []string{
		pypiWheelCachePath(cacheRoot, "demo-pkg", version, strings.Repeat("a", 64), "demo.whl"),
		pypiBlueprintCacheDir(cacheRoot, "demo-pkg", version, strings.Repeat("b", 64), "demo_pkg/reploy"),
	}
	for _, path := range paths {
		relative, err := filepath.Rel(cacheRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("untrusted version escaped cache root: %q", path)
		}
		if strings.Contains(filepath.ToSlash(relative), version) {
			t.Fatalf("cache path contains raw untrusted version: %q", path)
		}
	}
}

func TestExtractPackFromWheelRepairsTamperedBlueprint(t *testing.T) {
	cacheRoot := t.TempDir()
	blueprintPath := "demo_pkg/reploy/demo.blueprint.yaml"
	wheel := testPackWheelWithFiles(t, map[string]string{
		blueprintPath: aptOnlyBlueprintFixture,
	})
	wheelPath := filepath.Join(t.TempDir(), "demo.whl")
	if err := os.WriteFile(wheelPath, wheel, 0o644); err != nil {
		t.Fatal(err)
	}

	extracted, err := extractPackFromWheel(
		cacheRoot, "demo-pkg", "1.2.3", HashBytes(wheel), wheelPath, blueprintPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extracted, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	repaired, err := extractPackFromWheel(
		cacheRoot, "demo-pkg", "1.2.3", HashBytes(wheel), wheelPath, blueprintPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(repaired)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != aptOnlyBlueprintFixture {
		t.Fatalf("repaired blueprint = %q", content)
	}
}

func TestExtractPackFromWheelReplacesSymlinkedBlueprint(t *testing.T) {
	cacheRoot := t.TempDir()
	blueprintPath := "demo_pkg/reploy/demo.blueprint.yaml"
	wheel := testPackWheelWithFiles(t, map[string]string{
		blueprintPath: aptOnlyBlueprintFixture,
	})
	wheelPath := filepath.Join(t.TempDir(), "demo.whl")
	if err := os.WriteFile(wheelPath, wheel, 0o644); err != nil {
		t.Fatal(err)
	}

	extracted, err := extractPackFromWheel(
		cacheRoot, "demo-pkg", "1.2.3", HashBytes(wheel), wheelPath, blueprintPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.blueprint.yaml")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(extracted); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, extracted); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	repaired, err := extractPackFromWheel(
		cacheRoot, "demo-pkg", "1.2.3", HashBytes(wheel), wheelPath, blueprintPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(repaired)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("repaired blueprint mode = %v", info.Mode())
	}
	outsideContent, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(outsideContent) != "outside" {
		t.Fatalf("symlink target changed to %q", outsideContent)
	}
}

func TestExtractPackFromWheelRejectsSymlinkedCacheDirectory(t *testing.T) {
	cacheRoot := t.TempDir()
	blueprintPath := "demo_pkg/reploy/demo.blueprint.yaml"
	wheel := testPackWheelWithFiles(t, map[string]string{
		blueprintPath: aptOnlyBlueprintFixture,
	})
	wheelPath := filepath.Join(t.TempDir(), "demo.whl")
	if err := os.WriteFile(wheelPath, wheel, 0o644); err != nil {
		t.Fatal(err)
	}

	extracted, err := extractPackFromWheel(
		cacheRoot, "demo-pkg", "1.2.3", HashBytes(wheel), wheelPath, blueprintPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Dir(extracted)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, cacheDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err = extractPackFromWheel(
		cacheRoot, "demo-pkg", "1.2.3", HashBytes(wheel), wheelPath, blueprintPath,
	)
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, filepath.Base(extracted))); !os.IsNotExist(statErr) {
		t.Fatalf("cache repair wrote through directory symlink: %v", statErr)
	}
}

func TestExtractPackFromWheelPublishesConcurrentExtractionAtomically(t *testing.T) {
	cacheRoot := t.TempDir()
	blueprintPath := "demo_pkg/reploy/demo.blueprint.yaml"
	wheel := testPackWheelWithFiles(t, map[string]string{
		blueprintPath: aptOnlyBlueprintFixture,
	})
	wheelPath := filepath.Join(t.TempDir(), "demo.whl")
	if err := os.WriteFile(wheelPath, wheel, 0o644); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	start := make(chan struct{})
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			path, err := extractPackFromWheel(
				cacheRoot, "demo-pkg", "1.2.3", HashBytes(wheel), wheelPath, blueprintPath,
			)
			if err == nil {
				var content []byte
				content, err = os.ReadFile(path)
				if err == nil && string(content) != aptOnlyBlueprintFixture {
					err = &unexpectedBlueprintContentError{content: string(content)}
				}
			}
			errors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

type unexpectedBlueprintContentError struct {
	content string
}

func (err *unexpectedBlueprintContentError) Error() string {
	return "unexpected blueprint content: " + err.content
}
