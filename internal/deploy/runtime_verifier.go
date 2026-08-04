package deploy

import (
	"fmt"
	"strconv"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const (
	ApplicationStartupVerifierSchemaV1 = "application-startup-verifier-v1"
	ApplicationStartupVerifierRecipeV1 = "linux-proc-status-verify-exec-v1"
	ApplicationStartupVerifierPathV1   = "/opt/reploy/runtime/reploy-probe"
	ApplicationRuntimeLayerSchemaV1    = "application-runtime-layer-v1"
)

type ApplicationStartupVerifierV1 struct {
	Schema        string           `json:"schema"`
	RecipeVersion string           `json:"recipe_version"`
	Path          string           `json:"path"`
	Artifact      canonical.Digest `json:"artifact"`
	Size          string           `json:"size"`
}

type ApplicationRuntimeLayerV1 struct {
	Schema            string                       `json:"schema"`
	Verifier          ApplicationStartupVerifierV1 `json:"verifier"`
	TransactionDigest canonical.Digest             `json:"transaction_digest"`
	Upstream          providers.RealizedImageV1    `json:"upstream"`
	Result            providers.RealizedImageV1    `json:"result"`
}

func ApplicationStartupVerifierContractV1() ApplicationStartupVerifierV1 {
	return ApplicationStartupVerifierV1{
		Schema: ApplicationStartupVerifierSchemaV1, RecipeVersion: ApplicationStartupVerifierRecipeV1,
		Path: ApplicationStartupVerifierPathV1,
	}
}

func ValidateApplicationStartupVerifierV1(verifier ApplicationStartupVerifierV1, requireArtifact bool) error {
	want := ApplicationStartupVerifierContractV1()
	if verifier.Schema != want.Schema || verifier.RecipeVersion != want.RecipeVersion || verifier.Path != want.Path {
		return fmt.Errorf("application startup verifier does not use the supported fixed contract")
	}
	if !requireArtifact {
		if verifier.Artifact != "" || verifier.Size != "" {
			return fmt.Errorf("application startup verifier policy contract must not contain an artifact")
		}
		return nil
	}
	if err := verifier.Artifact.Validate(); err != nil {
		return fmt.Errorf("application startup verifier artifact: %w", err)
	}
	size, err := strconv.ParseUint(verifier.Size, 10, 64)
	if err != nil || size == 0 || strconv.FormatUint(size, 10) != verifier.Size {
		return fmt.Errorf("application startup verifier size must be a canonical positive integer")
	}
	return nil
}

func ApplicationRuntimeLayerTransactionDigestV1(
	verifier ApplicationStartupVerifierV1,
	upstream providers.RealizedImageV1,
	platform blueprint.Platform,
) (canonical.Digest, error) {
	if err := ValidateApplicationStartupVerifierV1(verifier, true); err != nil {
		return "", err
	}
	if err := upstream.Validate(); err != nil {
		return "", fmt.Errorf("application runtime layer upstream: %w", err)
	}
	if err := platform.Validate(); err != nil {
		return "", fmt.Errorf("application runtime layer platform: %w", err)
	}
	return canonical.Sum("application-runtime-layer", ApplicationRuntimeLayerSchemaV1, struct {
		Verifier ApplicationStartupVerifierV1 `json:"verifier"`
		Upstream providers.RealizedImageV1    `json:"upstream"`
		Platform blueprint.Platform           `json:"platform"`
	}{Verifier: verifier, Upstream: upstream, Platform: platform})
}

func ValidateApplicationRuntimeLayerV1(layer ApplicationRuntimeLayerV1, platform blueprint.Platform) error {
	if layer.Schema != ApplicationRuntimeLayerSchemaV1 {
		return fmt.Errorf("application runtime layer schema must be %q", ApplicationRuntimeLayerSchemaV1)
	}
	want, err := ApplicationRuntimeLayerTransactionDigestV1(layer.Verifier, layer.Upstream, platform)
	if err != nil {
		return err
	}
	if layer.TransactionDigest != want {
		return fmt.Errorf("application runtime layer transaction digest does not match its inputs")
	}
	if err := layer.Result.Validate(); err != nil {
		return fmt.Errorf("application runtime layer result: %w", err)
	}
	if layer.Result.RootFSSubject == layer.Upstream.RootFSSubject {
		return fmt.Errorf("application runtime layer result must add the verifier filesystem layer")
	}
	return nil
}
