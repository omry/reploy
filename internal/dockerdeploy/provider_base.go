package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

var collectProviderBaseExecutableEvidence = CollectFullImageExecutableEvidence

// RealizeProviderBase converts an inspected immutable Docker base into the
// provider graph's image identity and initial executable catalog. All declared
// base exports are inspected together in one image-validation session.
func RealizeProviderBase(
	ctx context.Context,
	store providerstore.Store,
	plan providers.ProviderPlanV1,
	descriptor deploy.ImageDescriptor,
) (providers.RealizedImageV1, []providers.RealizedOutput, error) {
	if ctx == nil {
		return providers.RealizedImageV1{}, nil, fmt.Errorf("realize provider base requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.RealizedImageV1{}, nil, err
	}
	if err := descriptor.Validate(); err != nil {
		return providers.RealizedImageV1{}, nil, fmt.Errorf("realize provider base descriptor: %w", err)
	}
	if err := providers.ValidateProviderPlanV1(plan); err != nil {
		return providers.RealizedImageV1{}, nil, err
	}
	rootFSSubject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		return providers.RealizedImageV1{}, nil, fmt.Errorf("realize provider base rootfs: %w", err)
	}
	imageDigest := descriptor.ManifestDigest
	if imageDigest == "" {
		imageDigest = descriptor.ConfigDigest
	}
	image := providers.RealizedImageV1{
		Digest: imageDigest, ConfigDigest: descriptor.ConfigDigest, RootFSSubject: rootFSSubject,
	}
	if err := image.Validate(); err != nil {
		return providers.RealizedImageV1{}, nil, fmt.Errorf("realize provider base image: %w", err)
	}

	base, found := providerBaseNode(plan)
	if !found {
		return providers.RealizedImageV1{}, nil, fmt.Errorf("provider plan has no base root")
	}
	checks := make([]FullImageExecutableProbe, 0, len(base.OutputDeclarations))
	for _, declaration := range base.OutputDeclarations {
		checks = append(checks, FullImageExecutableProbe{
			ID: declaration.Name, InvocationPath: declaration.CandidatePath,
			Binding: ProbeExecutableBinding{
				Output: providers.QualifiedOutput{Component: declaration.SupplierComponent, Name: declaration.Name},
				Facts:  declaration.Provenance,
			},
		})
	}
	evidence, err := collectProviderBaseExecutableEvidence(ctx, store, descriptor, checks)
	if err != nil {
		return providers.RealizedImageV1{}, nil, fmt.Errorf("realize provider base exports: %w", err)
	}
	evidenceByOutput := make(map[providers.QualifiedOutput]providers.ExecutableEvidence, len(evidence))
	for _, item := range evidence {
		if _, exists := evidenceByOutput[item.Output]; exists {
			return providers.RealizedImageV1{}, nil, fmt.Errorf("realize provider base exports returned duplicate evidence for %s.%s", item.Output.Component, item.Output.Name)
		}
		evidenceByOutput[item.Output] = item
	}
	catalog, err := providers.RealizeBaseCatalog(ctx, plan, image, func(
		_ context.Context, declaration providers.OutputDeclaration, _ providers.RealizedImageV1,
	) (providers.ExecutableEvidence, error) {
		output := providers.QualifiedOutput{Component: declaration.SupplierComponent, Name: declaration.Name}
		item, found := evidenceByOutput[output]
		if !found {
			return providers.ExecutableEvidence{}, fmt.Errorf("missing batched evidence for %s.%s", output.Component, output.Name)
		}
		delete(evidenceByOutput, output)
		return item, nil
	})
	if err != nil {
		return providers.RealizedImageV1{}, nil, err
	}
	if len(evidenceByOutput) != 0 {
		return providers.RealizedImageV1{}, nil, fmt.Errorf("realize provider base exports returned undeclared evidence")
	}
	return image, catalog, nil
}

func providerBaseNode(plan providers.ProviderPlanV1) (providers.NodeSpec, bool) {
	for _, node := range plan.Nodes {
		if node.ID == "base" {
			return node, true
		}
	}
	return providers.NodeSpec{}, false
}
