package dockerdeploy

import (
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const MaterializationRendererProfile = "docker-materialization-v1"

func MaterializationAssemblyKey(transaction providers.MaterializationTransaction, platform blueprint.Platform) (providers.AssemblyKeyV1, canonical.Digest, error) {
	return materializationAssemblyKey(transaction, platform, nil)
}

func materializationAssemblyKey(transaction providers.MaterializationTransaction, platform blueprint.Platform, builder *generatedImageBuilder) (providers.AssemblyKeyV1, canonical.Digest, error) {
	rendererProfile := MaterializationRendererProfile
	if generatedBuilderModeOf(builder) == generatedPodmanAPI {
		// API execution does not execute the pinned syntax frontend.
		rendererProfile += "-docker-api"
	} else if generatedBuilderModeOf(builder) != generatedBuildKit {
		return providers.AssemblyKeyV1{}, "", fmt.Errorf("unknown generated-image builder mode")
	}
	transactionDigest, err := providers.MaterializationTransactionDigest(transaction)
	if err != nil {
		return providers.AssemblyKeyV1{}, "", fmt.Errorf("materialization assembly transaction: %w", err)
	}
	_, frontend, found := strings.Cut(MaterializationDockerfileSyntax, "@")
	if !found {
		return providers.AssemblyKeyV1{}, "", fmt.Errorf("materialization Dockerfile frontend is not digest-pinned")
	}
	key := providers.AssemblyKeyV1{
		Schema:             providers.AssemblyKeySchemaV1,
		Parent:             transaction.Upstream,
		TransactionDigest:  transactionDigest,
		RendererProfile:    rendererProfile,
		DockerfileFrontend: canonical.Digest(frontend),
		Platform:           platform,
	}
	digest, err := providers.AssemblyKeyDigest(key)
	if err != nil {
		return providers.AssemblyKeyV1{}, "", fmt.Errorf("materialization assembly key: %w", err)
	}
	return key, digest, nil
}
