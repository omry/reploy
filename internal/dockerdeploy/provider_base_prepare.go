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

// SelectedProviderBase contains the immutable base selection and normalized
// config needed to decide exact build reuse. It deliberately contains no
// output evidence; collecting that evidence is a separate realization step.
type SelectedProviderBase struct {
	Plan       providers.ProviderPlanV1
	Descriptor deploy.ImageDescriptor
	Config     deploy.BaseConfig
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
	selected, err := SelectProviderBase(ctx, request)
	if err != nil {
		return PreparedProviderBase{}, err
	}
	return RealizeSelectedProviderBase(ctx, store, selected)
}

// SelectProviderBase plans the request and resolves the exact immutable base
// descriptor and normalized config. It does not inspect declared base outputs.
func SelectProviderBase(
	ctx context.Context,
	request providers.ResolvedRequestV1,
) (SelectedProviderBase, error) {
	if ctx == nil {
		return SelectedProviderBase{}, fmt.Errorf("select provider base requires a context")
	}
	if err := ctx.Err(); err != nil {
		return SelectedProviderBase{}, err
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err != nil {
		return SelectedProviderBase{}, err
	}
	plan, err := registry.Plan(providers.PlanInput{
		Components: request.Components, Platform: request.Platform,
	})
	if err != nil {
		return SelectedProviderBase{}, fmt.Errorf("prepare provider plan: %w", err)
	}
	baseReference, err := resolvedRequestBaseReference(request)
	if err != nil {
		return SelectedProviderBase{}, err
	}
	descriptor, config, err := resolveProviderBaseImage(ctx, baseReference, request.Platform)
	if err != nil {
		return SelectedProviderBase{}, fmt.Errorf("prepare provider Docker base: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return SelectedProviderBase{}, fmt.Errorf("select provider base descriptor: %w", err)
	}
	if err := config.Validate(); err != nil {
		return SelectedProviderBase{}, fmt.Errorf("select provider base config: %w", err)
	}
	return SelectedProviderBase{Plan: plan, Descriptor: descriptor, Config: config}, nil
}

// RealizeSelectedProviderBase validates declared base outputs and produces the
// graph's initial image and catalog after a caller has ruled out exact reuse.
func RealizeSelectedProviderBase(
	ctx context.Context,
	store providerstore.Store,
	selected SelectedProviderBase,
) (PreparedProviderBase, error) {
	if ctx == nil {
		return PreparedProviderBase{}, fmt.Errorf("realize selected provider base requires a context")
	}
	if err := ctx.Err(); err != nil {
		return PreparedProviderBase{}, err
	}
	if err := selected.Config.Validate(); err != nil {
		return PreparedProviderBase{}, fmt.Errorf("realize selected provider base config: %w", err)
	}
	image, catalog, err := realizePreparedProviderBase(ctx, store, selected.Plan, selected.Descriptor)
	if err != nil {
		return PreparedProviderBase{}, err
	}
	return PreparedProviderBase{
		Plan: selected.Plan, Descriptor: selected.Descriptor, Config: selected.Config, Image: image,
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
