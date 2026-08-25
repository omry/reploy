package toolcatalog

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omry/reploy/internal/canonical"
)

func composerTestDefinitionsV1(t *testing.T) []PortableToolDefinitionRecordV1 {
	t.Helper()
	files := catalogTestFilesV1(t)
	paths := make([]string, 0, len(files))
	for filename := range files {
		paths = append(paths, filename)
	}
	sort.Strings(paths)

	definitions := make([]PortableToolDefinitionRecordV1, 0, len(paths))
	for _, filename := range paths {
		record, err := decodeRecordV1(filename, files[filename].Data)
		if err != nil {
			t.Fatalf("decode composer fixture %s: %v", filename, err)
		}
		for _, edge := range mutableCatalogReferencesV1(record.Value) {
			edge.reference.Digest = ""
		}
		definitions = append(definitions, PortableToolDefinitionRecordV1{
			Path:   strings.TrimPrefix(filename, "catalog/"),
			Record: record.Value.(PortableToolRecordV1),
		})
	}
	return definitions
}

func TestComposePortableToolCatalogV1IsCanonicalDeterministicAndImmutable(t *testing.T) {
	definitions := composerTestDefinitionsV1(t)
	definitions = appendComposerTargetVariantV1(t, definitions)
	before := make([][]byte, len(definitions))
	for index, definition := range definitions {
		payload, err := canonical.Marshal(definition.Record)
		if err != nil {
			t.Fatal(err)
		}
		before[index] = payload
	}

	first, err := ComposePortableToolCatalogV1(definitions)
	if err != nil {
		t.Fatalf("compose a complete definition: %v", err)
	}
	for index, definition := range definitions {
		after, marshalErr := canonical.Marshal(definition.Record)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if !bytes.Equal(after, before[index]) {
			t.Fatalf("composer mutated input record %q", definition.Path)
		}
	}

	reversed := append([]PortableToolDefinitionRecordV1(nil), definitions...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second, err := ComposePortableToolCatalogV1(reversed)
	if err != nil {
		t.Fatalf("compose reordered definition: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("input order changed canonical catalog output")
	}
	tool := composerRecordV1[*ToolRecordV1](t, definitions)
	for _, output := range first {
		if output.ID == tool.Releases[0].ID {
			tool.Releases[0].Digest = output.Digest
			break
		}
	}
	withCheckedDigest, err := ComposePortableToolCatalogV1(definitions)
	if err != nil {
		t.Fatalf("compose definition with a correct exact digest: %v", err)
	}
	if !reflect.DeepEqual(first, withCheckedDigest) {
		t.Fatal("a correct author-supplied digest changed canonical output")
	}

	files := make(fstest.MapFS, len(first))
	profileRecords := 0
	profileReferences := 0
	payloadRecords := 0
	payloadReferences := 0
	contractRecords := 0
	lastPath := ""
	for _, output := range first {
		if lastPath != "" && lastPath >= output.Path {
			t.Fatalf("output paths are not canonical: %q before %q", lastPath, output.Path)
		}
		lastPath = output.Path
		record, decodeErr := decodeRecordV1(output.Path, output.CanonicalBytes)
		if decodeErr != nil {
			t.Fatalf("decode composed record %q: %v", output.Path, decodeErr)
		}
		if record.ID != output.ID || record.Digest != output.Digest {
			t.Fatalf("output identity for %q = (%q, %s), decoded = (%q, %s)", output.Path, output.ID, output.Digest, record.ID, record.Digest)
		}
		canonicalBytes, marshalErr := canonical.Marshal(record.Value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if !bytes.Equal(canonicalBytes, output.CanonicalBytes) {
			t.Fatalf("output %q is not canonical JSON", output.Path)
		}
		if record.Schema == ValidationProfileSchemaV1 {
			profileRecords++
		}
		if record.Schema == PayloadRecordSchemaV1 {
			payloadRecords++
		}
		if record.Schema == ReleaseContractSchemaV1 {
			contractRecords++
		}
		for _, edge := range catalogReferencesV1(record.Value) {
			if strings.HasSuffix(edge.Reference.ID, "/validation/profiles/default") {
				profileReferences++
			}
			if strings.Contains(edge.Reference.ID, "/payloads/") {
				payloadReferences++
			}
			if edge.Reference.Digest == "" {
				t.Fatalf("output %q contains an unresolved reference", output.Path)
			}
		}
		files[output.Path] = &fstest.MapFile{Data: append([]byte(nil), output.CanonicalBytes...)}
	}
	if profileRecords != 1 || profileReferences < 5 {
		t.Fatalf("shared profile records = %d, references = %d; want one record used from multiple scopes", profileRecords, profileReferences)
	}
	if payloadRecords != 1 || payloadReferences < 3 || contractRecords != 1 {
		t.Fatalf("shared payload records = %d, references = %d, contract records = %d; want target-independent records emitted once", payloadRecords, payloadReferences, contractRecords)
	}
	if _, err := loadCatalogV1(files, "."); err != nil {
		t.Fatalf("ordinary catalog loader rejected composed output: %v", err)
	}
}

func appendComposerTargetVariantV1(t *testing.T, definitions []PortableToolDefinitionRecordV1) []PortableToolDefinitionRecordV1 {
	t.Helper()
	manifest := composerRecordV1[*ReleaseManifestV1](t, definitions)
	target := composerRecordV1[*TargetRecordV1](t, definitions)
	fixture := composerRecordV1[*IntegrationFixtureRecordV1](t, definitions)

	variantTarget := cloneTargetRecordV1(target)
	variantTarget.ID = "tool:demo/releases/1.2.3/targets/ubuntu/24.04/amd64"
	variantTarget.Target.OSReleaseID = "ubuntu"
	variantTarget.Target.VersionID = "24.04"
	variantTarget.IntegrationFixtures = []RecordReferenceV1{{
		ID: "tool:demo/releases/1.2.3/validation/fixtures/ubuntu-2404-amd64",
	}}

	variantFixture := cloneIntegrationFixtureV1(fixture)
	variantFixture.ID = variantTarget.IntegrationFixtures[0].ID
	variantFixture.Name = "ubuntu-2404-amd64"
	variantFixture.Target = variantTarget.Target
	variantFixture.BaseImage = "docker.io/library/ubuntu:24.04"

	manifest.Targets = append(manifest.Targets, RecordReferenceV1{ID: variantTarget.ID})
	return append(definitions,
		PortableToolDefinitionRecordV1{
			Path:   "demo/releases/1.2.3/targets/ubuntu/24.04/amd64.json",
			Record: &variantTarget,
		},
		PortableToolDefinitionRecordV1{
			Path:   "demo/releases/1.2.3/validation/fixtures/ubuntu-2404-amd64.json",
			Record: &variantFixture,
		},
	)
}

func TestComposePortableToolCatalogV1RejectsInvalidComposition(t *testing.T) {
	t.Run("empty definition", func(t *testing.T) {
		assertComposerErrorV1(t, nil, "must contain records")
	})

	t.Run("nil record", func(t *testing.T) {
		var record *ToolRecordV1
		assertComposerErrorV1(t, []PortableToolDefinitionRecordV1{{Path: "demo/tool.json", Record: record}}, "must not be nil")
	})

	t.Run("noncanonical path", func(t *testing.T) {
		definitions := composerTestDefinitionsV1(t)
		definitions[0].Path = "demo/../demo/tool.json"
		assertComposerErrorV1(t, definitions, "canonical relative slash path")
	})

	t.Run("non-JSON path", func(t *testing.T) {
		definitions := composerTestDefinitionsV1(t)
		definitions[0].Path = "demo/tool.yaml"
		assertComposerErrorV1(t, definitions, "must be a JSON file")
	})

	t.Run("missing reference", func(t *testing.T) {
		definitions := composerTestDefinitionsV1(t)
		tool := composerRecordV1[*ToolRecordV1](t, definitions)
		tool.Releases[0].ID += "/missing"
		assertComposerErrorV1(t, definitions, "references missing record")
	})

	t.Run("wrong exact digest", func(t *testing.T) {
		definitions := composerTestDefinitionsV1(t)
		tool := composerRecordV1[*ToolRecordV1](t, definitions)
		tool.Releases[0].Digest = recordTestDigest
		assertComposerErrorV1(t, definitions, "does not match any definition")
	})

	t.Run("schema mismatch", func(t *testing.T) {
		definitions := composerTestDefinitionsV1(t)
		tool := composerRecordV1[*ToolRecordV1](t, definitions)
		contract := composerRecordV1[*ReleaseContractV1](t, definitions)
		tool.Releases[0].ID = contract.ID
		assertComposerErrorV1(t, definitions, "incompatible record schema")
	})

	t.Run("ambiguous record revision", func(t *testing.T) {
		definitions := composerTestDefinitionsV1(t)
		contract := composerRecordV1[*ReleaseContractV1](t, definitions)
		variant := cloneReleaseContractV1(contract)
		variant.SupportedReploy = ">=1.0.0"
		definitions = append(definitions, PortableToolDefinitionRecordV1{
			Path:   "demo/releases/1.2.3/contract-alternative.json",
			Record: &variant,
		})
		assertComposerErrorV1(t, definitions, "ambiguous without an exact digest")
	})

	t.Run("conflicting path", func(t *testing.T) {
		definitions := composerTestDefinitionsV1(t)
		definitions = append(definitions, definitions[0])
		assertComposerErrorV1(t, definitions, "conflicting path")
	})

	t.Run("cycle", func(t *testing.T) {
		first := &ToolRecordV1{
			Schema: ReleaseManifestSchemaV1,
			ID:     "tool:demo/cycle/first",
			Releases: []RecordReferenceV1{{
				ID: "tool:demo/cycle/second",
			}},
		}
		second := &ToolRecordV1{
			Schema: ReleaseManifestSchemaV1,
			ID:     "tool:demo/cycle/second",
			Releases: []RecordReferenceV1{{
				ID: "tool:demo/cycle/first",
			}},
		}
		assertComposerErrorV1(t, []PortableToolDefinitionRecordV1{
			{Path: "demo/cycle/first.json", Record: first},
			{Path: "demo/cycle/second.json", Record: second},
		}, "references form a cycle")
	})

	t.Run("over-depth graph", func(t *testing.T) {
		definitions := make([]PortableToolDefinitionRecordV1, maxCatalogGraphDepthV1+2)
		for index := range definitions {
			id := fmt.Sprintf("tool:demo/depth/%03d", index)
			record := &ToolRecordV1{Schema: ReleaseManifestSchemaV1, ID: id}
			if index+1 < len(definitions) {
				record.Releases = []RecordReferenceV1{{
					ID: fmt.Sprintf("tool:demo/depth/%03d", index+1),
				}}
			}
			definitions[index] = PortableToolDefinitionRecordV1{
				Path:   fmt.Sprintf("demo/depth/%03d.json", index),
				Record: record,
			}
		}
		assertComposerErrorV1(t, definitions, "exceeds depth 64")
	})
}

func composerRecordV1[T PortableToolRecordV1](t *testing.T, definitions []PortableToolDefinitionRecordV1) T {
	t.Helper()
	for _, definition := range definitions {
		if record, ok := definition.Record.(T); ok {
			return record
		}
	}
	var zero T
	t.Fatalf("composer fixture contains no %T", zero)
	return zero
}

func assertComposerErrorV1(t *testing.T, definitions []PortableToolDefinitionRecordV1, want string) {
	t.Helper()
	_, err := ComposePortableToolCatalogV1(definitions)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("composer error = %v, want substring %q", err, want)
	}
}
