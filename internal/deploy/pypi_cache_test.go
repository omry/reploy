package deploy

import (
	"path/filepath"
	"strings"
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
