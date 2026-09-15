package python

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the callers of wheelinventory. In particular, an
// unrelated malformed member must be rejected before any wheel metadata is
// consumed.
func TestPreparedWheelInspectionRunsStructuralInventoryBeforeMetadata(t *testing.T) {
	for _, name := range []string{"prepared", "source"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			wheelPath := filepath.Join(dir, "demo-1-py3-none-any.whl")
			writeWheelWithUnrelatedMember(t, wheelPath, "../outside", "hostile")

			if name == "prepared" {
				_, err := InspectPreparedWheelDistributionsV1(context.Background(), dir)
				if err == nil || !strings.Contains(err.Error(), "wheel archive member") {
					t.Fatalf("prepared inspection error = %v", err)
				}
				return
			}
			if _, _, err := DescribeSourceWheelFileV1(wheelPath, "source-wheels/demo-1-py3-none-any.whl"); err == nil || !strings.Contains(err.Error(), "wheel archive member") {
				t.Fatalf("source inspection error = %v", err)
			}
		})
	}
}

func TestWheelDependencyInspectionRunsStructuralInventoryBeforeMetadata(t *testing.T) {
	wheelPath := filepath.Join(t.TempDir(), "demo-1-py3-none-any.whl")
	writeWheelWithUnrelatedMember(t, wheelPath, "demo-1.dist-info", "hostile")
	if _, err := InspectWheelDeclaredDependenciesV1(wheelPath, []string{"dependency"}); err == nil || !strings.Contains(err.Error(), "directory prefix") {
		t.Fatalf("dependency inspection error = %v", err)
	}
}

func TestWheelInventoryCallersPreserveOrdinaryMetadata(t *testing.T) {
	dir := t.TempDir()
	wheelPath := filepath.Join(dir, "demo-1-py3-none-any.whl")
	writeTestWheelWithDependencies(t, dir, filepath.Base(wheelPath), "Demo", "1", []string{"dependency>=1"})

	distributions, err := InspectPreparedWheelDistributionsV1(context.Background(), dir)
	if err != nil || len(distributions) != 1 || distributions[0] != "demo" {
		t.Fatalf("prepared inspection = %#v, %v", distributions, err)
	}
	descriptor, metadata, err := DescribeSourceWheelFileV1(wheelPath, "source-wheels/"+filepath.Base(wheelPath))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Distribution != "demo" || metadata.Version != "1" || len(metadata.Tags) != 1 || metadata.Tags[0] != "py3-none-any" {
		t.Fatalf("source metadata = %#v", metadata)
	}
	if _, err := InspectSourceWheelFileV1(wheelPath, descriptor); err != nil {
		t.Fatal(err)
	}
	dependencies, err := InspectWheelDeclaredDependenciesV1(wheelPath, []string{"dependency"})
	if err != nil || len(dependencies) != 1 || dependencies[0] != "dependency" {
		t.Fatalf("dependencies = %#v, %v", dependencies, err)
	}
}

func writeWheelWithUnrelatedMember(t *testing.T, filename, unrelated, content string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	// Keep the consumed metadata malformed so a metadata-first caller would
	// report a different error than the structural inventory.
	writeZipFile(t, archive, "demo-1.dist-info/METADATA", "Metadata-Version: 2.1\nVersion: 1\n\n")
	writeZipFile(t, archive, "demo-1.dist-info/WHEEL", "Wheel-Version: 1.0\nTag: py3-none-any\n\n")
	writeZipFile(t, archive, unrelated, content)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
