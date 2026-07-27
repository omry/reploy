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
	SourceManifestSchemaV1      = "python-source-manifest-v1"
	SourceBuilderProfileV1      = "python-uv-wheel-v1"
	SourceBuilderRequirementV1  = "uv==0.11.26"
	SourceBuildSettingsSchemaV1 = "python-source-build-settings-v1"
	SourceMetadataSchemaV1      = "python-source-metadata-v1"
)

type SourceWheelMetadataV1 struct {
	Distribution string   `json:"distribution"`
	Version      string   `json:"version"`
	Tags         []string `json:"tags"`
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

// NewResolvedSourceInputV1 creates the path-free source record consumed by the
// common provider graph after a wheel has been inspected.
func NewResolvedSourceInputV1(
	component string,
	logicalPackage string,
	sourceManifestDigest canonical.Digest,
	artifact providerstore.ArtifactDescriptor,
	metadata SourceWheelMetadataV1,
) (providers.ResolvedSourceInput, error) {
	if err := validateSourceWheelMetadataV1(metadata); err != nil {
		return providers.ResolvedSourceInput{}, err
	}
	normalized := NormalizeDistributionName(logicalPackage)
	if normalized == "" || normalized != logicalPackage || metadata.Distribution != normalized {
		return providers.ResolvedSourceInput{}, fmt.Errorf("Python source wheel distribution %q does not match declared package %q", metadata.Distribution, logicalPackage)
	}
	if err := artifact.Validate(); err != nil {
		return providers.ResolvedSourceInput{}, fmt.Errorf("Python source wheel artifact: %w", err)
	}
	if artifact.Kind != "wheel" {
		return providers.ResolvedSourceInput{}, fmt.Errorf("Python source artifact must be a wheel")
	}
	source := providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV1, Component: component, LogicalPackage: logicalPackage,
		SourceManifestDigest: sourceManifestDigest, BuilderProfile: SourceBuilderProfileV1,
		BuildSettings: CanonicalSourceBuildSettingsV1(), EcosystemMetadata: CanonicalSourceMetadataV1(metadata),
		ArtifactDigest: artifact.SHA256,
	}
	if err := ValidateResolvedSourceInputV1(source); err != nil {
		return providers.ResolvedSourceInput{}, err
	}
	return source, nil
}

func CanonicalSourceBuildSettingsV1() providers.CanonicalProviderData {
	return providers.CanonicalProviderData{
		Schema: SourceBuildSettingsSchemaV1,
		Value: canonical.Object{
			"builder_requirement":    SourceBuilderRequirementV1,
			"source_manifest_schema": SourceManifestSchemaV1,
			"vcs_metadata":           false,
		},
	}
}

func CanonicalSourceMetadataV1(metadata SourceWheelMetadataV1) providers.CanonicalProviderData {
	tags := make([]any, len(metadata.Tags))
	for index, tag := range metadata.Tags {
		tags[index] = tag
	}
	return providers.CanonicalProviderData{
		Schema: SourceMetadataSchemaV1,
		Value: canonical.Object{
			"distribution": metadata.Distribution,
			"version":      metadata.Version,
			"tags":         tags,
		},
	}
}

func ValidateResolvedSourceInputV1(source providers.ResolvedSourceInput) error {
	if err := providers.ValidateResolvedSourceInput(source); err != nil {
		return err
	}
	if source.BuilderProfile != SourceBuilderProfileV1 {
		return fmt.Errorf("Python source builder profile must be %q", SourceBuilderProfileV1)
	}
	if !reflect.DeepEqual(source.BuildSettings, CanonicalSourceBuildSettingsV1()) {
		return fmt.Errorf("Python source build settings do not match %q", SourceBuildSettingsSchemaV1)
	}
	metadata, err := decodeSourceMetadataV1(source.EcosystemMetadata)
	if err != nil {
		return err
	}
	if NormalizeDistributionName(source.LogicalPackage) != source.LogicalPackage || metadata.Distribution != source.LogicalPackage {
		return fmt.Errorf("Python source metadata distribution %q does not match logical package %q", metadata.Distribution, source.LogicalPackage)
	}
	return nil
}

func decodeSourceMetadataV1(data providers.CanonicalProviderData) (SourceWheelMetadataV1, error) {
	if data.Schema != SourceMetadataSchemaV1 || len(data.Value) != 3 {
		return SourceWheelMetadataV1{}, fmt.Errorf("Python source metadata must use schema %q and the exact value shape", SourceMetadataSchemaV1)
	}
	encoded, err := canonical.Marshal(data.Value)
	if err != nil {
		return SourceWheelMetadataV1{}, err
	}
	var metadata SourceWheelMetadataV1
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return SourceWheelMetadataV1{}, fmt.Errorf("decode Python source metadata: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return SourceWheelMetadataV1{}, err
	}
	if err := validateSourceWheelMetadataV1(metadata); err != nil {
		return SourceWheelMetadataV1{}, err
	}
	return metadata, nil
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
