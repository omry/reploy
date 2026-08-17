package toolcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestValidateLoadedRecordV1RejectsInvalidFieldsBySchema(t *testing.T) {
	values := validRecordValuesV1()
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "tool URL query", value: func() any { value := *(values[0].(*ToolRecordV1)); value.Source += "?token=secret"; return &value }(), want: "credential-free HTTPS"},
		{name: "tool version scheme", value: func() any { value := *(values[0].(*ToolRecordV1)); value.VersionScheme = "debian"; return &value }(), want: "version scheme is unsupported"},
		{name: "ordered default version", value: func() any { value := *(values[0].(*ToolRecordV1)); value.DefaultVersion = "1.2.3"; return &value }(), want: "must not declare"},
		{name: "opaque default version", value: func() any {
			value := *(values[0].(*ToolRecordV1))
			value.VersionScheme = "opaque"
			return &value
		}(), want: "requires a canonical default"},
		{name: "manifest revision", value: func() any { value := *(values[1].(*ReleaseManifestV1)); value.Revision = "01"; return &value }(), want: "canonical decimal"},
		{name: "manifest duplicate alias", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Aliases = []string{"1.2", "1.2"}
			return &value
		}(), want: "unique sorted"},
		{name: "manifest exact alias", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Aliases = []string{"1.2.3"}
			return &value
		}(), want: "redundantly equals"},
		{name: "manifest unencoded version ID", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Version = "1!2"
			return &value
		}(), want: "release manifest ID must be"},
		{name: "manifest noncanonical provenance", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Provenance = []string{"https://example.com/a", "https://example.com/%61"}
			return &value
		}(), want: "canonical spelling"},
		{name: "manifest contract outside release", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Contract.ID = "tool:demo/releases/2.0.0/contract"
			return &value
		}(), want: "current release contract"},
		{name: "manifest target outside tool", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Targets = append([]RecordReferenceV1{}, value.Targets...)
			value.Targets[0].ID = "tool:other/releases/1.2.3/targets/debian/12/amd64"
			return &value
		}(), want: "inside namespace"},
		{name: "manifest source outside revision", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/payloads/demo-linux-amd64"),
				Source:         recordTestReference("tool:demo/releases/1.2.3/revisions/2/sources/demo-linux-amd64"),
			}}
			return &value
		}(), want: "inside namespace"},
		{name: "manifest source mapping names a nonartifact record", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/contract"),
				Source:         recordTestReference("tool:demo/releases/1.2.3/revisions/1/sources/demo-linux-amd64"),
			}}
			return &value
		}(), want: "payload or binding artifact"},
		{name: "manifest source mapping names a binding contract", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/bindings/python/contract"),
				Source:         recordTestReference("tool:demo/releases/1.2.3/revisions/1/sources/demo-linux-amd64"),
			}}
			return &value
		}(), want: "payload or binding artifact"},
		{name: "contract context", value: func() any {
			value := *(values[2].(*ReleaseContractV1))
			value.Contexts = []string{"install"}
			return &value
		}(), want: "unsupported"},
		{name: "contract supported Reploy", value: func() any {
			value := *(values[2].(*ReleaseContractV1))
			value.SupportedReploy = ">= 0.0"
			return &value
		}(), want: "canonical SemVer"},
		{name: "contract ID", value: func() any {
			value := *(values[2].(*ReleaseContractV1))
			value.ID = "tool:demo/releases/1.2.3/payloads/contract"
			return &value
		}(), want: "release contract ID must use"},
		{name: "target ID", value: func() any { value := *(values[3].(*TargetRecordV1)); value.ID += "-wrong"; return &value }(), want: "complete tool release target namespace"},
		{name: "target unrelated prefix", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.ID = "tool:demo/unrelated/targets/debian/12/amd64"
			return &value
		}(), want: "tool release namespace"},
		{name: "target missing fixtures", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.IntegrationFixtures = []RecordReferenceV1{}
			return &value
		}(), want: "must not be empty"},
		{name: "target payload outside release", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.Payloads = append([]RecordReferenceV1{}, value.Payloads...)
			value.Payloads[0].ID = "tool:demo/releases/2.0.0/payloads/demo-linux-amd64"
			return &value
		}(), want: "inside namespace"},
		{name: "empty target selection", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.Selections = []TargetSelectionV1{{Name: "browser", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, Probes: []RecordProbeV1{}}}
			return &value
		}(), want: "must contribute"},
		{name: "binding requirements", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.Requirements = []string{"support>=1,<2", "demo==1.2.3"}
			return &value
		}(), want: "unique sorted"},
		{name: "binding package-manager option", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.Requirements = []string{"--index-url=https://example.invalid/simple"}
			return &value
		}(), want: "must not be a package-manager option"},
		{name: "binding malformed requirement", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.Requirements = []string{"demo ???"}
			return &value
		}(), want: "must not contain whitespace"},
		{name: "binding malformed distribution", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.Requirements = []string{"demo-"}
			return &value
		}(), want: "invalid Python package root requirement"},
		{name: "conflicting binding requirements", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.Requirements = []string{"demo==1", "demo==2"}
			return &value
		}(), want: "name the same distribution"},
		{name: "binding malformed supported Python", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.SupportedPython = []string{"banana"}
			return &value
		}(), want: "must use major.minor"},
		{name: "binding contract ID", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.ID = "tool:demo"
			return &value
		}(), want: "binding contract ID must use"},
		{name: "binding artifact size", value: func() any { value := *(values[5].(*BindingArtifactRecordV1)); value.Size = "042"; return &value }(), want: "canonical decimal"},
		{name: "binding artifact ID", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			value.ID = "tool:demo"
			return &value
		}(), want: "must match binding"},
		{name: "binding artifact arbitrary tag", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			value.Tags = []string{"anything"}
			return &value
		}(), want: "exactly match"},
		{name: "binding artifact wrong platform", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			value.Filename = "demo-1.2.3-py3-none-win_amd64.whl"
			value.Tags = []string{"py3-none-win_amd64"}
			return &value
		}(), want: "incompatible with platform"},
		{name: "binding artifact musllinux platform", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			value.Filename = "demo-1.2.3-py3-none-musllinux_1_2_x86_64.whl"
			value.Tags = []string{"py3-none-musllinux_1_2_x86_64"}
			return &value
		}(), want: "incompatible with platform"},
		{name: "binding artifact requires Python", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			value.RequiresPython = "banana"
			return &value
		}(), want: "canonical PEP 440"},
		{name: "binding artifact malformed wheel filename", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			value.Filename = "demo-1-extra-build-py3-none-manylinux1_x86_64.whl"
			return &value
		}(), want: "must contain distribution"},
		{name: "binding artifact malformed wheel version", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			value.Filename = "demo-banana-py3-none-manylinux1_x86_64.whl"
			return &value
		}(), want: "invalid distribution or version"},
		{name: "binding artifact oversized compressed tags", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			pythonTags := strings.TrimSuffix(strings.Repeat("py3.", maxDefinitionReferences+1), ".")
			value.Filename = "demo-1.2.3-" + pythonTags + "-none-manylinux1_x86_64.whl"
			return &value
		}(), want: "expands to more than"},
		{name: "payload escape", value: func() any { value := *(values[6].(*PayloadRecordV1)); value.Executable = "../chrome"; return &value }(), want: "invalid segment"},
		{name: "payload ID", value: func() any {
			value := *(values[6].(*PayloadRecordV1))
			value.ID = "tool:demo/releases/1.2.3/bindings/chromium"
			return &value
		}(), want: "release payload namespace"},
		{name: "source duplicate mirror", value: func() any {
			value := *(values[7].(*ArtifactSourceRecordV1))
			value.Mirrors = []string{"https://example.com/a", "https://example.com/b", "https://example.com/a"}
			return &value
		}(), want: "must be unique"},
		{name: "source noncanonical mirror", value: func() any {
			value := *(values[7].(*ArtifactSourceRecordV1))
			value.Mirrors = []string{"https://example.com/a", "https://example.com/%61"}
			return &value
		}(), want: "canonical spelling"},
		{name: "source noncanonical provenance", value: func() any {
			value := *(values[7].(*ArtifactSourceRecordV1))
			value.Provenance = []string{"https://example.com/a", "https://example.com/%61"}
			return &value
		}(), want: "canonical spelling"},
		{name: "source ID", value: func() any {
			value := *(values[7].(*ArtifactSourceRecordV1))
			value.ID = "tool:demo"
			return &value
		}(), want: "release revision source namespace"},
		{name: "package manager", value: func() any { value := *(values[8].(*NativePackageSetV1)); value.Manager = "dnf"; return &value }(), want: "identity is incomplete"},
		{name: "package requirement constraint", value: func() any {
			value := *(values[8].(*NativePackageSetV1))
			value.Requirements = []string{"libfoo>=1"}
			return &value
		}(), want: "exact Debian binary package name"},
		{name: "package requirement option", value: func() any {
			value := *(values[8].(*NativePackageSetV1))
			value.Requirements = []string{"--allow-unauthenticated"}
			return &value
		}(), want: "exact Debian binary package name"},
		{name: "conflicting package requirements", value: func() any {
			value := *(values[8].(*NativePackageSetV1))
			value.Requirements = []string{"libfoo=1", "libfoo=2"}
			return &value
		}(), want: "name the same package"},
		{name: "package set ID", value: func() any {
			value := *(values[8].(*NativePackageSetV1))
			value.ID = "tool:demo"
			return &value
		}(), want: "release package-set namespace"},
		{name: "fixture base image", value: func() any {
			value := *(values[9].(*IntegrationFixtureRecordV1))
			value.BaseImage = "https://example.com/image:tag"
			return &value
		}(), want: "canonical tagged OCI reference"},
		{name: "fixture name mismatch", value: func() any {
			value := *(values[9].(*IntegrationFixtureRecordV1))
			value.ID = "tool:demo/releases/1.2.3/validation/fixtures/other"
			return &value
		}(), want: "use its name"},
		{name: "profile network", value: func() any {
			value := *(values[10].(*ValidationProfileRecordV1))
			value.Network = "default"
			return &value
		}(), want: "disable networking"},
		{name: "profile validator version", value: func() any {
			value := *(values[10].(*ValidationProfileRecordV1))
			value.ValidatorVersion = ""
			return &value
		}(), want: "validator version"},
		{name: "profile missing probes", value: func() any {
			value := *(values[10].(*ValidationProfileRecordV1))
			value.Probes = []RecordProbeV1{}
			return &value
		}(), want: "nonempty bounded array"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := loadedRecordV1{ID: recordIDV1(test.value), Schema: recordSchemaV1(test.value), Value: test.value}
			err := validateLoadedRecordV1(record)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	valid := values[0].(*ToolRecordV1)
	if err := validateLoadedRecordV1(loadedRecordV1{ID: valid.ID, Schema: ReleaseContractSchemaV1, Value: valid}); err == nil || !strings.Contains(err.Error(), "identity is inconsistent") {
		t.Fatalf("mismatched loaded schema error = %v", err)
	}
}

func TestRecordCollectionLimitsV1(t *testing.T) {
	if err := validateReferenceListV1("references", nil); err == nil {
		t.Fatal("nil reference list was accepted")
	}
	references := make([]RecordReferenceV1, maxDefinitionReferences+1)
	if err := validateReferenceListV1("references", references); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("oversized reference error = %v", err)
	}
	values := validRecordValuesV1()
	source := *(values[7].(*ArtifactSourceRecordV1))
	source.Mirrors = make([]string, maxDefinitionArtifactMirrors+1)
	if err := validateLoadedRecordV1(loadedRecordV1{ID: source.ID, Schema: source.Schema, Value: &source}); err == nil || !strings.Contains(err.Error(), "between 1 and") {
		t.Fatalf("oversized mirror error = %v", err)
	}
	packages := *(values[8].(*NativePackageSetV1))
	packages.Requirements = []string{"bad\nrequirement"}
	if err := validateLoadedRecordV1(loadedRecordV1{ID: packages.ID, Schema: packages.Schema, Value: &packages}); err == nil || !strings.Contains(err.Error(), "canonical values") {
		t.Fatalf("control-character requirement error = %v", err)
	}
	binding := *(values[4].(*BindingContractV1))
	binding.Package = "bad package"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: binding.ID, Schema: binding.Schema, Value: &binding}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("invalid binding package error = %v", err)
	}
	payload := *(values[6].(*PayloadRecordV1))
	payload.Revision = "bad\nrevision"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: payload.ID, Schema: payload.Schema, Value: &payload}); err == nil || !strings.Contains(err.Error(), "identity is incomplete") {
		t.Fatalf("invalid payload revision error = %v", err)
	}
}

func TestToolRecordAcceptsOpaqueDefaultVersion(t *testing.T) {
	value := *(validRecordValuesV1()[0].(*ToolRecordV1))
	value.VersionScheme = "opaque"
	value.DefaultVersion = "latest!vetted"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestNewRecordFieldsAreValidated(t *testing.T) {
	// Fields added to the record model during review must be constrained here,
	// not merely declared. Each case mutates one valid record and expects a
	// diagnostic naming that field.
	for _, testCase := range []struct {
		name    string
		mutate  func(any)
		index   int
		wantSub string
	}{
		{name: "tool documentation empty", index: 0, wantSub: "must not be empty",
			mutate: func(v any) { v.(*ToolRecordV1).Documentation = "" }},
		{name: "tool documentation not a canonical URL", index: 0, wantSub: "reference URL",
			mutate: func(v any) { v.(*ToolRecordV1).Documentation = "http://example.com/docs" }},
		{name: "binding supported tag malformed", index: 4, wantSub: "canonical three-part wheel tag",
			mutate: func(v any) { v.(*BindingContractV1).SupportedTags = []string{"py3-none"} }},
		{name: "binding bundled components unsorted", index: 4, wantSub: "unique and sorted",
			mutate: func(v any) {
				v.(*BindingContractV1).BundledComponents = []BundledComponentV1{
					{Name: "zeta", Version: "1", Path: "z"}, {Name: "alpha", Version: "1", Path: "a"}}
			}},
		{name: "binding artifact resolver unsupported", index: 5, wantSub: "resolver",
			mutate: func(v any) { v.(*BindingArtifactRecordV1).Resolver = "ftp" }},
		{name: "binding artifact ecosystem version noncanonical", index: 5, wantSub: "ecosystem version",
			mutate: func(v any) { v.(*BindingArtifactRecordV1).EcosystemVersion = "1 2" }},
		{name: "binding artifact contract outside its binding", index: 5, wantSub: "contract reference must be",
			mutate: func(v any) {
				v.(*BindingArtifactRecordV1).Contract = recordTestReference("tool:demo/releases/1.2.3/bindings/node/contract")
			}},
		{name: "binding artifact name disagrees with filename", index: 5, wantSub: "must match the wheel filename",
			mutate: func(v any) { v.(*BindingArtifactRecordV1).Name = "other" }},
		{name: "binding artifact ecosystem version disagrees with filename", index: 5, wantSub: "must match the wheel filename",
			mutate: func(v any) { v.(*BindingArtifactRecordV1).EcosystemVersion = "9.9.9" }},
		{name: "payload resolver unsupported", index: 6, wantSub: "resolver",
			mutate: func(v any) { v.(*PayloadRecordV1).Resolver = "" }},
		{name: "fixture selection capitalized", index: 9, wantSub: "canonical identifiers",
			mutate: func(v any) { v.(*IntegrationFixtureRecordV1).Selections = []string{"Chromium"} }},
		{name: "fixture selection contains a space", index: 9, wantSub: "canonical identifiers",
			mutate: func(v any) { v.(*IntegrationFixtureRecordV1).Selections = []string{"bad selection"} }},
		{name: "artifact source diagnostics unsorted", index: 7, wantSub: "diagnostics",
			mutate: func(v any) { v.(*ArtifactSourceRecordV1).Diagnostics = []string{"b", "a"} }},
		{name: "package set repositories unsorted", index: 8, wantSub: "repositories",
			mutate: func(v any) { v.(*NativePackageSetV1).Repositories = []string{"b", "a"} }},
		{name: "package set validation metadata unsorted", index: 8, wantSub: "validation metadata",
			mutate: func(v any) { v.(*NativePackageSetV1).ValidationMetadata = []string{"b", "a"} }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			values := validRecordValuesV1()
			value := values[testCase.index]
			testCase.mutate(value)
			err := validateLoadedRecordV1(loadedRecordV1{ID: recordIDV1(value), Schema: recordSchemaV1(value), Value: value})
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func TestReleaseAliasesFollowTheVersionRule(t *testing.T) {
	// An alias is an alternative version coordinate, so it must accept exactly
	// what the version field accepts, including scheme-native forms.
	value := *(validRecordValuesV1()[1].(*ReleaseManifestV1))
	value.Aliases = []string{"1!2", "1.2"}
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatalf("scheme-native alias rejected: %v", err)
	}

	tooMany := make([]string, maxDefinitionReferences+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("%06d", index)
	}
	value.Aliases = tooMany
	err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value})
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("oversized alias list error = %v", err)
	}
}

func TestReleaseManifestAcceptsEncodedSchemeNativeVersion(t *testing.T) {
	value := *(validRecordValuesV1()[1].(*ReleaseManifestV1))
	value.Targets = append([]RecordReferenceV1{}, value.Targets...)
	value.Version = "1!2"
	prefix := "tool:demo/releases/1%212"
	value.ID = prefix + "/revisions/1/manifest"
	value.Contract.ID = prefix + "/contract"
	value.ValidationProfile.ID = prefix + "/validation/profiles/default"
	value.Targets[0].ID = prefix + "/targets/debian/12/amd64"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseContractAcceptsEncodedSchemeNativeVersion(t *testing.T) {
	value := *(validRecordValuesV1()[2].(*ReleaseContractV1))
	value.ID = "tool:demo/releases/1%212/contract"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestValidationProfileAcceptsEncodedSchemeNativeVersion(t *testing.T) {
	value := *(validRecordValuesV1()[10].(*ValidationProfileRecordV1))
	value.Version = "1!2"
	value.ID = "tool:demo/releases/1%212/validation/profiles/default"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequestPoliciesV1(t *testing.T) {
	validBinding := BindingRequestV1{Options: []string{"python"}, Required: true, Default: ""}
	if err := validateBindingRequestV1(validBinding); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		binding BindingRequestV1
		want    string
	}{
		{name: "nil options", binding: BindingRequestV1{Options: nil}, want: "must use an array"},
		{name: "required empty", binding: BindingRequestV1{Options: []string{}, Required: true}, want: "at least one option"},
		{name: "invalid option", binding: BindingRequestV1{Options: []string{"Python"}}, want: "canonical identifiers"},
		{name: "unknown default", binding: BindingRequestV1{Options: []string{"python"}, Default: "node"}, want: "default binding"},
	} {
		t.Run("binding "+test.name, func(t *testing.T) {
			err := validateBindingRequestV1(test.binding)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	validSelections := SelectionRequestV1{
		Options: []string{"chromium", "webkit"}, Minimum: "1", Maximum: "2", Defaults: []string{},
		CompatibilityGroups: [][]string{{"chromium", "webkit"}},
	}
	if err := validateSelectionRequestV1(validSelections); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		selections SelectionRequestV1
		want       string
	}{
		{name: "noncanonical minimum", selections: SelectionRequestV1{Options: []string{}, Minimum: "01", Maximum: "0", Defaults: []string{}, CompatibilityGroups: [][]string{}}, want: "canonical decimal"},
		{name: "noncanonical maximum", selections: SelectionRequestV1{Options: []string{}, Minimum: "0", Maximum: "01", Defaults: []string{}, CompatibilityGroups: [][]string{}}, want: "canonical decimal"},
		{name: "invalid option", selections: SelectionRequestV1{Options: []string{"Chromium"}, Minimum: "0", Maximum: "1", Defaults: []string{}, CompatibilityGroups: [][]string{{"Chromium"}}}, want: "canonical identifiers"},
		{name: "minimum too large", selections: SelectionRequestV1{Options: []string{"chromium"}, Minimum: "2", Maximum: "1", Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium"}}}, want: "cardinality"},
		{name: "uncovered option", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "0", Maximum: "2", Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium"}}}, want: "do not cover"},
		{name: "nonmaximal groups", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "0", Maximum: "2", Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium"}, {"chromium", "webkit"}}}, want: "must be maximal"},
		{name: "unknown default", selections: SelectionRequestV1{Options: []string{"chromium"}, Minimum: "0", Maximum: "1", Defaults: []string{"webkit"}, CompatibilityGroups: [][]string{{"chromium"}}}, want: "not a declared option"},
		{name: "insufficient defaults", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "2", Maximum: "2", Defaults: []string{"chromium"}, CompatibilityGroups: [][]string{{"chromium", "webkit"}}}, want: "do not satisfy"},
		{name: "incompatible defaults", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "1", Maximum: "2", Defaults: []string{"chromium", "webkit"}, CompatibilityGroups: [][]string{{"chromium"}, {"webkit"}}}, want: "do not satisfy"},
		{name: "infeasible minimum", selections: SelectionRequestV1{Options: []string{"chromium", "webkit"}, Minimum: "2", Maximum: "2", Defaults: []string{}, CompatibilityGroups: [][]string{{"chromium"}, {"webkit"}}}, want: "cannot be satisfied"},
	} {
		t.Run("selections "+test.name, func(t *testing.T) {
			err := validateSelectionRequestV1(test.selections)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateRuntimeV1RejectsInconsistentContracts(t *testing.T) {
	valid := RecordRuntimeV1{
		InstallRoot: "/opt/demo", Environment: []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "/opt/demo"}},
	}
	if err := validateRuntimeV1([]string{"build", "runtime"}, &valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		contexts []string
		mutate   func(*RecordRuntimeV1)
		want     string
	}{
		{name: "missing runtime context", contexts: []string{"build"}, want: "inconsistent with contexts"},
		{name: "relative root", contexts: []string{"runtime"}, mutate: func(value *RecordRuntimeV1) { value.InstallRoot = "opt/demo" }, want: "inconsistent with contexts"},
		{name: "nil environment", contexts: []string{"runtime"}, mutate: func(value *RecordRuntimeV1) { value.Environment = nil }, want: "bounded array"},
		{name: "invalid environment name", contexts: []string{"runtime"}, mutate: func(value *RecordRuntimeV1) { value.Environment[0].Name = "demo_home" }, want: "unique and sorted"},
		{name: "environment NUL", contexts: []string{"runtime"}, mutate: func(value *RecordRuntimeV1) { value.Environment[0].Value = "bad\x00value" }, want: "unique and sorted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.Environment = append([]RecordEnvironmentVariableV1{}, valid.Environment...)
			if test.mutate != nil {
				test.mutate(&value)
			}
			err := validateRuntimeV1(test.contexts, &value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	if err := validateRuntimeV1([]string{"runtime"}, nil); err == nil || !strings.Contains(err.Error(), "requires a runtime contract") {
		t.Fatalf("missing runtime error = %v", err)
	}
}

func TestValidateProbeV1RequiresOfflineCanonicalExecution(t *testing.T) {
	valid := RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}, Network: "none"}
	if err := validateProbeV1(valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*RecordProbeV1)
		want   string
	}{
		{name: "relative path", mutate: func(value *RecordProbeV1) { value.Path = "demo" }, want: "absolute path"},
		{name: "nil args", mutate: func(value *RecordProbeV1) { value.Args = nil }, want: "argument array"},
		{name: "network", mutate: func(value *RecordProbeV1) { value.Network = "host" }, want: "network=none"},
		{name: "NUL", mutate: func(value *RecordProbeV1) { value.Args = []string{"bad\x00arg"} }, want: "control characters"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.Args = append([]string{}, valid.Args...)
			test.mutate(&value)
			err := validateProbeV1(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPayloadIDsEncodeSelectionAndPlatform(t *testing.T) {
	selected := *(validRecordValuesV1()[6].(*PayloadRecordV1))
	if err := validateLoadedRecordV1(loadedRecordV1{ID: selected.ID, Schema: selected.Schema, Value: &selected}); err != nil {
		t.Fatal(err)
	}
	unconditional := selected
	unconditional.Selection = ""
	unconditional.ID = "tool:demo/releases/1.2.3/payloads/chromium-linux-amd64"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: unconditional.ID, Schema: unconditional.Schema, Value: &unconditional}); err != nil {
		t.Fatal(err)
	}
}

func TestBindingArtifactAcceptsUniversalAndCompressedWheelTagsV1(t *testing.T) {
	value := *(validRecordValuesV1()[5].(*BindingArtifactRecordV1))
	value.Filename = "demo-1.2.3-py2.py3-none-any.whl"
	value.Tags = []string{"py2-none-any", "py3-none-any"}
	if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: &value}); err != nil {
		t.Fatal(err)
	}
}

func TestTypedParameterSchemasAndTargetConstraintsV1(t *testing.T) {
	contract := *(validRecordValuesV1()[2].(*ReleaseContractV1))
	colorDefault := "blue"
	workerDefault := "2"
	contract.Parameters = []ParameterSchemaV1{
		{Name: "color", Type: "enum", Required: false, Default: &colorDefault, Values: []string{"blue", "green"}, Minimum: "", Maximum: ""},
		{Name: "debug", Type: "boolean", Required: false, Default: nil, Values: []string{}, Minimum: "", Maximum: ""},
		{Name: "workers", Type: "integer", Required: true, Default: &workerDefault, Values: []string{}, Minimum: "-2", Maximum: "4"},
	}
	if err := validateLoadedRecordV1(loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Value: &contract}); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(&contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRecordV1("contract.json", payload); err != nil {
		t.Fatalf("decode contract with explicit null default: %v", err)
	}
	withoutDefault := bytes.Replace(payload, []byte(`,"default":null`), nil, 1)
	if _, err := decodeRecordV1("contract.json", withoutDefault); err == nil || !strings.Contains(err.Error(), `required field "default" is missing`) {
		t.Fatalf("missing nullable default error = %v", err)
	}

	target := *(validRecordValuesV1()[3].(*TargetRecordV1))
	target.Parameters = []TargetParameterConstraintV1{
		{Name: "color", Values: []string{"blue"}, Minimum: "", Maximum: ""},
		{Name: "workers", Values: []string{}, Minimum: "0", Maximum: "2"},
	}
	if err := validateLoadedRecordV1(loadedRecordV1{ID: target.ID, Schema: target.Schema, Value: &target}); err != nil {
		t.Fatal(err)
	}

	contract.Parameters[2].Default = func() *string { value := "5"; return &value }()
	if err := validateLoadedRecordV1(loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Value: &contract}); err == nil || !strings.Contains(err.Error(), "outside its declared domain") {
		t.Fatalf("out-of-range parameter default error = %v", err)
	}
	target.Parameters[1].Minimum = "02"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: target.ID, Schema: target.Schema, Value: &target}); err == nil || !strings.Contains(err.Error(), "canonical bounded integer") {
		t.Fatalf("noncanonical target parameter bound error = %v", err)
	}
	contract.Parameters[2].Default = &workerDefault
	contract.Parameters[2].Minimum = "0"
	contract.Parameters[2].Maximum = "1024"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Value: &contract}); err == nil || !strings.Contains(err.Error(), "enumerable domain limit") {
		t.Fatalf("oversized parameter domain error = %v", err)
	}
	contract.Parameters = make([]ParameterSchemaV1, 11)
	for index := range contract.Parameters {
		contract.Parameters[index] = ParameterSchemaV1{Name: fmt.Sprintf("p%02d", index), Type: "boolean", Required: true, Values: []string{}}
	}
	if err := validateLoadedRecordV1(loadedRecordV1{ID: contract.ID, Schema: contract.Schema, Value: &contract}); err == nil || !strings.Contains(err.Error(), "Cartesian coverage") {
		t.Fatalf("Cartesian parameter coverage error = %v", err)
	}
}

func TestRecordReferencesIDsAndQuantitiesAreCanonical(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
		want string
	}{
		{name: "empty tool name", run: func() error { return validateRecordIDV1("tool:") }, want: "invalid tool name"},
		{name: "uppercase tool name", run: func() error { return validateRecordIDV1("tool:Demo") }, want: "invalid tool name"},
		{name: "empty segment", run: func() error { return validateRecordIDV1("tool:demo//contract") }, want: "invalid path segment"},
		{name: "bad digest", run: func() error {
			return validateRecordReferenceV1(RecordReferenceV1{ID: "tool:demo", Digest: "sha256:ABC"})
		}, want: "digest"},
		{name: "leading zero", run: func() error { return validateCanonicalDecimalV1("size", "01", true) }, want: "canonical decimal"},
		{name: "zero", run: func() error { return validateCanonicalDecimalV1("size", "0", true) }, want: "positive decimal"},
		{name: "overflow", run: func() error { return validateCanonicalDecimalV1("size", "9223372036854775808", true) }, want: "positive decimal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRecordReferencesRequireCanonicalReleaseNamespaces(t *testing.T) {
	valid := recordTestReference("tool:demo/releases/1%212/contract")
	if err := validateRecordReferenceV1(valid); err != nil {
		t.Fatalf("valid encoded release reference: %v", err)
	}
	for _, id := range []string{
		"tool:demo/releases/%31/revisions/1/manifest",
		"tool:demo/releases/1/contract%21",
		"tool:demo/other/record",
	} {
		reference := recordTestReference(id)
		if err := validateRecordReferenceV1(reference); err == nil {
			t.Fatalf("noncanonical record reference %q was accepted", id)
		}
	}
}
