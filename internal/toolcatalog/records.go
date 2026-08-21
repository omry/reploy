package toolcatalog

import "github.com/omry/reploy/internal/canonical"

const (
	ToolRecordSchemaV1           = "portable-tool-v1"
	ReleaseManifestSchemaV1      = "portable-tool-release-manifest-v1"
	ReleaseContractSchemaV1      = "portable-tool-release-contract-v1"
	TargetRecordSchemaV1         = "portable-tool-target-v1"
	BindingContractSchemaV1      = "portable-tool-binding-v1"
	BindingArtifactSchemaV1      = "portable-tool-binding-artifact-v1"
	PayloadRecordSchemaV1        = "portable-tool-payload-v1"
	ArtifactSourceRecordSchemaV1 = "portable-tool-artifact-source-v1"
	NativePackageSetSchemaV1     = "portable-tool-package-set-v1"
	IntegrationFixtureSchemaV1   = "portable-tool-integration-fixture-v1"
	ValidationProfileSchemaV1    = "portable-tool-validation-profile-v1"
	ValidationEvidenceSchemaV1   = "portable-tool-validation-evidence-v1"
	SelectedClosureIdentityV1    = "portable-tool-selected-closure-v1"
	portableToolRecordIdentityV1 = "portable-tool-record-v1"
)

type RecordReferenceV1 struct {
	ID     string           `json:"id"`
	Digest canonical.Digest `json:"digest"`
}

type ToolRecordV1 struct {
	Schema         string              `json:"schema"`
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	VersionScheme  string              `json:"version_scheme"`
	DefaultVersion string              `json:"default_version,omitempty"`
	Summary        string              `json:"summary"`
	Upstream       string              `json:"upstream"`
	Source         string              `json:"source"`
	License        string              `json:"license"`
	Documentation  string              `json:"documentation"`
	Releases       []RecordReferenceV1 `json:"releases"`
}

type ReleaseManifestV1 struct {
	Schema             string                    `json:"schema"`
	ID                 string                    `json:"id"`
	Tool               string                    `json:"tool"`
	Version            string                    `json:"version"`
	Aliases            []string                  `json:"aliases"`
	Revision           string                    `json:"revision"`
	Contract           RecordReferenceV1         `json:"contract"`
	Targets            []RecordReferenceV1       `json:"targets"`
	ArtifactSources    []ArtifactSourceMappingV1 `json:"artifact_sources"`
	Provenance         []string                  `json:"provenance"`
	ValidationProfiles []RecordReferenceV1       `json:"validation_profiles"`
}

type ArtifactSourceMappingV1 struct {
	ArtifactSHA256 canonical.Digest  `json:"artifact_sha256"`
	Artifact       RecordReferenceV1 `json:"artifact"`
	Source         RecordReferenceV1 `json:"source"`
}

type ReleaseContractV1 struct {
	Schema             string             `json:"schema"`
	ID                 string             `json:"id"`
	Contexts           []string           `json:"contexts"`
	SupportedReploy    string             `json:"supported_reploy"`
	Binding            BindingSetSchemaV1 `json:"binding"`
	Selections         SelectionSchemaV1  `json:"selections"`
	Runtime            *RecordRuntimeV1   `json:"runtime,omitempty"`
	Exports            []ToolExportV1     `json:"exports"`
	ResolverPrimitives []string           `json:"resolver_primitives"`
}

type BindingSetSchemaV1 struct {
	Options []string `json:"options"`
}

type SelectionSchemaV1 struct {
	Dimensions   []SelectionDimensionV1   `json:"dimensions"`
	Combinations []SelectionCombinationV1 `json:"combinations"`
}

type SelectionDimensionV1 struct {
	Name    string   `json:"name"`
	Options []string `json:"options"`
}

type SelectionCombinationV1 struct {
	Values [][]string `json:"values"`
}

type ToolExportV1 struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type RecordProbeV1 struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type RecordRuntimeV1 struct {
	InstallRoot string                        `json:"install_root"`
	Environment []RecordEnvironmentVariableV1 `json:"environment"`
}

type RecordEnvironmentVariableV1 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TargetRecordV1 struct {
	Schema              string              `json:"schema"`
	ID                  string              `json:"id"`
	Target              TargetIdentityV1    `json:"target"`
	PackageSets         []RecordReferenceV1 `json:"package_sets"`
	Bindings            []TargetBindingV1   `json:"bindings"`
	Payloads            []RecordReferenceV1 `json:"payloads"`
	Selections          []TargetSelectionV1 `json:"selections"`
	Exports             []ToolExportV1      `json:"exports"`
	IntegrationFixtures []RecordReferenceV1 `json:"integration_fixtures"`
	ValidationProfiles  []RecordReferenceV1 `json:"validation_profiles"`
}

type TargetIdentityV1 struct {
	Platform           string `json:"platform"`
	OSReleaseID        string `json:"os_release_id"`
	VersionID          string `json:"version_id"`
	OCIArchitecture    string `json:"oci_architecture"`
	NativeArchitecture string `json:"native_architecture"`
	PackageManager     string `json:"package_manager"`
}

type TargetBindingV1 struct {
	Name               string              `json:"name"`
	Contract           RecordReferenceV1   `json:"contract"`
	Artifacts          []RecordReferenceV1 `json:"artifacts"`
	Payloads           []RecordReferenceV1 `json:"payloads"`
	PackageSets        []RecordReferenceV1 `json:"package_sets"`
	Exports            []ToolExportV1      `json:"exports"`
	ValidationProfiles []RecordReferenceV1 `json:"validation_profiles"`
}

type TargetSelectionV1 struct {
	Dimension          string              `json:"dimension"`
	Value              string              `json:"value"`
	Payloads           []RecordReferenceV1 `json:"payloads"`
	PackageSets        []RecordReferenceV1 `json:"package_sets"`
	Exports            []ToolExportV1      `json:"exports"`
	ValidationProfiles []RecordReferenceV1 `json:"validation_profiles"`
}

type BindingContractV1 struct {
	Schema            string               `json:"schema"`
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Package           string               `json:"package"`
	Requirements      []string             `json:"requirements"`
	SupportedPython   []string             `json:"supported_python"`
	SupportedTags     []string             `json:"supported_tags"`
	BundledComponents []BundledComponentV1 `json:"bundled_components"`
	CLI               string               `json:"cli"`
}

type BindingArtifactRecordV1 struct {
	Schema            string               `json:"schema"`
	ID                string               `json:"id"`
	Binding           string               `json:"binding"`
	Contract          RecordReferenceV1    `json:"contract"`
	Name              string               `json:"name"`
	EcosystemVersion  string               `json:"ecosystem_version"`
	Platform          string               `json:"platform"`
	Filename          string               `json:"filename"`
	Size              string               `json:"size"`
	SHA256            canonical.Digest     `json:"sha256"`
	Resolver          string               `json:"resolver"`
	Tags              []string             `json:"tags"`
	RequiresPython    string               `json:"requires_python"`
	BundledComponents []BundledComponentV1 `json:"bundled_components"`
}

type BundledComponentV1 struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

type PayloadRecordV1 struct {
	Schema           string           `json:"schema"`
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	Revision         string           `json:"revision"`
	UpstreamVersion  string           `json:"upstream_version"`
	Platform         string           `json:"platform"`
	LogicalPath      string           `json:"logical_path"`
	Kind             string           `json:"kind"`
	Size             string           `json:"size"`
	SHA256           canonical.Digest `json:"sha256"`
	Resolver         string           `json:"resolver"`
	Entries          string           `json:"entries"`
	UnpackedSize     string           `json:"unpacked_size"`
	InstallDirectory string           `json:"install_directory"`
	ArchiveRoot      string           `json:"archive_root"`
	Executable       string           `json:"executable"`
}

type ArtifactSourceRecordV1 struct {
	Schema      string           `json:"schema"`
	ID          string           `json:"id"`
	SHA256      canonical.Digest `json:"sha256"`
	Mirrors     []string         `json:"mirrors"`
	Provenance  []string         `json:"provenance"`
	Diagnostics []string         `json:"diagnostics"`
}

type NativePackageSetV1 struct {
	Schema             string   `json:"schema"`
	ID                 string   `json:"id"`
	Manager            string   `json:"manager"`
	Requirements       []string `json:"requirements"`
	Repositories       []string `json:"repositories"`
	ValidationMetadata []string `json:"validation_metadata"`
}

type IntegrationFixtureRecordV1 struct {
	Schema             string              `json:"schema"`
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Target             TargetIdentityV1    `json:"target"`
	BaseImage          string              `json:"base_image"`
	BaseImageDigest    canonical.Digest    `json:"base_image_digest"`
	Context            string              `json:"context"`
	Bindings           []string            `json:"bindings"`
	Selections         map[string][]string `json:"selections"`
	ValidationProfiles []RecordReferenceV1 `json:"validation_profiles"`
}

type ValidationProfileRecordV1 struct {
	Schema  string          `json:"schema"`
	ID      string          `json:"id"`
	Tool    string          `json:"tool"`
	Version string          `json:"version"`
	Probes  []RecordProbeV1 `json:"probes"`
}

type ValidationEvidenceV1 struct {
	Schema                string              `json:"schema"`
	Tool                  string              `json:"tool"`
	Version               string              `json:"version"`
	Revision              string              `json:"revision"`
	ManifestDigest        canonical.Digest    `json:"manifest_digest"`
	SelectedClosureDigest canonical.Digest    `json:"selected_closure_digest"`
	Target                TargetIdentityV1    `json:"target"`
	BaseImageDigest       canonical.Digest    `json:"base_image_digest"`
	Bindings              []string            `json:"bindings"`
	Selections            map[string][]string `json:"selections"`
	Fixture               string              `json:"fixture"`
	ValidatorVersion      string              `json:"validator_version"`
	Result                string              `json:"result"`
	ValidatorOutputDigest canonical.Digest    `json:"validator_output_digest"`
}

type recordHeaderV1 struct {
	Schema string `json:"schema"`
	ID     string `json:"id"`
}

type loadedRecordV1 struct {
	ID     string
	Schema string
	Digest canonical.Digest
	Path   string
	Value  any
}
