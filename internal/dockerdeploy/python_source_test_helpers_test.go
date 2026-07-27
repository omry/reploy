package dockerdeploy

import (
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func testPythonResolvedSource(
	component string,
	logicalPackage string,
	version string,
	manifest canonical.Digest,
	artifact canonical.Digest,
) providers.ResolvedSourceInput {
	sourceArtifact := providerstore.ArtifactDescriptor{
		LogicalPath: "sdists/" + logicalPackage + "-" + version + ".tar.gz",
		Kind:        "sdist", Size: "1", SHA256: manifest,
	}
	return testPythonResolvedSourceWithSourceArtifact(
		component, logicalPackage, version, manifest, sourceArtifact, artifact,
	)
}

func testPythonResolvedSourceWithSourceArtifact(
	component string,
	logicalPackage string,
	version string,
	sourceInput canonical.Digest,
	sourceArtifact providerstore.ArtifactDescriptor,
	outputArtifact canonical.Digest,
) providers.ResolvedSourceInput {
	metadata := pythonprovider.SourceWheelMetadataV1{
		Distribution: logicalPackage, Version: version, Tags: []string{"py3-none-any"},
	}
	return providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV2, Component: component, LogicalPackage: logicalPackage,
		SourceInputDigest: sourceInput, SourceArtifactDigest: sourceArtifact.SHA256,
		BuildEnvironmentDigest: sourceInput,
		BuilderProfile:         pythonprovider.SourceBuilderProfileV1,
		BuildSettings:          pythonprovider.CanonicalSourceBuildSettingsV1(),
		EcosystemMetadata:      pythonprovider.CanonicalSourceMetadataV2(metadata, sourceArtifact),
		OutputArtifactDigest:   outputArtifact,
	}
}
