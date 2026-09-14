package portabletool_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
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

func TestValidateBindingRecordReferencesV1(t *testing.T) {
	t.Parallel()
	digest := canonical.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	contract := portabletool.RecordReferenceV1{
		ID: "tool:demo/releases/1.0.0/bindings/python/contract", Digest: digest,
	}
	artifact := portabletool.RecordReferenceV1{
		ID: "tool:demo/releases/1.0.0/bindings/python/artifacts/linux-amd64", Digest: digest,
	}
	if err := portabletool.ValidateBindingRecordReferencesV1(contract, artifact); err != nil {
		t.Fatalf("valid binding references: %v", err)
	}
	for _, test := range []struct {
		name     string
		contract portabletool.RecordReferenceV1
		artifact portabletool.RecordReferenceV1
	}{
		{name: "malformed contract", contract: portabletool.RecordReferenceV1{ID: "contract", Digest: digest}, artifact: artifact},
		{name: "swapped categories", contract: artifact, artifact: contract},
		{name: "different release binding", contract: contract, artifact: portabletool.RecordReferenceV1{ID: "tool:other/releases/1.0.0/bindings/python/artifacts/linux-amd64", Digest: digest}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := portabletool.ValidateBindingRecordReferencesV1(test.contract, test.artifact); err == nil {
				t.Fatal("invalid binding references were accepted")
			}
		})
	}
}

func TestValidateBindingArtifactReferencePlatformV1(t *testing.T) {
	t.Parallel()
	digest := canonical.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	artifact := portabletool.RecordReferenceV1{
		ID: "tool:demo/releases/1.0.0/bindings/python/artifacts/linux-amd64", Digest: digest,
	}
	if err := portabletool.ValidateBindingArtifactReferencePlatformV1(artifact, "linux/amd64"); err != nil {
		t.Fatalf("matching artifact platform: %v", err)
	}
	if err := portabletool.ValidateBindingArtifactReferencePlatformV1(artifact, "linux/arm64"); err == nil {
		t.Fatal("mismatched artifact platform was accepted")
	}
	artifact.ID = "tool:demo/releases/1.0.0/bindings/python/artifacts/linux-arm64"
	if err := portabletool.ValidateBindingArtifactReferencePlatformV1(artifact, "linux/arm64"); err != nil {
		t.Fatalf("matching ARM64 artifact platform: %v", err)
	}
}

func TestValidateBindingWheelTagsForPlatformV1(t *testing.T) {
	t.Parallel()
	if err := portabletool.ValidateBindingWheelTagsForPlatformV1([]string{
		"py3-cp313-manylinux1_x86_64",
		"py3-none-manylinux1_x86_64",
	}, "linux/amd64"); err != nil {
		t.Fatalf("matching exact tags: %v", err)
	}
	if err := portabletool.ValidateBindingWheelTagsForPlatformV1(
		[]string{"py3-none-manylinux_2_17_aarch64"}, "linux/amd64",
	); err == nil {
		t.Fatal("incompatible exact tag platform was accepted")
	}
}

func TestValidatePythonPackageRootRequirementsV1RejectsDuplicateNormalizedRoots(t *testing.T) {
	t.Parallel()
	if err := portabletool.ValidatePythonPackageRootRequirementsV1([]string{"demo==1", "dependency>=2"}); err != nil {
		t.Fatalf("distinct package roots: %v", err)
	}
	if err := portabletool.ValidatePythonPackageRootRequirementsV1([]string{"Demo==1", "demo>=1"}); err == nil {
		t.Fatal("duplicate normalized package roots were accepted")
	}
	for _, requirement := range []string{"demo=1", "demo>=2||==1", "demo>=2||=1,==1"} {
		if err := portabletool.ValidatePythonPackageRootRequirementsV1([]string{requirement}); err == nil {
			t.Errorf("non-PEP package root %q was accepted", requirement)
		}
	}
}

func TestValidateBindingBundledComponentAgreementV1(t *testing.T) {
	t.Parallel()
	declared := []portabletool.BundledComponentV1{{Name: "runtime", Version: "1.0.0", Path: "demo/runtime"}}
	if err := portabletool.ValidateBindingBundledComponentAgreementV1(declared, append([]portabletool.BundledComponentV1{}, declared...)); err != nil {
		t.Fatalf("matching bundled component: %v", err)
	}
	if err := portabletool.ValidateBindingBundledComponentAgreementV1(declared, nil); err == nil {
		t.Fatal("missing bundled component was accepted")
	}
	mismatched := append([]portabletool.BundledComponentV1{}, declared...)
	mismatched[0].Version = "2.0.0"
	if err := portabletool.ValidateBindingBundledComponentAgreementV1(declared, mismatched); err == nil {
		t.Fatal("mismatched bundled component was accepted")
	}
}

func TestValidateBindingCLIExportV1(t *testing.T) {
	t.Parallel()
	if err := portabletool.ValidateBindingCLIExportV1(portabletool.ToolExportV1{Name: "demo-cli", Path: "/opt/demo/bin/demo"}); err != nil {
		t.Fatalf("valid binding CLI: %v", err)
	}
	for _, export := range []portabletool.ToolExportV1{
		{Name: "bad_name", Path: "/opt/demo/bin/demo"},
		{Name: "demo", Path: "/"},
		{Name: "demo", Path: "/opt/../demo"},
		{Name: "demo", Path: `/opt\demo`},
		{Name: "demo", Path: "/opt/demo\n"},
	} {
		if err := portabletool.ValidateBindingCLIExportV1(export); err == nil {
			t.Errorf("invalid binding CLI %#v was accepted", export)
		}
	}
}

func TestValidatePythonRequiresPythonV1(t *testing.T) {
	t.Parallel()
	if err := portabletool.ValidatePythonRequiresPythonV1(">=3.12,<3.13"); err != nil {
		t.Fatalf("canonical requires_python: %v", err)
	}
	for _, value := range []string{"", " >=3.12,<3.13 ", ">=3.12, <3.13", "not-a-specifier"} {
		if err := portabletool.ValidatePythonRequiresPythonV1(value); err == nil {
			t.Errorf("noncanonical requires_python %q was accepted", value)
		}
	}
}

func TestValidateRecordEnvelopeV1RequiresNormalizedSupportedPythonClaims(t *testing.T) {
	t.Parallel()
	for _, claims := range [][]any{
		{"3.10.1", "3.10"},
		{"3.10", "3.10.1"},
		{"3.10", "3.9"},
		{"3.10.1", "3.10.1"},
	} {
		value := readDefinitionObjectV1(t, "playwright/releases/1.61.0/bindings/python/contract.json")
		value["supported_python"] = claims
		if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{
			Schema: portabletool.BindingContractSchemaV1,
			Value:  value,
		}); err == nil {
			t.Errorf("non-normalized supported Python claims %#v were accepted", claims)
		}
	}

	value := readDefinitionObjectV1(t, "playwright/releases/1.61.0/bindings/python/contract.json")
	value["supported_python"] = []any{"3.9", "3.10.1", "3.10.2"}
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{
		Schema: portabletool.BindingContractSchemaV1,
		Value:  value,
	}); err != nil {
		t.Fatalf("normalized exact patch claims were rejected: %v", err)
	}
}

func TestProviderCanonicalSupportedPythonClaimsSatisfyRecordBoundaryV1(t *testing.T) {
	t.Parallel()
	for _, input := range [][]string{
		{"3.10.2", "3.9", "3.10", "3.10.1", "3.9", "4.0.1"},
		{"3.12.2", "3.12.1", "3.11"},
	} {
		normalized, err := providers.NormalizeSupportedPythonClaimsV1(input)
		if err != nil {
			t.Fatalf("normalize %#v: %v", input, err)
		}
		claims := make([]any, len(normalized))
		for index, claim := range normalized {
			claims[index] = claim
		}
		value := readDefinitionObjectV1(t, "playwright/releases/1.61.0/bindings/python/contract.json")
		value["supported_python"] = claims
		if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{
			Schema: portabletool.BindingContractSchemaV1,
			Value:  value,
		}); err != nil {
			t.Fatalf("provider canonical claims %#v rejected at record boundary: %v", normalized, err)
		}
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

func TestProjectWheelPlatformV1UsesOneBoundedManylinuxPolicy(t *testing.T) {
	t.Parallel()
	accepted := []struct {
		tag, kind, architecture, major, minor string
	}{
		{"any", portabletool.WheelPlatformAnyV1, "", "", ""},
		{"linux_x86_64", portabletool.WheelPlatformLinuxV1, "x86_64", "", ""},
		{"linux_aarch64", portabletool.WheelPlatformLinuxV1, "aarch64", "", ""},
		{"manylinux1_x86_64", portabletool.WheelPlatformManylinuxV1, "x86_64", "2", "5"},
		{"manylinux2010_x86_64", portabletool.WheelPlatformManylinuxV1, "x86_64", "2", "12"},
		{"manylinux2014_x86_64", portabletool.WheelPlatformManylinuxV1, "x86_64", "2", "17"},
		{"manylinux2014_aarch64", portabletool.WheelPlatformManylinuxV1, "aarch64", "2", "17"},
		{"manylinux_2_5_x86_64", portabletool.WheelPlatformManylinuxV1, "x86_64", "2", "5"},
		{"manylinux_2_6_x86_64", portabletool.WheelPlatformManylinuxV1, "x86_64", "2", "6"},
		{"manylinux_2_17_aarch64", portabletool.WheelPlatformManylinuxV1, "aarch64", "2", "17"},
		{"manylinux_2_40_aarch64", portabletool.WheelPlatformManylinuxV1, "aarch64", "2", "40"},
	}
	for _, test := range accepted {
		test := test
		t.Run(test.tag, func(t *testing.T) {
			t.Parallel()
			projection, err := portabletool.ProjectWheelPlatformV1(test.tag)
			if err != nil {
				t.Fatal(err)
			}
			if projection.Kind != test.kind || projection.Architecture != test.architecture || projection.MinimumGlibcMajor != test.major || projection.MinimumGlibcMinor != test.minor {
				t.Fatalf("projection = %#v", projection)
			}
		})
	}
	for _, tag := range []string{
		"manylinux1_aarch64", "manylinux2010_aarch64",
		"manylinux_2_4_x86_64", "manylinux_2_16_aarch64",
		"manylinux_1_17_aarch64", "manylinux_3_17_aarch64",
		"manylinux_02_17_aarch64", "manylinux_2_017_aarch64",
		"manylinux_x_17_aarch64", "musllinux_1_2_x86_64", "linux_ppc64le",
	} {
		if _, err := portabletool.ProjectWheelPlatformV1(tag); err == nil {
			t.Errorf("unsupported platform %q was accepted", tag)
		}
	}
	if _, err := portabletool.ProjectWheelPlatformForTargetV1("linux_x86_64", "linux/arm64"); err == nil {
		t.Fatal("x86_64 wheel platform was accepted for arm64 target")
	}
	direct, err := portabletool.ProjectWheelPlatformV1("manylinux_2_17_aarch64")
	if err != nil {
		t.Fatal(err)
	}
	targeted, err := portabletool.ProjectWheelPlatformForTargetV1("manylinux_2_17_aarch64", "linux/arm64")
	if err != nil {
		t.Fatalf("matching manylinux platform was rejected: %v", err)
	}
	if targeted != direct {
		t.Fatalf("target projection %#v differs from direct projection %#v", targeted, direct)
	}
	if projection, err := portabletool.ProjectWheelPlatformForTargetV1("any", "linux/amd64"); err != nil || projection.Kind != portabletool.WheelPlatformAnyV1 {
		t.Fatalf("any platform did not project for a supported target: %#v, %v", projection, err)
	}
	if _, err := portabletool.ProjectWheelPlatformForTargetV1("any", "darwin/amd64"); err == nil {
		t.Fatal("any wheel platform was accepted for an unsupported target")
	}
}

func TestProjectWheelFilenameV1ReturnsCanonicalIdentityAndExpandedTags(t *testing.T) {
	t.Parallel()
	projection, err := portabletool.ProjectWheelFilenameV1("demo_pkg-1.2.0-py3.cp313-none-manylinux1_x86_64.whl")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Distribution != "demo-pkg" || projection.EcosystemVersion != "1.2.0" || !reflect.DeepEqual(projection.Tags, []string{
		"cp313-none-manylinux1_x86_64",
		"py3-none-manylinux1_x86_64",
	}) {
		t.Fatalf("wheel filename projection = %#v", projection)
	}
}

func TestValidateRecordEnvelopeV1UsesWheelPlatformProjection(t *testing.T) {
	t.Parallel()
	value := readDefinitionObjectV1(t, "playwright/releases/1.61.0/bindings/python/linux-amd64.json")
	value["filename"] = "playwright-1.61.0-py3-none-manylinux_2_5_x86_64.whl"
	value["tags"] = []any{"py3-none-manylinux_2_5_x86_64"}
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{
		Schema: portabletool.BindingArtifactSchemaV1,
		Value:  value,
	}); err != nil {
		t.Fatalf("supported projected platform was rejected: %v", err)
	}

	value["filename"] = "playwright-1.61.0-py3-none-manylinux_3_17_x86_64.whl"
	value["tags"] = []any{"py3-none-manylinux_3_17_x86_64"}
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope{
		Schema: portabletool.BindingArtifactSchemaV1,
		Value:  value,
	}); err == nil {
		t.Fatal("record validation accepted an unsupported manylinux policy major")
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
