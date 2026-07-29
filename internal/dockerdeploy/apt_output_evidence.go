package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

// CollectAPTOutputEvidence performs filesystem probing and exact dpkg owner
// attribution in one held, networkless validation container.
func CollectAPTOutputEvidence(
	ctx context.Context,
	store providerstore.Store,
	descriptor deploy.ImageDescriptor,
	checks []FullImageExecutableProbe,
	bundle providers.ResolvedBundle,
) (result []providers.ExecutableEvidence, resultErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("collect APT output evidence requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if checks == nil || len(checks) != len(bundle.Payload.Outputs) {
		return nil, fmt.Errorf("APT output checks must match resolved bundle outputs")
	}
	if len(checks) == 0 {
		return []providers.ExecutableEvidence{}, nil
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	aptBundle, err := aptprovider.DecodeCanonicalBundleDataV1(bundle.Payload.ProviderPayload)
	if err != nil {
		return nil, err
	}
	checks = append([]FullImageExecutableProbe{}, checks...)
	sort.Slice(checks, func(left int, right int) bool { return checks[left].ID < checks[right].ID })
	inspections := make([]probe.ExecutableInspectionV1, 0, len(checks))
	for _, check := range checks {
		if check.Binding.Requirement != nil {
			return nil, fmt.Errorf("APT output check %q must not carry a consumer requirement", check.ID)
		}
		inspections = append(inspections, probe.ExecutableInspectionV1{ID: check.ID, InvocationPath: check.InvocationPath})
	}
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: inspections}
	if err := probe.ValidateRequestV1(request); err != nil {
		return nil, err
	}
	workspace, cleanup, err := prepareImageProbeWorkspace(ctx, store, descriptor.Platform)
	if err != nil {
		return nil, err
	}
	workspaceSafeToRemove := false
	defer func() {
		if !workspaceSafeToRemove {
			return
		}
		if cleanupErr := cleanup(); cleanupErr != nil {
			result = nil
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()
	session, err := OpenImageValidationSession(ctx, descriptor, workspace)
	if err != nil {
		workspaceSafeToRemove = !providerHelperCleanupFailed(err)
		return nil, err
	}
	defer func() {
		if closeErr := session.Close(context.WithoutCancel(ctx)); closeErr != nil {
			result = nil
			resultErr = errors.Join(resultErr, closeErr)
			return
		}
		workspaceSafeToRemove = true
	}()
	response, err := session.Probe(ctx, request)
	if err != nil {
		return nil, err
	}
	evidence := make([]providers.ExecutableEvidence, 0, len(checks))
	paths := map[string]bool{}
	alternativeGroups := map[string]string{}
	for index, observation := range response.Observations {
		item, err := ExecutableEvidenceFromProbe(observation, checks[index].Binding)
		if err != nil {
			return nil, fmt.Errorf("APT output probe %q: %w", checks[index].ID, err)
		}
		for linkIndex, link := range item.LinkChain {
			if group, managed := aptprovider.AlternativeGroupForPathV1(link.Path); managed {
				alternativeGroups[link.Path] = group
				if linkIndex > 0 {
					delete(paths, item.LinkChain[linkIndex-1].Path)
				}
			} else {
				paths[link.Path] = true
			}
		}
		paths[item.Terminal.Path] = true
		evidence = append(evidence, item)
	}
	orderedPaths := make([]string, 0, len(paths))
	for path := range paths {
		orderedPaths = append(orderedPaths, path)
	}
	sort.Strings(orderedPaths)
	rawOwners, err := session.QueryDPKGOwners(ctx, orderedPaths)
	if err != nil {
		return nil, err
	}
	owners, err := aptprovider.ParseDPKGSearchOutputV1(rawOwners, orderedPaths, aptBundle.NativeArchitecture)
	if err != nil {
		return nil, err
	}
	alternativePaths := make([]string, 0, len(alternativeGroups))
	for alternativePath := range alternativeGroups {
		alternativePaths = append(alternativePaths, alternativePath)
	}
	sort.Strings(alternativePaths)
	alternatives := make(map[string]aptprovider.AlternativeSelectionV1, len(alternativePaths))
	for _, alternativePath := range alternativePaths {
		group := alternativeGroups[alternativePath]
		raw, err := session.QueryAlternative(ctx, group)
		if err != nil {
			return nil, err
		}
		selection, err := aptprovider.ParseAlternativeQueryV1(raw, group)
		if err != nil {
			return nil, err
		}
		alternatives[alternativePath] = selection
	}
	return aptprovider.ApplyOutputOwnershipV1(aptBundle, evidence, owners, alternatives)
}
