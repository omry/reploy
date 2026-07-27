package dockerdeploy

import (
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func testPythonResolvedSource(
	component string,
	logicalPackage string,
	version string,
	manifest canonical.Digest,
	artifact canonical.Digest,
) providers.ResolvedSourceInput {
	metadata := pythonprovider.SourceWheelMetadataV1{
		Distribution: logicalPackage, Version: version, Tags: []string{"py3-none-any"},
	}
	return providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV1, Component: component, LogicalPackage: logicalPackage,
		SourceManifestDigest: manifest, BuilderProfile: pythonprovider.SourceBuilderProfileV1,
		BuildSettings: pythonprovider.CanonicalSourceBuildSettingsV1(), EcosystemMetadata: pythonprovider.CanonicalSourceMetadataV1(metadata),
		ArtifactDigest: artifact,
	}
}
