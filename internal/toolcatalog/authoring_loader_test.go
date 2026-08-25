package toolcatalog

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestLoadPortableToolAuthoringV1MatchesDirectCompositionDeterministically(t *testing.T) {
	definitions := composerTestDefinitionsV1(t)
	boundary := t.TempDir()
	entries := writeDirectAuthoringDefinitionV1(t, boundary, definitions)
	want, err := ComposePortableToolCatalogV1(definitions)
	if err != nil {
		t.Fatal(err)
	}

	first, err := LoadPortableToolAuthoringV1(boundary, entries)
	if err != nil {
		t.Fatalf("load direct authoring definition: %v", err)
	}
	if !reflect.DeepEqual(first.Records, want) {
		t.Fatal("authoring output differs from direct PTD-11 composition")
	}
	if len(first.Sources) != len(entries) {
		t.Fatalf("source manifest has %d entries, want %d", len(first.Sources), len(entries))
	}
	for index, source := range first.Sources {
		if index > 0 && first.Sources[index-1].Path >= source.Path {
			t.Fatalf("source manifest is not sorted: %q before %q", first.Sources[index-1].Path, source.Path)
		}
		payload, readErr := os.ReadFile(filepath.Join(boundary, filepath.FromSlash(source.Path)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		wantDigest := canonical.Digest(fmt.Sprintf("sha256:%x", sha256.Sum256(payload)))
		if source.SHA256 != wantDigest {
			t.Fatalf("source %q digest = %s, want %s", source.Path, source.SHA256, wantDigest)
		}
	}

	reversed := append([]PortableToolAuthoringEntryV1(nil), entries...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second, err := LoadPortableToolAuthoringV1(boundary, reversed)
	if err != nil {
		t.Fatalf("load reordered authoring definition: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("entry order changed authoring output or source manifest")
	}
}

func TestLoadPortableToolAuthoringV1ResolvesLocalizedExtensions(t *testing.T) {
	definitions := composerTestDefinitionsV1(t)
	profile := composerRecordV1[*ValidationProfileRecordV1](t, definitions)
	manifest := composerRecordV1[*ReleaseManifestV1](t, definitions)
	variant := cloneValidationProfileV1(profile)
	variant.ID += "-alternate"
	manifest.ValidationProfiles = append(manifest.ValidationProfiles, RecordReferenceV1{ID: variant.ID})
	definitions = append(definitions, PortableToolDefinitionRecordV1{
		Path:   "demo/releases/1.2.3/validation/profiles/alternate.json",
		Record: &variant,
	})
	want, err := ComposePortableToolCatalogV1(definitions)
	if err != nil {
		t.Fatal(err)
	}

	boundary := t.TempDir()
	entries := writeDirectAuthoringDefinitionV1(t, boundary, definitions[:len(definitions)-1])
	profileSource := authoringSourcePathV1("demo/releases/1.2.3/validation/profiles/default.json")
	variantSource := authoringSourcePathV1("demo/releases/1.2.3/validation/profiles/alternate.json")
	commonPath := "shared/profile-common.yaml"
	basePath := "shared/profile-base.yaml"
	commonFields, kind := authoringFieldsV1(t, profile)
	delete(commonFields, "id")
	probes := commonFields["probes"]
	delete(commonFields, "probes")
	writeAuthoringEnvelopeV1(t, boundary, basePath, map[string]any{
		"kind": kind, "fields": commonFields,
	})
	writeAuthoringEnvelopeV1(t, boundary, commonPath, map[string]any{
		"kind": kind,
		"imports": map[string]any{
			"base": map[string]any{"path": "/shared/profile-base.yaml"},
		},
		"extends": "base",
		"fields":  map[string]any{"probes": probes},
	})
	writeAuthoringEnvelopeV1(t, boundary, profileSource, map[string]any{
		"kind": kind,
		"imports": map[string]any{
			"root": "../../../../../../", "common": map[string]any{"path": "/shared/profile-common.yaml"},
		},
		"extends": "common",
		"fields":  map[string]any{"id": profile.ID},
	})
	writeAuthoringEnvelopeV1(t, boundary, variantSource, map[string]any{
		"kind": kind,
		"imports": map[string]any{
			"root":   "../../../../../../",
			"common": map[string]any{"path": "../../../../../../shared/profile-common.yaml"},
		},
		"extends": "common",
		"fields":  map[string]any{"id": variant.ID},
	})
	entries = append(entries, PortableToolAuthoringEntryV1{
		SourcePath: variantSource,
		OutputPath: "demo/releases/1.2.3/validation/profiles/alternate.json",
	})

	result, err := LoadPortableToolAuthoringV1(boundary, entries)
	if err != nil {
		t.Fatalf("load extended authoring definition: %v", err)
	}
	if !reflect.DeepEqual(result.Records, want) {
		t.Fatal("localized extension changed canonical composer output")
	}
	reversed := append([]PortableToolAuthoringEntryV1(nil), entries...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	reordered, err := LoadPortableToolAuthoringV1(boundary, reversed)
	if err != nil {
		t.Fatalf("load reordered nested extension definition: %v", err)
	}
	if !reflect.DeepEqual(result, reordered) {
		t.Fatal("entry order changed nested extension output or source manifest")
	}
	manifestCounts := map[string]int{}
	for _, source := range result.Sources {
		manifestCounts[source.Path]++
	}
	if manifestCounts[commonPath] != 1 || manifestCounts[basePath] != 1 {
		t.Fatalf("shared nested sources appear in manifest as common=%d base=%d, want once each", manifestCounts[commonPath], manifestCounts[basePath])
	}
	if len(result.Records) != len(entries) {
		t.Fatalf("emitted %d records for %d explicit entries; imported parent was emitted", len(result.Records), len(entries))
	}
}

func TestLoadPortableToolAuthoringV1RejectsNonJSONYAML(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    string
	}{
		{name: "invalid UTF-8", payload: []byte{0xff}, want: "valid UTF-8"},
		{name: "multiple documents", payload: []byte("kind: portable-tool-v1\nfields: {}\n---\n{}\n"), want: "multiple YAML documents"},
		{name: "duplicate key", payload: []byte("kind: portable-tool-v1\nkind: portable-tool-v1\nfields: {}\n"), want: "duplicate YAML mapping key"},
		{name: "non-string key", payload: []byte("kind: portable-tool-v1\nfields:\n  1: value\n"), want: "mapping keys must be untagged strings"},
		{name: "anchor", payload: []byte("kind: portable-tool-v1\nfields: &fields {}\n"), want: "anchors and aliases"},
		{name: "alias", payload: []byte("kind: &kind portable-tool-v1\nfields: *kind\n"), want: "anchors and aliases"},
		{name: "explicit tag", payload: []byte("kind: portable-tool-v1\nfields:\n  id: !!str tool:demo\n"), want: "explicit YAML tags"},
		{name: "merge key", payload: []byte("kind: portable-tool-v1\nfields:\n  <<: {}\n"), want: "mapping keys must be untagged strings"},
		{name: "timestamp", payload: []byte("kind: portable-tool-v1\nfields:\n  id: 2026-08-26\n"), want: "YAML-specific scalar tag"},
		{name: "YAML boolean", payload: []byte("kind: portable-tool-v1\nfields:\n  id: TRUE\n"), want: "YAML-specific boolean"},
		{name: "non-finite number", payload: []byte("kind: portable-tool-v1\nfields:\n  id: .inf\n"), want: "YAML-specific number"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertAuthoringPayloadErrorV1(t, test.payload, test.want)
		})
	}
}

func TestDecodePortableToolAuthoringEnvelopeV1AcceptsJSONDataModelScalars(t *testing.T) {
	envelope, err := decodePortableToolAuthoringEnvelopeV1([]byte(`{
  "kind": "portable-tool-v1",
  "fields": {
    "values": [null, true, false, 0, -1.25e+3, "text"]
  }
}`))
	if err != nil {
		t.Fatalf("decode JSON-compatible YAML scalars: %v", err)
	}
	values, ok := envelope.fields["values"].([]any)
	if !ok || len(values) != 6 || values[0] != nil || values[1] != true || values[2] != false || values[3] != json.Number("0") || values[4] != json.Number("-1.25e+3") || values[5] != "text" {
		t.Fatalf("decoded JSON-compatible values = %#v", envelope.fields["values"])
	}
}

func TestLoadPortableToolAuthoringV1RejectsMalformedEnvelope(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown field", body: "kind: portable-tool-v1\nfields: {}\nother: true\n", want: "unknown authoring field"},
		{name: "schema in fields", body: "kind: portable-tool-v1\nfields:\n  schema: portable-tool-v1\n", want: "reserved field"},
		{name: "imports without extends", body: "kind: portable-tool-v1\nimports: {}\nfields: {}\n", want: "imports require extends"},
		{name: "extends without imports", body: "kind: portable-tool-v1\nextends: common\nfields: {}\n", want: "requires an imports mapping"},
		{name: "missing alias", body: "kind: portable-tool-v1\nimports:\n  root: ../\nextends: common\nfields: {}\n", want: "no matching import"},
		{name: "extra alias", body: "kind: portable-tool-v1\nimports:\n  common: {path: common.yaml}\n  extra: {path: extra.yaml}\nextends: common\nfields: {}\n", want: "unused or extra import alias"},
		{name: "extra import member", body: "kind: portable-tool-v1\nimports:\n  common: {path: common.yaml, other: value}\nextends: common\nfields: {}\n", want: "exactly path"},
		{name: "invalid alias", body: "kind: portable-tool-v1\nimports:\n  Common: {path: common.yaml}\nextends: Common\nfields: {}\n", want: "canonical import alias"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertAuthoringPayloadErrorV1(t, []byte(test.body), test.want)
		})
	}
}

func TestLoadPortableToolAuthoringV1RejectsRecordLocalInvalidityBeforeComposition(t *testing.T) {
	definition := composerTestDefinitionsV1(t)
	tool := composerRecordV1[*ToolRecordV1](t, definition)
	tool.Releases = []RecordReferenceV1{}
	fields, kind := authoringFieldsV1(t, tool)
	boundary := t.TempDir()
	writeAuthoringEnvelopeV1(t, boundary, "tool.yaml", map[string]any{"kind": kind, "fields": fields})
	_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: "tool.yaml", OutputPath: "demo/tool.json"}})
	assertAuthoringErrorContainsV1(t, err, "chain tool.yaml")
	assertAuthoringErrorContainsV1(t, err, "tool metadata and releases must not be empty")
}

func TestLoadPortableToolAuthoringV1IncludesSourceChainForCompositionErrors(t *testing.T) {
	definitions := composerTestDefinitionsV1(t)
	tool := cloneToolRecordV1(composerRecordV1[*ToolRecordV1](t, definitions))
	tool.Releases[0] = RecordReferenceV1{ID: "tool:demo/releases/9.9.9/revisions/1/manifest"}
	fields, kind := authoringFieldsV1(t, &tool)
	delete(fields, "id")

	boundary := t.TempDir()
	writeAuthoringEnvelopeV1(t, boundary, "parent.yaml", map[string]any{"kind": kind, "fields": fields})
	writeAuthoringEnvelopeV1(t, boundary, "child.yaml", map[string]any{
		"kind": kind,
		"imports": map[string]any{
			"parent": map[string]any{"path": "parent.yaml"},
		},
		"extends": "parent",
		"fields":  map[string]any{"id": tool.ID},
	})

	_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: "child.yaml", OutputPath: "demo/tool.json"}})
	assertAuthoringErrorContainsV1(t, err, "child.yaml -> parent.yaml")
	assertAuthoringErrorContainsV1(t, err, "references missing record")
}

func TestPortableToolAuthoringLoaderV1RejectsSourceChangedAfterOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming an open source is not portable to Windows")
	}
	boundary := t.TempDir()
	writeAuthoringFileV1(t, boundary, "source.yaml", []byte("kind: portable-tool-v1\nfields: {}\n"))
	loader, err := newPortableToolAuthoringLoaderV1(boundary)
	if err != nil {
		t.Fatal(err)
	}
	defer loader.root.Close()
	if err := loader.validateRegularFile("source.yaml"); err != nil {
		t.Fatal(err)
	}
	file, err := loader.root.Open("source.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(boundary, "source.yaml"), filepath.Join(boundary, "original.yaml")); err != nil {
		t.Fatal(err)
	}
	writeAuthoringFileV1(t, boundary, "source.yaml", []byte("kind: portable-tool-v1\nfields: {id: replacement}\n"))
	assertAuthoringErrorContainsV1(t, loader.validateOpenedRegularFileV1("source.yaml", openedInfo), "changed while opening")
}

func TestLoadPortableToolAuthoringV1RejectsInvalidEntriesAndPaths(t *testing.T) {
	t.Run("empty entries", func(t *testing.T) {
		_, err := LoadPortableToolAuthoringV1(t.TempDir(), nil)
		assertAuthoringErrorContainsV1(t, err, "must contain entries")
	})
	t.Run("duplicate normalized source", func(t *testing.T) {
		_, err := LoadPortableToolAuthoringV1(t.TempDir(), []PortableToolAuthoringEntryV1{
			{SourcePath: "a.yaml", OutputPath: "demo/a.json"},
			{SourcePath: "dir/../a.yaml", OutputPath: "demo/b.json"},
		})
		assertAuthoringErrorContainsV1(t, err, "duplicate normalized source")
	})
	t.Run("duplicate output", func(t *testing.T) {
		_, err := LoadPortableToolAuthoringV1(t.TempDir(), []PortableToolAuthoringEntryV1{
			{SourcePath: "a.yaml", OutputPath: "demo/a.json"},
			{SourcePath: "b.yaml", OutputPath: "demo/a.json"},
		})
		assertAuthoringErrorContainsV1(t, err, "duplicate output path")
	})
	t.Run("invalid output", func(t *testing.T) {
		_, err := LoadPortableToolAuthoringV1(t.TempDir(), []PortableToolAuthoringEntryV1{{SourcePath: "a.yaml", OutputPath: "../a.json"}})
		assertAuthoringErrorContainsV1(t, err, "invalid segment")
	})
	t.Run("source escapes boundary", func(t *testing.T) {
		_, err := LoadPortableToolAuthoringV1(t.TempDir(), []PortableToolAuthoringEntryV1{{SourcePath: "../a.yaml", OutputPath: "demo/a.json"}})
		assertAuthoringErrorContainsV1(t, err, "escapes the trusted boundary")
	})
	t.Run("missing source", func(t *testing.T) {
		_, err := LoadPortableToolAuthoringV1(t.TempDir(), []PortableToolAuthoringEntryV1{{SourcePath: "missing.yaml", OutputPath: "demo/a.json"}})
		assertAuthoringErrorContainsV1(t, err, "chain missing.yaml")
	})
	t.Run("symlink source", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation requires privileges on Windows")
		}
		boundary := t.TempDir()
		writeAuthoringFileV1(t, boundary, "real.yaml", []byte("kind: portable-tool-v1\nfields: {}\n"))
		if err := os.Symlink("real.yaml", filepath.Join(boundary, "link.yaml")); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: "link.yaml", OutputPath: "demo/a.json"}})
		assertAuthoringErrorContainsV1(t, err, "must not be a symbolic link")
	})
	t.Run("symlink directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation requires privileges on Windows")
		}
		boundary := t.TempDir()
		writeAuthoringFileV1(t, boundary, "real/source.yaml", []byte("kind: portable-tool-v1\nfields: {}\n"))
		if err := os.Symlink("real", filepath.Join(boundary, "linked")); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: "linked/source.yaml", OutputPath: "demo/a.json"}})
		assertAuthoringErrorContainsV1(t, err, "path component \"linked\" must not be a symbolic link")
	})
}

func TestLoadPortableToolAuthoringV1RejectsInvalidExtensionGraphs(t *testing.T) {
	tests := []struct {
		name   string
		source string
		files  map[string]string
		want   string
	}{
		{
			name:  "root-relative without root",
			files: map[string]string{"child.yaml": extensionEnvelopeV1("portable-tool-v1", "/parent.yaml", "id", "tool:demo")},
			want:  "requires imports.root",
		},
		{
			name:  "missing parent includes chain",
			files: map[string]string{"child.yaml": extensionEnvelopeV1("portable-tool-v1", "parent.yaml", "id", "tool:demo")},
			want:  "child.yaml -> parent.yaml",
		},
		{
			name:  "URL import",
			files: map[string]string{"child.yaml": extensionEnvelopeV1("portable-tool-v1", "https://example.com/parent.yaml", "id", "tool:demo")},
			want:  "not a local slash path",
		},
		{
			name:  "cross-tool import",
			files: map[string]string{"child.yaml": extensionEnvelopeV1("portable-tool-v1", "tool:other/parent.yaml", "id", "tool:demo")},
			want:  "not a local slash path",
		},
		{
			name:   "logical root escape",
			source: "dir/child.yaml",
			files: map[string]string{
				"dir/child.yaml": "kind: portable-tool-v1\nimports:\n  root: .\n  common: {path: ../parent.yaml}\nextends: common\nfields: {id: 'tool:demo'}\n",
				"parent.yaml":    "kind: portable-tool-v1\nfields: {}\n",
			},
			want: "escapes logical root",
		},
		{
			name: "root redefinition",
			files: map[string]string{
				"child.yaml":  "kind: portable-tool-v1\nimports:\n  root: .\n  parent: {path: parent.yaml}\nextends: parent\nfields: {id: 'tool:demo'}\n",
				"parent.yaml": "kind: portable-tool-v1\nimports:\n  root: .\n  base: {path: base.yaml}\nextends: base\nfields: {}\n",
				"base.yaml":   "kind: portable-tool-v1\nfields: {}\n",
			},
			want: "cannot redefine imports.root",
		},
		{
			name: "cycle",
			files: map[string]string{
				"child.yaml":  extensionEnvelopeV1("portable-tool-v1", "parent.yaml", "id", "tool:demo"),
				"parent.yaml": extensionEnvelopeV1("portable-tool-v1", "child.yaml", "name", "demo"),
			},
			want: "child.yaml -> parent.yaml -> child.yaml",
		},
		{
			name: "kind mismatch",
			files: map[string]string{
				"child.yaml":  extensionEnvelopeV1("portable-tool-v1", "parent.yaml", "id", "tool:demo"),
				"parent.yaml": "kind: portable-tool-release-manifest-v1\nfields: {}\n",
			},
			want: "kind mismatch",
		},
		{
			name: "field overlap",
			files: map[string]string{
				"child.yaml":  extensionEnvelopeV1("portable-tool-v1", "parent.yaml", "id", "tool:demo"),
				"parent.yaml": "kind: portable-tool-v1\nfields: {id: 'tool:parent'}\n",
			},
			want: "owned by both parent source",
		},
		{
			name: "incomplete resolved entry includes chain",
			files: map[string]string{
				"child.yaml":  extensionEnvelopeV1("portable-tool-v1", "parent.yaml", "id", "tool:demo"),
				"parent.yaml": "kind: portable-tool-v1\nfields: {}\n",
			},
			want: "child.yaml -> parent.yaml",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			boundary := t.TempDir()
			for name, content := range test.files {
				writeAuthoringFileV1(t, boundary, name, []byte(content))
			}
			source := test.source
			if source == "" {
				source = "child.yaml"
			}
			_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: source, OutputPath: "demo/tool.json"}})
			assertAuthoringErrorContainsV1(t, err, test.want)
		})
	}
}

func TestLoadPortableToolAuthoringV1ReportsExtensionConflictsDeterministically(t *testing.T) {
	boundary := t.TempDir()
	writeAuthoringFileV1(t, boundary, "parent.yaml", []byte("kind: portable-tool-v1\nfields: {id: 'tool:parent', name: parent}\n"))
	writeAuthoringFileV1(t, boundary, "child.yaml", []byte("kind: portable-tool-v1\nimports:\n  parent: {path: parent.yaml}\nextends: parent\nfields: {id: 'tool:child', name: child}\n"))
	entries := []PortableToolAuthoringEntryV1{{SourcePath: "child.yaml", OutputPath: "demo/tool.json"}}
	for attempt := 0; attempt < 32; attempt++ {
		_, err := LoadPortableToolAuthoringV1(boundary, entries)
		assertAuthoringErrorContainsV1(t, err, `field "id" is owned by both`)
	}
}

func TestLoadPortableToolAuthoringV1BoundsEachSourceAndExtensionChain(t *testing.T) {
	t.Run("source bytes", func(t *testing.T) {
		boundary := t.TempDir()
		writeAuthoringFileV1(t, boundary, "large.yaml", []byte(strings.Repeat("x", maxDefinitionFileBytes+1)))
		_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: "large.yaml", OutputPath: "demo/tool.json"}})
		assertAuthoringErrorContainsV1(t, err, "size must be between")
	})
	t.Run("extension depth", func(t *testing.T) {
		boundary := t.TempDir()
		for index := 0; index <= maxCatalogGraphDepthV1+1; index++ {
			name := fmt.Sprintf("%03d.yaml", index)
			content := "kind: portable-tool-v1\nfields: {}\n"
			if index <= maxCatalogGraphDepthV1 {
				content = extensionEnvelopeV1("portable-tool-v1", fmt.Sprintf("%03d.yaml", index+1), fmt.Sprintf("field_%03d", index), "value")
			}
			writeAuthoringFileV1(t, boundary, name, []byte(content))
		}
		_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: "000.yaml", OutputPath: "demo/tool.json"}})
		assertAuthoringErrorContainsV1(t, err, "extension chain exceeds depth 64")
	})
	t.Run("source members", func(t *testing.T) {
		fields := make(map[string]any, maxDefinitionJSONMembers+1)
		for index := 0; index <= maxDefinitionJSONMembers; index++ {
			fields[fmt.Sprintf("field_%04d", index)] = "value"
		}
		payload, err := json.Marshal(map[string]any{"kind": ToolRecordSchemaV1, "fields": fields})
		if err != nil {
			t.Fatal(err)
		}
		assertAuthoringPayloadErrorV1(t, payload, "YAML member count exceeds")
	})
	t.Run("resolved record members", func(t *testing.T) {
		boundary := t.TempDir()
		parentFields := make(map[string]any, 2200)
		childFields := make(map[string]any, 2200)
		for index := 0; index < 2200; index++ {
			parentFields[fmt.Sprintf("parent_%04d", index)] = "value"
			childFields[fmt.Sprintf("child_%04d", index)] = "value"
		}
		writeAuthoringEnvelopeV1(t, boundary, "parent.yaml", map[string]any{"kind": ToolRecordSchemaV1, "fields": parentFields})
		writeAuthoringEnvelopeV1(t, boundary, "child.yaml", map[string]any{
			"kind": ToolRecordSchemaV1, "imports": map[string]any{"parent": map[string]any{"path": "parent.yaml"}}, "extends": "parent", "fields": childFields,
		})
		_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: "child.yaml", OutputPath: "demo/tool.json"}})
		assertAuthoringErrorContainsV1(t, err, "JSON member count exceeds")
	})
}

func writeDirectAuthoringDefinitionV1(t *testing.T, boundary string, definitions []PortableToolDefinitionRecordV1) []PortableToolAuthoringEntryV1 {
	t.Helper()
	entries := make([]PortableToolAuthoringEntryV1, 0, len(definitions))
	for _, definition := range definitions {
		fields, kind := authoringFieldsV1(t, definition.Record)
		source := authoringSourcePathV1(definition.Path)
		writeAuthoringEnvelopeV1(t, boundary, source, map[string]any{"kind": kind, "fields": fields})
		entries = append(entries, PortableToolAuthoringEntryV1{SourcePath: source, OutputPath: definition.Path})
	}
	return entries
}

func authoringFieldsV1(t *testing.T, record PortableToolRecordV1) (map[string]any, string) {
	t.Helper()
	payload, err := canonical.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	kind, ok := fields["schema"].(string)
	if !ok {
		t.Fatal("record fixture has no schema")
	}
	delete(fields, "schema")
	return fields, kind
}

func authoringSourcePathV1(output string) string {
	return "authoring/" + strings.TrimSuffix(output, ".json") + ".yaml"
}

func writeAuthoringEnvelopeV1(t *testing.T, boundary string, name string, envelope map[string]any) {
	t.Helper()
	payload, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeAuthoringFileV1(t, boundary, name, payload)
}

func writeAuthoringFileV1(t *testing.T, boundary string, name string, payload []byte) {
	t.Helper()
	filename := filepath.Join(boundary, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func extensionEnvelopeV1(kind string, parent string, field string, value string) string {
	return fmt.Sprintf("kind: %s\nimports:\n  parent: {path: %q}\nextends: parent\nfields:\n  %s: %q\n", kind, parent, field, value)
}

func assertAuthoringPayloadErrorV1(t *testing.T, payload []byte, want string) {
	t.Helper()
	boundary := t.TempDir()
	writeAuthoringFileV1(t, boundary, "source.yaml", payload)
	_, err := LoadPortableToolAuthoringV1(boundary, []PortableToolAuthoringEntryV1{{SourcePath: "source.yaml", OutputPath: "demo/tool.json"}})
	assertAuthoringErrorContainsV1(t, err, want)
}

func assertAuthoringErrorContainsV1(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("authoring error = %v, want substring %q", err, want)
	}
}
