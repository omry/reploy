package python

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	SourceInputManifestSchemaV1 = "python-source-input-manifest-v1"
	SourceBuilderProfileV1      = "python-uv-sdist-wheel-v1"
	SourceBuilderProfileV2      = "python-uv-local-recipe-v2"
	SourceBuilderRequirementV1  = "uv==0.11.26"
	SourceBuildSettingsSchemaV1 = "python-sdist-wheel-build-settings-v1"
	SourceBuildSettingsSchemaV2 = "python-local-recipe-build-settings-v2"
	SourceMetadataSchemaV2      = "python-source-metadata-v2"
	SourceBuildTypePEP517       = "pep517"
	SourceBuildTypeLegacy       = "setuptools-legacy"
)

type SourceWheelMetadataV1 struct {
	Distribution string   `json:"distribution"`
	Version      string   `json:"version"`
	Tags         []string `json:"tags"`
}

type SourceMetadataV2 struct {
	Distribution      string           `json:"distribution"`
	Version           string           `json:"version"`
	Tags              []string         `json:"tags"`
	SourceLogicalPath string           `json:"source_logical_path"`
	SourceKind        string           `json:"source_kind"`
	SourceSize        string           `json:"source_size"`
	SourceSHA256      canonical.Digest `json:"source_sha256"`
}

// DescribeSourceWheelFileV1 reads normal wheel metadata and computes the
// descriptor that must be used to publish the same bytes.
func DescribeSourceWheelFileV1(filename string, logicalPath string) (providerstore.ArtifactDescriptor, SourceWheelMetadataV1, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, SourceWheelMetadataV1{}, err
	}
	if !info.Mode().IsRegular() {
		return providerstore.ArtifactDescriptor{}, SourceWheelMetadataV1{}, fmt.Errorf("Python source wheel %q must be a regular file", path.Base(filename))
	}
	wheel, err := inspectWheel(filename)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, SourceWheelMetadataV1{}, err
	}
	descriptor := providerstore.ArtifactDescriptor{
		LogicalPath: logicalPath,
		Kind:        "wheel",
		Size:        strconv.FormatInt(info.Size(), 10),
		SHA256:      canonical.Digest("sha256:" + wheel.SHA256),
	}
	if err := descriptor.Validate(); err != nil {
		return providerstore.ArtifactDescriptor{}, SourceWheelMetadataV1{}, fmt.Errorf("Python source wheel descriptor: %w", err)
	}
	if path.Base(descriptor.LogicalPath) != path.Base(filename) {
		return providerstore.ArtifactDescriptor{}, SourceWheelMetadataV1{}, fmt.Errorf("Python source wheel descriptor does not identify %q", path.Base(filename))
	}
	metadata := SourceWheelMetadataV1{
		Distribution: wheel.Distribution, Version: wheel.Version, Tags: append([]string{}, wheel.Tags...),
	}
	if err := validateSourceWheelMetadataV1(metadata); err != nil {
		return providerstore.ArtifactDescriptor{}, SourceWheelMetadataV1{}, err
	}
	return descriptor, metadata, nil
}

// InspectSourceWheelFileV1 reads normal wheel metadata and binds it to the
// supplied artifact descriptor before that wheel is offered to pip.
func InspectSourceWheelFileV1(filename string, descriptor providerstore.ArtifactDescriptor) (SourceWheelMetadataV1, error) {
	if err := descriptor.Validate(); err != nil {
		return SourceWheelMetadataV1{}, fmt.Errorf("Python source wheel descriptor: %w", err)
	}
	if descriptor.Kind != "wheel" || path.Base(descriptor.LogicalPath) != path.Base(filename) {
		return SourceWheelMetadataV1{}, fmt.Errorf("Python source wheel descriptor does not identify %q", path.Base(filename))
	}
	observed, metadata, err := DescribeSourceWheelFileV1(filename, descriptor.LogicalPath)
	if err != nil {
		return SourceWheelMetadataV1{}, err
	}
	if descriptor.SHA256 != observed.SHA256 {
		return SourceWheelMetadataV1{}, fmt.Errorf("Python source wheel %q digest does not match its descriptor", path.Base(filename))
	}
	if descriptor.Size != observed.Size {
		return SourceWheelMetadataV1{}, fmt.Errorf("Python source wheel %q size does not match its descriptor", path.Base(filename))
	}
	return metadata, nil
}

// NewResolvedSourceInputV2 creates the path-free source record consumed by the
// common provider graph after the retained sdist and wheel have been inspected.
func NewResolvedSourceInputV2(
	component string,
	logicalPackage string,
	sourceInputDigest canonical.Digest,
	sourceArtifact providerstore.ArtifactDescriptor,
	buildEnvironmentDigest canonical.Digest,
	outputArtifact providerstore.ArtifactDescriptor,
	metadata SourceWheelMetadataV1,
) (providers.ResolvedSourceInput, error) {
	return NewResolvedSourceInputWithBuildV2(
		component, logicalPackage, sourceInputDigest, sourceArtifact, buildEnvironmentDigest,
		outputArtifact, metadata, SourceBuilderProfileV1, CanonicalSourceBuildSettingsV1(),
	)
}

// NewResolvedSourceInputWithBuildV2 preserves the v2 common source record
// while allowing a selected project-owned recipe to bind its canonical build
// protocol and tool requirements into cache and lock identity.
func NewResolvedSourceInputWithBuildV2(
	component string,
	logicalPackage string,
	sourceInputDigest canonical.Digest,
	sourceArtifact providerstore.ArtifactDescriptor,
	buildEnvironmentDigest canonical.Digest,
	outputArtifact providerstore.ArtifactDescriptor,
	metadata SourceWheelMetadataV1,
	builderProfile string,
	buildSettings providers.CanonicalProviderData,
) (providers.ResolvedSourceInput, error) {
	if err := validateSourceWheelMetadataV1(metadata); err != nil {
		return providers.ResolvedSourceInput{}, err
	}
	normalized := NormalizeDistributionName(logicalPackage)
	if normalized == "" || normalized != logicalPackage || metadata.Distribution != normalized {
		return providers.ResolvedSourceInput{}, fmt.Errorf("Python source wheel distribution %q does not match declared package %q", metadata.Distribution, logicalPackage)
	}
	if err := sourceArtifact.Validate(); err != nil {
		return providers.ResolvedSourceInput{}, fmt.Errorf("Python source distribution artifact: %w", err)
	}
	if sourceArtifact.Kind != "sdist" || path.Dir(sourceArtifact.LogicalPath) != "sdists" ||
		!strings.HasSuffix(strings.ToLower(sourceArtifact.LogicalPath), ".tar.gz") {
		return providers.ResolvedSourceInput{}, fmt.Errorf("Python source distribution artifact must be a .tar.gz sdist beneath sdists")
	}
	if err := outputArtifact.Validate(); err != nil {
		return providers.ResolvedSourceInput{}, fmt.Errorf("Python source wheel artifact: %w", err)
	}
	if outputArtifact.Kind != "wheel" {
		return providers.ResolvedSourceInput{}, fmt.Errorf("Python source output artifact must be a wheel")
	}
	source := providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV2, Component: component, LogicalPackage: logicalPackage,
		SourceInputDigest: sourceInputDigest, SourceArtifactDigest: sourceArtifact.SHA256,
		BuildEnvironmentDigest: buildEnvironmentDigest,
		BuilderProfile:         builderProfile, BuildSettings: buildSettings,
		EcosystemMetadata:    CanonicalSourceMetadataV2(metadata, sourceArtifact),
		OutputArtifactDigest: outputArtifact.SHA256,
	}
	if err := ValidateResolvedSourceInputV2(source); err != nil {
		return providers.ResolvedSourceInput{}, err
	}
	return source, nil
}

func CanonicalSourceBuildSettingsV2(
	buildType string,
	recipeDigest canonical.Digest,
	tools []string,
) (providers.CanonicalProviderData, error) {
	if buildType != SourceBuildTypePEP517 && buildType != SourceBuildTypeLegacy {
		return providers.CanonicalProviderData{}, fmt.Errorf("Python local recipe build type %q is unsupported", buildType)
	}
	if err := recipeDigest.Validate(); err != nil {
		return providers.CanonicalProviderData{}, fmt.Errorf("Python local recipe digest: %w", err)
	}
	toolValues := make([]any, len(tools))
	for index, tool := range tools {
		if tool == "" || strings.TrimSpace(tool) != tool {
			return providers.CanonicalProviderData{}, fmt.Errorf("Python local recipe tool must be nonempty plain text")
		}
		if index > 0 && tools[index-1] >= tool {
			return providers.CanonicalProviderData{}, fmt.Errorf("Python local recipe tools must be unique and sorted")
		}
		toolValues[index] = tool
	}
	return providers.CanonicalProviderData{
		Schema: SourceBuildSettingsSchemaV2,
		Value: canonical.Object{
			"builder_requirement": SourceBuilderRequirementV1,
			"build_sequence":      "sdist-then-wheel",
			"build_type":          buildType,
			"network":             "default",
			"recipe_digest":       string(recipeDigest),
			"source_input_schema": SourceInputManifestSchemaV1,
			"tools":               toolValues,
			"uv_sources":          false,
			"vcs_metadata":        false,
		},
	}, nil
}

func CanonicalSourceBuildSettingsV1() providers.CanonicalProviderData {
	return providers.CanonicalProviderData{
		Schema: SourceBuildSettingsSchemaV1,
		Value: canonical.Object{
			"builder_requirement": SourceBuilderRequirementV1,
			"build_sequence":      "sdist-then-wheel",
			"network":             "default",
			"source_input_schema": SourceInputManifestSchemaV1,
			"uv_sources":          false,
			"vcs_metadata":        false,
		},
	}
}

func CanonicalSourceMetadataV2(
	metadata SourceWheelMetadataV1,
	sourceArtifact providerstore.ArtifactDescriptor,
) providers.CanonicalProviderData {
	tags := make([]any, len(metadata.Tags))
	for index, tag := range metadata.Tags {
		tags[index] = tag
	}
	return providers.CanonicalProviderData{
		Schema: SourceMetadataSchemaV2,
		Value: canonical.Object{
			"distribution":        metadata.Distribution,
			"version":             metadata.Version,
			"tags":                tags,
			"source_logical_path": sourceArtifact.LogicalPath,
			"source_kind":         sourceArtifact.Kind,
			"source_size":         sourceArtifact.Size,
			"source_sha256":       string(sourceArtifact.SHA256),
		},
	}
}

func ValidateResolvedSourceInputV2(source providers.ResolvedSourceInput) error {
	if err := providers.ValidateResolvedSourceInput(source); err != nil {
		return err
	}
	if err := ValidateSourceBuildIdentityV2(source.BuilderProfile, source.BuildSettings); err != nil {
		return err
	}
	metadata, err := decodeSourceMetadataV2(source.EcosystemMetadata)
	if err != nil {
		return err
	}
	if NormalizeDistributionName(source.LogicalPackage) != source.LogicalPackage || metadata.Distribution != source.LogicalPackage {
		return fmt.Errorf("Python source metadata distribution %q does not match logical package %q", metadata.Distribution, source.LogicalPackage)
	}
	sourceArtifact := metadata.sourceArtifact()
	if err := sourceArtifact.Validate(); err != nil {
		return fmt.Errorf("Python source distribution artifact: %w", err)
	}
	if sourceArtifact.Kind != "sdist" || path.Dir(sourceArtifact.LogicalPath) != "sdists" ||
		!strings.HasSuffix(strings.ToLower(sourceArtifact.LogicalPath), ".tar.gz") {
		return fmt.Errorf("Python source distribution artifact must be a .tar.gz sdist beneath sdists")
	}
	if source.SourceArtifactDigest != sourceArtifact.SHA256 {
		return fmt.Errorf("Python source distribution digest does not match its descriptor")
	}
	return nil
}

func ValidateSourceBuildIdentityV2(
	builderProfile string,
	buildSettings providers.CanonicalProviderData,
) error {
	switch builderProfile {
	case SourceBuilderProfileV1:
		if !reflect.DeepEqual(buildSettings, CanonicalSourceBuildSettingsV1()) {
			return fmt.Errorf("Python source build settings do not match %q", SourceBuildSettingsSchemaV1)
		}
	case SourceBuilderProfileV2:
		if err := validateSourceBuildSettingsV2(buildSettings); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"Python source builder profile must be %q or %q",
			SourceBuilderProfileV1, SourceBuilderProfileV2,
		)
	}
	return nil
}

func validateSourceBuildSettingsV2(settings providers.CanonicalProviderData) error {
	if settings.Schema != SourceBuildSettingsSchemaV2 || len(settings.Value) != 9 {
		return fmt.Errorf("Python local recipe build settings must use schema %q and its exact shape", SourceBuildSettingsSchemaV2)
	}
	buildType, buildOK := settings.Value["build_type"].(string)
	recipeText, recipeOK := settings.Value["recipe_digest"].(string)
	toolValues, toolsOK := settings.Value["tools"].([]any)
	if !buildOK || !recipeOK || !toolsOK {
		return fmt.Errorf("Python local recipe build settings have invalid typed fields")
	}
	tools := make([]string, len(toolValues))
	for index, value := range toolValues {
		tool, ok := value.(string)
		if !ok {
			return fmt.Errorf("Python local recipe build setting tool %d must be text", index)
		}
		tools[index] = tool
	}
	expected, err := CanonicalSourceBuildSettingsV2(buildType, canonical.Digest(recipeText), tools)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(settings, expected) {
		return fmt.Errorf("Python local recipe build settings are not canonical")
	}
	return nil
}

func SourceArtifactDescriptorV2(source providers.ResolvedSourceInput) (providerstore.ArtifactDescriptor, error) {
	if err := ValidateResolvedSourceInputV2(source); err != nil {
		return providerstore.ArtifactDescriptor{}, err
	}
	metadata, err := decodeSourceMetadataV2(source.EcosystemMetadata)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, err
	}
	return metadata.sourceArtifact(), nil
}

func SourceWheelMetadataV2(source providers.ResolvedSourceInput) (SourceWheelMetadataV1, error) {
	if err := ValidateResolvedSourceInputV2(source); err != nil {
		return SourceWheelMetadataV1{}, err
	}
	metadata, err := decodeSourceMetadataV2(source.EcosystemMetadata)
	if err != nil {
		return SourceWheelMetadataV1{}, err
	}
	return SourceWheelMetadataV1{
		Distribution: metadata.Distribution,
		Version:      metadata.Version,
		Tags:         append([]string{}, metadata.Tags...),
	}, nil
}

func decodeSourceMetadataV2(data providers.CanonicalProviderData) (SourceMetadataV2, error) {
	if data.Schema != SourceMetadataSchemaV2 || len(data.Value) != 7 {
		return SourceMetadataV2{}, fmt.Errorf("Python source metadata must use schema %q and the exact value shape", SourceMetadataSchemaV2)
	}
	encoded, err := canonical.Marshal(data.Value)
	if err != nil {
		return SourceMetadataV2{}, err
	}
	var metadata SourceMetadataV2
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return SourceMetadataV2{}, fmt.Errorf("decode Python source metadata: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return SourceMetadataV2{}, err
	}
	if err := validateSourceWheelMetadataV1(SourceWheelMetadataV1{
		Distribution: metadata.Distribution, Version: metadata.Version, Tags: metadata.Tags,
	}); err != nil {
		return SourceMetadataV2{}, err
	}
	return metadata, nil
}

func (metadata SourceMetadataV2) sourceArtifact() providerstore.ArtifactDescriptor {
	return providerstore.ArtifactDescriptor{
		LogicalPath: metadata.SourceLogicalPath,
		Kind:        metadata.SourceKind,
		Size:        metadata.SourceSize,
		SHA256:      metadata.SourceSHA256,
	}
}

func validateSourceWheelMetadataV1(metadata SourceWheelMetadataV1) error {
	if err := blueprint.ValidatePythonDistributionName("Python source wheel distribution", metadata.Distribution); err != nil {
		return err
	}
	if NormalizeDistributionName(metadata.Distribution) != metadata.Distribution {
		return fmt.Errorf("Python source wheel distribution must be normalized")
	}
	if strings.TrimSpace(metadata.Version) == "" || metadata.Version != strings.TrimSpace(metadata.Version) {
		return fmt.Errorf("Python source wheel version must be nonempty valid text")
	}
	if metadata.Tags == nil || !sort.StringsAreSorted(metadata.Tags) {
		return fmt.Errorf("Python source wheel tags must use a sorted array")
	}
	for index, tag := range metadata.Tags {
		if tag == "" || index > 0 && metadata.Tags[index-1] == tag {
			return fmt.Errorf("Python source wheel tags must be nonempty and unique")
		}
	}
	return nil
}
