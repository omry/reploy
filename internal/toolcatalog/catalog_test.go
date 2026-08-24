package toolcatalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omry/reploy/internal/canonical"
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
	fixture := *(values[9].(*IntegrationFixtureRecordV1))
	fixture.ValidationProfiles = []RecordReferenceV1{ref(profile.ID)}
	place("demo/releases/1.2.3/validation/fixtures/debian-12-amd64.json", &fixture)
	// The sample target advertises no binding, so a binding contract and artifact
	// would be orphaned catalog data. Reachability rejects orphans, so the
	// fixture stays closed rather than carrying records nothing selects.

	payload := values[6].(*PayloadRecordV1)
	place("demo/releases/1.2.3/payloads/demo-linux-amd64.json", payload)

	source := *(values[7].(*ArtifactSourceRecordV1))
	source.SHA256 = recordTestDigest
	place("demo/releases/1.2.3/revisions/1/sources/demo-linux-amd64.json", &source)

	contract := values[2].(*ReleaseContractV1)
	place("demo/releases/1.2.3/contract.json", contract)

	target := *(values[3].(*TargetRecordV1))
	target.ValidationProfiles = []RecordReferenceV1{ref(profile.ID)}
	target.IntegrationFixtures = []RecordReferenceV1{ref(fixture.ID)}
	target.Payloads = []RecordReferenceV1{ref(payload.ID)}
	place("demo/releases/1.2.3/targets/debian/12/amd64.json", &target)

	manifest := *(values[1].(*ReleaseManifestV1))
	manifest.Contract = ref(contract.ID)
	manifest.ValidationProfiles = []RecordReferenceV1{ref(profile.ID)}
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
	// wide: no record-count or aggregate-byte ceiling exists. Materialize the
	// records rather than merely asserting this property over the baseline.
	files = catalogTestFilesV1(t)
	const width = 1024
	for index := 0; index < width; index++ {
		name := fmt.Sprintf("wide-%04d", index)
		value := *(validRecordValuesV1()[8].(*NativePackageSetV1))
		value.ID = "tool:demo/releases/1.2.3/package-sets/" + name
		payload, marshalErr := json.Marshal(&value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		files["catalog/demo/releases/1.2.3/package-sets/"+name+".json"] = &fstest.MapFile{Data: payload}
	}
	catalog, err := loadCatalogV1(files, "catalog")
	if err != nil {
		t.Fatalf("wide catalog rejected: %v", err)
	}
	if got := len(catalog.records); got != width+8 {
		t.Errorf("wide catalog retained %d records, want %d", got, width+8)
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
	selfKey := recordKeyV1{ID: "tool:demo", Digest: recordTestDigest}
	catalog := &CatalogV1{
		records: map[recordKeyV1]loadedRecordV1{selfKey: {ID: "tool:demo",
			Schema: ToolRecordSchemaV1, Digest: recordTestDigest, Value: selfReferential}},
		tools: map[string]recordKeyV1{"demo": selfKey},
	}
	err := catalog.verifyReferenceDepthV1()
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("self-reference error = %v, want a cycle rejection", err)
	}

	// A chain longer than the depth limit fails on depth rather than recursing.
	records := map[recordKeyV1]loadedRecordV1{}
	const length = maxCatalogGraphDepthV1 + 5
	for index := 0; index < length; index++ {
		id := "tool:demo/releases/1.2.3/revisions/" + strings.Repeat("1", index+1) + "/manifest"
		next := "tool:demo/releases/1.2.3/revisions/" + strings.Repeat("1", index+2) + "/manifest"
		manifest := &ReleaseManifestV1{Schema: ReleaseManifestSchemaV1, ID: id,
			Targets: []RecordReferenceV1{recordTestReference(next)}}
		records[recordKeyV1{ID: id, Digest: recordTestDigest}] = loadedRecordV1{ID: id, Schema: manifest.Schema, Digest: recordTestDigest, Value: manifest}
	}
	deep := &CatalogV1{records: records, tools: map[string]recordKeyV1{}}
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
		ValidationProfiles:  []RecordReferenceV1{recordTestReference(release + "/validation/profiles/target")},
		IntegrationFixtures: []RecordReferenceV1{recordTestReference(release + "/validation/fixtures/a"), recordTestReference(release + "/validation/fixtures/b")},
		PackageSets:         []RecordReferenceV1{recordTestReference(release + "/package-sets/base")},
		Bindings: []TargetBindingV1{{Name: "python",
			Contract:           recordTestReference(release + "/bindings/python/contract"),
			Artifacts:          []RecordReferenceV1{recordTestReference(release + "/bindings/python/artifacts/linux-amd64"), recordTestReference(release + "/bindings/python/artifacts/linux-arm64")},
			Payloads:           []RecordReferenceV1{recordTestReference(release + "/payloads/python-linux-amd64")},
			PackageSets:        []RecordReferenceV1{recordTestReference(release + "/package-sets/python")},
			ValidationProfiles: []RecordReferenceV1{recordTestReference(release + "/validation/profiles/python")}}},
		Payloads: []RecordReferenceV1{recordTestReference(release + "/payloads/demo-linux-amd64")},
		Selections: []TargetSelectionV1{{Dimension: "browser", Value: "chromium",
			Payloads:           []RecordReferenceV1{recordTestReference(release + "/payloads/chromium/chromium-linux-amd64")},
			PackageSets:        []RecordReferenceV1{recordTestReference(release + "/package-sets/chromium")},
			ValidationProfiles: []RecordReferenceV1{recordTestReference(release + "/validation/profiles/chromium")}}},
	}
	seen := map[string]struct{}{}
	for _, edge := range catalogReferencesV1(target) {
		seen[edge.Reference.ID] = struct{}{}
	}
	for _, required := range []string{
		release + "/validation/profiles/target",
		release + "/validation/fixtures/a", release + "/validation/fixtures/b",
		release + "/bindings/python/artifacts/linux-amd64", release + "/bindings/python/artifacts/linux-arm64",
		release + "/payloads/python-linux-amd64", release + "/validation/profiles/python",
		release + "/package-sets/python", release + "/package-sets/chromium",
		release + "/payloads/chromium/chromium-linux-amd64", release + "/validation/profiles/chromium",
	} {
		if _, found := seen[required]; !found {
			t.Errorf("reference enumeration omitted %q", required)
		}
	}
	if len(seen) != 14 {
		t.Errorf("enumerated %d distinct references, want 14", len(seen))
	}

	// A binding artifact carries its own contract reference, so the edge exists.
	artifact := &BindingArtifactRecordV1{Schema: BindingArtifactSchemaV1,
		ID: release + "/bindings/python/artifacts/linux-amd64", Binding: "python",
		Contract: recordTestReference(release + "/bindings/python/contract")}
	edges := catalogReferencesV1(artifact)
	if len(edges) != 1 || edges[0].Reference.ID != release+"/bindings/python/contract" {
		t.Errorf("binding artifact edges = %+v", edges)
	}

	fixture := &IntegrationFixtureRecordV1{ValidationProfiles: []RecordReferenceV1{
		recordTestReference(release + "/validation/profiles/fixture"),
	}}
	edges = catalogReferencesV1(fixture)
	if len(edges) != 1 || edges[0].Reference.ID != release+"/validation/profiles/fixture" {
		t.Errorf("integration fixture edges = %+v", edges)
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
	const sourcePath = "catalog/demo/releases/1.2.3/revisions/1/sources/demo-linux-amd64.json"
	if _, present := files[sourcePath]; !present {
		t.Fatalf("fixture no longer contains %s, so this test would prove nothing", sourcePath)
	}
	delete(files, sourcePath)
	if _, err := loadCatalogV1(files, "catalog"); err == nil {
		t.Error("a catalog with a broken release graph loaded successfully")
	}
}

// Design rule 5: the same record ID may exist at different digests when
// separate immutable release revisions select them. An ID-keyed index would
// reject the second revision and make a valid multi-revision catalog unloadable.
func TestCatalogHoldsOneIDAtSeveralDigestsAcrossRevisionsV1(t *testing.T) {
	first := &ReleaseContractV1{Schema: ReleaseContractSchemaV1, ID: "tool:demo/releases/1.2.3/contract"}
	second := &ReleaseContractV1{Schema: ReleaseContractSchemaV1, ID: "tool:demo/releases/1.2.3/contract",
		Contexts: []string{"build"}}
	firstDigest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("fixture records must differ in digest")
	}
	catalog := &CatalogV1{records: map[recordKeyV1]loadedRecordV1{}, tools: map[string]recordKeyV1{}}
	for _, pair := range []struct {
		value  *ReleaseContractV1
		digest canonical.Digest
	}{{first, firstDigest}, {second, secondDigest}} {
		record := loadedRecordV1{ID: pair.value.ID, Schema: pair.value.Schema, Digest: pair.digest, Value: pair.value}
		if err := catalog.placeRecordV1(record, "catalog/demo/releases/1.2.3/contract.json", "catalog"); err != nil {
			t.Fatalf("placing %s: %v", pair.digest, err)
		}
	}
	if len(catalog.records) != 2 {
		t.Errorf("catalog holds %d records, want both digests of one ID", len(catalog.records))
	}

	// The exact same (id, digest) twice is a duplicate definition and fails.
	duplicate := loadedRecordV1{ID: first.ID, Schema: first.Schema, Digest: firstDigest, Value: first}
	err = catalog.placeRecordV1(duplicate, "catalog/demo/releases/1.2.3/contract.json", "catalog")
	if err == nil || !strings.Contains(err.Error(), "duplicate definition") {
		t.Errorf("duplicate (id, digest) error = %v", err)
	}
}

// Memoizing only that a node was visited lets a long chain reached later clear
// a bound it should fail: the settled suffix reports zero remaining depth.
func TestReferenceDepthMemoizationPreservesSuffixDepthV1(t *testing.T) {
	records := map[recordKeyV1]loadedRecordV1{}
	const length = maxCatalogGraphDepthV1 + 5
	id := func(index int) string {
		return "tool:demo/releases/1.2.3/revisions/" + strings.Repeat("1", index+1) + "/manifest"
	}
	// Build a chain where each record points at the next. Sorted ID order visits
	// the shortest ID first, so the deep suffix is settled early with a small
	// observed depth, which is exactly the case that hides the violation.
	for index := 0; index < length; index++ {
		manifest := &ReleaseManifestV1{Schema: ReleaseManifestSchemaV1, ID: id(index)}
		if index+1 < length {
			manifest.Targets = []RecordReferenceV1{{ID: id(index + 1), Digest: recordTestDigest}}
		}
		records[recordKeyV1{ID: id(index), Digest: recordTestDigest}] = loadedRecordV1{
			ID: id(index), Schema: manifest.Schema, Digest: recordTestDigest, Value: manifest}
	}
	catalog := &CatalogV1{records: records, tools: map[string]recordKeyV1{}}
	err := catalog.verifyReferenceDepthV1()
	if err == nil || !strings.Contains(err.Error(), "exceeds depth") {
		t.Errorf("error = %v, want the depth bound to hold through memoized suffixes", err)
	}
}

// A filesystem already rooted at the catalog uses the conventional root ".",
// where WalkDir yields entries without a leading "./".
func TestLoadCatalogAcceptsAFilesystemRootedAtDotV1(t *testing.T) {
	files := catalogTestFilesV1(t)
	rooted := fstest.MapFS{}
	for name, file := range files {
		rooted[strings.TrimPrefix(name, "catalog/")] = file
	}
	if _, err := loadCatalogV1(rooted, "."); err != nil {
		t.Errorf("a catalog rooted at dot was rejected: %v", err)
	}
}

// A tool advertising two immutable revisions that select different digests of
// the same semantic record must load. Merging the revisions' closures into one
// ID-keyed view would make that collide, which is the case design rule 5 exists
// to permit.
func TestLoadCatalogAcceptsTwoRevisionsSelectingDifferentDigestsV1(t *testing.T) {
	files := catalogTestFilesV1(t)
	const release = "tool:demo/releases/1.2.3"

	digestOf := func(value any) canonical.Digest {
		digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, value)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	write := func(relative string, value any) {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		files["catalog/"+relative] = &fstest.MapFile{Data: payload}
	}
	read := func(relative string, into any) {
		if err := json.Unmarshal(files["catalog/"+relative].Data, into); err != nil {
			t.Fatal(err)
		}
	}

	// Revision 2 selects a contract that differs from revision 1's, so the same
	// contract ID exists in the catalog at two digests.
	var contract ReleaseContractV1
	read("demo/releases/1.2.3/contract.json", &contract)
	revisedContract := contract
	revisedContract.SupportedReploy = ">=0.0.1"
	if digestOf(&revisedContract) == digestOf(&contract) {
		t.Fatal("the two contract revisions must differ in digest")
	}

	var manifest ReleaseManifestV1
	read("demo/releases/1.2.3/revisions/1/manifest.json", &manifest)
	second := manifest
	second.ID = release + "/revisions/2/manifest"
	second.Revision = "2"
	second.Contract = RecordReferenceV1{ID: contract.ID, Digest: digestOf(&revisedContract)}
	// Revision 2 reaches the same payload, so it carries the same mapping to the
	// same source. Two revisions sharing an artifact is the ordinary case.

	// Both contract digests must be resident, so the second revision's contract
	// is written beside the first rather than replacing it.
	// Sources live in their own revision namespace, so revision 2 owns a source
	// record describing the same content as revision 1's.
	var firstSource ArtifactSourceRecordV1
	read("demo/releases/1.2.3/revisions/1/sources/demo-linux-amd64.json", &firstSource)
	secondSource := firstSource
	secondSource.ID = release + "/revisions/2/sources/demo-linux-amd64"
	write("demo/releases/1.2.3/revisions/2/sources/demo-linux-amd64.json", &secondSource)
	second.ArtifactSources = []ArtifactSourceMappingV1{{
		ArtifactSHA256: manifest.ArtifactSources[0].ArtifactSHA256,
		Artifact:       manifest.ArtifactSources[0].Artifact,
		Source:         RecordReferenceV1{ID: secondSource.ID, Digest: digestOf(&secondSource)},
	}}

	write("demo/releases/1.2.3/contract-revision-2.json", &revisedContract)
	write("demo/releases/1.2.3/revisions/2/manifest.json", &second)

	var tool ToolRecordV1
	read("demo/tool.json", &tool)
	tool.Releases = append(tool.Releases, RecordReferenceV1{ID: second.ID, Digest: digestOf(&second)})
	write("demo/tool.json", &tool)

	if _, err := loadCatalogV1(files, "catalog"); err != nil {
		t.Fatalf("a two-revision catalog was rejected: %v", err)
	}
}
