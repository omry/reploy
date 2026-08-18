package toolcatalog

import (
	"encoding/json"

	"github.com/omry/reploy/internal/canonical"
	"strings"
	"testing"
	"testing/fstest"
)

// catalogTestFilesV1 builds an injected filesystem holding a complete, closed
// catalog. References carry the true digest of the record they name, computed
// exactly as decoding computes it, so the fixture exercises the real resolution
// path rather than a placeholder that could never appear in a real catalog.
func catalogTestFilesV1(t *testing.T) fstest.MapFS {
	t.Helper()
	const release = "tool:demo/releases/1.2.3"
	files := fstest.MapFS{}
	digests := map[string]canonical.Digest{}

	place := func(relative string, value any) canonical.Digest {
		digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, value)
		if err != nil {
			t.Fatalf("digest %s: %v", relative, err)
		}
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", relative, err)
		}
		files["catalog/"+relative] = &fstest.MapFile{Data: payload}
		digests[recordIDV1(value)] = digest
		return digest
	}
	ref := func(id string) RecordReferenceV1 {
		digest, found := digests[id]
		if !found {
			t.Fatalf("reference %q has no placed record", id)
		}
		return RecordReferenceV1{ID: id, Digest: digest}
	}

	values := validRecordValuesV1()
	// Leaves first, so every reference can carry a digest that already exists.
	profile := values[10].(*ValidationProfileRecordV1)
	place("demo/releases/1.2.3/validation/profiles/default.json", profile)
	fixture := values[9].(*IntegrationFixtureRecordV1)
	place("demo/releases/1.2.3/validation/fixtures/debian-12-amd64.json", fixture)
	// The sample target advertises no binding, so a binding contract and artifact
	// would be orphaned catalog data. Reachability rejects orphans, so the
	// fixture stays closed rather than carrying records nothing selects.

	payload := &PayloadRecordV1{Schema: PayloadRecordSchemaV1,
		ID: release + "/payloads/demo-linux-amd64", Name: "demo",
		Revision: "1", UpstreamVersion: "1.2.3", Platform: "linux/amd64",
		LogicalPath: "tools/demo/demo.tar.gz", Kind: "jdk-archive",
		Size: "42", SHA256: recordTestDigest, Resolver: "https-sha256",
		Entries: "2", UnpackedSize: "84", InstallDirectory: "demo-1",
		ArchiveRoot: "demo", Executable: "demo/bin/demo"}
	place("demo/releases/1.2.3/payloads/demo-linux-amd64.json", payload)

	source := *(values[7].(*ArtifactSourceRecordV1))
	source.SHA256 = recordTestDigest
	source.Size = "42"
	place("demo/releases/1.2.3/revisions/1/sources/demo-linux-amd64.json", &source)

	contract := values[2].(*ReleaseContractV1)
	place("demo/releases/1.2.3/contract.json", contract)

	target := *(values[3].(*TargetRecordV1))
	target.ValidationProfile = ref(profile.ID)
	target.IntegrationFixtures = []RecordReferenceV1{ref(fixture.ID)}
	target.Payloads = []RecordReferenceV1{ref(payload.ID)}
	place("demo/releases/1.2.3/targets/debian/12/amd64.json", &target)

	manifest := *(values[1].(*ReleaseManifestV1))
	manifest.Contract = ref(contract.ID)
	manifest.ValidationProfile = ref(profile.ID)
	manifest.Targets = []RecordReferenceV1{ref(target.ID)}
	manifest.ArtifactSources = []ArtifactSourceMappingV1{{
		ArtifactSHA256: recordTestDigest,
		Artifact:       ref(payload.ID),
		Source:         ref(source.ID),
	}}
	place("demo/releases/1.2.3/revisions/1/manifest.json", &manifest)

	tool := *(values[0].(*ToolRecordV1))
	tool.Releases = []RecordReferenceV1{ref(manifest.ID)}
	place("demo/tool.json", &tool)
	return files
}

func TestLoadCatalogAcceptsAWellFormedCatalogV1(t *testing.T) {
	catalog, err := loadCatalogV1(catalogTestFilesV1(t), "catalog")
	if err != nil {
		t.Fatalf("a well-formed catalog was rejected: %v", err)
	}
	if got := strings.Join(catalog.Names(), ","); got != "demo" {
		t.Errorf("catalog names = %q, want demo", got)
	}
	if len(catalog.records) == 0 {
		t.Error("catalog loaded no records")
	}
}

func TestLoadCatalogRejectsMisplacedAndMalformedEntriesV1(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(fstest.MapFS)
		wantSub string
	}{
		{name: "non JSON entry", wantSub: "must be a JSON file",
			mutate: func(f fstest.MapFS) { f["catalog/demo/notes.txt"] = &fstest.MapFile{Data: []byte("x")} }},
		{name: "record outside its tool namespace", wantSub: "must live below",
			mutate: func(f fstest.MapFS) {
				f["catalog/other/releases/1.2.3/contract.json"] = f["catalog/demo/releases/1.2.3/contract.json"]
				delete(f, "catalog/demo/releases/1.2.3/contract.json")
			}},
		{name: "tool record at the wrong path", wantSub: "must use path",
			mutate: func(f fstest.MapFS) {
				f["catalog/demo/elsewhere.json"] = f["catalog/demo/tool.json"]
				delete(f, "catalog/demo/tool.json")
			}},
		{name: "undecodable record", wantSub: "decode",
			mutate: func(f fstest.MapFS) {
				f["catalog/demo/broken.json"] = &fstest.MapFile{Data: []byte("{")}
			}},
		// Record validation rejects a non-tool-qualified ID before the loader's
		// own tool-name extraction is reached, so that extraction is defensive.
		// Its own behaviour is covered directly by TestRecordToolNameV1.
		{name: "record whose ID declares no tool", wantSub: "tool-qualified ID",
			mutate: func(f fstest.MapFS) {
				stray := *(validRecordValuesV1()[4].(*BindingContractV1))
				stray.ID = "notatool/bindings/python/contract"
				payload, err := json.Marshal(&stray)
				if err != nil {
					panic(err)
				}
				f["catalog/demo/stray.json"] = &fstest.MapFile{Data: payload}
			}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			files := catalogTestFilesV1(t)
			testCase.mutate(files)
			_, err := loadCatalogV1(files, "catalog")
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func TestLoadCatalogRejectsAnEmptyCatalogV1(t *testing.T) {
	if _, err := loadCatalogV1(fstest.MapFS{}, "catalog"); err == nil {
		t.Error("an empty catalog loaded")
	}
}

// The loader declares no aggregate ceiling, so a wide catalog is legal. What is
// bounded is each record's own parse, which fails before the record is decoded.
func TestLoadCatalogBoundsEachRecordRatherThanTheCatalogV1(t *testing.T) {
	files := catalogTestFilesV1(t)
	oversized := make([]byte, maxDefinitionFileBytes+1)
	for index := range oversized {
		oversized[index] = 'x'
	}
	files["catalog/demo/huge.json"] = &fstest.MapFile{Data: oversized}
	_, err := loadCatalogV1(files, "catalog")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("oversized record error = %v, want a per-record byte rejection", err)
	}

	// The same catalog with many small extra records is not rejected for being
	// wide: no record-count or aggregate-byte ceiling exists.
	files = catalogTestFilesV1(t)
	if _, err := loadCatalogV1(files, "catalog"); err != nil {
		t.Fatalf("baseline catalog rejected: %v", err)
	}
}

func TestRecordToolNameV1(t *testing.T) {
	for _, testCase := range []struct {
		id   string
		want string
		ok   bool
	}{
		{id: "tool:demo/releases/1.2.3/contract", want: "demo", ok: true},
		{id: "tool:demo", want: "demo", ok: true},
		{id: "demo/releases", ok: false},
		{id: "tool:Demo", ok: false},
		{id: "tool:", ok: false},
	} {
		name, err := recordToolNameV1(testCase.id)
		if testCase.ok && (err != nil || name != testCase.want) {
			t.Errorf("recordToolNameV1(%q) = %q, %v", testCase.id, name, err)
		}
		if !testCase.ok && err == nil {
			t.Errorf("recordToolNameV1(%q) accepted", testCase.id)
		}
	}
}

// Depth bounds recursion. A record referencing itself is a cycle, and a chain
// deeper than the limit fails rather than exhausting the stack.
func TestVerifyReferenceDepthBoundsRecursionV1(t *testing.T) {
	selfReferential := &ToolRecordV1{Schema: ToolRecordSchemaV1, ID: "tool:demo"}
	selfReferential.Releases = []RecordReferenceV1{recordTestReference("tool:demo")}
	catalog := &CatalogV1{
		records: map[string]loadedRecordV1{"tool:demo": {ID: "tool:demo",
			Schema: ToolRecordSchemaV1, Digest: recordTestDigest, Value: selfReferential}},
		tools: map[string]string{"demo": "tool:demo"},
	}
	err := catalog.verifyReferenceDepthV1()
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("self-reference error = %v, want a cycle rejection", err)
	}

	// A chain longer than the depth limit fails on depth rather than recursing.
	records := map[string]loadedRecordV1{}
	const length = maxCatalogGraphDepthV1 + 5
	for index := 0; index < length; index++ {
		id := "tool:demo/releases/1.2.3/revisions/" + strings.Repeat("1", index+1) + "/manifest"
		next := "tool:demo/releases/1.2.3/revisions/" + strings.Repeat("1", index+2) + "/manifest"
		manifest := &ReleaseManifestV1{Schema: ReleaseManifestSchemaV1, ID: id,
			Targets: []RecordReferenceV1{recordTestReference(next)}}
		records[id] = loadedRecordV1{ID: id, Schema: manifest.Schema, Digest: recordTestDigest, Value: manifest}
	}
	deep := &CatalogV1{records: records, tools: map[string]string{}}
	err = deep.verifyReferenceDepthV1()
	if err == nil || !strings.Contains(err.Error(), "exceeds depth") {
		t.Errorf("deep chain error = %v, want a depth rejection", err)
	}
}

// The loader must enumerate the plural model. Enumerating the parked singular
// shape would omit every binding artifact past the first and every
// selection-scoped package set from traversal.
func TestCatalogReferencesEnumeratesThePluralModelV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	target := &TargetRecordV1{Schema: TargetRecordSchemaV1, ID: release + "/targets/debian/12/amd64",
		ValidationProfile:   recordTestReference(release + "/validation/profiles/default"),
		IntegrationFixtures: []RecordReferenceV1{recordTestReference(release + "/validation/fixtures/a"), recordTestReference(release + "/validation/fixtures/b")},
		PackageSets:         []RecordReferenceV1{recordTestReference(release + "/package-sets/base")},
		Bindings: []TargetBindingV1{{Name: "python",
			Contract:    recordTestReference(release + "/bindings/python/contract"),
			Artifacts:   []RecordReferenceV1{recordTestReference(release + "/bindings/python/artifacts/linux-amd64"), recordTestReference(release + "/bindings/python/artifacts/linux-arm64")},
			PackageSets: []RecordReferenceV1{recordTestReference(release + "/package-sets/python")}}},
		Payloads: []RecordReferenceV1{recordTestReference(release + "/payloads/demo-linux-amd64")},
		Selections: []TargetSelectionV1{{Name: "chromium",
			Payloads:    []RecordReferenceV1{recordTestReference(release + "/payloads/chromium/chromium-linux-amd64")},
			PackageSets: []RecordReferenceV1{recordTestReference(release + "/package-sets/chromium")}}},
	}
	seen := map[string]struct{}{}
	for _, edge := range catalogReferencesV1(target) {
		seen[edge.Reference.ID] = struct{}{}
	}
	for _, required := range []string{
		release + "/validation/fixtures/a", release + "/validation/fixtures/b",
		release + "/bindings/python/artifacts/linux-amd64", release + "/bindings/python/artifacts/linux-arm64",
		release + "/package-sets/python", release + "/package-sets/chromium",
		release + "/payloads/chromium/chromium-linux-amd64",
	} {
		if _, found := seen[required]; !found {
			t.Errorf("reference enumeration omitted %q", required)
		}
	}
	if len(seen) != 11 {
		t.Errorf("enumerated %d distinct references, want 11", len(seen))
	}

	// A binding artifact carries its own contract reference, so the edge exists.
	artifact := &BindingArtifactRecordV1{Schema: BindingArtifactSchemaV1,
		ID: release + "/bindings/python/artifacts/linux-amd64", Binding: "python",
		Contract: recordTestReference(release + "/bindings/python/contract")}
	edges := catalogReferencesV1(artifact)
	if len(edges) != 1 || edges[0].Reference.ID != release+"/bindings/python/contract" {
		t.Errorf("binding artifact edges = %+v", edges)
	}
}

// Loading gives the release graph walker its production caller: a catalog whose
// records are individually valid but whose graph is broken must fail to load.
func TestLoadCatalogInvokesTheReleaseGraphWalkerV1(t *testing.T) {
	files := catalogTestFilesV1(t)
	if _, err := loadCatalogV1(files, "catalog"); err != nil {
		t.Fatalf("baseline catalog rejected: %v", err)
	}

	// Remove the artifact source mapping's source record. Every remaining record
	// is still individually valid; only the resolved graph is broken.
	var removed bool
	for name := range files {
		if strings.Contains(name, "revisions_") && strings.Contains(name, "sources") {
			delete(files, name)
			removed = true
			break
		}
	}
	if !removed {
		t.Skip("fixture does not contain a source record to remove")
	}
	if _, err := loadCatalogV1(files, "catalog"); err == nil {
		t.Error("a catalog with a broken release graph loaded successfully")
	}
}
