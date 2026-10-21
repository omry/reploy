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
	sourceArtifact := providerstore.ArtifactDescriptor{
		LogicalPath: "sdists/" + logicalPackage + "-" + version + ".tar.gz",
		Kind:        "sdist", Size: "1", SHA256: manifest,
	}
	return providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV2, Component: component, LogicalPackage: logicalPackage,
		SourceInputDigest: manifest, SourceArtifactDigest: sourceArtifact.SHA256,
		BuildEnvironmentDigest: manifest,
		BuilderProfile:         SourceBuilderProfileV1,
		BuildSettings:          CanonicalSourceBuildSettingsV1(),
		EcosystemMetadata:      CanonicalSourceMetadataV2(metadata, sourceArtifact),
		OutputArtifactDigest:   artifact,
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
	sourceArtifact := providerstore.ArtifactDescriptor{
		LogicalPath: "sdists/demo_pkg-1.2.3.tar.gz", Kind: "sdist",
		Size: "1", SHA256: schemaTestDigest("6"),
	}
	source, err := NewResolvedSourceInputV2(
		"application", "demo-pkg", schemaTestDigest("4"), sourceArtifact, schemaTestDigest("7"), descriptor, metadata,
	)
	if err != nil {
		t.Fatal(err)
	}
	if source.BuilderProfile != SourceBuilderProfileV1 || source.OutputArtifactDigest != descriptor.SHA256 ||
		source.EcosystemMetadata.Value["version"] != "1.2.3" || source.BuildSettings.Value["vcs_metadata"] != false {
		t.Fatalf("source = %#v", source)
	}
	if err := ValidateResolvedSourceInputV2(source); err != nil {
		t.Fatal(err)
	}

	if _, err := NewResolvedSourceInputV2(
		"application", "other", schemaTestDigest("4"), sourceArtifact, schemaTestDigest("7"), descriptor, metadata,
	); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("distribution mismatch error = %v", err)
	}
	stale := descriptor
	stale.SHA256 = schemaTestDigest("5")
	if _, err := InspectSourceWheelFileV1(filename, stale); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("stale descriptor error = %v", err)
	}
}

func TestCanonicalSourceBuildSettingsV2BindsLocalRecipe(t *testing.T) {
	settings, err := CanonicalSourceBuildSettingsV2(
		SourceBuildTypeLegacy, schemaTestDigest("8"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Schema != SourceBuildSettingsSchemaV2 ||
		settings.Value["build_type"] != SourceBuildTypeLegacy ||
		settings.Value["recipe_digest"] != string(schemaTestDigest("8")) {
		t.Fatalf("settings = %#v", settings)
	}
	if _, found := settings.Value["tools"]; found {
		t.Fatalf("Python source-build settings retained name-only portable tools: %#v", settings)
	}
	if err := ValidateSourceBuildIdentityV2(SourceBuilderProfileV2, settings); err != nil {
		t.Fatal(err)
	}
}

func TestSourceBuilderUVUpgradeInvalidatesProfileFacts(t *testing.T) {
	for _, buildType := range []string{"", SourceBuildTypePEP517, SourceBuildTypeLegacy} {
		name := buildType
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			source := testPythonSourceInput("application", "demo", "1.0", schemaTestDigest("1"), schemaTestDigest("2"))
			if buildType != "" {
				settings, err := CanonicalSourceBuildSettingsV2(buildType, schemaTestDigest("3"))
				if err != nil {
					t.Fatal(err)
				}
				source.BuilderProfile = SourceBuilderProfileV2
				source.BuildSettings = settings
			}
			if got := source.BuildSettings.Value["builder_requirement"]; got != "uv==0.13.4" {
				t.Fatalf("builder requirement = %v, want uv==0.13.4", got)
			}
			current := CanonicalProfileFactsV2("application", []providers.ResolvedSourceInput{source})
			if _, _, err := decodeProfileFactsV2(current); err != nil {
				t.Fatal(err)
			}
			currentBytes, err := canonical.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}

			// Keep the source, output wheel, recipe, and environment identical:
			// only the old frontend requirement should invalidate cached facts.
			source.BuildSettings.Value["builder_requirement"] = "uv==0.11.26"
			stale := CanonicalProfileFactsV2("application", []providers.ResolvedSourceInput{source})
			staleBytes, err := canonical.Marshal(stale)
			if err != nil {
				t.Fatal(err)
			}
			if string(currentBytes) == string(staleBytes) {
				t.Fatal("uv upgrade did not change canonical profile facts used in cache identity")
			}
			if _, _, err := decodeProfileFactsV2(stale); err == nil {
				t.Fatal("cached profile facts with the old uv requirement were accepted")
			}
		})
	}
}
