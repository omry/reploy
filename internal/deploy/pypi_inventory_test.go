package deploy

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPyPIWheelBlueprintAccessInventoriesBeforeReadingOrPublishing(t *testing.T) {
	for _, test := range []struct {
		name      string
		members   [][2]string
		wantError string
	}{
		{name: "unsafe unrelated member", members: [][2]string{{"demo_pkg/reploy/demo.blueprint.yaml", aptOnlyBlueprintFixture}, {"../outside", "hostile"}}, wantError: "wheel archive member"},
		{name: "duplicate blueprint", members: [][2]string{{"demo_pkg/reploy/demo.blueprint.yaml", aptOnlyBlueprintFixture}, {"demo_pkg/reploy/demo.blueprint.yaml", aptOnlyBlueprintFixture}}, wantError: "duplicate normalized path"},
		{name: "directory conflict", members: [][2]string{{"demo_pkg", "hostile"}, {"demo_pkg/reploy/demo.blueprint.yaml", aptOnlyBlueprintFixture}}, wantError: "directory prefix"},
		{name: "portable directory alias", members: [][2]string{{"demo_pkg/reploy/demo.blueprint.yaml", aptOnlyBlueprintFixture}, {"demo_pkg/reploy/Bin/a", "first"}, {"demo_pkg/reploy/bin/b", "second"}}, wantError: "aliases"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cacheRoot := t.TempDir()
			wheelPath := filepath.Join(t.TempDir(), "demo.whl")
			writePackWheelMembers(t, wheelPath, test.members)
			_, err := readBlueprintContentFromWheel(wheelPath, "demo_pkg/reploy/demo.blueprint.yaml")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("blueprint read error = %v", err)
			}
			if _, err := extractPackFromWheel(cacheRoot, "demo-pkg", "1.2.3", "hash", wheelPath, "demo_pkg/reploy/demo.blueprint.yaml"); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("blueprint extraction error = %v", err)
			}
			if entries, err := os.ReadDir(cacheRoot); err != nil && !os.IsNotExist(err) {
				t.Fatalf("inspect cache after rejected wheel: %v", err)
			} else if len(entries) != 0 {
				t.Fatalf("unsafe wheel published cache contents: %#v", entries)
			}
		})
	}
}

func TestPyPIWheelBlueprintAccessPreservesOrdinaryBlueprint(t *testing.T) {
	wheelPath := filepath.Join(t.TempDir(), "demo.whl")
	blueprintPath := "demo_pkg/reploy/demo.blueprint.yaml"
	writePackWheelMembers(t, wheelPath, [][2]string{{blueprintPath, aptOnlyBlueprintFixture}, {"demo_pkg/reploy/README", "ordinary"}})

	content, err := readBlueprintContentFromWheel(wheelPath, blueprintPath)
	if err != nil || string(content) != aptOnlyBlueprintFixture {
		t.Fatalf("blueprint content = %q, %v", content, err)
	}
	cacheRoot := t.TempDir()
	extracted, err := extractPackFromWheel(cacheRoot, "demo-pkg", "1.2.3", "hash", wheelPath, blueprintPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(extracted)
	if err != nil || string(got) != aptOnlyBlueprintFixture {
		t.Fatalf("extracted blueprint = %q, %v", got, err)
	}
}

func writePackWheelMembers(t *testing.T, filename string, members [][2]string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for _, member := range members {
		entry, err := archive.Create(member[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(member[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
