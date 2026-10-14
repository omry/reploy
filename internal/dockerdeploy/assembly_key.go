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
	buildKit, err := materializationBuildKitSetting()
	if err != nil {
		return providers.AssemblyKeyV1{}, "", err
	}
	rendererProfile := MaterializationRendererProfile
	if buildKit == "0" {
		// The syntax digest remains a rendered input, not evidence that the
		// Docker-compatible API builder executed that frontend.
		rendererProfile += "-docker-api"
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
