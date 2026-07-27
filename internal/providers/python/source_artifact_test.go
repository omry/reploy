package python

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func testPythonSourceInput(
	component string,
	logicalPackage string,
	version string,
	manifest canonical.Digest,
	artifact canonical.Digest,
) providers.ResolvedSourceInput {
	metadata := SourceWheelMetadataV1{Distribution: logicalPackage, Version: version, Tags: []string{"py3-none-any"}}
	return providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV1, Component: component, LogicalPackage: logicalPackage,
		SourceManifestDigest: manifest, BuilderProfile: SourceBuilderProfileV1,
		BuildSettings: CanonicalSourceBuildSettingsV1(), EcosystemMetadata: CanonicalSourceMetadataV1(metadata),
		ArtifactDigest: artifact,
	}
}

func TestNewResolvedSourceInputV1BindsInspectedWheelMetadata(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "demo_pkg-1.2.3-py3-none-any.whl")
	writeTestWheel(t, dir, filepath.Base(filename), "Demo-Pkg", "1.2.3", nil)
	digest, err := fileSHA256(filename)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := providerstore.ArtifactDescriptor{
		LogicalPath: "source-wheels/" + filepath.Base(filename), Kind: "wheel",
		Size: strconv.FormatInt(info.Size(), 10), SHA256: canonical.Digest("sha256:" + digest),
	}
	described, describedMetadata, err := DescribeSourceWheelFileV1(filename, descriptor.LogicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if described != descriptor || describedMetadata.Distribution != "demo-pkg" || describedMetadata.Version != "1.2.3" {
		t.Fatalf("described wheel = %#v, %#v", described, describedMetadata)
	}
	metadata, err := InspectSourceWheelFileV1(filename, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewResolvedSourceInputV1("application", "demo-pkg", schemaTestDigest("4"), descriptor, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if source.BuilderProfile != SourceBuilderProfileV1 || source.ArtifactDigest != descriptor.SHA256 ||
		source.EcosystemMetadata.Value["version"] != "1.2.3" || source.BuildSettings.Value["vcs_metadata"] != false {
		t.Fatalf("source = %#v", source)
	}
	if err := ValidateResolvedSourceInputV1(source); err != nil {
		t.Fatal(err)
	}

	if _, err := NewResolvedSourceInputV1("application", "other", schemaTestDigest("4"), descriptor, metadata); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("distribution mismatch error = %v", err)
	}
	stale := descriptor
	stale.SHA256 = schemaTestDigest("5")
	if _, err := InspectSourceWheelFileV1(filename, stale); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("stale descriptor error = %v", err)
	}
}
