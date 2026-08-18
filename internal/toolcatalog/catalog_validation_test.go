package toolcatalog

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omry/reploy/internal/canonical"
)

// catalogFromFilesV1 loads a fixture catalog, failing the test if it does not
// load, so a mutation's effect is attributable to the mutation.
func catalogFromFilesV1(t *testing.T, files fstest.MapFS) (*CatalogV1, error) {
	t.Helper()
	return loadCatalogV1(files, "catalog")
}

func TestCatalogReferencesMustResolveExactlyV1(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(*testing.T, fstest.MapFS)
		wantSub string
	}{
		{name: "reference to a missing record", wantSub: "references missing record",
			mutate: func(t *testing.T, f fstest.MapFS) {
				delete(f, "catalog/demo/releases/1.2.3/validation/fixtures/debian-12-amd64.json")
			}},
		{name: "reference carrying the wrong digest", wantSub: "want",
			mutate: func(t *testing.T, f fstest.MapFS) {
				var manifest ReleaseManifestV1
				if err := json.Unmarshal(f["catalog/demo/releases/1.2.3/revisions/1/manifest.json"].Data, &manifest); err != nil {
					t.Fatal(err)
				}
				manifest.Contract.Digest = canonical.Digest("sha256:" + strings.Repeat("d", 64))
				payload, err := json.Marshal(&manifest)
				if err != nil {
					t.Fatal(err)
				}
				f["catalog/demo/releases/1.2.3/revisions/1/manifest.json"] = &fstest.MapFile{Data: payload}
			}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			files := catalogTestFilesV1(t)
			testCase.mutate(t, files)
			_, err := catalogFromFilesV1(t, files)
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

// An orphan is catalog data no request can select, so it can drift out of
// agreement with the reachable records without anything failing.
func TestCatalogRejectsUnreachableRecordsV1(t *testing.T) {
	files := catalogTestFilesV1(t)
	if _, err := catalogFromFilesV1(t, files); err != nil {
		t.Fatalf("baseline catalog rejected: %v", err)
	}
	orphan := *(validRecordValuesV1()[8].(*NativePackageSetV1))
	payload, err := json.Marshal(&orphan)
	if err != nil {
		t.Fatal(err)
	}
	files["catalog/demo/releases/1.2.3/package-sets/orphan.json"] = &fstest.MapFile{Data: payload}
	_, err = catalogFromFilesV1(t, files)
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("orphan error = %v, want an unreachable rejection", err)
	}
}

func TestReleaseNamespaceV1(t *testing.T) {
	for _, testCase := range []struct {
		id   string
		want string
		ok   bool
	}{
		{id: "tool:demo/releases/1.2.3/contract", want: "tool:demo/releases/1.2.3", ok: true},
		{id: "tool:demo/releases/1.2.3", want: "tool:demo/releases/1.2.3", ok: true},
		{id: "tool:demo", ok: false},
		{id: "tool:demo/other/1.2.3", ok: false},
	} {
		got, err := releaseNamespaceV1(testCase.id)
		if testCase.ok && (err != nil || got != testCase.want) {
			t.Errorf("releaseNamespaceV1(%q) = %q, %v", testCase.id, got, err)
		}
		if !testCase.ok && err == nil {
			t.Errorf("releaseNamespaceV1(%q) accepted", testCase.id)
		}
	}
}

// Every artifact the catalog holds needs exactly one acquisition source, and
// records sharing a digest must agree on size across the whole catalog rather
// than only inside one manifest.
func TestCatalogAcquisitionMappingsV1(t *testing.T) {
	files := catalogTestFilesV1(t)
	if _, err := catalogFromFilesV1(t, files); err != nil {
		t.Fatalf("baseline catalog rejected: %v", err)
	}

	// Drop the manifest's only source mapping: the payload then has none.
	var manifest ReleaseManifestV1
	if err := json.Unmarshal(files["catalog/demo/releases/1.2.3/revisions/1/manifest.json"].Data, &manifest); err != nil {
		t.Fatal(err)
	}
	stripped := manifest
	stripped.ArtifactSources = []ArtifactSourceMappingV1{}
	payload, err := json.Marshal(&stripped)
	if err != nil {
		t.Fatal(err)
	}
	files["catalog/demo/releases/1.2.3/revisions/1/manifest.json"] = &fstest.MapFile{Data: payload}
	_, err = catalogFromFilesV1(t, files)
	if err == nil {
		t.Error("an artifact with no acquisition source mapping was accepted")
	}
}

func TestArtifactContentV1(t *testing.T) {
	payload := &PayloadRecordV1{SHA256: recordTestDigest, Size: "42"}
	digest, size, ok := artifactContentV1(payload)
	if !ok || digest != recordTestDigest || size != "42" {
		t.Errorf("payload content = %q, %q, %v", digest, size, ok)
	}
	artifact := &BindingArtifactRecordV1{SHA256: recordTestDigest, Size: "7"}
	if digest, size, ok := artifactContentV1(artifact); !ok || digest != recordTestDigest || size != "7" {
		t.Errorf("binding artifact content = %q, %q, %v", digest, size, ok)
	}
	if _, _, ok := artifactContentV1(&ReleaseContractV1{}); ok {
		t.Error("a release contract reported artifact content")
	}
}

func TestPackageRequirementNameV1(t *testing.T) {
	for _, testCase := range []struct {
		requirement string
		want        string
		ok          bool
	}{
		{requirement: "libdemo=1.0", want: "libdemo", ok: true},
		{requirement: "libdemo", want: "libdemo", ok: true},
		{requirement: "=1.0", ok: false},
	} {
		got, err := packageRequirementNameV1(testCase.requirement)
		if testCase.ok && (err != nil || got != testCase.want) {
			t.Errorf("packageRequirementNameV1(%q) = %q, %v", testCase.requirement, got, err)
		}
		if !testCase.ok && err == nil {
			t.Errorf("packageRequirementNameV1(%q) accepted", testCase.requirement)
		}
	}
}

func TestOverlappingRecordPathsV1(t *testing.T) {
	if !overlappingRecordPathsV1("a", "a/b") {
		t.Error("nested paths reported as non-overlapping")
	}
	if overlappingRecordPathsV1("a", "ab") {
		t.Error("a non-boundary prefix reported as overlapping")
	}
}
