package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type FullImageExecutableProbe struct {
	ID             string
	InvocationPath string
	Binding        ProbeExecutableBinding
}

var prepareImageProbeWorkspace = PrepareProbeWorkspace
var runPreparedImageProbe = RunImageProbe

// CollectFullImageExecutableEvidence inspects every currently requested final
// or layer-validation path in one container. It is not a consumer prerequisite
// runner: those checks execute inside the existing resolver or materializer.
// The embedded helper is extracted once into deployment-local scratch and
// removed after confirmed container cleanup. If removal fails, scratch remains
// available to the next operation's abandoned-helper recovery.
func CollectFullImageExecutableEvidence(
	ctx context.Context,
	store providerstore.Store,
	descriptor deploy.ImageDescriptor,
	checks []FullImageExecutableProbe,
) (result []providers.ExecutableEvidence, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("collect image executable evidence requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if checks == nil {
		return nil, fmt.Errorf("image executable checks must use an array")
	}
	if len(checks) == 0 {
		return []providers.ExecutableEvidence{}, nil
	}
	if err := descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("collect image executable evidence descriptor: %w", err)
	}
	if descriptor.Platform.OS != "linux" {
		return nil, fmt.Errorf("collect image executable evidence requires a Linux image")
	}
	checks = append([]FullImageExecutableProbe{}, checks...)
	sort.Slice(checks, func(left int, right int) bool { return checks[left].ID < checks[right].ID })
	inspections := make([]probe.ExecutableInspectionV1, 0, len(checks))
	for _, check := range checks {
		if check.Binding.Requirement != nil {
			return nil, fmt.Errorf("full image probe check %q must not carry a consumer requirement", check.ID)
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
	defer func() {
		if providerHelperCleanupFailed(err) {
			return
		}
		if cleanupErr := cleanup(); cleanupErr != nil {
			result = nil
			err = errors.Join(err, cleanupErr)
		}
	}()
	response, err := runPreparedImageProbe(ctx, descriptor, workspace, request)
	if err != nil {
		return nil, err
	}
	result = make([]providers.ExecutableEvidence, 0, len(checks))
	for index, observation := range response.Observations {
		evidence, err := ExecutableEvidenceFromProbe(observation, checks[index].Binding)
		if err != nil {
			return nil, fmt.Errorf("image probe check %q: %w", checks[index].ID, err)
		}
		result = append(result, evidence)
	}
	return result, nil
}
