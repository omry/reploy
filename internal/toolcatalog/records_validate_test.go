package toolcatalog

import (
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
		}(), want: "must name a target record"},
		{name: "manifest source outside revision", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/payloads/demo-linux-amd64"),
				Source:         recordTestReference("tool:demo/releases/1.2.3/revisions/2/sources/demo-linux-amd64"),
			}}
			return &value
		}(), want: "in revision"},
		{name: "manifest source appends segments to a source record", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/payloads/demo-linux-amd64"),
				Source:         recordTestReference("tool:demo/releases/1.2.3/revisions/1/sources/demo/extra"),
			}}
			return &value
		}(), want: "artifact source record"},
		{name: "manifest source leaf is not an identifier", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/payloads/demo-linux-amd64"),
				Source:         recordTestReference("tool:demo/releases/1.2.3/revisions/1/sources/Demo"),
			}}
			return &value
		}(), want: "artifact source record"},
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
		{name: "manifest source mapping names an impossible binding artifact", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/bindings/python/artifacts/contract"),
				Source:         recordTestReference("tool:demo/releases/1.2.3/revisions/1/sources/demo-linux-amd64"),
			}}
			return &value
		}(), want: "payload or binding artifact"},
		{name: "manifest source mapping appends segments to a binding artifact", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/bindings/python/artifacts/linux-amd64/extra"),
				Source:         recordTestReference("tool:demo/releases/1.2.3/revisions/1/sources/demo-linux-amd64"),
			}}
			return &value
		}(), want: "payload or binding artifact"},
		{name: "manifest source mapping payload leaf lacks a platform", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ArtifactSources = []ArtifactSourceMappingV1{{
				ArtifactSHA256: recordTestDigest,
				Artifact:       recordTestReference("tool:demo/releases/1.2.3/payloads/demo"),
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
		}(), want: "must name a payload record"},
		{name: "empty target selection", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.Selections = []TargetSelectionV1{{Dimension: "browser", Value: "chromium", Payloads: []RecordReferenceV1{}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}}}
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
		{name: "payload escape", value: func() any {
			value := *(values[6].(*PayloadRecordV1))
			value.Executables = []string{"../chrome"}
			return &value
		}(), want: "invalid segment"},
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
		{name: "profile name", value: func() any {
			value := *(values[10].(*ValidationProfileRecordV1))
			value.ID = "tool:demo/releases/1.2.3/validation/profiles/Bad"
			return &value
		}(), want: "canonical name"},
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
	// The default must name an advertised release, so the release it names is
	// the one this record advertises.
	segment, err := encodeToolVersionSegmentV1(value.DefaultVersion)
	if err != nil {
		t.Fatal(err)
	}
	value.Releases = []RecordReferenceV1{recordTestReference("tool:demo/releases/" + segment + "/revisions/1/manifest")}
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
			mutate: func(v any) { v.(*IntegrationFixtureRecordV1).Selections = map[string][]string{"browser": {"Chromium"}} }},
		{name: "fixture selection contains a space", index: 9, wantSub: "canonical identifiers",
			mutate: func(v any) {
				v.(*IntegrationFixtureRecordV1).Selections = map[string][]string{"browser": {"bad selection"}}
			}},
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

func TestRebuiltRecordShapesAreValidatedV1(t *testing.T) {
	const release = "tool:demo/releases/1.2.3"
	validPayload := recordTestReference(release + "/payloads/demo-linux-amd64")
	validProfile := recordTestReference(release + "/validation/profiles/default")
	for _, testCase := range []struct {
		name    string
		index   int
		mutate  func(any)
		wantSub string
	}{
		{name: "manifest profiles empty", index: 1, wantSub: "must not be empty", mutate: func(value any) {
			value.(*ReleaseManifestV1).ValidationProfiles = []RecordReferenceV1{}
		}},
		{name: "contract binding option", index: 2, wantSub: "canonical identifiers", mutate: func(value any) {
			value.(*ReleaseContractV1).Binding.Options = []string{"Python"}
		}},
		{name: "contract selection tuple", index: 2, wantSub: "not advertised", mutate: func(value any) {
			value.(*ReleaseContractV1).Selections = SelectionSchemaV1{
				Dimensions:   []SelectionDimensionV1{{Name: "browser", Options: []string{"chromium"}}},
				Combinations: []SelectionCombinationV1{{"browser": {"webkit"}}},
			}
		}},
		{name: "contract compatibility constraints", index: 2, wantSub: "unique sorted", mutate: func(value any) {
			value.(*ReleaseContractV1).CompatibilityConstraints = []string{"z", "a"}
		}},
		{name: "target profile namespace", index: 3, wantSub: "validation profile", mutate: func(value any) {
			value.(*TargetRecordV1).ValidationProfiles = []RecordReferenceV1{recordTestReference("tool:other/releases/1.2.3/validation/profiles/default")}
		}},
		{name: "target support cases absent", index: 3, wantSub: "support cases", mutate: func(value any) {
			value.(*TargetRecordV1).SupportCases = nil
		}},
		{name: "target support case selections absent", index: 3, wantSub: "dimension-keyed map", mutate: func(value any) {
			value.(*TargetRecordV1).SupportCases[0].Selections = nil
		}},
		{name: "target support cases duplicate", index: 3, wantSub: "unique and sorted", mutate: func(value any) {
			target := value.(*TargetRecordV1)
			target.SupportCases = append(target.SupportCases, cloneTargetSupportCasesV1(target.SupportCases)...)
		}},
		{name: "target support cases unsorted", index: 3, wantSub: "unique and sorted", mutate: func(value any) {
			value.(*TargetRecordV1).SupportCases = []TargetSupportCaseV1{
				{Context: "runtime", Bindings: []string{}, Selections: map[string][]string{}},
				{Context: "build", Bindings: []string{}, Selections: map[string][]string{}},
			}
		}},
		{name: "target binding payload namespace", index: 3, wantSub: "payload record", mutate: func(value any) {
			binding := targetBindingWithArtifactV1(release + "/bindings/python/artifacts/linux-amd64")[0]
			binding.Payloads = []RecordReferenceV1{recordTestReference("tool:other/releases/1.2.3/payloads/demo-linux-amd64")}
			value.(*TargetRecordV1).Bindings = []TargetBindingV1{binding}
		}},
		{name: "target binding profile namespace", index: 3, wantSub: "validation profile", mutate: func(value any) {
			binding := targetBindingWithArtifactV1(release + "/bindings/python/artifacts/linux-amd64")[0]
			binding.ValidationProfiles = []RecordReferenceV1{recordTestReference(release + "/validation/profiles/Bad")}
			value.(*TargetRecordV1).Bindings = []TargetBindingV1{binding}
		}},
		{name: "target selections unsorted", index: 3, wantSub: "unique, sorted", mutate: func(value any) {
			value.(*TargetRecordV1).Selections = []TargetSelectionV1{
				{Dimension: "browser", Value: "webkit", Payloads: []RecordReferenceV1{validPayload}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
				{Dimension: "browser", Value: "chromium", Payloads: []RecordReferenceV1{validPayload}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{}, ValidationProfiles: []RecordReferenceV1{}},
			}
		}},
		{name: "target selection profile namespace", index: 3, wantSub: "validation profile", mutate: func(value any) {
			value.(*TargetRecordV1).Selections = []TargetSelectionV1{{
				Dimension: "browser", Value: "chromium", Payloads: []RecordReferenceV1{validPayload}, PackageSets: []RecordReferenceV1{}, Exports: []ToolExportV1{},
				ValidationProfiles: []RecordReferenceV1{recordTestReference(release + "/validation/profiles/Bad")},
			}}
		}},
		{name: "binding CLI", index: 4, wantSub: "absolute path", mutate: func(value any) {
			value.(*BindingContractV1).CLI.Path = "bin/demo"
		}},
		{name: "payload executable outside root", index: 6, wantSub: "outside archive root", mutate: func(value any) {
			value.(*PayloadRecordV1).Executables = []string{"other/demo"}
		}},
		{name: "fixture binding", index: 9, wantSub: "canonical identifiers", mutate: func(value any) {
			value.(*IntegrationFixtureRecordV1).Bindings = []string{"Python"}
		}},
		{name: "fixture dimension", index: 9, wantSub: "dimension", mutate: func(value any) {
			value.(*IntegrationFixtureRecordV1).Selections = map[string][]string{"Browser": {"chromium"}}
		}},
		{name: "fixture selection order", index: 9, wantSub: "unique sorted", mutate: func(value any) {
			value.(*IntegrationFixtureRecordV1).Selections = map[string][]string{"browser": {"webkit", "chromium"}}
		}},
		{name: "fixture profile namespace", index: 9, wantSub: "validation profile", mutate: func(value any) {
			value.(*IntegrationFixtureRecordV1).ValidationProfiles = []RecordReferenceV1{recordTestReference(release + "/validation/profiles/Bad")}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value := validRecordValuesV1()[testCase.index]
			testCase.mutate(value)
			err := validateLoadedRecordV1(loadedRecordV1{ID: recordIDV1(value), Schema: recordSchemaV1(value), Value: value})
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}

	profile := *(validRecordValuesV1()[10].(*ValidationProfileRecordV1))
	profile.ID = release + "/validation/profiles/smoke"
	if err := validateLoadedRecordV1(loadedRecordV1{ID: profile.ID, Schema: profile.Schema, Value: &profile}); err != nil {
		t.Fatalf("named validation profile rejected: %v", err)
	}
	if err := validateProfileReferenceListV1("profiles", []RecordReferenceV1{validProfile}, release, false); err != nil {
		t.Fatalf("valid profile list rejected: %v", err)
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

// The design requires an opaque default_version to name one advertised release,
// because a versionless opaque request normalizes to equality with it.
func TestOpaqueDefaultVersionMustNameAnAdvertisedReleaseV1(t *testing.T) {
	opaque := func(defaultVersion string, coordinates ...string) *ToolRecordV1 {
		value := *(validRecordValuesV1()[0].(*ToolRecordV1))
		value.VersionScheme = "opaque"
		value.DefaultVersion = defaultVersion
		value.Releases = nil
		for _, coordinate := range coordinates {
			segment, err := encodeToolVersionSegmentV1(coordinate)
			if err != nil {
				t.Fatalf("encodeToolVersionSegmentV1(%q): %v", coordinate, err)
			}
			value.Releases = append(value.Releases, recordTestReference("tool:demo/releases/"+segment+"/revisions/1/manifest"))
		}
		return &value
	}
	for _, testCase := range []struct {
		name           string
		value          *ToolRecordV1
		wantAcceptance bool
	}{
		{name: "default is advertised", value: opaque("2024ru1", "2024ru1"), wantAcceptance: true},
		{name: "default is one of several", value: opaque("2024ru1", "2023ru9", "2024ru1"), wantAcceptance: true},
		{name: "default names no release", value: opaque("2024ru2", "2024ru1")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateLoadedRecordV1(loadedRecordV1{ID: testCase.value.ID, Schema: testCase.value.Schema, Value: testCase.value})
			if testCase.wantAcceptance {
				if err != nil {
					t.Errorf("rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "must name an advertised release") {
				t.Errorf("error = %v, want an unadvertised-default rejection", err)
			}
		})
	}
}

// targetBindingWithArtifactV1 builds the one advertised binding entry with a
// caller-chosen artifact reference, so a test can vary only that reference.
func targetBindingWithArtifactV1(artifactID string) []TargetBindingV1 {
	const release = "tool:demo/releases/1.2.3"
	return []TargetBindingV1{{
		Name:               "python",
		Contract:           recordTestReference(release + "/bindings/python/contract"),
		Artifacts:          []RecordReferenceV1{recordTestReference(artifactID)},
		Payloads:           []RecordReferenceV1{},
		PackageSets:        []RecordReferenceV1{},
		Exports:            []ToolExportV1{},
		ValidationProfiles: []RecordReferenceV1{},
	}}
}

// A namespace prefix alone admits IDs no record can own. Every cross-record
// reference must be rejected unless it matches the shape its owning record's ID
// validator enforces.
func TestCrossRecordReferencesRequireOwnableIDsV1(t *testing.T) {
	values := validRecordValuesV1()
	const release = "tool:demo/releases/1.2.3"
	for _, testCase := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "release target with extra segments", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Targets = []RecordReferenceV1{recordTestReference(release + "/targets/debian/12/amd64/extra")}
			return &value
		}(), want: "must name a target record"},
		{name: "release target with unsupported architecture", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.Targets = []RecordReferenceV1{recordTestReference(release + "/targets/debian/12/sparc")}
			return &value
		}(), want: "must name a target record"},
		{name: "tool release coordinate violates the version scheme", value: func() any {
			value := *(values[0].(*ToolRecordV1))
			value.Releases = []RecordReferenceV1{recordTestReference("tool:demo/releases/banana/revisions/1/manifest")}
			return &value
		}(), want: "not canonical SemVer"},
		{name: "tool release revision is not canonical", value: func() any {
			value := *(values[0].(*ToolRecordV1))
			value.Releases = []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/revisions/latest/manifest")}
			return &value
		}(), want: "revision"},
		{name: "tool release revision is zero", value: func() any {
			value := *(values[0].(*ToolRecordV1))
			value.Releases = []RecordReferenceV1{recordTestReference("tool:demo/releases/1.2.3/revisions/0/manifest")}
			return &value
		}(), want: "revision"},
		{name: "target integration fixture leaf is not an identifier", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.IntegrationFixtures = []RecordReferenceV1{recordTestReference(release + "/validation/fixtures/debian.12")}
			return &value
		}(), want: "must name a fixture record"},
		{name: "release validation profile appends a segment", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ValidationProfiles = []RecordReferenceV1{recordTestReference(release + "/validation/profiles/default/extra")}
			return &value
		}(), want: "release validation profile"},
		{name: "release validation profile with a nondefault leaf", value: func() any {
			value := *(values[1].(*ReleaseManifestV1))
			value.ValidationProfiles = []RecordReferenceV1{recordTestReference(release + "/validation/profiles/Bad")}
			return &value
		}(), want: "release validation profile"},
		{name: "target package set with extra segments", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.PackageSets = []RecordReferenceV1{recordTestReference(release + "/package-sets/base/extra")}
			return &value
		}(), want: "must name a native package-set record"},
		{name: "target payload leaf lacks a platform", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.Payloads = []RecordReferenceV1{recordTestReference(release + "/payloads/demo")}
			return &value
		}(), want: "must name a payload record"},
		{name: "target integration fixture with extra segments", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.IntegrationFixtures = []RecordReferenceV1{recordTestReference(release + "/validation/fixtures/debian-12-amd64/extra")}
			return &value
		}(), want: "must name a fixture record"},
		{name: "target binding artifact of another binding", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.Bindings = targetBindingWithArtifactV1(release + "/bindings/other/artifacts/linux-amd64")
			return &value
		}(), want: "must name an artifact of binding"},
		{name: "target binding artifact with a nonplatform leaf", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			value.Bindings = targetBindingWithArtifactV1(release + "/bindings/python/artifacts/contract")
			return &value
		}(), want: "must name an artifact of binding"},
		{name: "target binding package set with extra segments", value: func() any {
			value := *(values[3].(*TargetRecordV1))
			bindings := targetBindingWithArtifactV1(release + "/bindings/python/artifacts/linux-amd64")
			bindings[0].PackageSets = []RecordReferenceV1{recordTestReference(release + "/package-sets/base/extra")}
			value.Bindings = bindings
			return &value
		}(), want: "must name a native package-set record"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateLoadedRecordV1(loadedRecordV1{ID: recordIDV1(testCase.value), Schema: recordSchemaV1(testCase.value), Value: testCase.value})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("error = %v, want substring %q", err, testCase.want)
			}
		})
	}
}

// Every sorted string collection carries the same per-collection bound as the
// reference lists, so a caller cannot bypass it by choosing a string field.
func TestSortedStringCollectionsAreBoundedV1(t *testing.T) {
	tooMany := make([]string, maxDefinitionReferences+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("%06d", index)
	}
	values := validRecordValuesV1()
	for _, testCase := range []struct {
		name  string
		value any
	}{
		{name: "binding requirements", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.Requirements = tooMany
			return &value
		}()},
		{name: "supported Python", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.SupportedPython = tooMany
			return &value
		}()},
		{name: "binding supported tags", value: func() any {
			value := *(values[4].(*BindingContractV1))
			value.SupportedTags = tooMany
			return &value
		}()},
		{name: "native package requirements", value: func() any {
			value := *(values[8].(*NativePackageSetV1))
			value.Requirements = tooMany
			return &value
		}()},
		{name: "native package repositories", value: func() any {
			value := *(values[8].(*NativePackageSetV1))
			value.Repositories = tooMany
			return &value
		}()},
		{name: "binding artifact tags", value: func() any {
			value := *(values[5].(*BindingArtifactRecordV1))
			value.Tags = tooMany
			return &value
		}()},
		{name: "integration fixture selections", value: func() any {
			value := *(values[9].(*IntegrationFixtureRecordV1))
			value.Selections = map[string][]string{"browser": tooMany}
			return &value
		}()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateLoadedRecordV1(loadedRecordV1{ID: recordIDV1(testCase.value), Schema: recordSchemaV1(testCase.value), Value: testCase.value})
			if err == nil || !strings.Contains(err.Error(), "at most") {
				t.Errorf("oversized %s error = %v", testCase.name, err)
			}
		})
	}
}

func TestReleaseManifestAcceptsEncodedSchemeNativeVersion(t *testing.T) {
	value := *(validRecordValuesV1()[1].(*ReleaseManifestV1))
	value.Targets = append([]RecordReferenceV1{}, value.Targets...)
	value.Version = "1!2"
	prefix := "tool:demo/releases/1%212"
	value.ID = prefix + "/revisions/1/manifest"
	value.Contract.ID = prefix + "/contract"
	value.ValidationProfiles = append([]RecordReferenceV1{}, value.ValidationProfiles...)
	value.ValidationProfiles[0].ID = prefix + "/validation/profiles/default"
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

func TestValidateBindingAndSelectionSchemasV1(t *testing.T) {
	if err := validateBindingSetSchemaV1(BindingSetSchemaV1{Options: []string{"node", "python"}}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		binding BindingSetSchemaV1
		want    string
	}{
		{name: "nil options", binding: BindingSetSchemaV1{Options: nil}, want: "must use an array"},
		{name: "invalid option", binding: BindingSetSchemaV1{Options: []string{"Python"}}, want: "canonical identifiers"},
		{name: "unsorted options", binding: BindingSetSchemaV1{Options: []string{"python", "node"}}, want: "unique sorted"},
	} {
		t.Run("binding "+test.name, func(t *testing.T) {
			err := validateBindingSetSchemaV1(test.binding)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	validSelections := SelectionSchemaV1{
		Dimensions: []SelectionDimensionV1{{Name: "browser", Options: []string{"chromium", "webkit"}}},
		Combinations: []SelectionCombinationV1{
			{"browser": {"chromium"}},
			{"browser": {"webkit"}},
		},
	}
	if err := validateSelectionSchemaV1(validSelections); err != nil {
		t.Fatal(err)
	}
	optionalSelection := SelectionSchemaV1{
		Dimensions: []SelectionDimensionV1{
			{Name: "browser", Options: []string{"chromium"}},
			{Name: "mode", Options: []string{"headless"}},
		},
		Combinations: []SelectionCombinationV1{{"browser": {"chromium"}}},
	}
	if err := validateSelectionSchemaV1(optionalSelection); err != nil {
		t.Fatalf("omitted optional dimension rejected: %v", err)
	}
	canonicalByteOrder := SelectionSchemaV1{
		Dimensions: []SelectionDimensionV1{{Name: "browser", Options: []string{"chromium", "webkit"}}},
		Combinations: []SelectionCombinationV1{
			{"browser": {"chromium", "webkit"}},
			{"browser": {"chromium"}},
		},
	}
	if err := validateSelectionSchemaV1(canonicalByteOrder); err != nil {
		t.Fatalf("canonical encoded-byte order rejected: %v", err)
	}
	tooManyCombinations := make([]SelectionCombinationV1, maxDefinitionValidationCases+1)
	for index := range tooManyCombinations {
		tooManyCombinations[index] = SelectionCombinationV1{"browser": {"chromium"}}
	}
	for _, test := range []struct {
		name       string
		selections SelectionSchemaV1
		want       string
	}{
		{name: "nil dimensions", selections: SelectionSchemaV1{Dimensions: nil, Combinations: []SelectionCombinationV1{}}, want: "bounded array"},
		{name: "invalid option", selections: SelectionSchemaV1{Dimensions: []SelectionDimensionV1{{Name: "browser", Options: []string{"Chromium"}}}, Combinations: []SelectionCombinationV1{{"browser": {"Chromium"}}}}, want: "canonical identifiers"},
		{name: "missing combinations", selections: SelectionSchemaV1{Dimensions: validSelections.Dimensions, Combinations: []SelectionCombinationV1{}}, want: "both be empty"},
		{name: "nil combination", selections: SelectionSchemaV1{Dimensions: validSelections.Dimensions, Combinations: []SelectionCombinationV1{nil}}, want: "dimension-keyed map"},
		{name: "undeclared dimension", selections: SelectionSchemaV1{Dimensions: validSelections.Dimensions, Combinations: []SelectionCombinationV1{{"mode": {"headless"}}}}, want: "is not declared"},
		{name: "empty value set", selections: SelectionSchemaV1{Dimensions: validSelections.Dimensions, Combinations: []SelectionCombinationV1{{"browser": {}}}}, want: "must not be empty"},
		{name: "undeclared value", selections: SelectionSchemaV1{Dimensions: validSelections.Dimensions, Combinations: []SelectionCombinationV1{{"browser": {"firefox"}}}}, want: "not advertised"},
		{name: "duplicate combinations", selections: SelectionSchemaV1{Dimensions: validSelections.Dimensions, Combinations: []SelectionCombinationV1{{"browser": {"chromium"}}, {"browser": {"chromium"}}}}, want: "unique and sorted"},
		{name: "semantic rather than encoded order", selections: SelectionSchemaV1{Dimensions: canonicalByteOrder.Dimensions, Combinations: []SelectionCombinationV1{{"browser": {"chromium"}}, {"browser": {"chromium", "webkit"}}}}, want: "unique and sorted"},
		{name: "too many combinations", selections: SelectionSchemaV1{Dimensions: validSelections.Dimensions, Combinations: tooManyCombinations}, want: "at most"},
	} {
		t.Run("selections "+test.name, func(t *testing.T) {
			err := validateSelectionSchemaV1(test.selections)
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
	valid := RecordProbeV1{Path: "/opt/demo/bin/demo", Args: []string{"--version"}}
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

func TestBindingArtifactPlatformTagPoliciesFollowTheirPEPsV1(t *testing.T) {
	arm64 := func(tag string) *BindingArtifactRecordV1 {
		value := *(validRecordValuesV1()[5].(*BindingArtifactRecordV1))
		value.ID = "tool:demo/releases/1.2.3/bindings/python/artifacts/linux-arm64"
		value.Platform = "linux/arm64"
		value.Filename = "demo-1.2.3-py3-none-" + tag + ".whl"
		value.Tags = []string{"py3-none-" + tag}
		return &value
	}
	// PEP 513 and PEP 571 define manylinux1 and manylinux2010 for x86_64 and
	// i686 only. PEP 599 adds aarch64 with manylinux2014, and PEP 600 covers it
	// with the versioned manylinux_x_y policies.
	for _, tag := range []string{"manylinux1_aarch64", "manylinux2010_aarch64"} {
		value := arm64(tag)
		if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: value}); err == nil {
			t.Errorf("%s accepted on linux/arm64", tag)
		}
	}
	for _, tag := range []string{"manylinux2014_aarch64", "manylinux_2_28_aarch64", "linux_aarch64"} {
		value := arm64(tag)
		if err := validateLoadedRecordV1(loadedRecordV1{ID: value.ID, Schema: value.Schema, Value: value}); err != nil {
			t.Errorf("%s rejected on linux/arm64: %v", tag, err)
		}
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
