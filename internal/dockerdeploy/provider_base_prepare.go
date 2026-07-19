package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type PreparedProviderBase struct {
	Plan       providers.ProviderPlanV1
	Descriptor deploy.ImageDescriptor
	Config     deploy.BaseConfig
	Image      providers.RealizedImageV1
	Catalog    []providers.RealizedOutput
}

var resolveProviderBaseImage = ResolveBase
var realizePreparedProviderBase = RealizeProviderBase

// PrepareProviderBase plans a validated resolved request, resolves its exact
// platform base, and creates the initial image/catalog inputs for graph
// execution. It performs no provider-node resolution or materialization.
func PrepareProviderBase(
	ctx context.Context,
	store providerstore.Store,
	request providers.ResolvedRequestV1,
) (PreparedProviderBase, error) {
	if ctx == nil {
		return PreparedProviderBase{}, fmt.Errorf("prepare provider base requires a context")
	}
	if err := ctx.Err(); err != nil {
		return PreparedProviderBase{}, err
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err != nil {
		return PreparedProviderBase{}, err
	}
	plan, err := registry.Plan(providers.PlanInput{
		BlueprintDigest: request.BlueprintDigest, Components: request.Components, Platform: request.Platform,
	})
	if err != nil {
		return PreparedProviderBase{}, fmt.Errorf("prepare provider plan: %w", err)
	}
	baseReference, err := resolvedRequestBaseReference(request)
	if err != nil {
		return PreparedProviderBase{}, err
	}
	descriptor, config, err := resolveProviderBaseImage(ctx, baseReference, request.Platform)
	if err != nil {
		return PreparedProviderBase{}, fmt.Errorf("prepare provider Docker base: %w", err)
	}
	image, catalog, err := realizePreparedProviderBase(ctx, store, plan, descriptor)
	if err != nil {
		return PreparedProviderBase{}, err
	}
	return PreparedProviderBase{
		Plan: plan, Descriptor: descriptor, Config: config, Image: image,
		Catalog: append([]providers.RealizedOutput{}, catalog...),
	}, nil
}

func resolvedRequestBaseReference(request providers.ResolvedRequestV1) (string, error) {
	for _, component := range request.Components {
		if component.Component != "base" {
			continue
		}
		image, ok := component.Request.Value["image"].(string)
		if !ok || image == "" {
			return "", fmt.Errorf("resolved base request has no image reference")
		}
		return image, nil
	}
	return "", fmt.Errorf("resolved request has no base component")
}
