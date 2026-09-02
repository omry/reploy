package portabletool_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/toolcatalog"
)

func TestValidateRecordEnvelopeV1AcceptsEmbeddedRecordKinds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		schema string
		path   string
	}{
		{portabletool.ReleaseManifestSchemaV1, "java/releases/21/revisions/1/manifest.json"},
		{portabletool.BindingContractSchemaV1, "playwright/releases/1.61.0/bindings/python/contract.json"},
		{portabletool.BindingArtifactSchemaV1, "playwright/releases/1.61.0/bindings/python/linux-amd64.json"},
		{portabletool.PayloadRecordSchemaV1, "playwright/releases/1.61.0/payloads/chromium/chromium-linux-amd64.json"},
		{portabletool.ArtifactSourceRecordSchemaV1, "java/releases/21/revisions/1/sources/jdk-linux-amd64.json"},
		{portabletool.NativePackageSetSchemaV1, "playwright/releases/1.61.0/package-sets/ubuntu-t64-amd64.json"},
		{portabletool.ValidationProfileSchemaV1, "java/releases/21/validation/profiles/default.json"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.schema, func(t *testing.T) {
			t.Parallel()
			value := readDefinitionObjectV1(t, test.path)
			if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{Schema: test.schema, Value: value}); err != nil {
				t.Fatalf("validate embedded %s record: %v", test.schema, err)
			}
		})
	}
}

func TestValidateRecordEnvelopeV1RejectsUnknownNestedField(t *testing.T) {
	t.Parallel()
	value := readDefinitionObjectV1(t, "playwright/releases/1.61.0/bindings/python/contract.json")
	cli, ok := value["cli"].(map[string]any)
	if !ok {
		t.Fatalf("binding CLI has type %T", value["cli"])
	}
	cli["unexpected"] = "value"
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{
		Schema: portabletool.BindingContractSchemaV1,
		Value:  value,
	}); err == nil {
		t.Fatal("nested unknown field was accepted")
	}
}

func TestValidateRecordEnvelopeV1RejectsNoncanonicalRecordID(t *testing.T) {
	t.Parallel()
	value := readDefinitionObjectV1(t, "playwright/releases/1.61.0/bindings/python/contract.json")
	value["id"] = "not-a-tool/releases/1.61.0/bindings/python/contract"
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{
		Schema: portabletool.BindingContractSchemaV1,
		Value:  value,
	}); err == nil {
		t.Fatal("noncanonical record ID was accepted")
	}
}

func TestValidateRecordEnvelopeV1RejectsShortPayloadID(t *testing.T) {
	t.Parallel()
	value := readDefinitionObjectV1(t, "playwright/releases/1.61.0/payloads/chromium/chromium-linux-amd64.json")
	value["id"] = "tool:demo"
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{
		Schema: portabletool.PayloadRecordSchemaV1,
		Value:  value,
	}); err == nil {
		t.Fatal("short payload ID was accepted")
	}
}

func TestToolcatalogCompatibilityAliasPreservesCanonicalIdentity(t *testing.T) {
	t.Parallel()
	shared := &portabletool.BindingContractV1{
		Schema:          portabletool.BindingContractSchemaV1,
		ID:              "tool:example/releases/1/bindings/python/contract",
		Name:            "python",
		Package:         "example",
		Requirements:    []string{"example==1"},
		SupportedPython: []string{"3.13"},
		SupportedTags:   []string{"py3-none-any"},
		BundledComponents: []portabletool.BundledComponentV1{{
			Name: "example", Version: "1", Path: "example",
		}},
		CLI: portabletool.ToolExportV1{Name: "example", Path: "/opt/example"},
	}
	compatibility := (*toolcatalog.BindingContractV1)(shared)
	sharedBytes, err := canonical.Marshal(shared)
	if err != nil {
		t.Fatal(err)
	}
	compatibilityBytes, err := canonical.Marshal(compatibility)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sharedBytes, compatibilityBytes) {
		t.Fatalf("compatibility alias changed canonical bytes:\nshared: %s\ncompatibility: %s", sharedBytes, compatibilityBytes)
	}
	sharedDigest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, shared)
	if err != nil {
		t.Fatal(err)
	}
	compatibilityDigest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, compatibility)
	if err != nil {
		t.Fatal(err)
	}
	if sharedDigest != compatibilityDigest {
		t.Fatalf("compatibility alias changed digest: %s != %s", sharedDigest, compatibilityDigest)
	}
	var _ portabletool.RecordV1 = compatibility
}

func readDefinitionObjectV1(t *testing.T, relative string) canonical.Object {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "toolcatalog", "definitions", filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	var value canonical.Object
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
